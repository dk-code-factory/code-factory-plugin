package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func materialAnalysis(t *testing.T) map[string]any {
	t.Helper()
	a := parity(t).Analysis
	a.Questions = []Question{{ID: "scope", Status: "open", Material: true, Statements: "Scope conflicts", AffectedWork: "Review placement", Recommendation: "Preserve boundary", ProposedChanges: "Choose scope", Options: []Option{{"narrow", "Less change", "Deferred work"}, {"wide", "Full coverage", "More change"}}, Evidence: []string{"pr/body"}}}
	var fields map[string]any
	if err := json.Unmarshal(rawJSON(a), &fields); err != nil {
		t.Fatal(err)
	}
	fields["questions"].([]any)[0].(map[string]any)["resolution"] = ""
	return fields
}

func analysisObjectAt(t *testing.T, fields map[string]any, path ...any) map[string]any {
	t.Helper()
	var current any = fields
	for _, key := range path {
		switch key := key.(type) {
		case string:
			current = current.(map[string]any)[key]
		case int:
			current = current.([]any)[key]
		default:
			t.Fatalf("invalid test path key %v", key)
		}
	}
	return current.(map[string]any)
}

func TestDecodeAnalysisRejectsSchemaMemberAliases(t *testing.T) {
	for _, object := range []struct {
		name string
		path []any
	}{
		{"root", nil},
		{"boundary_dimensions", []any{"boundaries"}},
		{"placement_dimensions", []any{"placement"}},
		{"boundary_conclusion", []any{"boundaries", "correctness", 0}},
		{"placement_conclusion", []any{"placement", "production"}},
		{"test_strategy", []any{"test_strategy", 0}},
		{"question", []any{"questions", 0}},
		{"option", []any{"questions", 0, "options", 0}},
	} {
		for member := range analysisObjectAt(t, materialAnalysis(t), object.path...) {
			for _, duplicate := range []bool{false, true} {
				name := object.name + "/" + member + "/renamed"
				if duplicate {
					name = object.name + "/" + member + "/additional_alias"
				}
				t.Run(name, func(t *testing.T) {
					fields := materialAnalysis(t)
					entry := analysisObjectAt(t, fields, object.path...)
					entry[strings.ToUpper(member)] = entry[member]
					if !duplicate {
						delete(entry, member)
					}
					var a Analysis
					if err := DecodeAnalysis(rawJSON(fields), &a); err == nil {
						t.Fatal("noncanonical schema member accepted")
					}
				})
			}
		}
	}
}

func TestDecodeAnalysisRequiresExplicitBooleans(t *testing.T) {
	for _, field := range []struct {
		name string
		path []any
		key  string
	}{
		{"boundary", []any{"boundaries", "correctness", 0}, "material"},
		{"placement", []any{"placement", "production"}, "material"},
		{"question", []any{"questions", 0}, "material"},
		{"synchronization", nil, "sync_required"},
	} {
		for _, value := range []struct {
			name    string
			value   any
			present bool
			valid   bool
		}{
			{"missing", nil, false, false}, {"null", nil, true, false},
			{"string", "false", true, false}, {"number", 0, true, false},
			{"false", false, true, true}, {"true", true, true, true},
		} {
			t.Run(field.name+"/"+value.name, func(t *testing.T) {
				fields := materialAnalysis(t)
				entry := analysisObjectAt(t, fields, field.path...)
				delete(entry, field.key)
				if value.present {
					entry[field.key] = value.value
				}
				var a Analysis
				if err := DecodeAnalysis(rawJSON(fields), &a); (err == nil) != value.valid {
					t.Fatalf("valid=%t: %v", value.valid, err)
				}
			})
		}
	}
}

func TestDecodeAnalysisRejectsAliasedCollectionsWithOmittedMateriality(t *testing.T) {
	for _, collection := range []struct {
		name string
		path []any
	}{
		{"boundaries", []any{"boundaries", "correctness", 0}},
		{"placement", []any{"placement", "production"}},
		{"questions", []any{"questions", 0}},
	} {
		for _, null := range []bool{false, true} {
			t.Run(collection.name+"/"+map[bool]string{false: "missing", true: "null"}[null], func(t *testing.T) {
				fields := materialAnalysis(t)
				entry := analysisObjectAt(t, fields, collection.path...)
				delete(entry, "material")
				if null {
					entry["material"] = nil
				}
				fields[strings.ToUpper(collection.name)] = fields[collection.name]
				delete(fields, collection.name)
				var a Analysis
				if err := DecodeAnalysis(rawJSON(fields), &a); err == nil {
					t.Fatal("collection alias bypassed explicit materiality")
				}
			})
		}
	}
}

func TestDecodeAnalysisPreservesStrictJSONAndReadiness(t *testing.T) {
	canonical := string(rawJSON(materialAnalysis(t)))
	for _, input := range []string{
		strings.Replace(canonical, `"material":true`, `"material":false,"material":true`, 1),
		strings.Replace(canonical, `"material":true`, `"material":false,"mater\u0069al":true`, 1),
		strings.Replace(canonical, `"material":true`, `"material":true,"unknown":false`, 1),
		canonical + ` {}`,
	} {
		var a Analysis
		if err := DecodeAnalysis([]byte(input), &a); err == nil {
			t.Fatal("ambiguous JSON accepted", input)
		}
	}
	var a Analysis
	if err := DecodeAnalysis([]byte(canonical), &a); err != nil {
		t.Fatal(err)
	}
	if a.Readiness() != "needs_decision" {
		t.Fatal("canonical material question did not block review")
	}
	a.Questions[0].Material = false
	a.SyncRequired = true
	if a.Readiness() != "sync_pending" {
		t.Fatal("synchronization no longer blocks review")
	}
	a.SyncRequired = false
	if a.Readiness() != "ready" {
		t.Fatal("nonmaterial question unexpectedly blocks review")
	}
	a.Boundaries["correctness"][0].Material = true
	a.Boundaries["correctness"][0].Agreement = "proposal"
	if a.Readiness() != "needs_decision" {
		t.Fatal("material boundary proposal did not block review")
	}
	a.Boundaries["correctness"][0].Material = false
	c := a.Placement["production"]
	c.Material, c.Agreement = true, "proposal"
	a.Placement["production"] = c
	if a.Readiness() != "needs_decision" {
		t.Fatal("material placement proposal did not block review")
	}
}

type analysisResponseWorkers struct {
	*fixtureWorkers
	responses   [][]byte
	corrections []string
}

func (w *analysisResponseWorkers) Prepare(_ context.Context, _ PreparationInput, correction string) ([]byte, error) {
	w.corrections = append(w.corrections, correction)
	index := len(w.corrections) - 1
	if index >= len(w.responses) {
		index = len(w.responses) - 1
	}
	return w.responses[index], nil
}

func TestAnalysisMaterialityCorrectionCannotBypassDecisionGate(t *testing.T) {
	for _, corrected := range []bool{false, true} {
		t.Run(map[bool]string{false: "exhausted", true: "corrected_material_question"}[corrected], func(t *testing.T) {
			w, s, workers, _ := workflowFixture(t)
			invalid := materialAnalysis(t)
			delete(analysisObjectAt(t, invalid, "questions", 0), "material")
			invalid["Questions"] = invalid["questions"]
			delete(invalid, "questions")
			responses := [][]byte{rawJSON(invalid)}
			if corrected {
				responses = append(responses, rawJSON(materialAnalysis(t)))
			}
			responsesWorker := &analysisResponseWorkers{fixtureWorkers: workers, responses: responses}
			w.Workers = responsesWorker
			result, err := w.Run(context.Background(), s)
			if err == nil || result.Complete || len(workers.calls) != 0 {
				t.Fatal("invalid or unanswered material analysis reached review", result, err)
			}
			if len(responsesWorker.corrections) != 2 || responsesWorker.corrections[0] != "" || responsesWorker.corrections[1] == "" {
				t.Fatal("analysis correction was not bounded to one", responsesWorker.corrections)
			}
			if corrected {
				if !errors.Is(err, ErrWaiting) || s.Gate == nil || len(s.Gate.Questions) != 1 || !s.Gate.Questions[0].Material {
					t.Fatal("corrected material analysis lost its durable gate", s.Gate, err)
				}
			} else if errors.Is(err, ErrWaiting) || s.Preparation != nil || s.Gate != nil {
				t.Fatal("invalid correction retained a preparation or decision gate", err)
			}
		})
	}
}
