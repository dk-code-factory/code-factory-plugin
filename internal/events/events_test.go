package events

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func eventFixture(t *testing.T) (*Service, context.Context) {
	return eventFixtureIn(t, t.TempDir())
}

func eventFixtureIn(t *testing.T, dir string) (*Service, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	v, e := store.NewVault(s, dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Put(ctx, "config", "settings", domain.Defaults(), ""); e != nil {
		t.Fatal(e)
	}
	service := New(s, v)
	t.Cleanup(func() {
		cancel()
		deadline := time.Now().Add(time.Second)
		for len(service.proofs) != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if len(service.proofs) != 0 {
			t.Error("verification workers did not exit during cleanup")
		}
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		select {
		case e := <-closed:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(time.Second):
			t.Error("event store did not close during cleanup")
		}
	})
	return service, ctx
}
func TestSubscriptionVerificationRetryDedupRefreshAndExpiry(t *testing.T) {
	s, ctx := eventFixture(t)
	secret := "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	var delivered []string
	fail := true
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		expected, e := Sign(secret, r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), b)
		if e != nil || expected != r.Header.Get("webhook-signature") {
			t.Fatal("invalid signature", e)
		}
		var body map[string]any
		if e = json.Unmarshal(b, &body); e != nil {
			t.Fatal(e)
		}
		status := 200
		response := `{}`
		if body["type"] == "verification" {
			response = fmt.Sprintf(`{"challenge":%q}`, body["challenge"])
		} else {
			delivered = append(delivered, body["eventId"].(string))
			if fail {
				status = 503
				fail = false
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	in := SubscribeInput{Name: "attention.required", Arguments: map[string]string{"entity": "run-1"}, Delivery: Delivery{Mode: "webhook", URL: "https://callback.example/events", Secret: secret}}
	res, e := s.Subscribe(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Subscribe(ctx, in)
	if e != nil || again["id"] != res["id"] {
		t.Fatal("idempotency", e)
	}
	id := res["id"].(string)
	if e = s.Store.Write(ctx, func(tx *store.Tx) error {
		if e := tx.Event("attention.required", "run-2", nil); e != nil {
			return e
		}
		return tx.Event("attention.required", "run-1", nil)
	}); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	var sub Subscription
	_ = s.Store.Get(ctx, "subscription", id, &sub)
	if len(delivered) != 1 || sub.Attempts != 1 {
		t.Fatal("retry missing", sub, delivered)
	}
	sub.RetryAt = time.Now().Add(-time.Second)
	if e = s.Store.Put(ctx, "subscription", id, sub, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	if len(delivered) != 2 || delivered[0] != delivered[1] {
		t.Fatal("retry event ID changed", delivered)
	}
	if e = s.Tick(ctx); e != nil || len(delivered) != 2 {
		t.Fatal("acknowledged delivery repeated", e)
	}
	_ = s.Store.Get(ctx, "subscription", id, &sub)
	sub.Expires = time.Now().Add(-time.Second)
	_ = s.Store.Put(ctx, "subscription", id, sub, "")
	_ = s.Store.Write(ctx, func(tx *store.Tx) error { return tx.Event("attention.required", "run-1", nil) })
	_ = s.Tick(ctx)
	if len(delivered) != 2 {
		t.Fatal("expired subscription delivered")
	}
	if e = s.Unsubscribe(ctx, in); e != nil {
		t.Fatal(e)
	}
	if e = s.Unsubscribe(ctx, in); e != nil {
		t.Fatal("unsubscribe must be idempotent", e)
	}
}
func TestCallbackSafetyAndFailedChallenge(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "2001:db8::1", "224.0.0.1"} {
		if PublicIP(net.ParseIP(ip)) {
			t.Fatal("allowed non-public address", ip)
		}
	}
	if !PublicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("blocked public address")
	}
	for _, u := range []string{"http://example.com", "https://user@example.com", "https://example.com:8443"} {
		if ValidateURL(u) == nil {
			t.Fatal("invalid URL accepted", u)
		}
	}
	s, ctx := eventFixture(t)
	s.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"challenge":"wrong"}`))}, nil
	})}
	_, e := s.Subscribe(ctx, SubscribeInput{Name: "inbox.changed", Delivery: Delivery{Mode: "webhook", URL: "https://callback.example/", Secret: "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))}})
	if e == nil {
		t.Fatal("wrong challenge accepted")
	}
	subs, _ := store.List[Subscription](ctx, s.Store, "subscription")
	if len(subs) != 0 {
		t.Fatal("failed verification persisted subscription")
	}
}

func TestPRMonitoringAndSessionQuestionsUseMetadataOnlyNotifications(t *testing.T) {
	s, ctx := eventFixture(t)
	secret := "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	names := map[string]int{}
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var body map[string]any
		if err = json.Unmarshal(b, &body); err != nil {
			return nil, err
		}
		response := "{}"
		if body["type"] == "verification" {
			response = fmt.Sprintf(`{"challenge":%q}`, body["challenge"])
		} else {
			if strings.Contains(string(b), "private-question-context") {
				t.Error("notification included private content")
			}
			names[body["name"].(string)]++
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	for _, name := range []string{"inbox.changed", "attention.required", "session.changed"} {
		if _, err := s.Subscribe(ctx, SubscribeInput{Name: name, Delivery: Delivery{Mode: "webhook", URL: "https://callback.example/events", Secret: secret}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Event("pr.changed", "org/repo#1", map[string]string{"context": "private-question-context"}); err != nil {
			return err
		}
		if err := tx.Event("attention.required", "session-1", map[string]string{"origin": "native", "context": "private-question-context"}); err != nil {
			return err
		}
		return tx.Event("session.changed", "session-1", map[string]string{"summary": "private-question-context"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if names["inbox.changed"] != 1 || names["attention.required"] != 1 || names["session.changed"] != 1 {
		t.Fatal("monitoring, native question or session alert was lost or duplicated")
	}
}

func eventAwait[T any](t *testing.T, label string, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
	var zero T
	return zero
}

func eventRelease(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	ch := make(chan struct{})
	release := sync.OnceFunc(func() { close(ch) })
	t.Cleanup(release)
	return ch, release
}

func eventRun(t *testing.T, release func(), work func() error) <-chan error {
	t.Helper()
	result, exited := make(chan error, 1), make(chan struct{})
	t.Cleanup(func() {
		release()
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Error("event worker did not exit during cleanup")
		}
	})
	go func() { defer close(exited); result <- work() }()
	return result
}

func eventCall(t *testing.T, release func(), work func() error) error {
	t.Helper()
	return eventAwait(t, "foreground event operation", eventRun(t, release, work))
}

func eventInput(url string, fill byte) SubscribeInput {
	return SubscribeInput{Name: "inbox.changed", Arguments: map[string]string{}, Delivery: Delivery{Mode: "webhook", URL: url, Secret: "whsec_" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string([]byte{fill}), 32)))}}
}

func challengeResponse(r *http.Request) (*http.Response, error) {
	var body map[string]any
	if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
		return nil, e
	}
	b, _ := json.Marshal(map[string]any{"challenge": body["challenge"]})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
}

type waitingBody struct {
	io.Reader
	before func()
	closed chan struct{}
	once   sync.Once
}

func (b *waitingBody) Read(p []byte) (int, error) {
	b.once.Do(b.before)
	return b.Reader.Read(p)
}
func (b *waitingBody) Close() error {
	if b.closed != nil {
		close(b.closed)
	}
	return nil
}

func TestVerificationDeadlineCoversUncooperativeHeadersAndBody(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx := eventFixture(t)
			closed := make(chan struct{})
			s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				res, e := challengeResponse(r)
				if e != nil {
					return nil, e
				}
				b, _ := io.ReadAll(res.Body)
				res.Body.Close()
				res.Body = &waitingBody{Reader: strings.NewReader(string(b)), before: func() {
					if phase == "body" {
						time.Sleep(125 * time.Millisecond)
					}
				}, closed: closed}
				if phase == "headers" {
					time.Sleep(125 * time.Millisecond)
				}
				return res, nil
			})}
			in := eventInput("https://callback.example/delayed", 1)
			start := time.Now()
			_, e := s.Subscribe(ctx, in)
			if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
				t.Fatalf("verification rejection exceeded 100ms: %s", elapsed)
			}
			var callback *CallbackError
			if !errors.As(e, &callback) || callback.Reason != "connection_failed" {
				t.Fatalf("expected bounded callback rejection, got %v", e)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("late verification did not close body")
			}
			rows, e := store.List[Subscription](ctx, s.Store, "subscription")
			if e != nil || len(rows) != 0 {
				t.Fatal("late verification activated subscription", rows, e)
			}
			if _, e = s.Vault.Get(ctx, identity(in)); !errors.Is(e, store.ErrNotFound) {
				t.Fatal("failed verification saved signing material", e)
			}
			s.mu.Lock()
			pending, verified := len(s.pending), len(s.verified)
			s.mu.Unlock()
			if pending != 0 || verified != 0 {
				t.Fatal("failed verification retained ownership/cache", pending, verified)
			}
		})
	}
}

func TestPendingVerificationDoesNotBlockOrResurrect(t *testing.T) {
	for _, action := range []string{"unsubscribe", "replace"} {
		t.Run(action, func(t *testing.T) {
			s, ctx := eventFixture(t)
			started, closed := make(chan struct{}), make(chan struct{})
			release, unblock := eventRelease(t)
			var requests atomic.Int32
			s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				res, e := challengeResponse(r)
				if requests.Add(1) == 1 {
					close(started)
					<-release // Deliberately ignore cancellation.
					defer close(closed)
				}
				return res, e
			})}
			in := eventInput("https://callback.example/pending", 1)
			done := eventRun(t, unblock, func() error { _, e := s.Subscribe(ctx, in); return e })
			eventAwait(t, "callback start", started)
			other := eventInput("https://callback.example/unrelated", 2)
			start := time.Now()
			if e := eventCall(t, unblock, func() error {
				if _, e := s.Subscribe(ctx, other); e != nil {
					return e
				}
				return s.Unsubscribe(ctx, other)
			}); e != nil {
				t.Fatal(e)
			}
			if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
				t.Fatal("unrelated subscription queued behind proof", elapsed)
			}
			replacement := in
			replacement.Delivery.Secret = eventInput("", 3).Delivery.Secret
			start = time.Now()
			if e := eventCall(t, unblock, func() error {
				if action == "replace" {
					_, e := s.Subscribe(ctx, replacement)
					return e
				}
				return s.Unsubscribe(ctx, in)
			}); e != nil {
				t.Fatal(e)
			}
			if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
				t.Fatal("foreground operation queued behind proof", elapsed)
			}
			unblock()
			if e := eventAwait(t, "event worker result", done); e == nil {
				t.Fatal("superseded proof reported activation")
			}
			eventAwait(t, "callback body close", closed)
			var sub Subscription
			e := s.Store.Get(ctx, "subscription", identity(in), &sub)
			if action == "unsubscribe" {
				if !errors.Is(e, store.ErrNotFound) {
					t.Fatal("late proof resurrected subscription", sub, e)
				}
			} else {
				secret, secretErr := s.Vault.Get(ctx, identity(in))
				if e != nil || secretErr != nil || secret != replacement.Delivery.Secret || sub.Generation == "" {
					t.Fatal("late proof replaced newer activation", sub, e, secretErr)
				}
			}
			s.mu.Lock()
			pending := len(s.pending)
			s.mu.Unlock()
			if pending != 0 {
				t.Fatal("terminal proof leaked ownership", pending)
			}
		})
	}
}

func TestVerificationAdmissionRemainsBoundedUntilActualExit(t *testing.T) {
	s, ctx := eventFixture(t)
	started := make(chan struct{}, 32)
	release, unblock := eventRelease(t)
	var callbacks atomic.Int32
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		callbacks.Add(1)
		started <- struct{}{}
		<-release
		return challengeResponse(r)
	})}
	workers := []<-chan error{}
	for n := 0; n < 32; n++ {
		workers = append(workers, eventRun(t, unblock, func() error {
			_, e := s.Subscribe(ctx, eventInput(fmt.Sprintf("https://callback.example/%d", n), 1))
			return e
		}))
	}
	for n := 0; n < 32; n++ {
		eventAwait(t, "callback start", started)
	}
	for _, done := range workers {
		if e := eventAwait(t, "event worker result", done); e == nil {
			t.Fatal("stalled proof activated")
		}
	}
	start := time.Now()
	if _, e := s.Subscribe(ctx, eventInput("https://callback.example/overflow", 1)); e == nil {
		t.Fatal("saturated proof admission succeeded")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatal("proof admission waited", elapsed)
	}
	if callbacks.Load() != 32 || len(s.proofs) != 32 {
		t.Fatal("timed-out workers lost admission", callbacks.Load(), len(s.proofs))
	}
	unblock()
	deadline := time.Now().Add(time.Second)
	for len(s.proofs) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.proofs) != 0 {
		t.Fatal("finished proofs retained admission")
	}
	rows, e := store.List[Subscription](ctx, s.Store, "subscription")
	if e != nil || len(rows) != 0 {
		t.Fatal("late proofs activated", rows, e)
	}
}

func TestInFlightDeliveryCannotOverwriteReplacementOrResurrect(t *testing.T) {
	for _, status := range []int{200, 503, 410} {
		for _, action := range []string{"unsubscribe", "replace"} {
			t.Run(fmt.Sprintf("%d/%s", status, action), func(t *testing.T) {
				s, ctx := eventFixture(t)
				started := make(chan struct{})
				release, unblock := eventRelease(t)
				var deliveries atomic.Int32
				s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
					var body map[string]any
					b, e := io.ReadAll(r.Body)
					if e != nil {
						return nil, e
					}
					if e = json.Unmarshal(b, &body); e != nil {
						return nil, e
					}
					if body["type"] == "verification" {
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"challenge":%q}`, body["challenge"])))}, nil
					}
					if deliveries.Add(1) == 1 {
						// Stall body drain as well as testing verification header stalls.
						return &http.Response{StatusCode: status, Body: &waitingBody{Reader: strings.NewReader("{}"), before: func() { close(started); <-release }}}, nil
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
				})}
				in := eventInput("https://callback.example/delivery", 1)
				result, e := s.Subscribe(ctx, in)
				if e != nil {
					t.Fatal(e)
				}
				id := result["id"].(string)
				var before Subscription
				if e = s.Store.Get(ctx, "subscription", id, &before); e != nil {
					t.Fatal(e)
				}
				if e = s.Store.Write(ctx, func(tx *store.Tx) error { return tx.Event("inbox.changed", "item-1", nil) }); e != nil {
					t.Fatal(e)
				}
				done := eventRun(t, unblock, func() error { return s.Tick(ctx) })
				eventAwait(t, "callback start", started)
				start := time.Now()
				if action == "replace" {
					in.Delivery.Secret = eventInput("", 2).Delivery.Secret
				}
				if e = eventCall(t, unblock, func() error {
					if action == "unsubscribe" {
						return s.Unsubscribe(ctx, in)
					}
					_, e := s.Subscribe(ctx, in)
					return e
				}); e != nil {
					t.Fatal(e)
				}
				if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
					t.Fatal("foreground blocked behind delivery", elapsed)
				}
				var replacement Subscription
				if action == "replace" {
					if e = s.Store.Get(ctx, "subscription", id, &replacement); e != nil {
						t.Fatal(e)
					}
					if replacement.Generation == before.Generation {
						t.Fatal("refresh reused generation")
					}
				}
				unblock()
				if e = eventAwait(t, "event worker result", done); e != nil {
					t.Fatal(e)
				}
				var after Subscription
				e = s.Store.Get(ctx, "subscription", id, &after)
				if action == "unsubscribe" {
					if !errors.Is(e, store.ErrNotFound) {
						t.Fatal("delivery resurrected subscription", after, e)
					}
					if e = s.Tick(ctx); e != nil || deliveries.Load() != 1 {
						t.Fatal("unsubscribed callback delivered again", deliveries.Load(), e)
					}
				} else {
					want, _ := json.Marshal(replacement)
					got, _ := json.Marshal(after)
					if e != nil || string(want) != string(got) {
						t.Fatal("stale delivery altered replacement", after, replacement, e)
					}
					secret, e := s.Vault.Get(ctx, id)
					if e != nil || secret != in.Delivery.Secret {
						t.Fatal("stale delivery changed signing key", e)
					}
				}
			})
		}
	}
}

func TestRefreshRetainsConcurrentProgressAndRotatedSignatures(t *testing.T) {
	s, ctx := eventFixture(t)
	in := eventInput("https://callback.example/rotate", 1)
	rotated := in
	rotated.Delivery.Secret = eventInput("", 2).Delivery.Secret
	proofStarted := make(chan struct{})
	proofRelease, unblock := eventRelease(t)
	var proofs atomic.Int32
	var signatures []string
	var eventIDs []string
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, e := io.ReadAll(r.Body)
		if e != nil {
			return nil, e
		}
		var body map[string]any
		if e = json.Unmarshal(b, &body); e != nil {
			return nil, e
		}
		if body["type"] == "verification" {
			if proofs.Add(1) == 2 {
				close(proofStarted)
				<-proofRelease
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"challenge":%q}`, body["challenge"])))}, nil
		}
		current := in.Delivery.Secret
		if len(signatures) > 0 {
			current = rotated.Delivery.Secret
		}
		want, e := Sign(current, r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), b)
		if e != nil {
			return nil, e
		}
		if len(signatures) > 0 {
			old, _ := Sign(in.Delivery.Secret, r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), b)
			want += " " + old
		}
		if r.Header.Get("webhook-signature") != want {
			return nil, fmt.Errorf("signing generation mixed: got %q, want %q", r.Header.Get("webhook-signature"), want)
		}
		signatures = append(signatures, want)
		eventIDs = append(eventIDs, r.Header.Get("webhook-id"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	result, e := s.Subscribe(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	id := result["id"].(string)
	if e = s.Store.Write(ctx, func(tx *store.Tx) error { return tx.Event("inbox.changed", "item-1", nil) }); e != nil {
		t.Fatal(e)
	}
	done := eventRun(t, unblock, func() error { _, e := s.Subscribe(ctx, rotated); return e })
	eventAwait(t, "refresh proof start", proofStarted)
	if e = eventCall(t, unblock, func() error { return s.Tick(ctx) }); e != nil {
		t.Fatal(e)
	}
	var delivered Subscription
	if e = s.Store.Get(ctx, "subscription", id, &delivered); e != nil {
		t.Fatal(e)
	}
	unblock()
	if e = eventAwait(t, "event worker result", done); e != nil {
		t.Fatal(e)
	}
	var refreshed Subscription
	if e = s.Store.Get(ctx, "subscription", id, &refreshed); e != nil {
		t.Fatal(e)
	}
	if refreshed.Cursor != delivered.Cursor || refreshed.Cursor == 0 || !refreshed.DeliveredAt.Equal(delivered.DeliveredAt) {
		t.Fatal("refresh lost concurrent delivery progress", delivered, refreshed)
	}
	if _, e = s.Subscribe(ctx, rotated); e != nil {
		t.Fatal(e)
	}
	if proofs.Load() != 2 {
		t.Fatal("verified same-secret refresh repeated proof", proofs.Load())
	}
	if e = s.Store.Write(ctx, func(tx *store.Tx) error { return tx.Event("inbox.changed", "item-2", nil) }); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	if len(signatures) != 2 || eventIDs[0] == eventIDs[1] {
		t.Fatal("refresh replayed acknowledged event", eventIDs)
	}
	if e = s.Tick(ctx); e != nil || len(signatures) != 2 {
		t.Fatal("delivery acknowledgement repeated", eventIDs, e)
	}
}

func TestConcurrentTicksSerializeDeliveryWithoutForegroundLock(t *testing.T) {
	s, ctx := eventFixture(t)
	started := make(chan struct{})
	release, unblock := eventRelease(t)
	var deliveries atomic.Int32
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			return nil, e
		}
		if body["type"] == "verification" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"challenge":%q}`, body["challenge"])))}, nil
		}
		if deliveries.Add(1) == 1 {
			close(started)
			<-release
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	if _, e := s.Subscribe(ctx, eventInput("https://callback.example/ticks", 1)); e != nil {
		t.Fatal(e)
	}
	if e := s.Store.Write(ctx, func(tx *store.Tx) error { return tx.Event("inbox.changed", "item-1", nil) }); e != nil {
		t.Fatal(e)
	}
	first := eventRun(t, unblock, func() error { return s.Tick(ctx) })
	eventAwait(t, "callback start", started)
	second := eventRun(t, unblock, func() error { return s.Tick(ctx) })
	other := eventInput("https://callback.example/unrelated", 2)
	if e := eventCall(t, unblock, func() error {
		if _, e := s.Subscribe(ctx, other); e != nil {
			return e
		}
		return s.Unsubscribe(ctx, other)
	}); e != nil {
		t.Fatal(e)
	}
	unblock()
	for _, done := range []<-chan error{first, second} {
		if e := eventAwait(t, "Tick exit", done); e != nil {
			t.Fatal(e)
		}
	}
	if deliveries.Load() != 1 {
		t.Fatal("concurrent ticks repeated acknowledged callback", deliveries.Load())
	}
}

func TestFailedRefreshKeepsActiveStateAndRetryProgress(t *testing.T) {
	s, ctx := eventFixture(t)
	in := eventInput("https://callback.example/failed-refresh", 1)
	var proofs atomic.Int32
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if proofs.Add(1) == 2 {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"challenge":"wrong"}`))}, nil
		}
		return challengeResponse(r)
	})}
	result, e := s.Subscribe(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	id := result["id"].(string)
	var before Subscription
	if e = s.Store.Get(ctx, "subscription", id, &before); e != nil {
		t.Fatal(e)
	}
	before.Attempts, before.Error, before.RetryAt = 3, "Webhook delivery failed", time.Now().Add(time.Minute)
	if e = s.Store.Put(ctx, "subscription", id, before, ""); e != nil {
		t.Fatal(e)
	}
	rotated := in
	rotated.Delivery.Secret = eventInput("", 2).Delivery.Secret
	if _, e = s.Subscribe(ctx, rotated); e == nil {
		t.Fatal("failed refresh activated")
	}
	var after Subscription
	if e = s.Store.Get(ctx, "subscription", id, &after); e != nil {
		t.Fatal(e)
	}
	want, _ := json.Marshal(before)
	got, _ := json.Marshal(after)
	if string(want) != string(got) {
		t.Fatal("failed refresh changed active state", before, after)
	}
	secret, e := s.Vault.Get(ctx, id)
	if e != nil || secret != in.Delivery.Secret {
		t.Fatal("failed refresh changed signing material", e)
	}
	if _, e = s.Subscribe(ctx, rotated); e != nil {
		t.Fatal(e)
	}
	if e = s.Store.Get(ctx, "subscription", id, &after); e != nil {
		t.Fatal(e)
	}
	if after.Attempts != before.Attempts || after.Error != before.Error || !after.RetryAt.Equal(before.RetryAt) {
		t.Fatal("successful refresh lost retry progress", before, after)
	}
}

func TestCanceledQueuedRequestCannotDisruptPendingVerification(t *testing.T) {
	for _, method := range []string{"subscribe", "unsubscribe"} {
		t.Run(method, func(t *testing.T) {
			s, ctx := eventFixture(t)
			started := make(chan struct{})
			release, unblock := eventRelease(t)
			s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				close(started)
				<-release
				return challengeResponse(r)
			})}
			in := eventInput("https://callback.example/canceled-queue", 1)
			// This older request loses its response budget before it can acquire
			// the state lock. The valid proof must keep ownership when it arrives.
			older, cancel := context.WithCancel(ctx)
			defer cancel()
			valid := eventRun(t, unblock, func() error { _, e := s.Subscribe(ctx, in); return e })
			eventAwait(t, "callback start", started)
			s.mu.Lock()
			unlock := sync.OnceFunc(s.mu.Unlock)
			t.Cleanup(unlock)
			queued := make(chan struct{})
			rejected := eventRun(t, func() { unlock(); unblock() }, func() error {
				close(queued)
				if method == "subscribe" {
					_, e := s.Subscribe(older, in)
					return e
				}
				return s.Unsubscribe(older, in)
			})
			eventAwait(t, "queued request start", queued)
			cancel()
			unlock()
			if e := eventAwait(t, "canceled request result", rejected); e == nil {
				t.Fatal("canceled queued request succeeded")
			}
			unblock()
			if e := eventAwait(t, "valid proof result", valid); e != nil {
				t.Fatal("canceled queued request canceled valid proof", e)
			}
			var sub Subscription
			if e := s.Store.Get(ctx, "subscription", identity(in), &sub); e != nil || sub.Generation == "" {
				t.Fatal("valid proof lost activation", sub, e)
			}
		})
	}
}

func TestRequestOriginCancellationRetainsSDKValues(t *testing.T) {
	type sdkKey struct{}
	origin, cancelOrigin := context.WithCancel(context.Background())
	defer cancelOrigin()
	detached := context.WithValue(context.WithoutCancel(WithRequestContext(origin)), sdkKey{}, "sdk-session")
	ctx, cancel := requestContext(detached)
	defer cancel()
	if ctx.Value(sdkKey{}) != "sdk-session" {
		t.Fatal("HTTP cancellation bridge discarded SDK values")
	}
	cancelOrigin()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("origin cancellation was not immediately visible", ctx.Err())
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("origin cancellation did not close Done")
	}

	sdk, cancelSDK := context.WithCancel(context.WithoutCancel(WithRequestContext(context.Background())))
	ctx, cancel = requestContext(sdk)
	defer cancel()
	cancelSDK()
	eventAwait(t, "SDK cancellation", ctx.Done())
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("SDK cancellation was lost", ctx.Err())
	}
}

// Count persisted subscription writes, rather than inferring I/O from elapsed
// time. The trigger runs in the actual Store.Write transaction being exercised.
func eventSubscriptionWrites(t *testing.T, dir string) func() int {
	t.Helper()
	db, e := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := db.Close(); e != nil {
			t.Error(e)
		}
	})
	for _, ddl := range []string{
		`CREATE TABLE test_subscription_writes(id INTEGER PRIMARY KEY)`,
		`CREATE TRIGGER test_subscription_update AFTER UPDATE ON entities WHEN NEW.kind='subscription' BEGIN INSERT INTO test_subscription_writes(id) VALUES(NULL); END`,
		`CREATE TRIGGER test_subscription_insert AFTER INSERT ON entities WHEN NEW.kind='subscription' BEGIN INSERT INTO test_subscription_writes(id) VALUES(NULL); END`,
	} {
		if _, e = db.Exec(ddl); e != nil {
			t.Fatal(e)
		}
	}
	return func() int {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var n int
		if e := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM test_subscription_writes`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
}

func TestIgnoredEventPagesCommitOnceWithoutSigningMaterial(t *testing.T) {
	for _, reason := range []string{"unrelated", "filtered", "notifications-disabled"} {
		t.Run(reason, func(t *testing.T) {
			dir := t.TempDir()
			s, ctx := eventFixtureIn(t, dir)
			var callbacks atomic.Int32
			s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				callbacks.Add(1)
				return challengeResponse(r)
			})}
			in := eventInput("https://callback.example/ignored", 1)
			if reason == "filtered" {
				in.Arguments["entity"] = "wanted"
			}
			result, e := s.Subscribe(ctx, in)
			if e != nil {
				t.Fatal(e)
			}
			if reason == "notifications-disabled" {
				config := domain.Defaults()
				config.Notifications = false
				if e = s.Store.Put(ctx, "config", "settings", config, ""); e != nil {
					t.Fatal(e)
				}
			}
			id := result["id"].(string)
			var before Subscription
			if e = s.Store.Get(ctx, "subscription", id, &before); e != nil {
				t.Fatal(e)
			}
			writes := eventSubscriptionWrites(t, dir)
			// An ignored page must not access the vault at all. Accidental lookup
			// fails immediately, even if an implementation would ignore its error.
			s.Vault = nil
			for page := 1; page <= 2; page++ {
				if e = s.Store.Write(ctx, func(tx *store.Tx) error {
					for n := 0; n < 100; n++ {
						kind := "inbox.changed"
						if reason == "unrelated" {
							kind = "session.changed"
						}
						if e := tx.Event(kind, "ignored", nil); e != nil {
							return e
						}
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
				if e = s.Tick(ctx); e != nil {
					t.Fatal(e)
				}
				var after Subscription
				if e = s.Store.Get(ctx, "subscription", id, &after); e != nil {
					t.Fatal(e)
				}
				if writes() != page {
					t.Fatal("ignored page did not use one cursor write", writes(), page)
				}
				if after.Cursor != s.Store.Latest(ctx) || after.Generation != before.Generation || !after.DeliveredAt.Equal(before.DeliveredAt) || after.Attempts != 0 || !after.RetryAt.IsZero() {
					t.Fatal("ignored page altered delivery/generation state", before, after)
				}
				if e = s.Tick(ctx); e != nil || writes() != page {
					t.Fatal("empty page rewrote cursor", writes(), e)
				}
			}
			if callbacks.Load() != 1 {
				t.Fatal("ignored events invoked a callback", callbacks.Load())
			}
		})
	}
}

func TestIgnoredPageCommitRejectsChangedGenerationAndDeletion(t *testing.T) {
	for _, action := range []string{"replace", "delete"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			s, ctx := eventFixtureIn(t, dir)
			s.Client = &http.Client{Transport: roundTrip(challengeResponse)}
			result, e := s.Subscribe(ctx, eventInput("https://callback.example/ignored-generation", 1))
			if e != nil {
				t.Fatal(e)
			}
			id := result["id"].(string)
			var original Subscription
			if e = s.Store.Get(ctx, "subscription", id, &original); e != nil {
				t.Fatal(e)
			}
			if e = s.Store.Write(ctx, func(tx *store.Tx) error {
				for n := 0; n < 100; n++ {
					if e := tx.Event("session.changed", "ignored", nil); e != nil {
						return e
					}
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			writes := eventSubscriptionWrites(t, dir)
			s.Vault = nil
			replacement := original
			replacement.Generation = "new-activation"
			replacement.Cursor = 17
			replacement.Attempts = 3
			replacement.RetryAt = time.Now().UTC().Add(time.Hour)
			release, unblock := eventRelease(t)
			writerStarted := make(chan struct{})
			writer := eventRun(t, unblock, func() error {
				return s.Store.Write(ctx, func(tx *store.Tx) error {
					close(writerStarted)
					<-release
					if action == "delete" {
						return tx.Delete("subscription", id)
					}
					return tx.Put("subscription", id, replacement)
				})
			})
			eventAwait(t, "held writer", writerStarted)
			tick := eventRun(t, unblock, func() error { return s.Tick(ctx) })
			// Tick can read the old page under WAL. Its state lock becomes held
			// only when the conditional cursor save waits for this store writer.
			deadline := time.Now().Add(time.Second)
			waiting := false
			for time.Now().Before(deadline) {
				if !s.mu.TryLock() {
					waiting = true
					break
				}
				s.mu.Unlock()
				time.Sleep(time.Millisecond)
			}
			if !waiting {
				t.Fatal("Tick did not reach pending cursor commit")
			}
			unblock()
			if e = eventAwait(t, "writer exit", writer); e != nil {
				t.Fatal(e)
			}
			if e = eventAwait(t, "Tick exit", tick); e != nil {
				t.Fatal(e)
			}
			var after Subscription
			e = s.Store.Get(ctx, "subscription", id, &after)
			if action == "delete" {
				if !errors.Is(e, store.ErrNotFound) || writes() != 0 {
					t.Fatal("ignored cursor commit resurrected deleted subscription", after, writes(), e)
				}
			} else {
				want, _ := json.Marshal(replacement)
				got, _ := json.Marshal(after)
				if e != nil || string(want) != string(got) || writes() != 1 {
					t.Fatal("ignored cursor commit overwrote replacement", after, replacement, writes(), e)
				}
			}
		})
	}
}
