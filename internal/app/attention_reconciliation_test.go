package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAttentionReconciliationRejectsChunkedConversationChanges(t *testing.T) {
	for _, tc := range []struct {
		name, changeType string
		long, otherRun   bool
		stale            bool
	}{
		{name: "unchanged"},
		{name: "other-run-long-owner", changeType: "userMessage", long: true, otherRun: true},
		{name: "ordinary-tool", changeType: "commandExecution"},
		{name: "ordinary-owner-correction", changeType: "userMessage", stale: true},
		{name: "long-owner-correction", changeType: "userMessage", long: true, stale: true},
		{name: "long-agent-correction", changeType: "agentMessage", long: true, stale: true},
		// Opaque legacy chunks cannot establish their item type without importing
		// omitted content. A long tool completion conservatively preserves the ask.
		{name: "opaque-long-tool", changeType: "commandExecution", long: true, stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ctx := fixture(t)
			r := prepareRun(t, a, ctx)
			r.Status, r.TurnID = "idle", "unchanged-turn"
			if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
				t.Fatal(err)
			}
			request := domain.Attention{ID: "question", RunID: r.ID, Origin: "async", Method: questionMethod, Status: "pending", Created: time.Now().UTC(), Params: raw(map[string]any{"questions": []question{{ID: "q1", Question: "Which contract?", IsOther: true}}})}
			if err := a.Store.Put(ctx, "attention", request.ID, request, ""); err != nil {
				t.Fatal(err)
			}
			live := &session{run: r, pending: map[string]domain.Attention{}, completed: map[string]bool{}}
			evidence := []attentionEvidence{{Role: "user", Text: "Use stable IDs"}, {Role: "agent", Text: "Stable IDs accepted"}}
			complete := func(s *session, kind, text string) {
				t.Helper()
				item := map[string]any{"id": ID(), "type": kind, "text": text}
				if kind == "userMessage" {
					delete(item, "text")
					item["content"] = []map[string]string{{"type": "text", "text": text}}
				}
				a.Runs.onMessage(s, codex.Message{Method: "item/completed", Params: raw(map[string]any{"turnId": r.TurnID, "item": item})})
			}
			for n := range evidence {
				kind := "agentMessage"
				if evidence[n].Role == "user" {
					kind = "userMessage"
				}
				complete(live, kind, evidence[n].Text)
				evidence[n].Seq = a.Store.Latest(ctx)
			}
			decision := attentionDecision{QuestionID: "q1", Status: "resolved", UserEvent: evidence[0].Seq, AgentEvent: evidence[1].Seq, UserQuote: evidence[0].Text, AgentQuote: evidence[1].Text}
			if tc.changeType != "" {
				target := live
				if tc.otherRun {
					other := r
					other.ID = "other-run"
					target = &session{run: other, pending: map[string]domain.Attention{}, completed: map[string]bool{}}
				}
				text := "Do not settle q1; the earlier decision is withdrawn."
				if tc.long {
					text = strings.Repeat(text, 1000)
				}
				before := a.Store.Latest(ctx)
				complete(target, tc.changeType, text)
				page, err := a.Store.RunEvents(ctx, target.run.ID, before, 200)
				if err != nil {
					t.Fatal(err)
				}
				events := page["events"].([]domain.Event)
				if len(events) == 0 || tc.long && len(events) < 2 {
					t.Fatal("actual completion producer did not journal expected chunks", len(events))
				}
				if tc.long {
					for _, event := range events {
						var body struct{ Method string }
						if err := json.Unmarshal(event.Data, &body); err != nil || body.Method != "item/completed/chunk" {
							t.Fatal("oversized completion bypassed chunk producer", body, err)
						}
					}
				}
			}
			// The bounded helper input still contains exactly the earlier explicit
			// owning exchange. Omitted content may invalidate it, never prove a reply.
			saved, err := a.Store.ConversationEvidence(ctx, r.ID, request.Created)
			want := 2
			if tc.changeType == "userMessage" && !tc.long && !tc.otherRun {
				want++
			}
			if err != nil || len(saved) != want {
				t.Fatal("bounded same-run evidence changed unexpectedly", len(saved), err)
			}
			if tc.long && len(saved) == 2 && (saved[0].Seq != evidence[1].Seq || saved[1].Seq != evidence[0].Seq) {
				t.Fatal("chunked content replaced explicit bounded evidence", saved)
			}
			var unchanged domain.Run
			if err := a.Store.Get(ctx, "run", r.ID, &unchanged); err != nil || unchanged.Status != r.Status || unchanged.TurnID != r.TurnID {
				t.Fatal("fixture changed run guards instead of conversation", unchanged, err)
			}
			err = a.Runs.applyAttentionDecisions(ctx, r, request, evidence, []attentionDecision{decision})
			var current domain.Attention
			if loadErr := a.Store.Get(ctx, "attention", request.ID, &current); loadErr != nil {
				t.Fatal(loadErr)
			}
			if tc.stale {
				if err == nil || !strings.Contains(err.Error(), "conversation changed") {
					t.Fatal("stale owning conversation was accepted", err)
				}
				if current.Status != "pending" || len(current.Resolutions) != 0 || string(current.Params) != string(request.Params) {
					t.Fatal("stale inference changed unanswered questions", current)
				}
			} else if err != nil || current.Status != "resolved" || len(current.Resolutions) != 1 {
				t.Fatal("unchanged owning evidence no longer settles", current, err)
			}
		})
	}
}

func TestDeliveredMessageReconcilesOnlyAcknowledgedQuestions(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		remaining     int
	}{
		{"partial", "Q1: use stable IDs", 1},
		{"review-completion", "Q1: stable IDs; Q2: include PREPARE", 0},
		{"unrelated-continuation", "Investigate the timing before I decide", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", "async-question-multi")
			a, ctx := fixture(t)
			r := prepareRun(t, a, ctx)
			if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
				t.Fatal(err)
			}
			at := pendingQuestion(t, a, "async")
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
			// Merely finishing the question's source turn must preserve its asks.
			page, err := a.ListAttention(ctx, AttentionInput{RunID: r.ID})
			if err != nil || len(page.Requests) != 1 {
				t.Fatal("source completion discarded questions", err)
			}
			d, err := a.Runs.QueueMessage(ctx, r.ID, tc.message, "")
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool {
				v, _ := a.MessageDelivery(ctx, r.ID)
				return v != nil && v.ID == d.ID && v.State == "delivered"
			})
			waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
			if tc.remaining < 2 {
				waitFor(t, func() bool {
					_ = a.Store.Get(ctx, "attention", at.ID, &at)
					var p struct{ Questions []question }
					_ = json.Unmarshal(at.Params, &p)
					return len(p.Questions) == tc.remaining
				})
			} else if err := a.Runs.reconcileAttention(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
			_ = a.Store.Get(ctx, "attention", at.ID, &at)
			var p struct{ Questions []question }
			_ = json.Unmarshal(at.Params, &p)
			if len(p.Questions) != tc.remaining || len(at.Resolutions) != 2-tc.remaining {
				t.Fatal("wrong per-question lifecycle", at)
			}
			if tc.remaining == 1 && p.Questions[0].ID != "q2" {
				t.Fatal("resolved the unanswered question")
			}
			tracked, err := a.TrackWork(ctx, TrackInput{Target: r.ID})
			if err != nil {
				t.Fatal(err)
			}
			trackedRequests := tracked["attention"].(AttentionPage).Requests
			if tc.remaining == 0 && len(trackedRequests) != 0 || tc.remaining > 0 && len(trackedRequests) != 1 {
				t.Fatal("tracking disagrees with actionable attention")
			}
			if tc.remaining > 0 {
				var visible struct{ Questions []question }
				_ = json.Unmarshal(trackedRequests[0].Request.Params, &visible)
				if len(visible.Questions) != tc.remaining {
					t.Fatal("tracking returned settled questions")
				}
			}
			page, err = a.ListAttention(ctx, AttentionInput{RunID: r.ID})
			if err != nil || (len(page.Requests) == 0) != (tc.remaining == 0) {
				t.Fatal("actionable queue retained resolved requests", err, page)
			}
			if tc.remaining == 1 {
				if err := a.Runs.Answer(ctx, at.ID, raw(map[string]any{"answers": map[string]any{"q2": map[string]any{"answers": []string{"Include recovery PREPARE"}}}})); err != nil {
					t.Fatal("remaining question cannot be answered", err)
				}
				waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
				waitForResolvedCompletion(t, a, ctx, r.ID)
				a.Runs.Close()
				if err := a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
				page, err := a.ListAttention(ctx, AttentionInput{RunID: r.ID})
				if err != nil || len(page.Requests) != 0 {
					t.Fatal("restart resurrected settled questions", err, page)
				}
			}
		})
	}
}

func TestAttentionReconciliationPreservesUnprovenOrOtherRunRequests(t *testing.T) {
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	r.Status = "idle"
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	at := domain.Attention{ID: "earlier-questions", RunID: r.ID, Origin: "async", Method: questionMethod, Status: "pending", Created: time.Now().UTC(), Params: raw(map[string]any{"questions": []question{{ID: "q1", Question: "Which contract?", IsOther: true}}})}
	if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
		t.Fatal(err)
	}
	// A later run on the same item has both decisions and a completed review.
	later := r
	later.ID = "later-review"
	if err := a.Store.Put(ctx, "run", later.ID, later, ""); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"userMessage", "agentMessage"} {
		if err := a.Store.Write(ctx, func(tx *store.Tx) error {
			return tx.Event("run.output", later.ID, map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"type": role, "text": "Both decisions accepted. Review published."}}})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Runs.reconcileAttention(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	var current domain.Attention
	_ = a.Store.Get(ctx, "attention", at.ID, &current)
	if current.Status != "pending" || len(current.Resolutions) != 0 {
		t.Fatal("other-run completion cleared questions")
	}
	// A helper cannot clear a question using fabricated, misordered or stale evidence.
	evidence := []attentionEvidence{{Seq: 1, Role: "user", Text: "Use stable IDs"}, {Seq: 2, Role: "agent", Text: "Decision accepted"}}
	for _, d := range []attentionDecision{
		{QuestionID: "q1", Status: "resolved", UserEvent: 1, AgentEvent: 2, UserQuote: "invented", AgentQuote: "Decision accepted"},
		{QuestionID: "unknown", Status: "resolved", UserEvent: 1, AgentEvent: 2, UserQuote: "Use stable IDs", AgentQuote: "Decision accepted"},
		{QuestionID: "q1", Status: "resolved", UserEvent: 2, AgentEvent: 1, UserQuote: "Decision accepted", AgentQuote: "Use stable IDs"},
	} {
		if err := a.Runs.applyAttentionDecisions(ctx, r, at, evidence, []attentionDecision{d}); err == nil {
			t.Fatal("unproven decision accepted")
		}
	}
	// Journal the owning conversation so version validation succeeds and only
	// the changed run snapshot can reject this otherwise valid inference.
	for n := range evidence {
		e := evidence[n]
		kind := "agentMessage"
		if e.Role == "user" {
			kind = "userMessage"
		}
		if err := a.Store.Write(ctx, func(tx *store.Tx) error {
			return tx.Event("run.output", r.ID, map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"type": kind, "text": e.Text}}})
		}); err != nil {
			t.Fatal(err)
		}
		evidence[n].Seq = a.Store.Latest(ctx)
	}
	d := attentionDecision{QuestionID: "q1", Status: "resolved", UserEvent: evidence[0].Seq, AgentEvent: evidence[1].Seq, UserQuote: evidence[0].Text, AgentQuote: evidence[1].Text}
	for _, change := range []string{"status", "turn"} {
		t.Run("stale-"+change, func(t *testing.T) {
			changed := r
			if change == "status" {
				changed.Status = "running"
			} else {
				changed.TurnID = "new-turn"
			}
			if err := a.Store.Put(ctx, "run", r.ID, changed, ""); err != nil {
				t.Fatal(err)
			}
			if err := a.Runs.applyAttentionDecisions(ctx, r, at, evidence, []attentionDecision{d}); err == nil {
				t.Fatal("stale inference overwrote changed run", change)
			}
			if err := a.Store.Get(ctx, "attention", at.ID, &current); err != nil {
				t.Fatal(err)
			}
			if current.Status != "pending" || len(current.Resolutions) != 0 || string(current.Params) != string(at.Params) {
				t.Fatal("stale inference changed pending questions", current)
			}
		})
	}
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.applyAttentionDecisions(ctx, r, at, evidence, []attentionDecision{d}); err != nil {
		t.Fatal("unchanged owning conversation rejected", err)
	}
	if err := a.Store.Get(ctx, "attention", at.ID, &current); err != nil || current.Status != "resolved" || len(current.Resolutions) != 1 || current.Resolutions[0].QuestionID != "q1" {
		t.Fatal("valid owning conversation did not settle its question", current, err)
	}
}

func TestAttentionReconciliationHelperFailureRetainsQuestions(t *testing.T) {
	t.Setenv("CF_FAKE_MODE", "async-question-multi")
	t.Setenv("CF_FAKE_ATTENTION_DECISIONS", `{"decisions":[{"questionId":"q1","status":"resolved","userEvent":0,"agentEvent":0,"userQuote":"forged","agentQuote":"forged"}]}`)
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "async")
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
	if _, err := a.Runs.QueueMessage(ctx, r.ID, "Q1: use stable IDs", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { d, _ := a.MessageDelivery(ctx, r.ID); return d != nil && d.State == "delivered" })
	waitFor(t, func() bool { _ = a.Store.Get(ctx, "run", r.ID, &r); return r.Status == "idle" })
	if err := a.Runs.reconcileAttention(context.Background(), r.ID); err == nil {
		t.Fatal("invalid helper evidence was accepted")
	}
	_ = a.Store.Get(ctx, "attention", at.ID, &at)
	if at.Status != "pending" || len(at.Resolutions) != 0 {
		t.Fatal("helper failure discarded question")
	}
}

func TestAttentionReconciliationRetiresRecoveryAndPreservesNativeApprovals(t *testing.T) {
	a, ctx := fixture(t)
	r := prepareRun(t, a, ctx)
	r.Status = "idle"
	if err := a.Store.Put(ctx, "run", r.ID, r, ""); err != nil {
		t.Fatal(err)
	}
	recovery := domain.QuestionRecovery{ID: "recovery", RunID: r.ID, Stage: "fallback", Confirmation: "native", Question: "Which scope?"}
	if err := a.Store.Put(ctx, "question-recovery", recovery.ID, recovery, ""); err != nil {
		t.Fatal(err)
	}
	at := domain.Attention{ID: "fallback", RunID: r.ID, RecoveryID: recovery.ID, Origin: "text", Method: questionMethod, Status: "pending", Params: raw(map[string]any{"questions": []question{{ID: "q1", Question: "Which scope?", IsOther: true}}})}
	native := domain.Attention{ID: "native-approval", RunID: r.ID, Origin: "rpc", Method: "item/commandExecution/requestApproval", Status: "pending"}
	for _, v := range []domain.Attention{at, native} {
		if err := a.Store.Put(ctx, "attention", v.ID, v, ""); err != nil {
			t.Fatal(err)
		}
	}
	evidence := []attentionEvidence{{Role: "user", Text: "Cancel that scope question"}, {Role: "agent", Text: "Scope question cancelled"}}
	for n := range evidence {
		e := evidence[n]
		kind := "agentMessage"
		if e.Role == "user" {
			kind = "userMessage"
		}
		if err := a.Store.Write(ctx, func(tx *store.Tx) error {
			return tx.Event("run.output", r.ID, map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"type": kind, "text": e.Text}}})
		}); err != nil {
			t.Fatal(err)
		}
		evidence[n].Seq = a.Store.Latest(ctx)
	}
	d := attentionDecision{QuestionID: "q1", Status: "cancelled", UserEvent: evidence[0].Seq, AgentEvent: evidence[1].Seq, UserQuote: evidence[0].Text, AgentQuote: evidence[1].Text}
	if err := a.Runs.applyAttentionDecisions(ctx, r, at, evidence, []attentionDecision{d}); err != nil {
		t.Fatal(err)
	}
	_ = a.Store.Get(ctx, "question-recovery", recovery.ID, &recovery)
	_ = a.Store.Get(ctx, "attention", native.ID, &native)
	if recovery.Stage != "resolved" || native.Status != "pending" {
		t.Fatal("recovery remained live or native approval was granted", recovery.Stage, native.Status)
	}
}
