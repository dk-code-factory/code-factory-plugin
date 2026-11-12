package setup

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFetchDiagnosticsPollsAcceptedOperationAndPreservesFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			var mu sync.Mutex
			names := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Params struct {
						Name      string
						Arguments map[string]any
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				names = append(names, request.Params.Name)
				n := len(names)
				mu.Unlock()

				var result any
				response := map[string]any{"state": "ready"}
				if n == 2 {
					if request.Params.Name != "diagnostics" || request.Params.Arguments["instanceId"] != "fixture" || request.Params.Arguments["requestId"] == nil {
						t.Error("diagnostics lacks operation identity", request)
					}
					response = map[string]any{"state": "queued"}
				} else if n > 2 {
					if request.Params.Name != "operation_status" {
						t.Error("diagnostics resubmitted instead of polling", request)
					}
					if failure {
						response = map[string]any{"state": "failed", "error": "fixture failure"}
					} else {
						response = map[string]any{"state": "succeeded", "result": Diagnostics{Version: "fixture", Checks: []Check{{ID: "provider.codex", State: Verified}}}}
					}
				}
				result = map[string]any{"structuredContent": map[string]any{"data": map[string]any{"apiVersion": 2, "instanceId": "fixture", "response": response}}}

				_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
			}))
			defer server.Close()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "owner.token"), []byte(strings.Repeat("f", 64)), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := FetchDiagnostics(ctx, dir, strings.TrimPrefix(server.URL, "http://"))
			if failure {
				if err == nil {
					t.Fatal("asynchronous failure reported success")
				}
			} else if err != nil || result.Version != "fixture" || len(result.Checks) != 1 {
				t.Fatal("asynchronous diagnostics lost result", result, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(names) != 3 || names[0] != "open_factory" || names[1] != "diagnostics" || names[2] != "operation_status" {
				t.Fatal("unexpected caller sequence", names)
			}
		})
	}
}

func TestFetchDiagnosticsReadsExactChunksAndBusyStatusWithoutResubmission(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint("corrupt=", corrupt), func(t *testing.T) {
			want := Diagnostics{Version: "fixture"}
			for n := 0; n < 500; n++ {
				want.Checks = append(want.Checks, Check{ID: fmt.Sprint(n), State: Verified, Detail: strings.Repeat("exact 界<&", 40)})
			}
			data, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			version := fmt.Sprintf("%x", sha256.Sum256(data))
			chunkCount := (len(data) + (24 << 10) - 1) / (24 << 10)
			var identity string
			var mu sync.Mutex
			submissions, polls, chunks := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				var request struct {
					Params struct {
						Name      string
						Arguments struct {
							RequestID  string `json:"requestId"`
							InstanceID string `json:"instanceId"`
							Version    string `json:"version"`
							Index      int    `json:"index"`
						}
					}
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("invalid request")
					return
				}
				response := map[string]any{"state": "ready"}
				switch request.Params.Name {
				case "open_factory":
				case "diagnostics":
					submissions++
					identity = request.Params.Arguments.RequestID
					if identity == "" || request.Params.Arguments.InstanceID != "fixture" {
						t.Error("missing identity")
					}
					response["state"] = "queued"
				case "operation_status":
					polls++
					if request.Params.Arguments.RequestID != identity || request.Params.Arguments.InstanceID != "fixture" {
						t.Error("operation identity changed")
					}
					if polls == 1 {
						response["state"] = "busy"
					} else {
						response = map[string]any{"state": "succeeded", "result": map[string]any{"document": map[string]any{"version": version, "bytes": len(data), "chunks": chunkCount, "encoding": "base64-json"}}}
					}
				case "document_chunk":
					chunks++
					if request.Params.Arguments.Version != version {
						t.Error("document identity changed")
					}
					if chunks == 1 {
						response["state"] = "busy"
					} else {
						index := request.Params.Arguments.Index
						start := index * (24 << 10)
						part := append([]byte(nil), data[start:min(start+(24<<10), len(data))]...)
						if corrupt && index == chunkCount-1 {
							part[0] ^= 1
						}
						response = map[string]any{"state": "ready", "version": version, "index": index, "chunk": base64.StdEncoding.EncodeToString(part)}
					}
				default:
					t.Error("unexpected tool", request.Params.Name)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"structuredContent": map[string]any{"data": map[string]any{"apiVersion": 2, "instanceId": "fixture", "response": response}}}})
			}))
			defer server.Close()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "owner.token"), []byte(strings.Repeat("f", 64)), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			got, err := FetchDiagnostics(ctx, dir, strings.TrimPrefix(server.URL, "http://"))
			if corrupt {
				if err == nil {
					t.Fatal("corrupt document accepted")
				}
			} else if err != nil || len(got.Checks) != len(want.Checks) || got.Checks[len(got.Checks)-1].Detail != want.Checks[len(want.Checks)-1].Detail {
				t.Fatal("large diagnostics lost exact data", len(got.Checks), err)
			}
			mu.Lock()
			defer mu.Unlock()
			if submissions != 1 || polls != 2 || chunks != chunkCount+1 {
				t.Fatal("unexpected caller sequence", submissions, polls, chunks)
			}
		})
	}
}

func TestFetchDiagnosticsBoundsEntirePendingOperation(t *testing.T) {
	for _, state := range []string{"running", "busy"} {
		t.Run(state, func(t *testing.T) {
			var mu sync.Mutex
			polls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct{ Params struct{ Name string } }
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				response := map[string]any{"state": "ready"}
				if request.Params.Name == "diagnostics" {
					response["state"] = "queued"
				}
				if request.Params.Name == "operation_status" {
					mu.Lock()
					polls++
					mu.Unlock()
					response["state"] = state
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"structuredContent": map[string]any{"data": map[string]any{"apiVersion": 2, "instanceId": "fixture", "response": response}}}})
			}))
			defer server.Close()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "owner.token"), []byte(strings.Repeat("f", 64)), 0600); err != nil {
				t.Fatal(err)
			}
			// A request-level timeout alone cannot bound an endless stream of fast replies.
			started := time.Now()
			_, err := fetchDiagnostics(context.Background(), dir, strings.TrimPrefix(server.URL, "http://"), 600*time.Millisecond)
			if err == nil || time.Since(started) > 2*time.Second {
				t.Fatal("pending diagnostics exceeded total budget", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if polls < 2 {
				t.Fatal("fixture did not exercise repeated pending replies", polls)
			}
		})
	}
}
