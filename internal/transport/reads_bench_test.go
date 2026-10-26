package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/store"
)

// Opt in explicitly: this measures real loopback MCP/JSON encoding separately
// from caller time, using synthetic pilot counts and no production database.
func TestReadPerformancePilotFixture(t *testing.T) {
	if os.Getenv("CF_READ_BENCH") != "1" {
		t.Skip("set CF_READ_BENCH=1 for loopback MCP performance measurements")
	}
	f := newTrackingFixture(t)
	ctx, db := f.ctx, f.a.Store
	now := time.Now().UTC()
	if err := db.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Put("repository", "pilot/repo", domain.Repository{ID: "pilot/repo", Paused: true}); err != nil {
			return err
		}
		if err := tx.Put("identity", "github", domain.GitHubViewer{Login: "pilot"}); err != nil {
			return err
		}
		for n := 1; n <= 452; n++ {
			id := fmt.Sprintf("pilot/repo#%d", n)
			i := domain.Item{ID: id, Repository: "pilot/repo", Number: n, Kind: "pr", State: "open", Title: fmt.Sprintf("Pilot review %d", n), Body: strings.Repeat("source context ", 500), Head: "fixture-head", Updated: now, DetailSyncedAt: now, Activity: []domain.Activity{{Body: strings.Repeat("retained history ", 2000)}}, PR: &domain.PullRequest{Head: "fixture-head", CI: "success", ReviewDecision: "approved", SyncedAt: now}}
			if n <= 11 {
				i.ReviewRequests = []string{"pilot"}
			}
			if err := tx.Put("item", id, i); err != nil {
				return err
			}
			if err := tx.Put("user", id, domain.UserState{ID: id}); err != nil {
				return err
			}
		}
		for n := 0; n < 1372; n++ {
			l := domain.Relationship{ID: fmt.Sprintf("link%04d", n), Source: fmt.Sprintf("pilot/repo#%d", n%452+1), Target: fmt.Sprintf("pilot/repo#%d", (n+1)%452+1)}
			if err := tx.Put("relationship", l.ID, l); err != nil {
				return err
			}
		}
		for n := 0; n < 265; n++ {
			id := fmt.Sprintf("outside/repo#%d", n)
			if err := tx.Put("linked-pr", id, domain.Item{ID: id, Kind: "pr", Activity: []domain.Activity{{Body: strings.Repeat("linked history ", 4000)}}}); err != nil {
				return err
			}
		}
		for n := 0; n < 32; n++ {
			r := domain.Run{ID: fmt.Sprintf("run%02d", n), ItemID: fmt.Sprintf("pilot/repo#%d", n+1), Status: "idle", Created: now, Prompt: strings.Repeat("prepared instructions ", 900), Skills: []domain.Skill{{ID: "fixture", Content: strings.Repeat("skill ", 1000), Files: map[string]string{"reference": strings.Repeat("retained bundle ", 40000)}}}}
			if err := tx.Put("run", r.ID, r); err != nil {
				return err
			}
		}
		return tx.Put("pr-reviews", "pilot/repo#1", domain.PRReviews{ItemID: "pilot/repo#1", SyncedAt: now, LastAttempt: now, Reviews: []domain.PublishedReview{}})
	}); err != nil {
		t.Fatal(err)
	}
	handler := f.server.Handler(f.token)
	measured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		start := time.Now()
		handler.ServeHTTP(rec, r)
		elapsed := time.Since(start)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.Header().Set("X-Fixture-Server-Nanos", strconv.FormatInt(elapsed.Nanoseconds(), 10))
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	defer measured.Close()
	type sample struct {
		ServerMS float64 `json:"serverMs"`
		CallerMS float64 `json:"callerMs"`
		Bytes    int     `json:"bytes"`
	}
	call := func(name string, args map[string]any) (sample, error) {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		req, _ := http.NewRequest("POST", measured.URL+"/mcp", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		req.Header.Set("Authorization", "Bearer "+f.token)
		start := time.Now()
		res, err := measured.Client().Do(req)
		if err != nil {
			return sample{}, err
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		elapsed := time.Since(start)
		ns, _ := strconv.ParseInt(res.Header.Get("X-Fixture-Server-Nanos"), 10, 64)
		var out struct {
			Result struct{ IsError bool }
			Error  any
		}
		if json.Unmarshal(b, &out) != nil || out.Error != nil || out.Result.IsError {
			err = fmt.Errorf("%s returned MCP error", name)
		}
		return sample{float64(ns) / 1e6, float64(elapsed.Nanoseconds()) / 1e6, len(b)}, err
	}
	args := map[string]map[string]any{"open_factory": {}, "list_inbox": {"queue": "review"}, "read_item": {"id": "pilot/repo#1"}, "list_runs": {}, "read_run": {"id": "run00"}, "read_pr_reviews": {"id": "pilot/repo#1"}, "track_work": {"target": "repo#1"}, "list_attention": {}}
	baseline := os.Getenv("CF_READ_BASELINE") == "1"
	names := []string{"open_factory", "list_inbox", "read_item", "list_runs", "read_run", "read_pr_reviews", "list_attention"}
	if !baseline {
		names = append(names, "track_work")
	}
	quantile := func(values []float64, p float64) float64 {
		sort.Float64s(values)
		return values[min(len(values)-1, int(float64(len(values))*p))]
	}
	report := map[string]any{"fixture": map[string]int{"items": 452, "relationships": 1372, "externalPRs": 265, "runs": 32}, "samplesPerAPI": 20, "scope": "synthetic loopback MCP; server includes tool execution and JSON encoding; caller includes local HTTP and body read; no ChatGPT orchestration or WAN"}
	api := map[string]any{}
	for _, name := range names {
		serverTimes, callerTimes := []float64{}, []float64{}
		maxBytes := 0
		for n := 0; n < 20; n++ {
			s, err := call(name, args[name])
			if err != nil {
				t.Fatal(err)
			}
			serverTimes = append(serverTimes, s.ServerMS)
			callerTimes = append(callerTimes, s.CallerMS)
			maxBytes = max(maxBytes, s.Bytes)
		}
		p95 := quantile(serverTimes, .95)
		api[name] = map[string]any{"serverP50Ms": quantile(serverTimes, .5), "serverP95Ms": p95, "callerP50Ms": quantile(callerTimes, .5), "callerP95Ms": quantile(callerTimes, .95), "maxResponseBytes": maxBytes}
		if !baseline {
			limit := 500.
			if name == "track_work" {
				limit = 200
			}
			if p95 > limit {
				t.Errorf("%s p95 %.2f exceeds %.0f ms", name, p95, limit)
			}
			if name == "track_work" && maxBytes > 32<<10 {
				t.Errorf("track_work response exceeds 32 KiB: %d", maxBytes)
			}
		}
	}
	report["apis"] = api
	groups := [][]string{{"open_factory", "list_inbox", "list_runs"}, {"read_item", "read_run", "read_pr_reviews"}, {"open_factory", "list_inbox", "list_runs", "read_item", "read_run", "read_pr_reviews"}}
	groupReport := []map[string]any{}
	for _, group := range groups {
		wall := []float64{}
		serverTimes, callerTimes := []float64{}, []float64{}
		totalBytes := 0
		for n := 0; n < 10; n++ {
			start := time.Now()
			var wg sync.WaitGroup
			var mu sync.Mutex
			for _, name := range group {
				wg.Add(1)
				go func(name string) {
					defer wg.Done()
					s, err := call(name, args[name])
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						t.Error(err)
					}
					serverTimes = append(serverTimes, s.ServerMS)
					callerTimes = append(callerTimes, s.CallerMS)
					totalBytes += s.Bytes
				}(name)
			}
			wg.Wait()
			wall = append(wall, float64(time.Since(start).Nanoseconds())/1e6)
		}
		groupReport = append(groupReport, map[string]any{"tools": group, "samples": 10, "wallP95Ms": quantile(wall, .95), "individualServerP95Ms": quantile(serverTimes, .95), "individualCallerP95Ms": quantile(callerTimes, .95), "meanGroupResponseBytes": totalBytes / 10})
	}
	report["parallelGroups"] = groupReport
	report["groupProvenance"] = "The supplied plan did not name the original parallel memberships; these explicit dashboard/detail/all-six groups are reproducible substitutes."
	b, _ := json.MarshalIndent(report, "", "  ")
	if path := os.Getenv("CF_READ_BENCH_OUT"); path != "" {
		if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(string(b))
}
