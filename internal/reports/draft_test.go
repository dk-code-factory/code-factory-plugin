package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func draftContext() *investigationContext {
	return &investigationContext{Version: 1, Repositories: []repositoryContext{{Repository: DefaultRepository, Labels: []string{"bug"}, Candidates: []duplicateCandidate{{Number: 17}}}, {Repository: routes["agent-workflow"]}, {Repository: routes["dk-workflows"]}}}
}
func TestDeterministicReportTemplatesAndFallback(t *testing.T) {
	markdown := normalizeTemplate(".github/ISSUE_TEMPLATE/bug.md", "---\nname: Bug\nabout: Report a bug\nlabels: bug, absent\n---\n## What happened\nDescribe the observation.\n\n## Reproduction\nDescribe the steps.\n")
	form := normalizeTemplate(".github/ISSUE_TEMPLATE/bug.yml", `name: Bug
description: Report a bug
labels: [bug, absent]
body:
- type: markdown
  attributes:
    value: Please describe the issue.
- type: input
  id: version
  attributes:
    label: Version
  validations:
    required: true
- type: textarea
  id: logs
  attributes:
    label: Logs
    render: shell
- type: dropdown
  id: os
  attributes:
    label: Operating system
    options: [Linux, macOS]
- type: checkboxes
  id: checked
  attributes:
    label: Checks
    options:
    - label: I reproduced this on a clean install
      required: true
`)
	if markdown.Unsupported != "" || form.Unsupported != "" {
		t.Fatal(markdown, form)
	}
	for _, test := range []struct {
		name     string
		template *issueTemplate
		answers  []fieldAnswer
		want     string
	}{
		{"generic", nil, nil, "Layout limitation:"},
		{"markdown", &markdown, []fieldAnswer{{ID: "section-1", Text: "Observed delivery failure"}, {ID: "section-2", Text: "Unknown timing"}}, "## What happened\n\nObserved delivery failure"},
		{"form", &form, []fieldAnswer{{ID: "version", Text: "1.2.3"}, {ID: "logs", Text: "delivery failed"}, {ID: "os", Choices: []string{"Linux"}}, {ID: "checked", Choices: []string{"I reproduced this on a clean install"}}}, "- [x] I reproduced this on a clean install"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := draftContext()
			d := draftFixture(findings())
			d.Labels = []string{}
			if test.template != nil {
				c.Repositories[0].Templates = []issueTemplate{*test.template}
				d.TemplateID = test.template.ID
				d.Answers = test.answers
			}
			f, err := renderDraft(d, c, observation(), []string{"I reproduced this on a clean install"})
			if err != nil || !strings.Contains(f.Body, test.want) || f.Repository != DefaultRepository || test.template == nil && !strings.Contains(f.Body, "## Uncertainty") {
				t.Fatal(f, err)
			}
			if test.template != nil && (len(f.Labels) != 1 || f.Labels[0] != "bug") {
				t.Fatal("template labels not filtered to catalog", f.Labels)
			}
			if test.name == "markdown" && strings.Index(f.Body, "## What happened") > strings.Index(f.Body, "## Reproduction") {
				t.Fatal("template order changed")
			}
			if test.name == "form" && (!strings.Contains(f.Body, "```shell\ndelivery failed\n```") || strings.Contains(f.Body, "Please describe the issue.") || strings.Contains(f.Body, "## Investigation report")) {
				t.Fatal("render hint lost", f.Body)
			}
		})
	}
}
func TestFormFieldsWithoutIDsGetStableOrderedIdentities(t *testing.T) {
	raw := "name: Bug\nbody:\n- type: input\n  attributes: {label: Version}\n- type: textarea\n  attributes: {label: Details}\n"
	template := normalizeTemplate("bug.yml", raw)
	if template.Unsupported != "" || len(template.Fields) != 2 || template.Fields[0].ID == template.Fields[1].ID {
		t.Fatal(template)
	}
	c := draftContext()
	c.Repositories[0].Templates = []issueTemplate{template}
	d := draftFixture(findings())
	d.TemplateID = template.ID
	if _, err := renderDraft(d, c, observation(), nil); err != nil {
		t.Fatal(err)
	}
	d.TemplateID = ""
	if _, err := renderDraft(d, c, observation(), nil); err == nil {
		t.Fatal("generic fallback bypassed an available required-field layout")
	}
}

func TestUnsupportedTemplatesFailToGenericLayout(t *testing.T) {
	for _, raw := range []struct{ id, text string }{
		{"bug.md", "---\nlabels: [broken\n---\n## Bug"},
		{"bug.md", "## Bug\n- [ ] Confirmed"},
		{"bug.md", "## Bug\n### Nested"},
		{"bug.md", "preamble\n## Bug"},
		{"bug.yml", "name: Bug\nbody:\n- type: upload\n  id: upload\n  attributes: {label: File}"},
		{"bug.yml", "name: Bug\nbody:\n- type: textarea\n  id: logs\n  attributes: {label: Logs, unknown: true}"},
		{"bug.yml", "name: One\nname: Two\nbody: []"},
	} {
		t.Run(raw.text, func(t *testing.T) {
			template := normalizeTemplate(raw.id, raw.text)
			if template.Unsupported == "" {
				t.Fatal("unsupported template accepted", template)
			}
			c := draftContext()
			c.Repositories[0].Templates = []issueTemplate{template}
			d := draftFixture(findings())
			d.TemplateID = raw.id
			f, err := renderDraft(d, c, observation(), nil)
			if err != nil || !strings.Contains(f.Body, "layout is unsupported:") {
				t.Fatal(f, err)
			}
		})
	}
}
func TestStructuredDraftRejectsContradictionsAndInventedAnswers(t *testing.T) {
	for _, change := range []func(*structuredDraft){
		func(d *structuredDraft) { d.Summary = " " },
		func(d *structuredDraft) { d.Classification = "other" },
		func(d *structuredDraft) { d.Classification = "insufficient_information" },
		func(d *structuredDraft) { d.Classification = "explained"; d.MissingInformation = "What happened?" },
		func(d *structuredDraft) { d.Classification = "duplicate"; d.DuplicateNumber = 0 },
		func(d *structuredDraft) { d.Classification = "duplicate"; d.DuplicateNumber = 99 },
		func(d *structuredDraft) { d.DuplicateNumber = 17 },
		func(d *structuredDraft) { d.MissingInformation = "Which version did you use?" },
		func(d *structuredDraft) { d.Component = "unknown" },
		func(d *structuredDraft) { d.SourceCommit = "main" },
		func(d *structuredDraft) { d.SourcePath = "../private" },
		func(d *structuredDraft) { d.OwnershipReason = "" },
		func(d *structuredDraft) { d.Labels = []string{"invented"} },
		func(d *structuredDraft) { d.TemplateID = "invented" },
		func(d *structuredDraft) { d.Title = strings.Repeat("x", 257) },
		func(d *structuredDraft) { d.Content.Evidence = strings.Repeat("x", 60001) },
		func(d *structuredDraft) { d.Version = 999 },
	} {
		d := draftFixture(findings())
		change(&d)
		if _, err := renderDraft(d, draftContext(), observation(), nil); err == nil {
			t.Fatalf("invalid structured draft accepted: %+v", d)
		}
	}
	for _, component := range []string{"plugin", "code-factory", "agent-workflow", "dk-workflows"} {
		d := draftFixture(findings())
		d.Component = component
		f, err := renderDraft(d, draftContext(), observation(), nil)
		if err != nil || f.Repository != routes[component] {
			t.Fatal("routing", component, f, err)
		}
	}
	if _, err := decodeStructured(json.RawMessage(`{"version":2,"repository":"invented"}`)); err == nil {
		t.Fatal("model still chooses repository")
	}
}
func TestRequiredFactsChoicesAttestationsAndRestrictions(t *testing.T) {
	template := normalizeTemplate("bug.yml", `name: Bug
body:
- type: input
  id: version
  attributes: {label: Version}
  validations: {required: true}
- type: dropdown
  id: os
  attributes: {label: OS, options: [Linux, macOS]}
- type: checkboxes
  id: checks
  attributes:
    label: Checks
    options:
    - label: I tested a clean install
      required: true
`)
	c := draftContext()
	c.Repositories[0].Templates = []issueTemplate{template}
	c.Repositories[0].Documents = []contextDocument{{Path: "SECURITY.md", Content: "Report vulnerabilities privately."}}
	d := draftFixture(findings())
	d.TemplateID = template.ID
	d.GuidanceReviewed = []string{"SECURITY.md"}
	f, err := renderDraft(d, c, observation(), nil)
	if err != nil || f.Classification != "insufficient_information" || !strings.Contains(f.MissingInformation, "Version") || !strings.Contains(f.MissingInformation, "I tested a clean install") {
		t.Fatal(f, err)
	}
	for _, answers := range [][]fieldAnswer{
		{{ID: "version", Text: "1.0\n2.0"}}, {{ID: "os", Choices: []string{"Windows"}}}, {{ID: "os", Choices: []string{"Linux", "macOS"}}}, {{ID: "os", Text: "Linux"}}, {{ID: "checks", Choices: []string{"I tested a clean install"}}}, {{ID: "unknown", Text: "x"}}, {{ID: "version", Text: "1"}, {ID: "version", Text: "2"}},
	} {
		d.Answers = answers
		if _, err := renderDraft(d, c, observation(), []string{"I did not: I tested a clean install"}); err == nil {
			t.Fatal("invented choice or attestation accepted", answers)
		}
	}
	d.Answers = nil
	d.Restriction = "security"
	d.RestrictionReason = "Use the private advisory channel from SECURITY.md"
	d.TemplateID = ""
	f, err = renderDraft(d, c, observation(), nil)
	if err != nil || f.Classification != "insufficient_information" || f.Body != "" || !strings.Contains(f.MissingInformation, "private advisory") {
		t.Fatal("generic fallback bypassed security restriction", f, err)
	}
	d.Restriction = "none"
	d.RestrictionReason = ""
	d.GuidanceReviewed = nil
	if _, err := renderDraft(d, c, observation(), nil); err == nil {
		t.Fatal("unreviewed security guidance accepted")
	}
	d.GuidanceReviewed = []string{"SECURITY.md"}
	d.Content.Environment = ""
	c.Repositories[0].Templates = nil
	f, err = renderDraft(d, c, observation(), nil)
	if err != nil || f.Classification != "insufficient_information" || !strings.Contains(f.MissingInformation, "Environment") {
		t.Fatal("missing known fact not actionable", f, err)
	}
}

func TestStructuredDraftSanitizedBeforeRetentionAndPublication(t *testing.T) {
	s, ctx := fixture(t)
	engine, g := setupEngine(s)
	engine.findings.Body = "Authorization: Bearer private-value\nretained diagnostic"
	r := submit(t, s, ctx)
	step(t, s, ctx)
	draft := get(t, s, ctx, r.ID)
	if draft.Findings == nil || strings.Contains(draft.Findings.Body, "private-value") || !strings.Contains(draft.Findings.Body, "retained diagnostic") {
		t.Fatal(draft.Findings)
	}
	step(t, s, ctx)
	if g.creates != 1 || strings.Contains(g.created[0].Body, "private-value") {
		t.Fatal(g.created)
	}
}

func TestLegacyDraftRemainsOnPublicationPath(t *testing.T) {
	s, ctx := fixture(t)
	_, g := setupEngine(s)
	r := submit(t, s, ctx)
	f := findings()
	f.Body = "retained legacy layout"
	r.Findings = &f
	r.Processing = "filing"
	if _, err := s.update(ctx, r.ID, func(current *Report) error { *current = r; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.putRuntime(ctx, r.ID, Runtime{Generation: 1, AdmissionID: r.ID, Intent: &f}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishReport(context.Background(), r.ID); err != nil {
		t.Fatal(err)
	}
	if g.creates != 1 || g.created[0].Body != f.Body || g.created[0].Repository != f.Repository {
		t.Fatal("legacy draft reinterpreted", g.created)
	}
}

func TestControllerRejectedContractFinalizesBeforeFreshRecovery(t *testing.T) {
	s, ctx := fixture(t)
	engine, g := setupEngine(s)
	engine.status = "failed"
	engine.workflowError = "job investigate: invalid structured output: $.version required"
	engine.completeErr = fmt.Errorf("lifecycle unavailable")
	r := submit(t, s, ctx)
	step(t, s, ctx)
	rt, _ := s.runtime(ctx, r.ID)
	if rt.UnusableResult || engine.completions != 1 || g.creates != 0 {
		t.Fatal("failed finalization released the investigation", rt)
	}
	engine.completeErr = nil
	if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID, Reconciled: true, Note: "Inspected rejected output; no external effects"}); err != nil {
		t.Fatal(err)
	}
	step(t, s, ctx)
	rt, _ = s.runtime(ctx, r.ID)
	if !rt.UnusableResult || get(t, s, ctx, r.ID).Processing != "needs_attention" || g.creates != 0 {
		t.Fatal("rejected contract not finalized as unusable", rt)
	}
	if _, err := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); err != nil {
		t.Fatal(err)
	}
	engine.status = "succeeded"
	engine.workflowError = ""
	engine.findings = findings()
	step(t, s, ctx)
	fresh, _ := s.runtime(ctx, r.ID)
	if fresh.Generation != 2 || g.preparations != 2 || fresh.AdmissionID == rt.AdmissionID || get(t, s, ctx, r.ID).Processing != "filing" {
		t.Fatal("recovery replayed rejected contract", fresh)
	}
}
