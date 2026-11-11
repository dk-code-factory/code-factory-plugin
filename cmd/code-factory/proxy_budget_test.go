package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProxyUnknownOutcomeDoesNotReplayMutation(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		io.WriteString(w, `{"error":"outcome_unknown"}`)
	}))
	defer backend.Close()
	inputR, inputW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inputR.Close()
	outputR, outputW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outputR.Close()
	defer outputW.Close()
	originalIn, originalOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inputR, outputW
	defer func() { os.Stdin, os.Stdout = originalIn, originalOut }()
	io.WriteString(inputW, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"session_chat","arguments":{"text":"private"}}}`+"\n")
	inputW.Close()
	err = proxy(context.Background(), strings.TrimPrefix(backend.URL, "http://"), strings.Repeat("f", 64))
	if err == nil || !strings.Contains(err.Error(), "outcome may be unknown") || calls.Load() != 1 {
		t.Fatal("proxy replayed or hid uncertain mutation", err, calls.Load())
	}
}
