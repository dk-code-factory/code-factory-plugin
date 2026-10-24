package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDependencyAdminRequiresOwnerAndRejectsAmbiguousRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &app.App{Store: db, DataDir: dir, GitHub: providers.NewGitHub()}
	a.Runs = app.NewManager(ctx, a, testutil.FakeCodex(t))
	defer a.Runs.Close()
	if err = a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Server{App: a}
	token := strings.Repeat("x", 64)
	server := httptest.NewServer(s.DependencyHandler(token))
	defer server.Close()
	request := func(method, path, body, authorization, origin string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("admin response cacheable")
		}
		return res.StatusCode, data
	}
	for _, auth := range []string{"", "Bearer wrong"} {
		if status, _ := request("GET", "/admin/dependencies", "", auth, ""); status != 401 {
			t.Fatal(status)
		}
	}
	if status, _ := request("GET", "/admin/dependencies", "", "Bearer "+token, "https://unrelated.example"); status != 403 {
		t.Fatal(status)
	}
	for _, body := range []string{`{"id":"one","Id":"two","action":"check"}`, `{"id":"one","id":"two","action":"check"}`, `{"id":"one","action":"check","command":"echo injected"}`, `{"id":"one","action":"check","targets":["unknown"]}`, `{"id":"one","action":"check"} {}`, `{"id":"one","action":"check","targets":{}}`} {
		if status, _ := request("POST", "/admin/dependencies/operations", body, "Bearer "+token, ""); status != 400 {
			t.Fatal(status, body)
		}
	}
	if status, _ := request("GET", "/admin/dependencies/operations/missing", "", "Bearer "+token, ""); status != 404 {
		t.Fatal(status)
	}
	body := `{"id":"owner-check","action":"check","targets":["skills"]}`
	if status, _ := request("POST", "/admin/dependencies/operations", body, "Bearer "+token, ""); status != 202 {
		t.Fatal(status)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		status, data := request("GET", "/admin/dependencies/operations/owner-check", "", "Bearer "+token, "")
		if status != 200 {
			t.Fatal(status)
		}
		var op app.DependencyOperation
		if json.Unmarshal(data, &op) != nil {
			t.Fatal("invalid operation")
		}
		if op.Status != "queued" && op.Status != "running" {
			if op.Status != "succeeded" {
				t.Fatal(op)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("operation did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status, _ := request("POST", "/admin/dependencies/operations", body, "Bearer "+token, ""); status != 202 {
		t.Fatal("idempotent operation failed", status)
	}
	rows, err := store.List[app.DependencyOperation](ctx, db, "dependency-operation")
	if err != nil || len(rows) != 1 {
		t.Fatal("duplicate operation", rows, err)
	}
	if status, data := request("GET", "/admin/dependencies", "", "Bearer "+token, ""); status != 200 || !bytes.Contains(data, []byte("externalDependencies")) {
		t.Fatal(status, string(data))
	}
}
