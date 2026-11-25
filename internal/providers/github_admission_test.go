package providers

import (
	"codefactory/internal/store"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookAdmissionCancelsBehindManagedHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, releaseHTTP := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseHTTP) })
	var calls, active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if r.URL.Path == "/owner" {
			close(entered)
			select {
			case <-releaseHTTP:
			case <-r.Context().Done():
				return
			}
		} else if r.URL.Path != "/after" {
			t.Error("expired request reached HTTP", r.URL.Path)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	waiterAuthenticated := make(chan struct{}, 1)
	var authCalls atomic.Int32
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(ctx context.Context) (string, error) {
		if authCalls.Add(1) > 1 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			waiterAuthenticated <- struct{}{}
		}
		return "fixture", nil
	}}
	g.ConfigureUsage(db, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 100}, nil })
	ownerDone, ownerStopped := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(ownerStopped)
		var result any
		_, err := g.get(ctx, "/owner", &result)
		ownerDone <- err
	}()
	var waiters []chan struct{}
	defer func() {
		release()
		cancel()
		for _, stopped := range append([]chan struct{}{ownerStopped}, waiters...) {
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Error("managed HTTP fixture did not stop")
			}
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("managed HTTP owner did not enter")
	}
	// getOne has released dispatch, but retains requestMu through HTTP and
	// durable accounting. Both canceled waiters must leave that ownership intact.
	for _, webhook := range []bool{true, false} {
		wait, stop := context.WithCancel(ctx)
		defer stop()
		done, stopped := make(chan error, 1), make(chan struct{})
		waiters = append(waiters, stopped)
		go func() {
			defer close(stopped)
			var err error
			if webhook {
				_, err = g.EnsureRepositoryWebhook(wait, "o/r", "https://callback.invalid/github/webhook", "fixture-secret")
			} else {
				_, _, err = g.Request(wait, http.MethodPost, "/cancelled", map[string]string{"title": "retained"}, "")
			}
			done <- err
		}()
		// Cancel only AFTER the live waiter has acquired dispatch and entered
		// authentication. A dispatch-only fix cannot pass via a pre-expired ctx.
		select {
		case <-waiterAuthenticated:
		case <-ctx.Done():
			t.Fatal("waiter did not pass dispatch with a live context")
		}
		stop()
		select {
		case err = <-done:
		case <-time.After(time.Second):
			t.Fatal("canceled usage admission waited for the unrelated HTTP owner")
		}
		var outcome *RequestError
		if !errors.Is(err, context.Canceled) || !errors.As(err, &outcome) || outcome.Phase != BeforeDispatch {
			t.Fatal("managed admission did not cancel before dispatch", webhook, err)
		}
		usage, err := g.UsageStatus(ctx)
		if err != nil || usage.RequestsLastHour != 1 || calls.Load() != 1 || peak.Load() != 1 {
			t.Fatal("expired waiter dispatched, charged or released its owner", usage, err, calls.Load(), peak.Load())
		}
		select {
		case <-ownerStopped:
			t.Fatal("waiter canceled unrelated HTTP owner")
		default:
		}
	}
	release()
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("managed HTTP owner did not resume")
	}
	if _, _, err := g.Request(ctx, http.MethodGet, "/after", nil, ""); err != nil {
		t.Fatal("canceled admission leaked ownership", err)
	}
	usage, err := g.UsageStatus(ctx)
	if err != nil || usage.RequestsLastHour != 2 || calls.Load() != 2 || peak.Load() != 1 || ctx.Err() != nil {
		t.Fatal("managed serialization or accounting changed", usage, err, calls.Load(), peak.Load())
	}
}

func TestExpiredGitHubAdmissionDoesNoWork(t *testing.T) {
	var calls, authCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	g := &GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) {
		authCalls.Add(1)
		return "fixture", nil
	}}
	g.ConfigureUsage(nil, func(context.Context) (GitHubPolicy, error) { return GitHubPolicy{RequestsPerHour: 100}, nil })
	for _, expired := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := context.Canceled
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		}
		cancel()
		_, _, err := g.Request(ctx, http.MethodPost, "/cancelled", nil, "")
		var outcome *RequestError
		if !errors.Is(err, want) || !errors.As(err, &outcome) || outcome.Phase != BeforeDispatch || authCalls.Load() != 0 || calls.Load() != 0 {
			t.Fatal("expired admission performed work", err, authCalls.Load(), calls.Load())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	usage, err := g.UsageStatus(ctx)
	if err != nil || usage.RequestsLastHour != 0 {
		t.Fatal("expired admission charged budget", usage, err)
	}
	if _, _, err := g.Request(ctx, http.MethodGet, "/after", nil, ""); err != nil || authCalls.Load() != 1 || calls.Load() != 1 {
		t.Fatal("zero-value admission did not recover", err, authCalls.Load(), calls.Load())
	}
}
