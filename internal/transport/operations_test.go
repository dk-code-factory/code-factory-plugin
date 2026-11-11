package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"log"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type privateOperationPanic string

func TestOperationPanicRetainsOwnerDiagnosticsWithoutPrivatePayload(t *testing.T) {
	var diagnostics bytes.Buffer
	previous := slog.Default()
	previousWriter, previousFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&diagnostics, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})
	o := &operations{ctx: context.Background(), entries: map[string]*pendingOperation{}}
	release := make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	defer unblock()
	_, fields, _ := o.run(context.Background(), "private-tool", func(context.Context) (any, error) {
		<-release
		panic(privateOperationPanic("owner_token=private-credential request=private-body"))
	})
	id := operationID(t, fields)
	unblock()
	waitOperationSignal(t, o.entries[id].done)
	result, _, _ := o.poll(id)
	message := result.Content[0].(*mcp.TextContent).Text
	log := diagnostics.String()
	for _, expected := range []string{"operation_id", id, "private-tool", "transport.privateOperationPanic", "stack", "runtime/debug.Stack", "TestOperationPanicRetainsOwnerDiagnosticsWithoutPrivatePayload"} {
		if !strings.Contains(log, expected) {
			t.Fatalf("owner diagnostics missing %q: %s", expected, log)
		}
	}
	for _, private := range []string{"private-credential", "private-body", "owner_token="} {
		if strings.Contains(message, private) || strings.Contains(log, private) {
			t.Fatal("panic payload escaped into response or diagnostics")
		}
	}
	if !result.IsError || !strings.Contains(message, "inspect saved state") {
		t.Fatal("panic did not return a safe uncertain-operation error", message)
	}
}

func waitOperationSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("operation signal did not arrive")
	}
}

func operationID(t *testing.T, fields map[string]any) string {
	t.Helper()
	operation, ok := fields["operation"].(map[string]any)
	if !ok {
		t.Fatalf("expected pending operation: %#v", fields)
	}
	return operation["id"].(string)
}

func TestOperationsRetainExactResultWithoutRepeatingMutation(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := &operations{ctx: lifetime, entries: map[string]*pendingOperation{}}
	var calls atomic.Int32
	release := make(chan struct{})
	request, disconnect := context.WithCancel(context.Background())
	result, fields, err := o.run(request, "mutation", func(ctx context.Context) (any, error) {
		calls.Add(1)
		select {
		case <-release:
			return map[string]any{"saved": true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	id := operationID(t, fields)
	disconnect()
	for n := 0; n < 3; n++ {
		_, pending, _ := o.poll(id)
		if operationID(t, pending) != id {
			t.Fatal("poll changed operation identity")
		}
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		result, fields, err = o.poll(id)
		if fields["operation"] == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion missing")
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil || result.IsError || fields["data"].(map[string]any)["saved"] != true || calls.Load() != 1 {
		t.Fatal("accepted action was lost or repeated", result, fields, err, calls.Load())
	}
	// A lost polling response may safely be read again, including nil results.
	for n := 0; n < 3; n++ {
		r, f, e := o.poll(id)
		if r.IsError || e != nil || f["data"].(map[string]any)["saved"] != true || calls.Load() != 1 {
			t.Fatal("completed polling repeated work", r, f, e)
		}
	}
}

func TestOperationsBoundCapacityExpiryCancellationAndErrors(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := &operations{ctx: lifetime, entries: map[string]*pendingOperation{}}
	for n := 0; n < operationCapacity; n++ {
		id := fmt.Sprint(n)
		o.entries[id] = &pendingOperation{id: id, done: make(chan struct{})}
	}
	called := false
	r, _, _ := o.run(context.Background(), "overflow", func(context.Context) (any, error) { called = true; return nil, nil })
	if !r.IsError || called {
		t.Fatal("capacity admitted work")
	}
	for _, entry := range o.entries {
		entry.finished = time.Now().Add(-operationRetention)
		close(entry.done)
	}
	r, f, _ := o.run(context.Background(), "inline", func(context.Context) (any, error) { return nil, nil })
	if r.IsError || f["operation"] != nil {
		t.Fatal("expired entries did not release capacity", r, f)
	}
	if _, ok := f["data"]; !ok {
		t.Fatal("nil success lost data field")
	}
	r, _, _ = o.poll("0")
	if !r.IsError {
		t.Fatal("expired result appeared successful")
	}
	request, stop := context.WithCancel(context.Background())
	stop()
	r, _, _ = o.run(request, "cancelled", func(context.Context) (any, error) { called = true; return nil, nil })
	if !r.IsError || called {
		t.Fatal("already cancelled request started work")
	}

	for _, panics := range []bool{false, true} {
		entered := make(chan struct{})
		release := make(chan struct{})
		_, f, _ := o.run(context.Background(), "failure", func(ctx context.Context) (any, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if panics {
				panic("private panic")
			}
			return nil, errors.New("original business error")
		})
		id := operationID(t, f)
		waitOperationSignal(t, entered)
		close(release)
		waitOperationSignal(t, o.entries[id].done)
		r, _, _ = o.poll(id)
		if !r.IsError {
			t.Fatal("delayed error was reported as success")
		}
		message := r.Content[0].(*mcp.TextContent).Text
		if panics {
			if strings.Contains(message, "private panic") || !strings.Contains(message, "inspect saved state") {
				t.Fatal("unsafe panic result", message)
			}
		} else if message != "original business error" {
			t.Fatal("original error changed", message)
		}

	}
	entered := make(chan struct{})
	finished := make(chan struct{})
	_, f, _ = o.run(context.Background(), "shutdown", func(ctx context.Context) (any, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	})
	id := operationID(t, f)
	waitOperationSignal(t, entered)
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel accepted work")
	}
	waitOperationSignal(t, o.entries[id].done)
	r, _, _ = o.poll(id)
	if !r.IsError {
		t.Fatal("shutdown reported success")
	}
	called = false
	r, _, _ = o.run(context.Background(), "after-shutdown", func(context.Context) (any, error) { called = true; return nil, nil })
	if !r.IsError || called {
		t.Fatal("shutdown admitted another mutation")
	}
	restarted := &operations{ctx: context.Background(), entries: map[string]*pendingOperation{}}
	r, _, _ = restarted.poll(id)
	if !r.IsError {
		t.Fatal("restart guessed operation success")
	}
}
