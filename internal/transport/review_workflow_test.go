package transport

import (
	"codefactory/internal/domain"
	"codefactory/internal/prreview"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPReviewPolicyPresenceInheritanceAndDependencyDiagnostic(t *testing.T) {
	f := newTrackingFixture(t)
	mutate := func(t *testing.T, name string, args map[string]any) (map[string]any, bool) {
		result, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, name, args)
		if err != nil {
			t.Fatal(err)
		}
		return result, result["isError"] == true
	}

	a, ctx := f.a, f.ctx
	if err := a.SaveRepository(ctx, domain.Repository{ID: "o/r"}); err != nil {
		t.Fatal(err)
	}
	pr := domain.Item{ID: "o/r#2", Repository: "o/r", Kind: "pr", Number: 2, URL: "https://github.com/o/r/pull/2", Author: "owner", Head: strings.Repeat("a", 40)}
	if err := a.Store.Put(ctx, "item", pr.ID, pr, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "identity", "github", domain.GitHubViewer{Login: "reviewer"}, ""); err != nil {
		t.Fatal(err)
	}
	config, _ := a.Config(ctx)
	config.Reviewers = []prreview.Reviewer{{ID: "one", Kind: "skill", SkillID: "pending-install"}}
	config.AutoPostReview = true
	config.ReviewRules = "Use comments"
	if err := a.Runs.SaveSkill(ctx, domain.Skill{Name: "dk-pr-iter", Content: "---\nname: dk-pr-iter\ndescription: Iteration fixture.\n---\nReview."}); err != nil {
		t.Fatal(err)
	}
	config.ExecutionMode = "single-codex"
	config.PRReviewSkillID = "managed:dk-pr-iter"
	if err := a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	// Old cached widgets cannot clear new settings by omission.
	encoded, _ := json.Marshal(config)
	var older map[string]any
	_ = json.Unmarshal(encoded, &older)
	delete(older, "autoPostReview")
	delete(older, "reviewers")
	delete(older, "reviewRules")
	delete(older, "executionMode")
	delete(older, "prReviewSkillId")
	_, bad := mutate(t, "save_settings", older)
	if bad {
		t.Fatal("omitted settings rejected")
	}
	saved, _ := a.Config(ctx)
	if saved.ExecutionMode != "single-codex" || saved.PRReviewSkillID != config.PRReviewSkillID || !saved.AutoPostReview || len(saved.Reviewers) != 1 || saved.ReviewRules != "Use comments" {
		t.Fatal("omitted settings cleared workflow configuration", saved)
	}
	config.ExecutionMode = "workflow"
	if err := a.SaveConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, bad = mutate(t, "save_repository", map[string]any{"id": "o/r", "autoPostReview": false, "reviewRules": "Local rules", "reviewers": []prreview.Reviewer{}}); bad {
		t.Fatal("repo policy rejected")
	}
	if _, bad = mutate(t, "save_repository", map[string]any{"id": "o/r", "base": "main"}); bad {
		t.Fatal("omitted repo policy rejected")
	}
	policy, bad := f.call(t, "read_review_policy", map[string]any{"id": pr.ID})
	if bad || policy["effective"].(map[string]any)["enabled"] != false || policy["effective"].(map[string]any)["source"] != "repository" {
		t.Fatal(policy)
	}
	if _, bad = f.call(t, "save_review_policy", map[string]any{"id": pr.ID, "autoPostReview": true}); bad {
		t.Fatal("PR policy rejected")
	}
	if _, bad = f.call(t, "save_review_policy", map[string]any{"id": pr.ID}); bad {
		t.Fatal("omitted PR policy rejected")
	}
	policy, bad = f.call(t, "read_review_policy", map[string]any{"id": pr.ID})
	if bad || policy["autoPostReview"] != true {
		t.Fatal("omitted PR override cleared", policy)
	}
	policy, bad = f.call(t, "save_review_policy", map[string]any{"id": pr.ID, "autoPostReview": nil})
	if bad || policy["effective"].(map[string]any)["source"] != "repository" {
		t.Fatal("null did not restore inheritance", policy)
	}
	if _, bad = mutate(t, "save_repository", map[string]any{"id": "o/r", "autoPostReview": nil, "reviewers": nil, "reviewRules": nil}); bad {
		t.Fatal("repository inheritance rejected")
	}
	run, bad := f.call(t, "prepare_pr_review", map[string]any{"itemId": pr.ID, "autoPostReview": false})
	if bad {
		t.Fatal(run)
	}
	if run["reviewWorkflow"] != prreview.Contract || run["effectiveAutoPostReview"].(map[string]any)["enabled"] != false || run["effectiveAutoPostReview"].(map[string]any)["source"] != "launch" {
		t.Fatal("launch policy not retained", run)
	}
	result, bad := f.call(t, "start_run", map[string]any{"id": run["id"]})
	if !bad || !strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "skill unavailable") {
		t.Fatal("missing retained reviewer skill did not fail before execution", result)
	}
	policy, bad = f.call(t, "read_review_policy", map[string]any{"id": pr.ID})
	if bad || policy["effective"].(map[string]any)["source"] != "global" {
		t.Fatal("repository null did not restore global policy", policy)
	}
}
func TestMCPRejectsForgedReviewArtifactAuthority(t *testing.T) {
	f := newTrackingFixture(t)
	run := domain.Run{ID: "legacy", Status: "idle", ItemID: "o/r#2"}
	if err := f.a.Store.Put(f.ctx, "run", run.ID, run, ""); err != nil {
		t.Fatal(err)
	}
	result, bad := f.call(t, "post_retained_review", map[string]any{"runId": run.ID, "artifact": map[string]any{"path": "/tmp/forged.json", "sha256": strings.Repeat("a", 64)}, "event": "APPROVE"})
	if !bad {
		t.Fatal("legacy/model publication claim accepted", result)
	}
}
