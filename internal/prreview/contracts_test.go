package prreview

import (
	"context"
	"strings"
	"testing"
)

func TestResponseRequiresExactSchemaMemberNames(t *testing.T) {
	patch := parity(t).Patch
	validFinding := `{"findings":[{"severity":"Minor","path":"a.go","line":2,"message":"State is lost","fix":"Preserve state"}]}`
	for _, response := range []string{
		`{"findings":[]}`,
		`{"\u0066indings":[]}`,
		validFinding,
		`{"status":"incomplete","findings":[],"message":"missing evidence"}`,
		`{"status":"tokens_exhausted","findings":[],"message":"usage limit"}`,
	} {
		if _, err := ValidateResponse([]byte(response), patch); err != nil {
			t.Fatalf("valid schema response %s: %v", response, err)
		}
	}
	for _, member := range []string{"findings", "severity", "path", "line", "message", "fix"} {
		for _, spelling := range []string{strings.ToUpper(member[:1]) + member[1:], strings.ToUpper(member)} {
			t.Run(spelling, func(t *testing.T) {
				response := strings.Replace(validFinding, `"`+member+`":`, `"`+spelling+`":`, 1)
				if _, err := ValidateResponse([]byte(response), patch); err == nil {
					t.Fatalf("mixed-case member accepted: %s", response)
				}
			})
		}
	}
	for _, member := range []string{"status", "findings", "message"} {
		t.Run("incomplete-"+member, func(t *testing.T) {
			response := strings.Replace(`{"status":"incomplete","findings":[],"message":"missing evidence"}`, `"`+member+`":`, `"`+strings.ToUpper(member[:1])+member[1:]+`":`, 1)
			if _, err := ValidateResponse([]byte(response), patch); err == nil {
				t.Fatalf("mixed-case incomplete member accepted: %s", response)
			}
		})
	}
}

func TestResponseStillRejectsAmbiguousAndMalformedSchema(t *testing.T) {
	for _, response := range []string{
		`{"findings":[],"\u0066indings":[]}`,
		`{"findings":[],"Findings":[]}`,
		`{"findings":[{"severity":"Minor","severity":"Major","path":"a.go","line":2,"message":"x","fix":"y"}]}`,
		`{"findings":[{"severity":"Minor","path":"a.go","line":2,"message":"x","fix":"y","extra":true}]}`,
		`{"findings":[],"extra":true}`,
		`{"findings":null}`,
		`{"findings":{}}`,
		`{"findings":[null]}`,
		`{"findings":[{"severity":"Minor","path":"a.go","line":2,"message":"x"}]}`,
		`{"findings":[{"severity":"Minor","path":"a.go","line":"2","message":"x","fix":"y"}]}`,
		`{"findings":[{"severity":"Minor","path":"a.go","line":2,"message":null,"fix":"y"}]}`,
		`{"status":null,"findings":[],"message":"reason"}`,
		`{"status":"completed","findings":[],"message":"reason"}`,
		`{"status":"Incomplete","findings":[],"message":"reason"}`,
		`{"status":"incomplete","findings":[],"message":null}`,
		`{"status":"incomplete","findings":[],"message":" "}`,
		`{"status":"incomplete","findings":[{"severity":"Minor","path":"a.go","line":2,"message":"x","fix":"y"}],"message":"reason"}`,
		`{"findings":[]} {}`,
	} {
		if _, err := ValidateResponse([]byte(response), parity(t).Patch); err == nil {
			t.Fatalf("malformed schema response accepted: %s", response)
		}
	}
}

func TestMixedCaseReviewerOutputGetsOneCorrection(t *testing.T) {
	for _, malformed := range []string{
		`{"Findings":[]}`,
		`{"findings":[{"Severity":"Minor","path":"a.go","line":2,"message":"State is lost","fix":"Preserve state"}]}`,
		`{"Status":"incomplete","findings":[],"message":"missing evidence"}`,
	} {
		for _, outcome := range []string{"completed", "failed", "incomplete"} {
			t.Run(malformed+"/"+outcome, func(t *testing.T) {
				w, s, workers, _ := workflowFixture(t)
				correction := `{"findings":[]}`
				switch outcome {
				case "failed":
					correction = malformed
				case "incomplete":
					correction = `{"status":"incomplete","findings":[],"message":"missing evidence"}`
				}
				// The third response must never run, even after two invalid ones.
				workers.results["one"] = [][]byte{[]byte(malformed), []byte(correction), []byte(`{"findings":[]}`)}
				result, err := w.Run(context.Background(), s)
				if err != nil {
					t.Fatal(err)
				}
				if workers.calls["one"] != 2 || workers.calls["two"] != 1 || result.Complete != (outcome == "completed") {
					t.Fatalf("correction accounting: %+v, calls %+v", result, workers.calls)
				}
				prompts := workers.corrections["one"]
				if len(prompts) != 2 || prompts[0] != "" || !strings.Contains(prompts[1], "Validator error:") || !strings.Contains(prompts[1], "Previous response: "+malformed) {
					t.Fatalf("correction lost validation evidence: %q", prompts)
				}
				var checkpoint Checkpoint
				if err := w.Artifacts.Load(s.Reviews["one"], &checkpoint); err != nil {
					t.Fatal(err)
				}
				if checkpoint.Status != outcome || (outcome != "completed" && (len(result.Failed) != 1 || result.Failed[0].Status != outcome)) {
					t.Fatalf("malformed/incomplete output became clean: %+v %+v", checkpoint, result)
				}
			})
		}
	}
}
