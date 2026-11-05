package factory

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
	"context"
	"database/sql"
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

	"github.com/google/jsonschema-go/jsonschema"
)

func TestDynamicToolSchemasUseArrayRequired(t *testing.T) {
	s, f, _ := fixture(t)
	_, err := s.Chat(context.Background(), "configuration", "inspect and propose", nil)
	must(t, err)
	var start struct {
		DynamicTools []struct {
			Name        string
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"dynamicTools"`
	}
	must(t, json.Unmarshal(f.tools[0], &start))
	found := false
	for _, tool := range start.DynamicTools {
		var wire map[string]any
		must(t, json.Unmarshal(tool.InputSchema, &wire))
		if _, ok := wire["required"].([]any); !ok {
			t.Fatalf("%s required is not an array", tool.Name)
		}
		var schema jsonschema.Schema
		must(t, json.Unmarshal(tool.InputSchema, &schema))
		_, err = schema.Resolve(nil)
		must(t, err)
		if tool.Name == "code_factory_propose_configuration" {
			found = true
			if len(wire["required"].([]any)) != 0 {
				t.Fatal("either proposal shape must remain optional at schema boundary")
			}
		}
	}
	if !found {
		t.Fatal("configuration proposal tool missing")
	}
}

func TestCommentDeletionNormalizesOnceAcrossWebhookAndPolling(t *testing.T) {
	for _, kind := range []string{"comment", "review-comment"} {
		for _, source := range []string{"webhook", "poll"} {
			t.Run(kind+"/"+source, func(t *testing.T) {
				s, _, _ := fixture(t)
				ctx := context.Background()
				settings, _ := s.Settings(ctx)
				typ := "issue_comment"
				if kind == "review-comment" {
					typ = "pull_request_review_comment"
				}
				settings.Rules = []Rule{{ID: "delete", Repositories: []string{"*"}, Types: []string{typ}, Actions: []string{"deleted"}, Instructions: "handle removed discussion"}}
				must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
				var item domain.Item
				must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
				item.Activity = []domain.Activity{{ID: kind + ":7", Kind: kind, Body: "same body", Updated: "2026-11-05T11:00:00Z"}}
				item.Revision = item.ContentRevision()
				must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
				must(t, s.Observe(ctx, observations(item), true))
				fresh := item
				fresh.Activity = nil
				fresh.PR = &domain.PullRequest{Head: item.Head, CI: "success"}
				fresh.DetailSyncedAt = s.Now()
				fresh.Revision = fresh.ContentRevision()
				must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
				if source == "webhook" {
					payload := map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": "deleted", "comment": map[string]any{"id": 7, "body": "same body", "updated_at": "2026-11-05T11:00:00Z"}}
					if kind == "comment" {
						payload["issue"] = map[string]any{"number": 1, "pull_request": map[string]any{}}
					} else {
						payload["pull_request"] = map[string]any{"number": 1, "state": "open", "head": map[string]string{"sha": "head-1"}}
					}
					for _, id := range []string{"delete-1", "distinct-redelivery"} {
						d := delivery{ID: id, Type: typ, State: "pending", At: s.Now(), Body: bytes(payload)}
						must(t, s.Store.Put(ctx, "factory-delivery", id, d, ""))
						must(t, s.processDelivery(ctx, d))
					}
				} else {
					s.App.GitHub = &fixtureGitHub{items: map[int]domain.Item{1: fresh}}
					must(t, s.refreshDetails(ctx, settings))
				}
				v, err := s.Session(ctx, item.ID)
				must(t, err)
				if len(v.Pending) != 1 || v.Pending[0].Event.Action != "deleted" || len(v.Pending[0].Actions) != 1 {
					t.Fatal("delete did not match exactly once", v.Pending)
				}
				removed, err := s.deletedActivities(ctx, fresh, s.Now())
				must(t, err)
				if len(removed) != 0 {
					t.Fatal("poll repeated observed deletion")
				}
			})
		}
	}
}

type fairGitHub struct {
	providers.GitHubSource
	items map[string]domain.Item
	calls []string
}

func (g *fairGitHub) ItemDetail(_ context.Context, item domain.Item) (domain.Item, error) {
	g.calls = append(g.calls, item.ID)
	if value, ok := g.items[item.ID]; ok {
		return value, nil
	}
	return domain.Item{}, fmt.Errorf("fixture read denied")
}
func TestDetailRefreshAdvancesPastSkippedAndFailedItems(t *testing.T) {
	for _, mode := range []string{"paused", "removed", "failed"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := fixture(t)
			ctx := context.Background()
			settings, _ := s.Settings(ctx)
			must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
			g := &fairGitHub{items: map[string]domain.Item{}}
			s.App.GitHub = g
			targetID := "o/r#9"
			if mode != "failed" {
				targetID = "o/r#1"
				if mode == "paused" {
					settings.Repositories = append(settings.Repositories, Repository{ID: "a/r", Paused: true})
				}
			}
			for n := 1; n <= 8; n++ {
				id := fmt.Sprintf("o/r#%d", n)
				repo := "o/r"
				if mode != "failed" {
					id = fmt.Sprintf("a/r#%d", n)
					repo = "a/r"
				}
				must(t, s.Store.Put(ctx, "item", id, domain.Item{ID: id, Repository: repo, Number: n, Kind: "pr", State: "open"}, ""))
			}
			target := domain.Item{ID: targetID, Repository: "o/r", Number: 9, Kind: "pr", State: "open", Title: "eligible", DetailSyncedAt: s.Now()}
			must(t, s.Store.Put(ctx, "item", targetID, domain.Item{ID: targetID, Repository: "o/r", Number: 9, Kind: "pr", State: "open"}, ""))
			must(t, s.Store.Put(ctx, "user", targetID, domain.UserState{ID: targetID}, ""))
			g.items[targetID] = target
			first := s.refreshDetails(ctx, settings)
			if mode == "failed" && first == nil {
				t.Fatal("read failure was hidden")
			}
			_ = s.refreshDetails(ctx, settings)
			found := false
			for _, id := range g.calls {
				found = found || id == targetID
			}
			if !found {
				t.Fatal("eligible item starved behind skipped/failed predecessors", g.calls)
			}
		})
	}
}
func TestInitialDetailBaselineCannotLaunchUnprocessedHistory(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	items := map[int]domain.Item{}
	var repo domain.Repository
	must(t, s.Store.Get(ctx, "repository", "o/r", &repo))
	repo.Imported = true
	must(t, s.Store.Put(ctx, "repository", repo.ID, repo, ""))
	for n := 1; n <= 9; n++ {
		item := domain.Item{ID: fmt.Sprintf("o/r#%d", n), Repository: "o/r", Number: n, Kind: "pr", State: "open", Activity: []domain.Activity{{ID: fmt.Sprintf("comment:%d", n), Kind: "comment", Body: "historical"}}}
		item.Revision = item.ContentRevision()
		items[n] = item
		must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
		must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""))
	}
	s.App.GitHub = &fixtureGitHub{items: items}
	for n := 0; n < 2; n++ {
		must(t, s.Poll(ctx))
		sessions, err := s.Sessions(ctx)
		must(t, err)
		if len(sessions) != 0 {
			t.Fatal("background detail refresh launched initial history")
		}
	}
}
func TestElicitationSchemaAndRemainingBlockers(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	params := map[string]any{"threadId": v.ThreadID, "turnId": v.TurnID, "mode": "form", "message": "Name", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]string{"type": "string"}}, "required": []string{"name"}}}
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes("form"), Method: "mcpServer/elicitation/request", Params: bytes(params)})
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes("approval"), Method: "item/fileChange/requestApproval", Params: bytes(map[string]any{"threadId": v.ThreadID, "turnId": v.TurnID})})
	qs, err := s.Questions(ctx)
	must(t, err)
	var form, approval Question
	for _, q := range qs {
		if q.Method == "mcpServer/elicitation/request" {
			form = q
		} else {
			approval = q
		}
	}
	before := len(f.replies)
	for _, answer := range []any{map[string]any{"action": "accept"}, map[string]any{"action": "accept", "content": map[string]any{"name": 1}}} {
		if err = s.Answer(ctx, form.ID, bytes(answer)); err == nil {
			t.Fatal("invalid form reached native response")
		}
	}
	if len(f.replies) != before {
		t.Fatal("invalid form was delivered")
	}
	var saved Question
	must(t, s.Store.Get(ctx, "factory-question", form.ID, &saved))
	if saved.Status != "pending" {
		t.Fatal("invalid answer retired request")
	}
	must(t, s.Answer(ctx, form.ID, bytes(map[string]any{"action": "accept", "content": map[string]any{"name": "owner"}})))
	if get(t, s).Execution != "waiting" {
		t.Fatal("remaining blocking request reported running")
	}
	must(t, s.Answer(ctx, approval.ID, bytes(map[string]any{"decision": "decline"})))
	if get(t, s).Execution != "running" {
		t.Fatal("fully answered turn did not continue")
	}
	for _, action := range []string{"decline", "cancel"} {
		must(t, validateResponse(form, bytes(map[string]string{"action": action})))
	}
	url := form
	url.Params = bytes(map[string]string{"mode": "url"})
	must(t, validateResponse(url, bytes(map[string]string{"action": "accept"})))
}
func TestSlowAttachLeavesOtherSessionRequestsResponsive(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	item.ID = "o/r#2"
	item.Number = 2
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""))
	b, err := s.Chat(ctx, item.ID, "start B", nil)
	must(t, err)
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	s.Workspace = func(ctx context.Context, v Session) (string, error) {
		if v.ID == "o/r#1" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return s.Dir, nil
	}
	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := s.Chat(attachCtx, "o/r#1", "slow A", nil); finished <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("attachment never entered")
	}
	handled := make(chan struct{})
	go func() {
		s.handle(b.ID, b.Epoch, codex.Message{ID: bytes(1), Method: "item/fileChange/requestApproval", Params: bytes(map[string]any{"threadId": b.ThreadID})})
		close(handled)
	}()
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("A attachment blocked B native request")
	}
	qs, err := s.Questions(ctx)
	must(t, err)
	answered := make(chan error, 1)
	go func() { answered <- s.Answer(ctx, qs[0].ID, bytes(map[string]string{"decision": "decline"})) }()
	select {
	case err = <-answered:
		must(t, err)
	case <-time.After(time.Second):
		t.Fatal("A attachment blocked B answer")
	}
	interrupted := make(chan error, 1)
	go func() { _, err := s.Control(ctx, b.ID, "interrupt"); interrupted <- err }()
	select {
	case err = <-interrupted:
		must(t, err)
	case <-time.After(time.Second):
		t.Fatal("A attachment blocked B interrupt")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("slow attachment ignored cancellation")
	}
}
func TestCachedPrioritySkipsFullHistoryDecode(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	settings, _ := s.Settings(ctx)
	settings.PrioritySkill = "any-installed-skill"
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	must(t, s.projectSettings(ctx))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	policy, _ := s.App.Config(ctx)
	assessment := domain.Assessment{ID: item.ID, Revision: item.Revision, Context: s.App.AssessmentContext(ctx, item), Policy: policy.PolicyVersion, Priority: 1, Status: "complete"}
	must(t, s.Store.Put(ctx, "assessment", item.ID, assessment, ""))
	var raw map[string]any
	must(t, json.Unmarshal(bytes(item), &raw))
	raw["body"] = map[string]string{"unread": "must not decode"}
	raw["activity"] = "must not decode"
	must(t, s.Store.Put(ctx, "item", item.ID, raw, ""))
	must(t, s.AssessNext(ctx))
	if len(f.methods) != 0 {
		t.Fatal("cached item reached native inference", f.methods)
	}
}
func TestPollReadsOnlyDuePendingDeliveries(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	for n := 0; n < 40; n++ {
		must(t, s.Store.Put(ctx, "factory-delivery", fmt.Sprint(n), map[string]any{"id": fmt.Sprint(n), "state": "processed", "at": "invalid typed archive timestamp", "body": strings.Repeat("archive", 1000)}, ""))
	}
	must(t, s.Store.Put(ctx, "factory-delivery", "future", map[string]any{"id": "future", "state": "pending", "retryAt": s.Now().Add(time.Hour).Format(time.RFC3339), "at": "not due / not decoded"}, ""))
	d := delivery{ID: "due", Type: "ping", State: "pending", At: s.Now(), Body: bytes(map[string]any{})}
	must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
	must(t, s.Poll(ctx))
	must(t, s.Store.Get(ctx, "factory-delivery", d.ID, &d))
	if d.State != "processed" {
		t.Fatal("due delivery not processed")
	}
}

func TestCIRefreshPreservesDiscussionWhenProviderReturnsEmptyActivity(t *testing.T) {
	for _, kind := range []string{"check_run", "check_suite", "workflow_run", "status"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := fixture(t)
			ctx := context.Background()
			var saved domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &saved))
			saved.Activity = []domain.Activity{{ID: "comment:1", Kind: "comment", Body: "retained discussion"}, {ID: "review:2", Kind: "review", Body: "retained review"}, {ID: "commit:3", Kind: "commit", Body: "retained commit"}}
			must(t, s.Store.Put(ctx, "item", saved.ID, saved, ""))
			fresh := saved
			fresh.Activity = []domain.Activity{}
			fresh.PR = &domain.PullRequest{Head: saved.Head, CI: "success"}
			s.App.GitHub = &fixtureGitHub{items: map[int]domain.Item{1: fresh}}
			c, _ := s.Settings(ctx)
			c.Rules = []Rule{{ID: "ci", Repositories: []string{"*"}, Types: []string{"ci"}, Instructions: "observe CI"}}
			must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
			payload := map[string]any{"repository": map[string]string{"full_name": "o/r"}, "sha": saved.Head}
			if kind != "status" {
				payload[kind] = map[string]any{"head_sha": saved.Head, "pull_requests": []any{map[string]int{"number": 1}}}
			}
			d := delivery{ID: kind, Type: kind, State: "pending", At: s.Now(), Body: bytes(payload)}
			must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
			must(t, s.processDelivery(ctx, d))
			var got domain.Item
			must(t, s.Store.Get(ctx, "item", saved.ID, &got))
			if string(bytes(got.Activity)) != string(bytes(saved.Activity)) {
				t.Fatal("CI removed saved discussion")
			}
			v := get(t, s)
			if len(v.Pending) != 1 || len(v.Pending[0].Event.Context.Activity) != 0 {
				t.Fatal("CI event duplicated current discussion")
			}
			v, err := s.Chat(ctx, v.ID, "deliver CI", nil)
			must(t, err)
			var batch Batch
			must(t, s.Store.Get(ctx, "factory-batch", v.BatchID, &batch))
			if string(bytes(batch.Context.Activity)) != string(bytes(saved.Activity)) || len(batch.Events) != 1 {
				t.Fatal("delivered current batch omitted discussion")
			}
		})
	}
}

func TestSecretVaultFailureCannotBecomeUnpersistedRetry(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	vault, err := store.NewVault(s.Store, s.Dir)
	must(t, err)
	s.App.Vault = vault
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes(1), Method: "item/tool/requestUserInput", Params: bytes(map[string]any{"threadId": v.ThreadID, "questions": []any{map[string]any{"id": "credential", "question": "Private credential", "isSecret": true}}})})
	qs, err := s.Questions(ctx)
	must(t, err)
	q := qs[0]
	answer := bytes(map[string]any{"answers": map[string]any{"credential": map[string]any{"answers": []string{"retry-sensitive-fixture"}}}})
	must(t, s.Store.ReadView(ctx, func(view *store.Store) error {
		readonly, e := store.NewVault(view, s.Dir)
		if e != nil {
			return e
		}
		s.App.Vault = readonly
		defer func() { s.App.Vault = vault }()
		if e = s.Answer(ctx, q.ID, answer); e == nil {
			t.Fatal("failed vault write delivered secret")
		}
		return nil
	}))
	var pending Question
	must(t, s.Store.Get(ctx, "factory-question", q.ID, &pending))
	if pending.Status != "pending" || len(rpc.replies) != 0 || contains(s.secrets[v.ID], "retry-sensitive-fixture") {
		t.Fatal("failed persistence advanced request or in-memory commitment")
	}
	must(t, s.Answer(ctx, q.ID, answer))
	stored, err := vault.Get(ctx, "factory-redactions:"+v.ID)
	must(t, err)
	if !strings.Contains(stored, "retry-sensitive-fixture") || len(rpc.replies) != 1 {
		t.Fatal("retry was not durably committed before delivery")
	}
	s.Close()
	resumed := New(ctx, s.App, "unused")
	defer resumed.Close()
	resumed.Now = s.Now
	resumed.Workspace = s.Workspace
	resumed.Start = s.Start
	must(t, resumed.Init(ctx))
	v, err = resumed.Chat(ctx, v.ID, "follow-up", nil)
	must(t, err)
	resumed.handle(v.ID, v.Epoch, codex.Message{Method: "item/completed", Params: bytes(map[string]any{"threadId": v.ThreadID, "item": map[string]string{"id": "after-restart", "type": "agentMessage", "text": "echo retry-sensitive-fixture"}})})
	page, err := s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	if strings.Contains(string(bytes(page)), "retry-sensitive-fixture") {
		t.Fatal("retry secret leaked after fresh-service resumption")
	}
}

func TestPollFirstWebhookEnrichesActionsWithoutDuplicateRules(t *testing.T) {
	for _, kind := range []string{"requests", "created", "edited"} {
		for _, delivered := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/delivered=%v", kind, delivered), func(t *testing.T) {
				s, _, _ := fixture(t)
				ctx := context.Background()
				c, _ := s.Settings(ctx)
				typ, action, facet := "issue_comment", kind, "activity:comment:7"
				if kind == "requests" {
					typ, action, facet = "pull_request", "review_requested", "requests"
				}
				c.Rules = []Rule{{ID: "specific", Repositories: []string{"*"}, Types: []string{typ}, Actions: []string{action}, Instructions: "specific action"}, {ID: "general", Repositories: []string{"*"}, Types: []string{typ}, Instructions: "general action"}}
				must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
				var item domain.Item
				must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
				polled := event(item, typ, "observed", facet, "same observable state")
				if kind == "requests" {
					polled.Action = "review_requests_changed"
				}
				must(t, s.Observe(ctx, []Event{polled}, false))
				v := get(t, s)
				if len(v.Pending) != 1 || len(v.Pending[0].Actions) != 1 || v.Pending[0].Actions[0].RuleID != "general" {
					t.Fatal("poll did not authorize generic rule")
				}
				id := v.Pending[0].Event.ID
				if delivered {
					_, err := s.Chat(ctx, v.ID, "deliver current work", nil)
					must(t, err)
				}
				webhook := polled
				webhook.Source = "webhook"
				webhook.Action = action
				webhook.Delivery = "signed-delivery"
				must(t, s.Observe(ctx, []Event{webhook}, false))
				must(t, s.Observe(ctx, []Event{webhook}, false))
				v = get(t, s)
				if len(v.Pending) != 1 || v.Pending[0].Event.ID != id || v.Pending[0].Event.Action != action {
					t.Fatal("action did not enrich canonical event", v.Pending)
				}
				want := 2
				if delivered {
					want = 1
				}
				if len(v.Pending[0].Actions) != want || v.Pending[0].Actions[0].RuleID != "specific" {
					t.Fatal("specific action lost or general rule repeated", v.Pending)
				}
				events, err := s.Store.List(ctx, "factory-event")
				must(t, err)
				if len(events) != 1 {
					t.Fatal("source overlap created duplicate durable events")
				}
			})
		}
	}
}

func TestCompletedOutputKeepsAgentMessagesAndByteBounds(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	before := s.Store.Latest(ctx)
	s.handle(v.ID, v.Epoch, codex.Message{Method: "item/completed", Params: bytes(map[string]any{"threadId": v.ThreadID, "item": map[string]string{"type": "commandExecution", "output": strings.Repeat("unused", 200000)}})})
	if s.Store.Latest(ctx) != before {
		t.Fatal("invisible native completion bloated transcript")
	}
	text := strings.Repeat("large-completion🙂", 80000)
	s.handle(v.ID, v.Epoch, codex.Message{Method: "item/completed", Params: bytes(map[string]any{"threadId": v.ThreadID, "item": map[string]string{"type": "agentMessage", "id": "large-message", "text": text}})})
	after := before
	parts := []string{}
	for {
		page, e := s.Store.SessionEvents(ctx, v.ID, after, 100)
		must(t, e)
		for _, event := range page["events"].([]domain.Event) {
			if len(event.Data) > store.SessionOutputMaxBytes {
				t.Fatal("unbounded completion row")
			}
			var p struct {
				Fragment string `json:"fragment"`
			}
			must(t, json.Unmarshal(event.Data, &p))
			parts = append(parts, p.Fragment)
		}
		after = page["cursor"].(int64)
		if !page["hasMore"].(bool) {
			break
		}
	}
	var restored struct {
		Params struct{ Item struct{ ID, Text string } }
	}
	must(t, json.Unmarshal([]byte(strings.Join(parts, "")), &restored))
	if restored.Params.Item.ID != "large-message" || restored.Params.Item.Text != text {
		t.Fatal("completion identity/text lost")
	}
}

func TestWebhookLifecycleReturningToBaselineStillRunsOnce(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	c, _ := s.Settings(ctx)
	c.Rules = []Rule{{ID: "reopened", Repositories: []string{"*"}, Types: []string{"pull_request"}, Actions: []string{"reopened"}, Instructions: "handle known reopen"}}
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	must(t, s.Observe(ctx, observations(item), true))
	if _, err := s.Session(ctx, item.ID); err != store.ErrNotFound {
		t.Fatal("baseline launched work", err)
	}
	d := delivery{ID: "reopen", Type: "pull_request", State: "pending", At: s.Now(), Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": "reopened", "pull_request": map[string]any{"number": 1, "state": "open", "title": item.Title, "head": map[string]string{"sha": item.Head}}})}
	must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
	must(t, s.processDelivery(ctx, d))
	must(t, s.processDelivery(ctx, d))
	// Reconciliation observes the same final open state; it must not repeat work.
	must(t, s.Observe(ctx, observations(item), false))
	v := get(t, s)
	if len(v.Pending) != 1 || v.Pending[0].Event.Action != "reopened" || len(v.Pending[0].Actions) != 1 {
		t.Fatal("known delivery disappeared or duplicated", v.Pending)
	}
	events, err := s.Store.List(ctx, "factory-event")
	must(t, err)
	if len(events) != 1 {
		t.Fatal("overlap created additional history", len(events))
	}
}

func TestReviewThreadWebhooksUseActionWithoutInventedResolvedField(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	c, _ := s.Settings(ctx)
	c.Rules = []Rule{{ID: "thread", Repositories: []string{"*"}, Types: []string{"pull_request_review_thread"}, Instructions: "handle thread"}}
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	item.ReviewThreads = []domain.ReviewThread{{ID: "PRRT_known", Resolved: false}}
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	must(t, s.Observe(ctx, observations(item), true))
	for _, action := range []string{"resolved", "unresolved"} {
		d := delivery{ID: action, Type: "pull_request_review_thread", State: "pending", At: s.Now(), Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": action, "pull_request": map[string]any{"number": 1, "state": "open", "head": map[string]string{"sha": item.Head}}, "thread": map[string]any{"node_id": "PRRT_known", "comments": []any{}}})}
		must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
		must(t, s.processDelivery(ctx, d))
		must(t, s.processDelivery(ctx, d))
		must(t, s.Store.Get(ctx, "item", item.ID, &item))
		if len(item.ReviewThreads) != 1 || item.ReviewThreads[0].Resolved != (action == "resolved") {
			t.Fatal("real action did not change thread", item.ReviewThreads)
		}
		must(t, s.Observe(ctx, observations(item), false))
	}
	v := get(t, s)
	if len(v.Pending) != 2 || v.Pending[0].Event.Action != "resolved" || v.Pending[1].Event.Action != "unresolved" {
		t.Fatal("thread transition lost or duplicated", v.Pending)
	}
}

func TestNativeConfigurationStartupFailureRemainsConfirmedAcrossRestart(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			s, f, _ := fixture(t)
			ctx := context.Background()
			file := filepath.Join(s.Dir, "config.toml")
			f.native = map[string]any{"config": map[string]any{"model": "old"}, "layers": []any{map[string]any{"name": map[string]any{"type": "user", "file": file}, "version": "v1"}}}
			n := NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{{KeyPath: "model", Value: "new", MergeStrategy: "replace"}}}
			p, err := s.Propose(ctx, nil, &n)
			must(t, err)
			// Confirmation remains deferred while another native turn is active.
			active := newSession("configuration", "configuration", "", "", Agent{}, s.Now())
			active.Execution = "running"
			must(t, s.save(ctx, active))
			p, err = s.Confirm(ctx, p.ID, true)
			must(t, err)
			if p.State != "confirmed" {
				t.Fatal(p.State)
			}
			active.Execution = "idle"
			must(t, s.save(ctx, active))
			starts := 0
			s.Start = func(context.Context, Agent, func(codex.Message)) (RPC, error) {
				starts++
				if !ambiguous && starts == 2 {
					return nil, fmt.Errorf("fixture startup failed before write")
				}
				return f, nil
			}
			if ambiguous {
				f.fail = "config/batchWrite"
			}
			if err = s.applyPending(ctx); err == nil {
				t.Fatal("failure stimulus did not fail")
			}
			must(t, s.Store.Get(ctx, "factory-proposal", p.ID, &p))
			want := "confirmed"
			if ambiguous {
				want = "uncertain"
			}
			if p.State != want {
				t.Fatal("wrong write certainty", p.State)
			}
			s.Close()
			next := New(ctx, s.App, "unused")
			defer next.Close()
			next.Start = func(context.Context, Agent, func(codex.Message)) (RPC, error) { return f, nil }
			f.fail = ""
			must(t, next.Init(ctx))
			must(t, next.applyPending(ctx))
			must(t, next.Store.Get(ctx, "factory-proposal", p.ID, &p))
			calls := 0
			for _, m := range f.methods {
				if m == "config/batchWrite" {
					calls++
				}
			}
			if calls != 1 {
				t.Fatal("write retried ambiguously or known absence not retried", calls)
			}
			if !ambiguous && (p.State != "applied" || len(f.writes) != 1) {
				t.Fatal("confirmed prewrite failure stranded proposal", p.State)
			}
			if ambiguous && p.State != "uncertain" {
				t.Fatal("uncertain write automatically retried", p.State)
			}
		})
	}
}

func TestRetainedConnectionVaultRedactsWithoutImportingLegacyApplicationState(t *testing.T) {
	s, f, _ := fixtureWithStoreFile(t, store.ApplicationFile)
	ctx := context.Background()
	legacy, err := store.Open(filepath.Join(s.Dir, "state.db"))
	must(t, err)
	defer legacy.Close()
	retained, err := store.NewVault(legacy, s.Dir)
	must(t, err)
	must(t, retained.Set(ctx, "tunnel-key", "retained-connection-credential"))
	must(t, legacy.Put(ctx, "item", "legacy-run", map[string]string{"title": "must not import"}, ""))
	fresh, err := store.NewVault(s.Store, s.Dir)
	must(t, err)
	fresh.IncludeRedactionsFrom(retained)
	s.App.Vault = fresh
	if _, err = fresh.Get(ctx, "tunnel-key"); err != store.ErrNotFound {
		t.Fatal("redaction source became application lookup", err)
	}
	var old any
	if err = s.Store.Get(ctx, "item", "legacy-run", &old); err != store.ErrNotFound {
		t.Fatal("legacy application data imported", err)
	}
	v, err := s.Chat(ctx, "o/r#1", "begin", nil)
	must(t, err)
	s.handle(v.ID, v.Epoch, codex.Message{Method: "item/agentMessage/delta", Params: bytes(map[string]any{"threadId": v.ThreadID, "itemId": "message", "delta": "retained-connection-"})})
	s.handle(v.ID, v.Epoch, codex.Message{Method: "item/agentMessage/delta", Params: bytes(map[string]any{"threadId": v.ThreadID, "itemId": "message", "delta": "credential"})})
	s.handle(v.ID, v.Epoch, codex.Message{Method: "item/completed", Params: bytes(map[string]any{"threadId": v.ThreadID, "item": map[string]string{"type": "agentMessage", "text": "retained-connection-credential"}})})
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes("connection-question"), Method: "item/tool/requestUserInput", Params: bytes(map[string]any{"threadId": v.ThreadID, "questions": []any{map[string]any{"id": "check", "question": "Use retained-connection-credential?", "options": []any{map[string]string{"label": "Use retained-connection-credential", "description": "retain"}}}}})})
	page, err := s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	qs, err := s.Questions(ctx)
	must(t, err)
	public := string(bytes([]any{page, qs}))
	if strings.Contains(public, "retained-connection-") || !strings.Contains(public, "[redacted]") {
		t.Fatal("retained connection credential escaped masking")
	}
	if len(f.replies) != 0 {
		t.Fatal("native question did not wait for owner")
	}
	// Owner/API audit events use the same masked vault, not just native output.
	if got := string(fresh.RedactJSON(ctx, bytes(map[string]string{"message": "retained-connection-credential"}), nil)); strings.Contains(got, "retained-connection-") {
		t.Fatal("connection secret leaked through audit redactor")
	}
}

func TestNativeStreamUsesCompactIdentityAndPreservesQueuedContexts(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	for n := 0; n < 20; n++ {
		e := incoming(s, fmt.Sprint(n), "queue")
		e.Context.Body = strings.Repeat("q", 100*1024)
		v.Pending = append(v.Pending, Pending{Event: e})
	}
	must(t, s.save(ctx, v))
	before, err := s.Store.Records(ctx, "factory-session")
	must(t, err)
	runtimes, err := s.Store.Records(ctx, "factory-session-runtime")
	must(t, err)
	var runtime struct {
		Pending      json.RawMessage
		PendingCount int `json:"pendingCount"`
	}
	must(t, json.Unmarshal(runtimes[v.ID], &runtime))
	if len(runtimes[v.ID]) > 512 || len(runtime.Pending) != 0 || runtime.PendingCount != len(v.Pending) {
		t.Fatal("identity projection includes retained queue")
	}
	cursor, err := s.Store.UILatest(ctx)
	must(t, err)
	for n := 0; n < 50; n++ {
		s.handle(v.ID, v.Epoch, codex.Message{Method: "item/agentMessage/delta", Params: bytes(map[string]string{"threadId": v.ThreadID, "itemId": "a", "delta": "x"})})
	}
	after, err := s.Store.Records(ctx, "factory-session")
	must(t, err)
	if string(before[v.ID]) != string(after[v.ID]) {
		t.Fatal("stream mutated retained queued contexts")
	}
	now, err := s.Store.UILatest(ctx)
	must(t, err)
	if now != cursor {
		t.Fatal("stream invalidated full state projection")
	}
	// An opaque queue sentinel proves this notification path never decodes Pending.
	var opaque map[string]any
	must(t, json.Unmarshal(before[v.ID], &opaque))
	opaque["pending"] = "not a Session array"
	must(t, s.Store.Put(ctx, "factory-session", v.ID, opaque, ""))
	s.handle(v.ID, v.Epoch, codex.Message{Method: "item/agentMessage/delta", Params: bytes(map[string]string{"threadId": v.ThreadID, "itemId": "a", "delta": "opaque queue untouched"})})
	page, err := s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	if !strings.Contains(string(bytes(page)), "opaque queue untouched") {
		t.Fatal("stream attempted full session decoding")
	}
	must(t, s.save(ctx, v))
	v.Epoch = "replacement-epoch"
	must(t, s.save(ctx, v))
	s.handle(v.ID, "old-epoch", codex.Message{Method: "item/agentMessage/delta", Params: bytes(map[string]string{"delta": "stale callback"})})
	page, err = s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	if strings.Contains(string(bytes(page)), "stale callback") {
		t.Fatal("compact identity accepted stale callback")
	}
}

func TestReviewStateCasingDeduplicatesActualRESTAndWebhookOverlap(t *testing.T) {
	for _, first := range []string{"poll", "webhook"} {
		t.Run(first, func(t *testing.T) {
			s, _, clock := fixture(t)
			ctx := context.Background()
			c, _ := s.Settings(ctx)
			c.Rules = []Rule{{ID: "review", Repositories: []string{"*"}, Types: []string{"pull_request_review"}, Instructions: "review action"}}
			must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
			// Exercise the production REST decoder with GitHub's uppercase review state.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/o/r/issues/1":
					fmt.Fprint(w, `{"number":1,"state":"open","title":"current PR","updated_at":"2026-11-05T12:00:00Z","pull_request":{}}`)
				case "/repos/o/r/pulls/1":
					fmt.Fprint(w, `{"number":1,"state":"open","head":{"sha":"head-1"}}`)
				case "/repos/o/r/pulls/1/reviews":
					fmt.Fprint(w, `[{"id":7,"body":"looks good","state":"APPROVED","submitted_at":"2026-11-05T11:00:00Z"}]`)
				case "/repos/o/r/commits/head-1/check-runs":
					fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
				case "/graphql":
					fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"headRefOid":"head-1","reviewDecision":"APPROVED","reviewThreads":{"nodes":[],"pageInfo":{"hasNextPage":false}},"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
				default:
					fmt.Fprint(w, `[]`)
				}
			}))
			defer server.Close()
			provider := &providers.GitHub{BaseURL: server.URL, Client: server.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
			var item domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
			polled, err := provider.ItemDetail(ctx, item)
			must(t, err)
			found := false
			for _, a := range polled.Activity {
				if a.ID == "review:7" {
					found = true
					if a.Body != "looks goodapproved" {
						t.Fatal("REST state not canonicalized", a.Body)
					}
				}
			}
			if !found {
				t.Fatal("REST fixture omitted review")
			}
			webhook := func() {
				d := delivery{ID: "lowercase-review", Type: "pull_request_review", State: "pending", At: s.Now(), Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": "submitted", "pull_request": map[string]any{"number": 1, "state": "open", "title": item.Title, "head": map[string]string{"sha": item.Head}}, "review": map[string]any{"id": 7, "body": "looks good", "state": "approved", "submitted_at": "2026-11-05T11:00:00Z"}})}
				must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
				must(t, s.processDelivery(ctx, d))
			}
			poll := func() { must(t, s.Observe(ctx, observations(polled), false)) }
			if first == "poll" {
				poll()
			} else {
				webhook()
			}
			due := get(t, s).Due
			*clock = clock.Add(30 * time.Second)
			if first == "poll" {
				webhook()
			} else {
				poll()
			}
			v := get(t, s)
			if len(v.Pending) != 1 || len(v.Pending[0].Actions) != 1 || !v.Due.Equal(due) {
				t.Fatal("same review duplicated or reset quiet period", len(v.Pending), v.Due, due)
			}
		})
	}
}

func TestCachedDetailCannotReverseNewerWebhookObservation(t *testing.T) {
	s, _, clock := fixture(t)
	ctx := context.Background()
	c, _ := s.Settings(ctx)
	c.Rules = []Rule{{ID: "threads", Repositories: []string{"*"}, Types: []string{"pull_request_review_thread"}, Instructions: "handle thread"}}
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	item.ReviewThreads = []domain.ReviewThread{{ID: "PRRT_known", Resolved: false}}
	item.ReviewObservedAt = s.Now().Add(-time.Minute)
	item.DetailSyncedAt = item.ReviewObservedAt
	item.Revision = item.ContentRevision()
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	must(t, s.Observe(ctx, observations(item), true))
	stale := item
	d := delivery{ID: "resolved-live", Type: "pull_request_review_thread", State: "pending", At: s.Now(), Body: bytes(map[string]any{"repository": map[string]string{"full_name": "o/r"}, "action": "resolved", "pull_request": map[string]any{"number": 1, "state": "open", "head": map[string]string{"sha": item.Head}}, "thread": map[string]any{"node_id": "PRRT_known", "comments": []any{}}})}
	must(t, s.Store.Put(ctx, "factory-delivery", d.ID, d, ""))
	must(t, s.processDelivery(ctx, d))
	must(t, s.Store.Put(ctx, "factory-baseline", "o/r", map[string]bool{"ready": true}, ""))
	s.App.GitHub = &fixtureGitHub{items: map[int]domain.Item{1: stale}}
	// This fetch starts after the webhook but returns a cached, earlier observation.
	*clock = clock.Add(time.Minute)
	must(t, s.refreshDetails(ctx, c))
	var latest domain.Item
	must(t, s.Store.Get(ctx, "item", item.ID, &latest))
	if !latest.ReviewThreads[0].Resolved || !latest.ReviewObservedAt.Equal(d.At) {
		t.Fatal("cached detail reversed newer webhook", latest.ReviewThreads, latest.ReviewObservedAt)
	}
	v := get(t, s)
	if len(v.Pending) != 1 || v.Pending[0].Event.Action != "resolved" {
		t.Fatal("stale detail queued a reverse event", v.Pending)
	}
	// A genuinely later snapshot remains observable and may change the thread.
	fresh := stale
	fresh.ReviewObservedAt = s.Now()
	fresh.DetailSyncedAt = s.Now()
	s.App.GitHub = &fixtureGitHub{items: map[int]domain.Item{1: fresh}}
	must(t, s.refreshDetails(ctx, c))
	must(t, s.Store.Get(ctx, "item", item.ID, &latest))
	v = get(t, s)
	if latest.ReviewThreads[0].Resolved || len(v.Pending) != 2 || v.Pending[1].Event.Action != "unresolved" {
		t.Fatal("fresh observable transition suppressed")
	}
}

func TestConfigurationAppliedMarkerAndAllBindingsCommitAtomically(t *testing.T) {
	s, f, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	// Include each binding category in the atomic transition.
	for _, session := range []Session{newSession("configuration", "configuration", "", "", Agent{}, s.Now()), newSession("priority:o/r", "priority", "", "o/r", Agent{}, s.Now())} {
		must(t, s.save(ctx, session))
	}
	c, _ := s.Settings(ctx)
	c.ItemAgents = map[string]Agent{v.ID: {Profile: "native"}}
	c.ConfigurationAgent = Agent{Profile: "native"}
	c.PriorityAgent = Agent{Profile: "native"}
	c.QuietSeconds = 7
	p, err := s.Propose(ctx, &c, nil)
	must(t, err)
	p, err = s.Confirm(ctx, p.ID, true)
	must(t, err)
	s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]string{"id": v.TurnID, "status": "completed"}})})
	raw, err := sql.Open("sqlite", filepath.Join(s.Dir, "state.db"))
	must(t, err)
	defer raw.Close()
	_, err = raw.ExecContext(ctx, `CREATE TRIGGER reject_binding BEFORE UPDATE OF data ON entities WHEN NEW.kind='factory-session' BEGIN SELECT RAISE(ABORT,'fixture binding write failure'); END`)
	must(t, err)
	if err = s.applyPending(ctx); err == nil {
		t.Fatal("binding failure not stimulated")
	}
	current, _ := s.Settings(ctx)
	must(t, s.Store.Get(ctx, "factory-proposal", p.ID, &p))
	if current.QuietSeconds == 7 || p.State != "confirmed" || get(t, s).Agent.Profile != "" || s.client(v.ID) != f {
		t.Fatal("partial configuration or applied marker survived rollback", p.State)
	}
	_, err = raw.ExecContext(ctx, `DROP TRIGGER reject_binding`)
	must(t, err)
	s.Close()
	restarted := New(ctx, s.App, "unused")
	defer restarted.Close()
	must(t, restarted.Init(ctx))
	must(t, restarted.applyPending(ctx))
	must(t, restarted.Store.Get(ctx, "factory-proposal", p.ID, &p))
	current, _ = restarted.Settings(ctx)
	if p.State != "applied" || current.QuietSeconds != 7 {
		t.Fatal("confirmed configuration stranded after rollback/restart")
	}
	for _, id := range []string{v.ID, "configuration", "priority:o/r"} {
		actual, err := restarted.Session(ctx, id)
		must(t, err)
		if actual.Agent.Profile != "native" || actual.Epoch != "" {
			t.Fatal("applied marker lacks durable binding", id, actual.Agent, actual.Epoch)
		}
	}
}

func TestMultipleNativeProposalsNeverRestoreRetiredEpoch(t *testing.T) {
	s, main, _ := fixture(t)
	ctx := context.Background()
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	config := &fakeRPC{nativeByCWD: map[string]map[string]any{}}
	s.Start = func(context.Context, Agent, func(codex.Message)) (RPC, error) { return config, nil }
	proposals := []Proposal{}
	for _, name := range []string{"one", "two"} {
		cwd := filepath.Join(s.Dir, name)
		file := filepath.Join(cwd, ".codex", "config.toml")
		config.nativeByCWD[cwd] = map[string]any{"config": map[string]any{"model": "old"}, "layers": []any{map[string]any{"name": map[string]any{"type": "project", "file": file}, "version": "v1"}}}
		n := NativeEdit{CWD: cwd, File: file, Version: "v1", Edits: []ConfigEdit{{KeyPath: "model", Value: "new", MergeStrategy: "replace"}}}
		p, e := s.Propose(ctx, nil, &n)
		must(t, e)
		p, e = s.Confirm(ctx, p.ID, true)
		must(t, e)
		if p.State != "confirmed" {
			t.Fatal("active turn did not defer native edit")
		}
		proposals = append(proposals, p)
	}
	s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]string{"id": v.TurnID, "status": "completed"}})})
	release := make(chan struct{})
	done := make(chan struct{})
	main.onClose = func() {
		go func() { <-release; s.handle(v.ID, v.Epoch, codex.Message{Method: "process/closed"}); close(done) }()
	}
	must(t, s.applyPending(ctx))
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delayed close did not finish")
	}
	latest := get(t, s)
	if latest.Epoch != "" || latest.Execution != "idle" || latest.Hold || s.client(v.ID) != nil {
		t.Fatal("later proposal restored retired connection", latest)
	}
	if len(config.writes) != 2 {
		t.Fatal("independent native proposals did not both apply", len(config.writes))
	}
	for _, p := range proposals {
		must(t, s.Store.Get(ctx, "factory-proposal", p.ID, &p))
		if p.State != "applied" {
			t.Fatal(p.State)
		}
	}
}

func TestCommentCatchupStoresLinearDeltasAndWritesQueueOnce(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	c, _ := s.Settings(ctx)
	c.Rules = []Rule{{ID: "comments", Repositories: []string{"*"}, Types: []string{"issue_comment"}, Instructions: "handle each comment"}}
	must(t, s.Store.Put(ctx, "factory-settings", "effective", c, ""))
	var item domain.Item
	must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
	item.Body = strings.Repeat("description", 1000)
	item.PR = &domain.PullRequest{Head: item.Head, CI: "success", Checks: []domain.Check{{Name: "known-check"}}}
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	must(t, s.Observe(ctx, observations(item), true))
	raw, err := sql.Open("sqlite", filepath.Join(s.Dir, "state.db"))
	must(t, err)
	defer raw.Close()
	for _, stmt := range []string{`CREATE TABLE queue_writes(n INTEGER)`, `CREATE TRIGGER count_queue_insert AFTER INSERT ON entities WHEN NEW.kind='factory-session' BEGIN INSERT INTO queue_writes VALUES(1); END`, `CREATE TRIGGER count_queue_update AFTER UPDATE ON entities WHEN NEW.kind='factory-session' BEGIN INSERT INTO queue_writes VALUES(1); END`} {
		_, err = raw.ExecContext(ctx, stmt)
		must(t, err)
	}
	for n := 0; n < 100; n++ {
		item.Activity = append(item.Activity, domain.Activity{ID: fmt.Sprintf("comment:%d", n), Kind: "comment", Body: fmt.Sprintf("comment-%03d:", n) + strings.Repeat("x", 4096), Updated: fmt.Sprint(n)})
	}
	item.Revision = item.ContentRevision()
	must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
	must(t, s.Observe(ctx, observations(item), false))
	var writes int
	must(t, raw.QueryRowContext(ctx, `SELECT count(*) FROM queue_writes`).Scan(&writes))
	if writes != 1 {
		t.Fatal("queue serialized repeatedly in one transaction", writes)
	}
	events, err := s.Store.Records(ctx, "factory-event")
	must(t, err)
	total := 0
	for _, b := range events {
		total += len(b)
	}
	if len(events) != 100 || total > 1024*1024 {
		t.Fatal("events copied complete source history", len(events), total)
	}
	queued, err := s.Store.Records(ctx, "factory-session")
	must(t, err)
	if len(queued[item.ID]) > 1024*1024 {
		t.Fatal("pending contexts grew quadratically", len(queued[item.ID]))
	}
	v := get(t, s)
	for n, p := range v.Pending {
		if len(p.Event.Context.Activity) != 0 || p.Event.Context.Body != "" || p.Event.Context.PR != nil || len(p.Event.Details) == 0 {
			t.Fatal("event lacks compact delta", n)
		}
	}
	v, err = s.Chat(ctx, v.ID, "deliver catchup", nil)
	must(t, err)
	var batch Batch
	must(t, s.Store.Get(ctx, "factory-batch", v.BatchID, &batch))
	if len(batch.Context.Activity) != 100 || batch.Context.PR == nil || len(batch.Context.PR.Checks) != 1 || len(batch.Events) != 100 || len(batch.Actions) != 100 || len(rpc.inputs) != 1 {
		t.Fatal("batch lost full current context or authorized actions")
	}
	marker := "comment-042:"
	if strings.Count(string(bytes(batch)), marker) != 2 || strings.Count(string(rpc.inputs[0]), marker) != 2 {
		t.Fatal("native batch did not contain one delta plus one full current context")
	}
}

func TestDelayedCommentWebhookCannotReverseNewerActivity(t *testing.T) {
	for _, kind := range []string{"comment", "review-comment"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := fixture(t)
			ctx := context.Background()
			typ := "issue_comment"
			if kind == "review-comment" {
				typ = "pull_request_review_comment"
			}
			settings, err := s.Settings(ctx)
			must(t, err)
			settings.Rules = []Rule{{ID: "discussion", Repositories: []string{"*"}, Types: []string{typ}, Instructions: "handle current discussion"}}
			must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
			var item domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
			item.Activity = []domain.Activity{{ID: kind + ":7", Kind: kind, Body: "current B", Updated: "2026-11-05T12:00:02Z"}}
			item.Revision = item.ContentRevision()
			must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
			must(t, s.Observe(ctx, observations(item), true))
			send := func(id, body, updated string) {
				d := delivery{ID: id, Type: typ, At: s.Now(), State: "pending", Body: bytes(map[string]any{"action": "edited", "repository": map[string]string{"full_name": "o/r"}, "issue": map[string]any{"number": 1, "pull_request": map[string]string{"url": "fixture"}}, "pull_request": map[string]any{"number": 1, "state": "open", "head": map[string]string{"sha": item.Head}}, "comment": map[string]any{"id": 7, "body": body, "updated_at": updated}})}
				must(t, s.Store.Put(ctx, "factory-delivery", id, d, ""))
				must(t, s.processDelivery(ctx, d))
			}
			// This offset sorts after Z lexically but denotes an older instant.
			send("delayed", "old A", "2026-11-05T13:00:01+01:00")
			var current domain.Item
			must(t, s.Store.Get(ctx, "item", item.ID, &current))
			if len(current.Activity) != 1 || current.Activity[0].Body != "current B" {
				t.Fatal("delayed webhook replaced newer comment", current.Activity)
			}
			must(t, s.Observe(ctx, observations(item), false))
			if _, err = s.Session(ctx, item.ID); err != store.ErrNotFound {
				t.Fatal("old webhook or overlapping poll authorized duplicate work", err)
			}
			send("genuinely-new", "new C", "2026-11-05T12:00:03Z")
			v := get(t, s)
			if len(v.Pending) != 1 || len(v.Pending[0].Actions) != 1 || !strings.Contains(string(v.Pending[0].Event.Details), "new C") {
				t.Fatal("later comment version was suppressed", v.Pending)
			}
			must(t, s.Store.Get(ctx, "item", item.ID, &current))
			if current.Activity[0].Body != "new C" {
				t.Fatal("new comment missing")
			}
		})
	}
}

func TestSecretAnswerWithRedactedQuestionIDSurvivesResume(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	vault, err := store.NewVault(s.Store, s.Dir)
	must(t, err)
	s.App.Vault = vault
	must(t, vault.Set(ctx, "jira", "private-question-key"))
	v, err := s.Chat(ctx, "o/r#1", "start", nil)
	must(t, err)
	s.handle(v.ID, v.Epoch, codex.Message{ID: bytes(1), Method: "item/tool/requestUserInput", Params: bytes(map[string]any{"threadId": v.ThreadID, "questions": []any{map[string]any{"id": "private-question-key", "question": "Enter a new secret", "isSecret": true}}})})
	qs, err := s.Questions(ctx)
	must(t, err)
	if len(qs) != 1 || strings.Contains(string(qs[0].Params), "private-question-key") {
		t.Fatal("question ID was not masked")
	}
	must(t, s.Answer(ctx, qs[0].ID, bytes(map[string]any{"answers": map[string]any{"[redacted]": map[string]any{"answers": []string{"new-sensitive-answer"}}}})))
	var reply struct {
		Answers map[string]struct{ Answers []string }
	}
	must(t, json.Unmarshal(rpc.replies[len(rpc.replies)-1], &reply))
	if got := reply.Answers["private-question-key"].Answers; len(got) != 1 || got[0] != "new-sensitive-answer" {
		t.Fatal("original key/answer not delivered", reply)
	}
	retained, err := vault.Get(ctx, "factory-redactions:"+v.ID)
	must(t, err)
	if !strings.Contains(retained, "new-sensitive-answer") {
		t.Fatal("translated answer was not durably retained")
	}
	for n, part := range []string{"echo new-sensitive-", "answer"} {
		s.handle(v.ID, v.Epoch, codex.Message{Method: "item/agentMessage/delta", Params: bytes(map[string]any{"threadId": v.ThreadID, "itemId": "split", "delta": part, "part": n})})
	}
	s.handle(v.ID, v.Epoch, codex.Message{Method: "item/completed", Params: bytes(map[string]any{"threadId": v.ThreadID, "item": map[string]string{"id": "complete", "type": "agentMessage", "text": "echo new-sensitive-answer"}})})
	s.Close()
	resumed := New(ctx, s.App, "unused")
	defer resumed.Close()
	resumed.Now = s.Now
	resumed.Workspace = s.Workspace
	resumed.Start = s.Start
	must(t, resumed.Init(ctx))
	v, err = resumed.Chat(ctx, v.ID, "resume", nil)
	must(t, err)
	resumed.handle(v.ID, v.Epoch, codex.Message{Method: "item/completed", Params: bytes(map[string]any{"threadId": v.ThreadID, "item": map[string]string{"id": "resumed", "type": "agentMessage", "text": "echo new-sensitive-answer"}})})
	page, err := s.Store.SessionEvents(ctx, v.ID, 0, 100)
	must(t, err)
	out := string(bytes(page))
	if strings.Contains(out, "new-sensitive-") || strings.Contains(out, "private-question-key") || !strings.Contains(out, "[redacted]") {
		t.Fatal("secret question or answer leaked", out)
	}
}

func TestSchedulerSkipsHeldQueueDecodeAndBusyConfiguration(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	for n := 0; n < 30; n++ {
		id := fmt.Sprintf("held-%02d", n)
		pending := []any{}
		for j := 0; j < 10; j++ {
			pending = append(pending, map[string]any{"event": map[string]any{"context": map[string]any{"title": map[string]string{"poison": "must not decode"}}, "details": strings.Repeat("x", 4096)}})
		}
		execution, hold := "interrupted", true
		if n >= 15 {
			execution, hold = "idle", false // Due and otherwise runnable, but repository paused.
		}
		if n == 29 {
			execution, hold = "running", false
		}
		row := map[string]any{"id": id, "kind": "item", "repository": "o/r", "execution": execution, "hold": hold, "pending": pending, "due": s.Now().Add(-time.Minute)}
		must(t, s.Store.Put(ctx, "factory-session", id, row, ""))
		var runtime json.RawMessage
		must(t, s.Store.Get(ctx, "factory-session-runtime", id, &runtime))
		if len(runtime) > 512 || strings.Contains(string(runtime), "poison") {
			t.Fatal("runtime copied queue body", len(runtime))
		}
	}
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.Repositories[0].Paused = true
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	after := settings
	after.QuietSeconds++
	proposal, err := s.Propose(ctx, &after, nil)
	must(t, err)
	proposal, err = s.Confirm(ctx, proposal.ID, true)
	must(t, err)
	if proposal.State != "confirmed" {
		t.Fatal("active session did not defer configuration")
	}
	for n := 0; n < 3; n++ {
		must(t, s.Tick(ctx))
	}
	must(t, s.Store.Get(ctx, "factory-proposal", proposal.ID, &proposal))
	if proposal.State != "confirmed" || len(rpc.methods) != 0 {
		t.Fatal("idle scheduling decoded queues, applied busy config, or started native work")
	}
}

type notifyingRPC struct {
	RPC
	sent chan struct{}
	once *sync.Once
}

func (c notifyingRPC) Call(ctx context.Context, method string, params, out any) error {
	err := c.RPC.Call(ctx, method, params, out)
	if err == nil && method == "turn/start" && strings.Contains(string(bytes(params)), "o/r#2") {
		c.once.Do(func() { close(c.sent) })
	}
	return err
}
func TestAutomaticBatchBypassesBusyAndSlowPeerAttachments(t *testing.T) {
	for _, mode := range []string{"human-with-empty-queue", "automatic-with-due-queue"} {
		t.Run(mode, func(t *testing.T) {
			s, rpc, _ := fixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			settings, err := s.Settings(ctx)
			must(t, err)
			settings.QuietSeconds = 0
			must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
			var item domain.Item
			must(t, s.Store.Get(ctx, "item", "o/r#1", &item))
			item.ID = "o/r#2"
			item.Number = 2
			must(t, s.Store.Put(ctx, "item", item.ID, item, ""))
			must(t, s.Store.Put(ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""))
			entered, release, sent := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			s.Start = func(context.Context, Agent, func(codex.Message)) (RPC, error) {
				return notifyingRPC{rpc, sent, &once}, nil
			}
			s.Workspace = func(ctx context.Context, v Session) (string, error) {
				if v.ID == "o/r#1" {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return s.Dir, nil
			}
			finished := make(chan error, 1)
			if mode == "human-with-empty-queue" {
				go func() { _, e := s.Chat(ctx, "o/r#1", "slow human", nil); finished <- e }()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("human attachment missing")
				}
			}
			must(t, s.Observe(ctx, []Event{event(item, "pull_request", "synchronize", "head", "b")}, false))
			if mode == "automatic-with-due-queue" {
				must(t, s.Observe(ctx, []Event{incoming(s, "head", "a")}, false))
			}
			ticked := make(chan error, 1)
			go func() { ticked <- s.Tick(ctx) }()
			if mode == "automatic-with-due-queue" {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("automatic attachment missing")
				}
			}
			select {
			case <-sent:
			case <-ctx.Done():
				t.Fatal("A attachment blocked B automatic native delivery")
			}
			// Delivery of B is observed before releasing A, so later recovery cannot
			// make this pass if dispatch still waits for the earlier conversation.
			close(release)
			select {
			case err = <-ticked:
				must(t, err)
			case <-ctx.Done():
				t.Fatal("tick did not finish")
			}
			if mode == "human-with-empty-queue" {
				select {
				case err = <-finished:
					must(t, err)
				case <-ctx.Done():
					t.Fatal("human attach did not finish")
				}
			}
			b, err := s.Session(ctx, item.ID)
			must(t, err)
			if b.Execution != "running" || len(b.Pending) != 0 {
				t.Fatal("B queue was not delivered", b.Execution)
			}
		})
	}
}

func TestNativeProtocolFixtureCancelsBlockedStartup(t *testing.T) {
	s, _, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered := filepath.Join(t.TempDir(), "initialized-request")
	t.Setenv("CF_FAKE_BLOCK_RPC", "initialize")
	t.Setenv("CF_FAKE_RPC_ENTERED", entered)
	t.Setenv("CF_FAKE_RPC_RELEASE", filepath.Join(t.TempDir(), "never-released"))
	start := nativeStarter(testutil.FakeCodex(t))
	s.Start = func(_ context.Context, agent Agent, handler func(codex.Message)) (RPC, error) {
		return start(ctx, agent, handler)
	}
	finished := make(chan error, 1)
	go func() { _, err := s.Chat(ctx, "o/r#1", "blocked initialization", nil); finished <- err }()
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture never reached blocked initialize")
		case <-time.After(time.Millisecond):
		}
	}
	cancel() // The exact blocked startup is now observed, not assumed after a sleep.
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled startup succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("native fixture ignored cancellation before the polling deadline")
	}
}

func TestPausedRepositoryRetainsQueueAndAllowsExplicitChat(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	settings, err := s.Settings(ctx)
	must(t, err)
	settings.QuietSeconds = 0
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "before-pause")}, false))
	settings.Repositories[0].Paused = true
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	must(t, s.Tick(ctx))
	if len(rpc.inputs) != 0 || len(get(t, s).Pending) != 1 {
		t.Fatal("pause sent or discarded authorized queue")
	}
	v, err := s.Chat(ctx, "o/r#1", "explicit owner work", nil)
	must(t, err)
	if len(rpc.inputs) != 1 || len(v.Pending) != 0 {
		t.Fatal("pause blocked explicit chat or omitted authorized queue")
	}
	s.handle(v.ID, v.Epoch, codex.Message{Method: "turn/completed", Params: bytes(map[string]any{"turn": map[string]string{"id": v.TurnID, "status": "completed"}})})
	settings.Repositories[0].Paused = false
	must(t, s.Store.Put(ctx, "factory-settings", "effective", settings, ""))
	must(t, s.Observe(ctx, []Event{incoming(s, "head", "after-unpause")}, false))
	must(t, s.Tick(ctx))
	if len(rpc.inputs) != 2 || get(t, s).ThreadID != v.ThreadID {
		t.Fatal("unpaused runnable session failed to deliver through retained thread")
	}
}
