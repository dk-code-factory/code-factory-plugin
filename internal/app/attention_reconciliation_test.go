package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"testing"
	"time"
)

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
	changed := r
	changed.Status = "running"
	changed.TurnID = "new-turn"
	if err := a.Store.Put(ctx, "run", r.ID, changed, ""); err != nil {
		t.Fatal(err)
	}
	d := attentionDecision{QuestionID: "q1", Status: "resolved", UserEvent: 1, AgentEvent: 2, UserQuote: "Use stable IDs", AgentQuote: "Decision accepted"}
	if err := a.Runs.applyAttentionDecisions(ctx, r, at, evidence, []attentionDecision{d}); err == nil {
		t.Fatal("stale inference overwrote a new turn")
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
	recovery := domain.QuestionRecovery{ID: "recovery", RunID: r.ID, Stage: "fallback", Question: "Which scope?"}
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
