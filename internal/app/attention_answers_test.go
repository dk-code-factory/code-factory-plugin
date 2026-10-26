package app

import (
	"codefactory/internal/codex"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnsweredRepairNeverReappearsAfterRecovery(t *testing.T) {
	for _, stage := range []string{"answered", "awaiting-answer", "fallback"} {
		t.Run(stage, func(t *testing.T) {
			a, ctx := fixture(t)
			run := domain.Run{ID: "answered-repair", Status: "interrupted", InterruptRequested: true}
			recovery := domain.QuestionRecovery{ID: "repair", RunID: run.ID, Stage: stage, Confirmation: "native", Question: "Which target?"}
			at := domain.Attention{ID: "original", RunID: run.ID, Origin: "rpc", Method: questionMethod, Status: "answered", RecoveryID: recovery.ID}
			for kind, record := range map[string]struct {
				id string
				v  any
			}{"run": {run.ID, run}, "question-recovery": {recovery.ID, recovery}, "attention": {at.ID, at}} {
				if err := a.Store.Put(ctx, kind, record.id, record.v, ""); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "fallback" {
				if err := a.Runs.recoverTextQuestion(ctx, &recovery); err != nil {
					t.Fatal(err)
				}
			}
			unanswered := domain.Attention{ID: "other-question", RunID: run.ID, Origin: "async", Method: questionMethod, Status: "pending"}
			if err := a.Store.Put(ctx, "attention", unanswered.ID, unanswered, ""); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
				page, err := a.ListAttention(ctx, AttentionInput{RunID: run.ID})
				if err != nil || len(page.Requests) != 1 || page.Requests[0].Request.ID != unanswered.ID {
					t.Fatal("answered repair reappeared or another question was removed", err)
				}
			}
			if err := a.Store.Get(ctx, "question-recovery", recovery.ID, &recovery); err != nil || recovery.Stage != "resolved" {
				t.Fatal("answered recovery was not settled", err)
			}
		})
	}
}

func TestAttentionRepliesPersistEncryptedAndStayOutOfQueue(t *testing.T) {
	for _, tc := range []struct {
		mode, origin, questionID, value string
	}{
		{"question", "rpc", "scope", "Focused"},
		{"nonblocking", "rpc", "scope", "Broad"},
		{"approval", "rpc", "", ""},
		{"async-question", "async", "q1", "One"},
		{"recover-fail", "text", "reply", "private-reply-7421"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("CF_FAKE_MODE", tc.mode)
			a, ctx := fixture(t)
			run := prepareRun(t, a, ctx)
			if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
				t.Fatal(err)
			}
			at := pendingQuestion(t, a, tc.origin)
			answer := raw(map[string]any{"answers": map[string]any{tc.questionID: map[string]any{"answers": []string{tc.value}}}})
			if tc.mode == "approval" {
				answer = raw(map[string]string{"decision": "decline"})
			}
			if err := a.Runs.Answer(ctx, at.ID, json.RawMessage(`{}`)); err == nil {
				t.Fatal("invalid response accepted")
			}
			key := "attention-answer:" + at.ID
			if _, err := a.Vault.Get(ctx, key); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("invalid response was saved", err)
			}
			if err := a.Runs.Answer(ctx, at.ID, answer); err != nil {
				t.Fatal(err)
			}
			// A rejected second reply must never overwrite the accepted payload.
			duplicate := raw(map[string]any{"answers": map[string]any{tc.questionID: map[string]any{"answers": []string{"Another reply"}}}})
			if tc.mode == "approval" {
				duplicate = raw(map[string]string{"decision": "accept"})
			}
			if err := a.Runs.Answer(ctx, at.ID, duplicate); err == nil {
				t.Fatal("duplicate reply accepted")
			}
			waitFor(t, func() bool {
				_ = a.Store.Get(ctx, "run", run.ID, &run)
				return run.Status == "idle"
			})
			waitForResolvedCompletion(t, a, ctx, run.ID)
			a.Runs.Close()
			// Read through a new database connection and vault to prove disk
			// persistence, rather than relying on a manager's in-memory state.
			disk, err := store.Open(filepath.Join(a.DataDir, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer disk.Close()
			vault, err := store.NewVault(disk, a.DataDir)
			if err != nil {
				t.Fatal(err)
			}
			if saved, err := vault.Get(ctx, key); err != nil || saved != string(answer) {
				t.Fatal("exact reply was not retained across reopen", err)
			}
			var ciphertext string
			if err := disk.Get(ctx, "secret", key, &ciphertext); err != nil || strings.Contains(ciphertext, string(answer)) {
				t.Fatal("reply was not encrypted", err)
			}
			for range 2 {
				if err := a.Runs.Recover(ctx); err != nil {
					t.Fatal(err)
				}
				page, err := a.ListAttention(ctx, AttentionInput{RunID: run.ID})
				if err != nil || len(page.Requests) != 0 {
					t.Fatal("answered request returned after restart", err)
				}
				if err := a.Runs.Answer(ctx, at.ID, duplicate); err == nil {
					t.Fatal("restart allowed duplicate reply")
				}
			}
		})
	}
}

func TestUncertainAttentionReplyIsRetainedWithoutReopening(t *testing.T) {
	a, ctx := fixture(t)
	run := domain.Run{ID: "lost-session", Status: "interrupted", InterruptRequested: true}
	recovery := domain.QuestionRecovery{ID: "lost-repair", RunID: run.ID, Stage: "fallback", Confirmation: "native", Question: "Which target?"}
	if err := a.Store.Put(ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.saveRecovery(&recovery); err != nil {
		t.Fatal(err)
	}
	if err := a.Runs.recoverTextQuestion(ctx, &recovery); err != nil {
		t.Fatal(err)
	}
	id := recovery.ID + ":text"
	answer := raw(map[string]any{"answers": map[string]any{"reply": map[string]any{"answers": []string{"private-uncertain-reply-8310"}}}})
	if err := a.Runs.Answer(ctx, id, answer); err == nil {
		t.Fatal("unavailable session confirmed delivery")
	}
	var at domain.Attention
	if err := a.Store.Get(ctx, "attention", id, &at); err != nil || at.Status != "uncertain" {
		t.Fatal("uncertain delivery was not recorded", err)
	}
	if saved, err := a.Vault.Get(ctx, "attention-answer:"+id); err != nil || saved != string(answer) {
		t.Fatal("uncertain reply was lost", err)
	}
	if err := a.Runs.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := a.ListAttention(ctx, AttentionInput{RunID: run.ID})
	if err != nil || len(page.Requests) != 0 {
		t.Fatal("uncertain reply was reopened", err)
	}
	if err := a.Runs.Answer(ctx, id, answer); err == nil {
		t.Fatal("uncertain reply was replayed")
	}
	events, err := a.Store.Events(ctx, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Data), "private-uncertain-reply-8310") {
			t.Fatal("reply leaked into journal")
		}
	}
}

func TestNativeAnswerRollsBackWhenRecoveryCannotBeSaved(t *testing.T) {
	a, ctx := fixture(t)
	run := prepareRun(t, a, ctx)
	if _, err := a.Runs.Start(ctx, run.ID, false); err != nil {
		t.Fatal(err)
	}
	at := pendingQuestion(t, a, "rpc")
	at.RecoveryID = "missing-recovery"
	if err := a.Store.Put(ctx, "attention", at.ID, at, ""); err != nil {
		t.Fatal(err)
	}
	s, err := a.Runs.get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.pending[at.ID] = at
	s.mu.Unlock()
	answer := raw(map[string]any{"answers": map[string]any{"scope": map[string]any{"answers": []string{"Focused"}}}})
	if err := a.Runs.Answer(ctx, at.ID, answer); err == nil {
		t.Fatal("answer succeeded without saving recovery")
	}
	if err := a.Store.Get(ctx, "attention", at.ID, &at); err != nil || at.Status != "pending" {
		t.Fatal("failed transaction consumed the request", err)
	}
	if _, err := a.Vault.Get(ctx, "attention-answer:"+at.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed transaction left a saved answer", err)
	}
	page, err := a.ListAttention(ctx, AttentionInput{RunID: run.ID})
	if err != nil || len(page.Requests) != 1 || page.Requests[0].Request.ID != at.ID {
		t.Fatal("failed transaction hid the unanswered question", err)
	}
}

func TestAnsweredRepairDisconnectDoesNotCreateFallback(t *testing.T) {
	a, ctx := fixture(t)
	r := &domain.QuestionRecovery{ID: "repair", RunID: "run", Stage: "answered", Question: "Which target?"}
	if err := a.Runs.saveRecovery(r); err != nil {
		t.Fatal(err)
	}
	s := &session{run: domain.Run{ID: r.RunID, Status: "running"}, pending: map[string]domain.Attention{}, recovery: r}
	a.Runs.onMessage(s, codex.Message{Method: "process/closed"})
	page, err := a.ListAttention(ctx, AttentionInput{RunID: r.RunID})
	if err != nil || len(page.Requests) != 0 || r.Stage != "resolved" {
		t.Fatal("disconnect recreated an answered question", err)
	}
}
