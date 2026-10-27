package prreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Conclusion struct {
	Text      string   `json:"text"`
	Agreement string   `json:"agreement"`
	Material  bool     `json:"material"`
	Rationale string   `json:"rationale"`
	Evidence  []string `json:"evidence"`
	Locations []string `json:"locations,omitempty"`
}
type TestStrategy struct {
	Behavior   string   `json:"behavior"`
	Existing   string   `json:"existing"`
	Missing    string   `json:"missing"`
	Level      string   `json:"level"`
	Location   string   `json:"location"`
	Command    string   `json:"command"`
	Rationale  string   `json:"rationale"`
	Limitation string   `json:"limitation"`
	Evidence   []string `json:"evidence"`
}
type Option struct {
	Choice   string `json:"choice"`
	Benefits string `json:"benefits"`
	Costs    string `json:"costs"`
}
type Question struct {
	ID              string   `json:"id"`
	Status          string   `json:"status"`
	Material        bool     `json:"material"`
	Statements      string   `json:"statements"`
	AffectedWork    string   `json:"affected_work"`
	Recommendation  string   `json:"recommendation"`
	ProposedChanges string   `json:"proposed_changes"`
	Options         []Option `json:"options"`
	Resolution      string   `json:"resolution,omitempty"`
	Evidence        []string `json:"evidence"`
}
type Analysis struct {
	Boundaries         map[string][]Conclusion `json:"boundaries"`
	Placement          map[string]Conclusion   `json:"placement"`
	TestStrategy       []TestStrategy          `json:"test_strategy"`
	Questions          []Question              `json:"questions"`
	ReviewerAddenda    map[string]string       `json:"reviewer_addenda"`
	WorkerInstructions string                  `json:"worker_instructions"`
	CompletionCriteria string                  `json:"completion_criteria"`
	SyncRequired       bool                    `json:"sync_required"`
	SyncReason         string                  `json:"sync_reason"`
}
type Binding struct {
	Reviewer   Reviewer          `json:"reviewer"`
	Resources  map[string]string `json:"resources"`
	Modes      map[string]uint32 `json:"modes"`
	Entrypoint string            `json:"entrypoint"`
	Identity   string            `json:"identity"`
}

func (b Binding) Validate() error {
	if err := ValidateReviewers([]Reviewer{b.Reviewer}); err != nil {
		return err
	}
	if len(b.Resources) == 0 || !SafePath(b.Entrypoint) {
		return errors.New("reviewer package resources and entrypoint are required")
	}
	if _, ok := b.Resources[b.Entrypoint]; !ok {
		return errors.New("reviewer entrypoint absent from retained package")
	}
	for p := range b.Resources {
		if !SafePath(p) {
			return errors.New("unsafe retained reviewer resource")
		}
	}
	copy := b
	copy.Identity = ""
	if b.Identity != Digest(copy) {
		return errors.New("retained reviewer binding changed")
	}
	return nil
}
func PinBinding(b Binding) Binding { b.Identity = ""; b.Identity = Digest(b); return b }

func evidenceReferences(refs []string, e Evidence) error {
	if len(refs) == 0 {
		return errors.New("conclusion requires indexed evidence")
	}
	for _, r := range refs {
		key := strings.SplitN(r, "#", 2)[0]
		if _, ok := e.Sources[key]; !ok {
			return fmt.Errorf("unknown evidence reference %q", r)
		}
	}
	return nil
}
func (a Analysis) Validate(e Evidence, bindings []Binding, prior *Preparation) error {
	check := func(c Conclusion) error {
		if strings.TrimSpace(c.Text) == "" || strings.TrimSpace(c.Rationale) == "" || (c.Agreement != "established" && c.Agreement != "interpretation" && c.Agreement != "proposal") {
			return errors.New("conclusion requires text, agreement, materiality and rationale")
		}
		return evidenceReferences(c.Evidence, e)
	}
	if len(a.Boundaries) != 5 {
		return errors.New("all five engineering boundary dimensions required")
	}
	for _, key := range []string{"correctness", "remediation_depth", "performance", "compatibility", "complexity"} {
		cs := a.Boundaries[key]
		if len(cs) == 0 {
			return fmt.Errorf("missing %s assessment", key)
		}
		for _, c := range cs {
			if err := check(c); err != nil {
				return err
			}
		}
	}
	if len(a.Placement) != 5 {
		return errors.New("all five placement dimensions required")
	}
	for _, key := range []string{"production", "tests", "reuse", "entrypoints", "dependencies"} {
		c, ok := a.Placement[key]
		if !ok || len(c.Locations) == 0 {
			return fmt.Errorf("missing concrete %s placement", key)
		}
		for _, location := range c.Locations {
			if strings.TrimSpace(location) == "" {
				return errors.New("placement requires nonempty concrete locations")
			}
		}
		if err := check(c); err != nil {
			return err
		}
	}
	if len(a.TestStrategy) == 0 {
		return errors.New("test strategy is required")
	}
	for _, t := range a.TestStrategy {
		for _, v := range []string{t.Behavior, t.Existing, t.Missing, t.Level, t.Location, t.Command, t.Rationale, t.Limitation} {
			if strings.TrimSpace(v) == "" {
				return errors.New("test strategy lacks behavior, evidence or validation procedure")
			}
		}
		if t.Level != "inspection" && t.Level != "unit" && t.Level != "integration" && t.Level != "system" && t.Level != "performance" {
			return errors.New("invalid test strategy level")
		}
		if err := evidenceReferences(t.Evidence, e); err != nil {
			return err
		}
	}
	if a.Questions == nil {
		return errors.New("questions must be retained explicitly")
	}
	seen := map[string]bool{}
	for _, q := range a.Questions {
		if strings.TrimSpace(q.ID) == "" || seen[q.ID] || (q.Status != "open" && q.Status != "resolved") || strings.TrimSpace(q.Statements) == "" || strings.TrimSpace(q.AffectedWork) == "" || strings.TrimSpace(q.Recommendation) == "" || strings.TrimSpace(q.ProposedChanges) == "" || len(q.Options) < 2 || (q.Status == "resolved" && strings.TrimSpace(q.Resolution) == "") {
			return errors.New("invalid material decision question")
		}
		seen[q.ID] = true
		for _, o := range q.Options {
			if strings.TrimSpace(o.Choice) == "" || strings.TrimSpace(o.Benefits) == "" || strings.TrimSpace(o.Costs) == "" {
				return errors.New("decision options require choice, benefits and costs")
			}
		}
		if err := evidenceReferences(q.Evidence, e); err != nil {
			return err
		}
	}
	if prior != nil {
		for _, q := range prior.Analysis.Questions {
			if q.Status == "open" && !seen[q.ID] {
				return errors.New("unanswered question lost across preparation refresh")
			}
		}
	}
	if len(a.ReviewerAddenda) != len(bindings) {
		return errors.New("each configured reviewer requires a tailored addendum")
	}
	for _, b := range bindings {
		if strings.TrimSpace(a.ReviewerAddenda[b.Reviewer.ID]) == "" {
			return errors.New("missing tailored reviewer addendum")
		}
	}
	if strings.TrimSpace(a.WorkerInstructions) == "" || strings.TrimSpace(a.CompletionCriteria) == "" || (a.SyncRequired && strings.TrimSpace(a.SyncReason) == "") {
		return errors.New("worker instructions, completion criteria and synchronization assessment required")
	}
	return nil
}
func (a Analysis) Readiness() string {
	for _, cs := range a.Boundaries {
		for _, c := range cs {
			if c.Material && c.Agreement == "proposal" {
				return "needs_decision"
			}
		}
	}
	for _, c := range a.Placement {
		if c.Material && c.Agreement == "proposal" {
			return "needs_decision"
		}
	}
	for _, q := range a.Questions {
		if q.Status == "open" && q.Material {
			return "needs_decision"
		}
	}
	if a.SyncRequired {
		return "sync_pending"
	}
	return "ready"
}
func rawJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// Analysis uses exact schema member names; encoding/json otherwise accepts
// casing aliases for struct fields and can overwrite an earlier member.
func analysisMembers(fields map[string]json.RawMessage, allowed ...string) error {
	for field := range fields {
		found := false
		for _, name := range allowed {
			if field == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown analysis member %q", field)
		}
	}
	return nil
}

// DecodeAnalysis requires explicit materiality and synchronization booleans;
// absent/null fields must not silently inherit Go's false zero value.
func DecodeAnalysis(b []byte, a *Analysis) error {
	if err := StrictDecode(b, a); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	if err := analysisMembers(fields, "boundaries", "placement", "test_strategy", "questions", "reviewer_addenda", "worker_instructions", "completion_criteria", "sync_required", "sync_reason"); err != nil {
		return err
	}
	boolean := func(v json.RawMessage) bool { return string(v) == "true" || string(v) == "false" }
	if !boolean(fields["sync_required"]) {
		return errors.New("explicit synchronization boolean required")
	}
	conclusion := func(entry map[string]json.RawMessage, kind string) error {
		if err := analysisMembers(entry, "text", "agreement", "material", "rationale", "evidence", "locations"); err != nil {
			return err
		}
		if !boolean(entry["material"]) {
			return fmt.Errorf("explicit %s materiality required", kind)
		}
		return nil
	}
	var boundaries map[string]json.RawMessage
	if err := json.Unmarshal(fields["boundaries"], &boundaries); err != nil {
		return err
	}
	if err := analysisMembers(boundaries, "correctness", "remediation_depth", "performance", "compatibility", "complexity"); err != nil {
		return err
	}
	for _, raw := range boundaries {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if err := conclusion(entry, "boundary"); err != nil {
				return err
			}
		}
	}
	var placement map[string]json.RawMessage
	if err := json.Unmarshal(fields["placement"], &placement); err != nil {
		return err
	}
	if err := analysisMembers(placement, "production", "tests", "reuse", "entrypoints", "dependencies"); err != nil {
		return err
	}
	for _, raw := range placement {
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entry); err != nil {
			return err
		}
		if err := conclusion(entry, "placement"); err != nil {
			return err
		}
	}
	var strategies []map[string]json.RawMessage
	if err := json.Unmarshal(fields["test_strategy"], &strategies); err != nil {
		return err
	}
	for _, entry := range strategies {
		if err := analysisMembers(entry, "behavior", "existing", "missing", "level", "location", "command", "rationale", "limitation", "evidence"); err != nil {
			return err
		}
	}
	var questions []map[string]json.RawMessage
	if err := json.Unmarshal(fields["questions"], &questions); err != nil {
		return err
	}
	for _, entry := range questions {
		if err := analysisMembers(entry, "id", "status", "material", "statements", "affected_work", "recommendation", "proposed_changes", "options", "resolution", "evidence"); err != nil {
			return err
		}
		if !boolean(entry["material"]) {
			return errors.New("explicit question materiality required")
		}
		var options []map[string]json.RawMessage
		if err := json.Unmarshal(entry["options"], &options); err != nil {
			return err
		}
		for _, option := range options {
			if err := analysisMembers(option, "choice", "benefits", "costs"); err != nil {
				return err
			}
		}
	}
	return nil
}
