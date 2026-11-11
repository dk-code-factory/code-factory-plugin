package codex

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func blockedPipeClient(t *testing.T) (*Client, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{stdin: w, writeGate: make(chan struct{}, 1), done: make(chan struct{}), pending: map[string]chan Message{}}
	t.Cleanup(func() { _ = c.Close(); _ = r.Close() })
	return c, r
}

func waitBlockedNativeWrite(t *testing.T, r *os.File) byte {
	t.Helper()
	if err := r.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var prefix [1]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		t.Fatal("native write did not reach pipe", err)
	}
	return prefix[0]
}

func awaitNativeCall(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("native call did not return after cancellation")
		return nil
	}
}

func TestBlockedNativeWriteHonorsDeadlineAndPreservesUncertainDelivery(t *testing.T) {
	c, r := blockedPipeClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- c.Call(ctx, "turn/start", map[string]any{"text": strings.Repeat("x", 512<<10)}, nil)
	}()
	waitBlockedNativeWrite(t, r)
	err := awaitNativeCall(t, result)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "delivery may be unknown") {
		t.Fatal("blocked write lost its deadline or uncertain delivery", err)
	}
	select {
	case <-c.done:
	default:
		t.Fatal("partial write left a corrupt stream available for reuse")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 {
		t.Fatal("cancelled native call retained its pending response")
	}
}

func TestQueuedNativeWriterCancelsWithoutWritingOrDisconnectingActiveWriter(t *testing.T) {
	c, r := blockedPipeClient(t)
	activeCtx, cancelActive := context.WithCancel(context.Background())
	defer cancelActive()
	active := make(chan error, 1)
	go func() {
		active <- c.Call(activeCtx, "active", map[string]any{"text": strings.Repeat("x", 512<<10)}, nil)
	}()
	prefix := waitBlockedNativeWrite(t, r)
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	defer cancelQueued()
	queued := make(chan error, 1)
	go func() { queued <- c.Call(queuedCtx, "queued-must-not-write", nil, nil) }()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		count := len(c.pending)
		c.mu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second writer did not queue behind blocked write")
		}
		time.Sleep(time.Millisecond)
	}
	cancelQueued()
	if err := awaitNativeCall(t, queued); !errors.Is(err, context.Canceled) {
		t.Fatal("queued writer ignored cancellation", err)
	}
	select {
	case <-c.done:
		t.Fatal("cancelling admission disconnected an unrelated active write")
	case err := <-active:
		t.Fatal("active writer stopped before its own cancellation", err)
	default:
	}
	cancelActive()
	if err := awaitNativeCall(t, active); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "delivery may be unknown") {
		t.Fatal("active write did not terminate honestly", err)
	}
	if err := r.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	remaining, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	written := string(append([]byte{prefix}, remaining...))
	if strings.Contains(written, "queued-must-not-write") {
		t.Fatal("cancelled queued request reached native pipe")
	}
}
