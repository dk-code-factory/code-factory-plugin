package factory

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type refreshBarrier struct {
	providers.GitHubSource
	entered, release chan struct{}
	item             domain.Item
	fail             bool
	releaseOnce      sync.Once
}

func (g *refreshBarrier) finish() { g.releaseOnce.Do(func() { close(g.release) }) }
func (g *refreshBarrier) PullRequest(ctx context.Context, _ string, _ int) (domain.Item, error) {
	close(g.entered)
	select {
	case <-g.release:
	case <-ctx.Done():
		return domain.Item{}, ctx.Err()
	}
	if g.fail {
		return domain.Item{}, fmt.Errorf("fixture unavailable")
	}
	return g.item, nil
}

func TestRetainedRefreshCannotOverwriteConcurrentWebhook(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			s, f, now := fixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var old domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &old))
			old.Updated = now.Add(-time.Hour)
			old.Body = "before"
			old.ClosingIssues = []string{"o/r#8"}
			old.PR = &domain.PullRequest{Head: old.Head, CI: "pending"}
			old.Revision = old.ContentRevision()
			must(t, s.Store.Put(ctx, "item", old.ID, old, ""))
			must(t, s.Store.Put(ctx, "factory-session", old.ID, Session{ID: old.ID, ItemID: old.ID, Kind: "pr", Execution: "idle", Pending: []Pending{}}, ""))
			stale := old
			stale.Title = "stale refresh"
			stale.PR = &domain.PullRequest{Head: old.Head, CI: "failure"}
			stale.ClosingIssues = []string{"o/r#9"}
			g := &refreshBarrier{entered: make(chan struct{}), release: make(chan struct{}), item: stale, fail: failure}
			s.App.GitHub = g
			defer g.finish()
			finished := make(chan error, 1)
			go func() { finished <- s.App.RefreshPullRequests(ctx) }()
			joined := false
			defer func() {
				g.finish()
				cancel()
				if !joined {
					select {
					case <-finished:
					case <-time.After(time.Second):
						t.Error("refresh goroutine did not stop during cleanup")
					}
				}
			}()
			select {
			case <-g.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("refresh did not reach provider")
			}
			body := bytes(map[string]any{"action": "synchronize", "repository": map[string]any{"full_name": "o/r"}, "pull_request": map[string]any{"number": 1, "title": "new title", "body": "new body", "state": "open", "head": map[string]any{"sha": "head-B"}, "labels": []any{map[string]string{"name": "new-label"}}, "updated_at": now.Format(time.RFC3339)}})
			d := delivery{ID: "newer-webhook", Type: "pull_request", Body: body, State: "pending", At: *now}
			must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
			must(t, s.processDelivery(ctx, d))
			// Preserve additional current context and automatic links committed while the
			// request was in flight, even on the provider error/backoff path.
			var current domain.Item
			must(t, s.Store.Get(ctx, "item", old.ID, &current))
			current.Activity = []domain.Activity{{ID: "comment:7", Body: "new discussion"}}
			current.PR = &domain.PullRequest{Head: current.Head, CI: "success", Checks: []domain.Check{{Name: "new-check"}}}
			current.Revision = current.ContentRevision()
			must(t, s.Store.Put(ctx, "item", old.ID, current, ""))
			link := domain.Relationship{ID: "implementation:" + old.ID + "->o/r#8", Source: old.ID, Target: "o/r#8", Kind: "implementation"}
			must(t, s.Store.Put(ctx, "relationship", link.ID, link, ""))
			g.finish()
			select {
			case err := <-finished:
				joined = true
				must(t, err)
			case <-ctx.Done():
				t.Fatal("refresh did not finish after provider release", ctx.Err())
			}
			var saved domain.Item
			must(t, s.Store.Get(ctx, "item", old.ID, &saved))
			if string(bytes(saved)) != string(bytes(current)) {
				t.Fatal("in-flight refresh rolled back current context", saved)
			}
			var retained domain.Relationship
			must(t, s.Store.Get(ctx, "relationship", link.ID, &retained))
			if s.Store.Get(ctx, "relationship", "implementation:"+old.ID+"->o/r#9", &retained) != store.ErrNotFound {
				t.Fatal("stale refresh changed automatic links")
			}
			// The queued head event and full context delivered to the retained agent agree.
			_, err := s.Chat(ctx, old.ID, "continue", nil)
			must(t, err)
			v := get(t, s)
			var b Batch
			must(t, s.Store.Get(ctx, "factory-batch", v.BatchID, &b))
			if b.Context.Head != "head-B" || b.Context.Body != "new body" || len(b.Context.Activity) != 1 || b.Context.PR.CI != "success" || len(b.Events) != 1 || len(f.inputs) != 1 {
				t.Fatal("batch context differs from current webhook", b)
			}
		})
	}
}

type eventTransport func(*http.Request) (*http.Response, error)

func (f eventTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBlockedAndAnsweredTransitionsEmitSessionSubscription(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	vault, err := store.NewVault(s.Store, s.App.DataDir)
	must(t, err)
	ev := events.New(s.Store, vault)
	delivered := 0
	ev.Client = &http.Client{Transport: eventTransport(func(r *http.Request) (*http.Response, error) {
		b, e := io.ReadAll(r.Body)
		if e != nil {
			return nil, e
		}
		var payload map[string]any
		if e = json.Unmarshal(b, &payload); e != nil {
			return nil, e
		}
		response := "{}"
		if payload["type"] == "verification" {
			response = fmt.Sprintf(`{"challenge":%q}`, payload["challenge"])
		} else {
			delivered++
			if payload["name"] != "session.changed" || strings.Contains(string(b), "private-answer") || strings.Contains(string(b), "private-question") {
				t.Error("incorrect or content-bearing session notification", string(b))
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	_, err = ev.Subscribe(ctx, events.SubscribeInput{Name: "session.changed", Arguments: map[string]string{"entity": v.ID}, Delivery: events.Delivery{Mode: "webhook", URL: "https://callback.example/events", Secret: "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))}})
	must(t, err)
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes("blocked"), Method: "item/tool/call", Params: bytes(map[string]any{"threadId": v.ThreadID, "tool": "code_factory_report_status", "arguments": map[string]any{"state": "blocked", "summary": "private-question", "questions": []string{"private-question"}}})})
	must(t, ev.Tick(ctx))
	if delivered != 1 {
		t.Fatal("blocked transition omitted session subscription", delivered)
	}
	qs, err := s.Questions(ctx)
	must(t, err)
	must(t, s.Answer(ctx, qs[0].ID, bytes(map[string]string{"answer": "private-answer"})))
	must(t, ev.Tick(ctx))
	if delivered != 2 {
		t.Fatal("answer transition omitted session subscription", delivered)
	}
}

func TestAnswerPreservesOtherExplicitBlockedRequests(t *testing.T) {
	for _, nativeBlocking := range []bool{false, true} {
		t.Run(fmt.Sprint(nativeBlocking), func(t *testing.T) {
			s, _, _ := fixture(t)
			ctx := context.Background()
			v, err := s.Chat(ctx, "o/r#1", "start", nil)
			must(t, err)
			s.handle(v.ID, v.Epoch, codex.Message{ID: bytes("native"), Method: "item/tool/requestUserInput", Params: bytes(map[string]any{"threadId": v.ThreadID, "isBlocking": nativeBlocking, "questions": []any{map[string]any{"id": "q", "isOther": true}}})})
			for _, id := range []string{"status-A", "status-B"} {
				s.handle(v.ID, v.Epoch, codex.Message{ID: bytes(id), Method: "item/tool/call", Params: bytes(map[string]any{"threadId": v.ThreadID, "tool": "code_factory_report_status", "arguments": map[string]any{"state": "blocked", "summary": id, "questions": []string{"Choose"}}})})
			}
			qs, err := s.Questions(ctx)
			must(t, err)
			byRequest := map[string]Question{}
			for _, q := range qs {
				byRequest[string(q.RequestID)] = q
			}
			// Rejecting an invalid answer cannot release either work or attention.
			if s.Answer(ctx, byRequest[string(bytes("native"))].ID, bytes(map[string]any{"invalid": true})) == nil {
				t.Fatal("invalid answer accepted")
			}
			must(t, s.Answer(ctx, byRequest[string(bytes("native"))].ID, bytes(map[string]any{"answers": map[string]any{"q": map[string]any{"answers": []string{"chosen"}}}})))
			if actual := get(t, s); actual.Reported != "blocked" || actual.Execution != "waiting" {
				t.Fatal("native answer cleared explicit block", actual)
			}
			must(t, s.Answer(ctx, byRequest[string(bytes("status-A"))].ID, bytes(map[string]string{"answer": "A"})))
			if actual := get(t, s); actual.Reported != "blocked" || actual.Execution != "waiting" {
				t.Fatal("first explicit answer cleared second block", actual)
			}
			must(t, s.Answer(ctx, byRequest[string(bytes("status-B"))].ID, bytes(map[string]string{"answer": "B"})))
			if actual := get(t, s); actual.Reported != "working" || actual.Execution != "running" {
				t.Fatal("last explicit answer did not resume", actual)
			}
		})
	}
}

func TestRecoveryChoicesRequireHistoryBeforeDiscardOrRetry(t *testing.T) {
	for _, decision := range []string{"retry", "discard"} {
		for _, mode := range []string{"unavailable", "delivered", "absent"} {
			t.Run(decision+"/"+mode, func(t *testing.T) {
				s, f, now := fixture(t)
				ctx := context.Background()
				must(t, s.Observe(ctx, []Event{incoming(s, "head", "a")}, false))
				*now = now.Add(120 * time.Second)
				f.fail = "turn/start"
				must(t, s.Tick(ctx))
				v := get(t, s)
				batchID := v.BatchID
				before := string(bytes(v))
				f.fail = ""
				if mode == "unavailable" {
					f.fail = "thread/items/list"
				}
				if mode == "delivered" {
					f.history = []json.RawMessage{bytes(map[string]any{"text": "[code-factory-batch:" + batchID + "]"})}
				}
				err := s.ResolveDelivery(ctx, v.ID, decision)
				actual := get(t, s)
				var b Batch
				must(t, s.Store.Get(ctx, "factory-batch", batchID, &b))
				switch mode {
				case "absent":
					must(t, err)
					want := 0
					if decision == "retry" {
						want = 1
					}
					if actual.BatchID != "" || !actual.Hold || len(actual.Pending) != want || b.State != decision {
						t.Fatal("confirmed absent recovery state", actual, b)
					}
				case "unavailable":
					if err == nil || string(bytes(actual)) != before || b.State != "uncertain" {
						t.Fatal("unavailable history changed queue/claim", actual, b)
					}
				case "delivered":
					if err == nil || actual.BatchID != batchID || len(actual.Pending) != 0 || b.State != "delivered" {
						t.Fatal("confirmed delivery discarded or retried", actual, b)
					}
				}
				if len(f.inputs) != 0 {
					t.Fatal("recovery repeated uncertain send")
				}
			})
		}
	}
}

func TestUnchangedRetainedRefreshStillAppliesProviderResult(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			s, _, _ := fixture(t)
			ctx := context.Background()
			var old domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &old))
			must(t, s.Store.Put(ctx, "factory-session", old.ID, Session{ID: old.ID, ItemID: old.ID, Execution: "idle"}, ""))
			fresh := old
			fresh.Title = "new provider title"
			fresh.PR = &domain.PullRequest{Head: old.Head, CI: "success"}
			fresh.ClosingIssues = []string{"o/r#9"}
			g := &refreshBarrier{entered: make(chan struct{}), release: make(chan struct{}), item: fresh, fail: failure}
			g.finish()
			s.App.GitHub = g
			must(t, s.App.RefreshPullRequests(ctx))
			var actual domain.Item
			must(t, s.Store.Get(ctx, "item", old.ID, &actual))
			if failure {
				if actual.Title != old.Title || actual.PR == nil || actual.PR.Error == "" || actual.PR.RetryAt.IsZero() {
					t.Fatal("ordinary failed refresh did not retain/backoff", actual)
				}
			} else {
				if actual.Title != fresh.Title || actual.PR.CI != "success" {
					t.Fatal("unchanged row rejected valid provider refresh", actual)
				}
				var l domain.Relationship
				must(t, s.Store.Get(ctx, "relationship", "implementation:"+old.ID+"->o/r#9", &l))
			}
		})
	}
}

func TestCIHeadDiscoveryProjectsBeforeDecodingUnrelatedArchives(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	config, err := s.Settings(ctx)
	must(t, err)
	config.Rules[0].Types = []string{"ci"}
	must(t, s.Store.Put(ctx, "factory-settings", "effective", config, ""))
	live := map[int]domain.Item{}
	for _, n := range []int{1, 2, 3, 4, 5} {
		i := domain.Item{ID: fmt.Sprintf("o/r#%d", n), Number: n, Repository: "o/r", Kind: "pr", State: "open", Head: "shared", PR: &domain.PullRequest{Head: "shared", CI: "success"}}
		live[n] = i
		if n != 3 && n != 4 {
			saved := i
			if n == 5 {
				saved.Head = "shared"
				i.Head = "moved"
				i.PR.Head = "moved"
				live[n] = i
			}
			must(t, s.Store.Put(ctx, "item", saved.ID, saved, ""))
		}
	}
	moved := live[4]
	moved.Head = "different"
	live[4] = moved
	for n := 0; n < 120; n++ {
		row := map[string]any{"id": fmt.Sprintf("poison:%03d", n), "number": n + 100, "repository": "else/repo", "kind": "pr", "head": "shared", "body": strings.Repeat("archived", 4096), "activity": "invalid typed archive", "checks": map[string]bool{"poison": true}}
		if n%3 == 1 {
			row["repository"] = "o/r"
			row["kind"] = "issue"
		}
		if n%3 == 2 {
			row["repository"] = "o/r"
			row["head"] = "other"
		}
		must(t, s.Store.Put(ctx, "item", row["id"].(string), row, ""))
	}
	s.App.GitHub = &fixtureGitHub{items: live}
	for burst := 0; burst < 3; burst++ {
		d := delivery{ID: fmt.Sprintf("ci-%d", burst), Type: "check_run", State: "pending", At: s.Now(), Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "check_run": map[string]any{"head_sha": "shared", "pull_requests": []any{map[string]int{"number": 3}, map[string]int{"number": 4}, map[string]int{"number": 3}, map[string]int{"number": 0}}}})}
		must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
		must(t, s.processDelivery(ctx, d))
	}
	sessions, err := s.Sessions(ctx)
	must(t, err)
	if len(sessions) != 3 {
		t.Fatal("CI association omitted matching PR or included moved/wrong head", len(sessions))
	}
	for _, v := range sessions {
		if v.ID != "o/r#1" && v.ID != "o/r#2" && v.ID != "o/r#3" {
			t.Fatal("unrelated/moved PR associated", v.ID)
		}
		if len(v.Pending) != 1 || v.Pending[0].Event.Type != "ci" {
			t.Fatal("CI burst duplicated normalized observation", v)
		}
	}
}

func TestIncomingPriorityRelationshipsInvalidateCacheAndLateToolRecords(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.PrioritySkill = "any-installed-skill"
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	must(t, s.projectSettings(ctx))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	policy, err := s.App.Config(ctx)
	must(t, err)
	record := func(v Session, contextID string) error {
		_, _, e := s.dynamic(ctx, &v, "code_factory_record_priority", bytes(map[string]any{"itemId": item.ID, "revision": item.Revision, "context": contextID, "policy": policy.PolicyVersion, "priority": 1, "rationale": "context evidence"}), codex.Message{})
		return e
	}
	complete := func() string {
		t.Helper()
		v, e := s.Session(ctx, "priority:o/r")
		must(t, e)
		contextID := s.App.AssessmentContext(ctx, item)
		must(t, record(v, contextID))
		s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]string{"id": v.TurnID, "status": "completed"}})})
		must(t, s.AssessNext(ctx))
		return contextID
	}
	must(t, s.AssessNext(ctx))
	last := complete()
	if len(f.inputs) != 1 {
		t.Fatal("valid initial assessment not cached")
	}
	ticket := domain.Ticket{ID: "TEAM-1", Summary: "related urgency"}
	must(t, s.Store.Put(ctx, "ticket", ticket.ID, ticket, ""))
	link := domain.Relationship{ID: "incoming", Source: ticket.ID, Target: item.ID, Kind: "local", Manual: true}
	mutations := []func(){func() { must(t, s.Store.Put(ctx, "relationship", link.ID, link, "")) }, func() { ticket.Status = "critical"; must(t, s.Store.Put(ctx, "ticket", ticket.ID, ticket, "")) }, func() {
		link.Evidence = "changed context"
		must(t, s.Store.Put(ctx, "relationship", link.ID, link, ""))
	}, func() {
		must(t, s.Store.Write(ctx, func(tx *store.Tx) error { return tx.Delete("relationship", link.ID) }))
	}}
	for n, mutate := range mutations {
		mutate()
		v, e := s.Session(ctx, "priority:o/r")
		must(t, e)
		if record(v, last) == nil {
			t.Fatal("late assessment accepted after incoming relationship mutation", n)
		}
		rows, e := s.App.InboxSummaries(ctx, "all")
		must(t, e)
		if len(rows) != 1 || rows[0].PrioritySource != "pending" {
			t.Fatal("incoming relationship mutation retained AI cache", n, rows)
		}
		must(t, s.AssessNext(ctx))
		if len(f.inputs) != n+2 {
			t.Fatal("bookkeeping did not reassess changed incoming context", n, len(f.inputs))
		}
		last = complete()
		if len(f.inputs) != n+2 {
			t.Fatal("fresh changed-context assessment not cached", n)
		}
	}
	starts := 0
	for _, m := range f.methods {
		if m == "thread/start" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatal("relationship changes recreated bookkeeping thread")
	}
	// Existing precedence is preserved even when AI context is stale.
	item.Labels = []string{"P0"}
	item.Revision = item.ContentRevision()
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	must(t, s.AssessNext(ctx))
	rows, e := s.App.InboxSummaries(ctx, "all")
	must(t, e)
	if rows[0].PrioritySource != "label" || *rows[0].Priority != 0 {
		t.Fatal("mapped label precedence lost", rows)
	}
	override := 3
	must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID, Override: &override}, ""))
	must(t, s.AssessNext(ctx))
	rows, e = s.App.InboxSummaries(ctx, "all")
	must(t, e)
	if rows[0].PrioritySource != "override" || *rows[0].Priority != 3 || len(f.inputs) != 5 {
		t.Fatal("manual precedence lost or unnecessary assessment launched", rows, len(f.inputs))
	}
}
