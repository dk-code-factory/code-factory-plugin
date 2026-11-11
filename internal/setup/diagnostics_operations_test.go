package setup

import (
	"context"
	"encoding/json"
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
				if n == 1 {
					result = map[string]any{"structuredContent": map[string]any{"operation": map[string]any{"id": "accepted-id"}}}
				} else {
					if request.Params.Name != "operation_result" || request.Params.Arguments["id"] != "accepted-id" {
						t.Error("diagnostics resubmitted instead of polling", request)
					}
					if failure {
						result = map[string]any{"isError": true}
					} else {
						result = map[string]any{"structuredContent": map[string]any{"data": Diagnostics{Version: "fixture", Checks: []Check{{ID: "provider.codex", State: Verified}}}}}
					}
				}
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
			if len(names) != 2 || names[0] != "diagnostics" || names[1] != "operation_result" {
				t.Fatal("unexpected caller sequence", names)
			}
		})
	}
}
