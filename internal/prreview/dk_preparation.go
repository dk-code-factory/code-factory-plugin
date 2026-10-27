package prreview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DKPreparationPolicy = "3db03f690c6db2af361b32ab02eff42b19bcc7a9a467be62313192dd2ce37a88"

type SourceCapture struct {
	Path           string `json:"path"`
	Snapshot       string `json:"snapshot"`
	SHA256         string `json:"sha256"`
	Mode           string `json:"mode"`
	OriginIdentity string `json:"origin_identity,omitempty"`
}
type SourceInput struct {
	Identity  string `json:"identity"`
	Governing string `json:"governing"`
}
type PreparationInputs struct {
	Policy  string                 `json:"policy"`
	Tree    map[string][]string    `json:"tree"`
	Sources map[string]SourceInput `json:"sources"`
}
type PreparationIdentity struct {
	PRURL         string `json:"pr_url"`
	Base          string `json:"base_sha"`
	Head          string `json:"head_sha"`
	BodyHash      string `json:"body_hash"`
	DecisionsHash string `json:"decisions_hash"`
}
type SourceWatch struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}
type ConclusionNode struct {
	Location  string        `json:"location"`
	DependsOn []string      `json:"depends_on"`
	Paths     []SourceWatch `json:"paths"`
	Sources   []string      `json:"sources"`
	Decisions []string      `json:"decisions"`
}
type PreparationParent struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	PreparationID string `json:"preparation_id"`
	Migration     bool   `json:"migration"`
}
type Descriptor struct {
	Version         int         `json:"descriptor_version"`
	Reviewer        string      `json:"reviewer"`
	Complete        bool        `json:"complete"`
	Missing         []string    `json:"missing"`
	EffectivePrompt string      `json:"effective_prompt"`
	Aspects         []string    `json:"aspects"`
	Sources         []Reference `json:"sources"`
	Binding         Binding     `json:"code_factory_binding"`
}
type Preparation struct {
	Version          int                       `json:"preparation_version"`
	Mode             string                    `json:"mode"`
	ID               string                    `json:"preparation_id,omitempty"`
	EngineeringID    string                    `json:"engineering_id"`
	Identity         PreparationIdentity       `json:"identity"`
	WorkflowDir      string                    `json:"workflow_dir"`
	Workspace        string                    `json:"workspace"`
	RecoveryManifest Reference                 `json:"recovery_manifest"`
	RecoveryContext  Reference                 `json:"recovery_context"`
	Sources          map[string]SourceCapture  `json:"sources"`
	Inputs           PreparationInputs         `json:"inputs"`
	Graph            map[string]ConclusionNode `json:"graph"`
	RetiredIDs       []string                  `json:"retired_ids"`
	ReviewerOrder    []string                  `json:"reviewer_order"`
	Reviewers        map[string]Descriptor     `json:"reviewers"`
	ReviewerPaths    []string                  `json:"reviewer_paths"`
	Bindings         []Binding                 `json:"code_factory_bindings"`
	Answers          []Gate                    `json:"local_answers"`
	Analysis         Analysis                  `json:"analysis"`
	Readiness        string                    `json:"readiness"`
	Parent           *PreparationParent        `json:"parent"`
	Refresh          map[string]any            `json:"refresh"`
}

func gitEntries(ctx context.Context, e Evidence) (map[string][]string, error) {
	raw, err := gitOutput(ctx, e.Checkout, "ls-tree", "-rz", "--full-tree", e.Metadata.Head)
	if err != nil {
		return nil, err
	}
	entries := map[string][]string{}
	for _, record := range strings.Split(raw, "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\t", 2)
		if len(parts) != 2 {
			return nil, errors.New("invalid pinned Git tree")
		}
		fields := strings.Fields(parts[0])
		if len(fields) != 3 {
			return nil, errors.New("invalid Git entry")
		}
		entries[parts[1]] = fields
	}
	return entries, nil
}
func analysisLocations(a Analysis) []string {
	locations := []string{}
	for dimension, entries := range a.Boundaries {
		for i := range entries {
			locations = append(locations, fmt.Sprintf("/boundaries/%s/%d", dimension, i))
		}
	}
	for name := range a.Placement {
		locations = append(locations, "/placement/"+name)
	}
	for i := range a.TestStrategy {
		locations = append(locations, fmt.Sprintf("/test_strategy/%d", i))
	}
	for i := range a.Questions {
		locations = append(locations, fmt.Sprintf("/questions/%d", i))
	}
	for name := range a.ReviewerAddenda {
		name = strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
		locations = append(locations, "/reviewer_addenda/"+name)
	}
	locations = append(locations, "/worker_instructions", "/completion_criteria", "/sync_required")
	sort.Strings(locations)
	return locations
}
func guidance(location string) bool {
	return strings.HasPrefix(location, "/reviewer_addenda/") || location == "/worker_instructions" || location == "/completion_criteria"
}
func sourceSemantic(key string, b []byte) any {
	var value map[string]any
	_ = json.Unmarshal(b, &value)
	switch key {
	case "manifest":
		out := map[string]any{}
		for k, v := range value {
			switch k {
			case "context", "decisions", "evidence", "source_modes", "source_origins":
			default:
				out[k] = v
			}
		}
		return out
	case "context":
		out := map[string]any{}
		for k, v := range value {
			switch k {
			case "head_sha", "checkout", "repository", "patch", "metadata", "local_head", "worktree_clean", "local_matches_pr", "workflow_dir", "context_file":
			default:
				out[k] = v
			}
		}
		return out
	case "decisions":
		out := cloneJSON(value)
		if data, ok := out["data"].(map[string]any); ok {
			delete(data, "head_sha")
		} else {
			delete(out, "head_sha")
		}
		return out
	case "metadata":
		out := cloneJSON(value)
		delete(out, "headRefOid")
		return out
	case "patch":
		return map[string]any{"derived_from_git": true}
	}
	if strings.HasPrefix(key, "prior_preparation/") {
		return map[string]any{"lineage": true}
	}
	if strings.HasPrefix(key, "reviewers/") && value != nil {
		return normalizeDescriptor(value)
	}
	// Captures without a declared normalized schema remain opaque, including exact
	// human answers, rules and governing code/test evidence.
	return map[string]any{"opaque_sha256": Hash(b)}
}
func NewPreparation(e Evidence, ref Reference, bindings []Binding, a Analysis, prior *Preparation, parent *Reference, answerSets ...[]Gate) (Preparation, error) {
	if err := a.Validate(e, bindings, prior); err != nil {
		return Preparation{}, err
	}
	store := Artifacts{Root: filepath.Dir(ref.Path)}
	if err := e.ValidateStorage(store.Root); err != nil {
		return Preparation{}, err
	}
	answers := []Gate{}
	if len(answerSets) > 0 {
		answers = cloneJSON(answerSets[0])
		if answers == nil {
			answers = []Gate{}
		}
	}
	decisions := preparedDecisions(e, answers)
	p := Preparation{Version: 3, Mode: "standalone", WorkflowDir: store.Root, Workspace: e.Checkout, Sources: map[string]SourceCapture{}, Inputs: PreparationInputs{Policy: DKPreparationPolicy, Sources: map[string]SourceInput{}}, Graph: map[string]ConclusionNode{}, RetiredIDs: []string{}, ReviewerOrder: []string{}, Reviewers: map[string]Descriptor{}, ReviewerPaths: []string{}, Bindings: cloneJSON(bindings), Answers: answers, Analysis: a, Readiness: a.Readiness(), Refresh: map[string]any{"mode": "initial"}}
	p.Identity = PreparationIdentity{e.Metadata.URL, e.Metadata.Base, e.Metadata.Head, decisions.Data.BodyHash, CompactDigest(decisions.Data)}
	var err error
	p.Inputs.Tree, err = gitEntries(context.Background(), e)
	if err != nil {
		return p, err
	}
	capture := func(key string, b []byte) (Reference, error) {
		r, err := store.RetainBytes(b)
		if err != nil {
			return r, err
		}
		p.Sources[key] = SourceCapture{r.Path, r.Path, r.SHA256, "capture", key}
		p.Inputs.Sources[key] = SourceInput{key, CompactDigest(sourceSemantic(key, b))}
		return r, nil
	}
	captureJSON := func(key string, v any) (Reference, error) {
		b, err := CanonicalJSON(v)
		if err != nil {
			return Reference{}, err
		}
		return capture(key, b)
	}
	metadata := map[string]any{"url": e.Metadata.URL, "baseRefOid": e.Metadata.Base, "headRefOid": e.Metadata.Head, "body": e.Metadata.Body, "files": e.Files}
	metadataRef, err := captureJSON("metadata", metadata)
	if err != nil {
		return p, err
	}
	patchRef, err := capture("patch", []byte(e.Patch))
	if err != nil {
		return p, err
	}
	decisionRef, err := captureJSON("decisions", decisions)
	if err != nil {
		return p, err
	}
	contextValue := map[string]any{"url": e.Metadata.URL, "repository": e.Repository, "base_sha": e.Metadata.Base, "head_sha": e.Metadata.Head, "metadata": metadataRef.Path, "patch": patchRef.Path, "checkout": e.Checkout, "workflow_dir": store.Root}
	contextRef, err := captureJSON("context", contextValue)
	if err != nil {
		return p, err
	}
	p.RecoveryContext = contextRef
	categories := []string{"issues", "feedback", "choices", "user_instructions", "run_history", "questions", "prior_preparation", "rules", "production", "tests", "ci", "reviewers"}
	categoriesContent := map[string]any{
		"issues": e.Scope, "feedback": e.Scope, "choices": answers, "user_instructions": e.Constraints, "run_history": map[string]any{"verified_absence": true, "reason": "Standalone review has no iteration run history"}, "questions": a.Questions, "rules": e.Rules, "production": e.Sources, "tests": e.Sources, "ci": e.Sources,
	}
	evidencePaths := map[string][]string{}
	for _, category := range categories {
		if category == "reviewers" {
			continue
		}
		value, ok := categoriesContent[category]
		if !ok {
			value = map[string]any{"verified_absence": true, "reason": "No prior preparation selected"}
		}
		if category == "prior_preparation" && prior != nil {
			value = prior
		}
		r, err := captureJSON(category+"/0", value)
		if err != nil {
			return p, err
		}
		evidencePaths[category] = []string{r.Path}
	}
	for key, text := range e.Sources {
		if _, ok := p.Sources[key]; ok {
			continue
		}
		if _, err = capture(key, []byte(text)); err != nil {
			return p, err
		}
	}
	for i, b := range bindings {
		if err := b.Validate(); err != nil {
			return p, err
		}
		name := b.Reviewer.ID
		p.ReviewerOrder = append(p.ReviewerOrder, name)
		resources := []Reference{}
		paths := make([]string, 0, len(b.Resources))
		for path := range b.Resources {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for n, path := range paths {
			content, err := base64.StdEncoding.DecodeString(b.Resources[path])
			if err != nil {
				return p, errors.New("invalid retained reviewer package encoding")
			}
			r, err := capture(fmt.Sprintf("reviewer-source/%d/%d", i, n), content)
			if err != nil {
				return p, err
			}
			resources = append(resources, r)
		}
		prompt := "Review identical pinned evidence and preparation using the retained native instructions. Apply every governing source and return only the pinned reviewer response contract. Server publication authority is never derived from reviewer output."
		d := Descriptor{1, name, true, []string{}, prompt, []string{"correctness", "scope", "test sufficiency", "cumulative complexity"}, resources, b}
		p.Reviewers[name] = d
		r, err := captureJSON(fmt.Sprintf("reviewers/%d", i), d)
		if err != nil {
			return p, err
		}
		p.ReviewerPaths = append(p.ReviewerPaths, r.Path)
		evidencePaths["reviewers"] = append(evidencePaths["reviewers"], r.Path)
	}
	manifest := map[string]any{"mode": "standalone", "context": contextRef.Path, "decisions": decisionRef.Path, "evidence": evidencePaths}
	_, err = captureJSON("manifest", manifest)
	if err != nil {
		return p, err
	}
	recovery := cloneJSON(manifest)
	recovery["context"] = contextRef.Path
	p.RecoveryManifest, err = store.Retain(recovery)
	if err != nil {
		return p, err
	}
	if prior != nil {
		if parent == nil {
			return p, errors.New("prior preparation requires an immutable parent reference")
		}
		p.Parent = &PreparationParent{parent.Path, parent.SHA256, prior.ID, false}
		p.RetiredIDs = append(p.RetiredIDs, prior.RetiredIDs...)
		p.Refresh["mode"] = "incremental"
	}
	keys := make([]string, 0, len(p.Sources))
	for k := range p.Sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	oldLocations := map[string]string{}
	if prior != nil {
		for id, n := range prior.Graph {
			oldLocations[n.Location] = id
		}
	}
	active := map[string]bool{}
	for _, location := range analysisLocations(a) {
		id := oldLocations[location]
		if id == "" {
			id = "c-" + Hash([]byte(location))[:32]
			for _, retired := range p.RetiredIDs {
				if retired == id {
					id = "c-" + Hash([]byte(location + prior.ID))[:32]
				}
			}
		}
		active[id] = true
		p.Graph[id] = ConclusionNode{location, []string{}, []SourceWatch{{".", "subtree"}}, append([]string{}, keys...), []string{}}
	}
	if prior != nil {
		for id := range prior.Graph {
			if !active[id] {
				p.RetiredIDs = append(p.RetiredIDs, id)
			}
		}
	}
	governing := []string{}
	for id, node := range p.Graph {
		if !guidance(node.Location) {
			governing = append(governing, id)
		}
	}
	sort.Strings(governing)
	for id, node := range p.Graph {
		if guidance(node.Location) {
			node.DependsOn = append([]string{}, governing...)
			p.Graph[id] = node
		}
	}
	p.EngineeringID, err = EngineeringIdentity(p)
	if err != nil {
		return p, err
	}
	if prior != nil && prior.EngineeringID == p.EngineeringID {
		p.Refresh["mode"] = "operational"
	}
	p.ID = CompactDigest(p)
	return p, nil
}
func normalizeDescriptor(v map[string]any) map[string]any {
	copy := cloneJSON(v)
	replacements := map[string]string{}
	execution, _ := copy["execution"].(map[string]any)
	for _, key := range []string{"root", "origin_root"} {
		if value, ok := execution[key].(string); ok && value != "" {
			replacements[value] = "<provider-package>"
		}
	}
	if bundles, ok := execution["bundles"].([]any); ok {
		for i, item := range bundles {
			b, _ := item.(map[string]any)
			if root, ok := b["root"].(string); ok {
				replacements[root] = fmt.Sprintf("<command-package:%d>", i)
			}
		}
	}
	if sources, ok := copy["sources"].([]any); ok {
		for i, item := range sources {
			s, _ := item.(map[string]any)
			if path, ok := s["path"].(string); ok {
				replacements[path] = fmt.Sprintf("<instruction:%d>", i)
			}
		}
	}
	paths := make([]string, 0, len(replacements))
	for path := range replacements {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		if len(paths[i]) != len(paths[j]) {
			return len(paths[i]) > len(paths[j])
		}
		return paths[i] < paths[j]
	})
	var normalize func(any) any
	normalize = func(value any) any {
		switch x := value.(type) {
		case string:
			for _, path := range paths {
				x = strings.ReplaceAll(x, path, replacements[path])
				encoded, _ := json.Marshal(path)
				x = strings.ReplaceAll(x, string(encoded[1:len(encoded)-1]), replacements[path])
			}
			return x
		case map[string]any:
			for k, v := range x {
				x[k] = normalize(v)
			}
		case []any:
			for i, v := range x {
				x[i] = normalize(v)
			}
		}
		return value
	}
	return normalize(copy).(map[string]any)
}

// EngineeringIdentity ports the pinned DK v3 algorithm. Only explicitly declared
// operational fields and proven preparation ancestors are excluded.
func EngineeringIdentity(p Preparation) (string, error) {
	var prepared map[string]any
	_ = json.Unmarshal(rawJSON(p), &prepared)
	excluded := map[string]bool{}
	for _, key := range []string{"preparation_id", "engineering_id", "sources", "reviewer_paths", "parent", "refresh", "retired_ids", "workflow_dir", "workspace", "identity", "inputs", "reviewers", "recovery_manifest", "recovery_context"} {
		excluded[key] = true
	}
	content := map[string]any{}
	for key, value := range prepared {
		if !excluded[key] {
			content[key] = value
		}
	}
	graph := map[string]any{}
	for _, node := range p.Graph {
		deps := []string{}
		for _, id := range node.DependsOn {
			target, ok := p.Graph[id]
			if !ok {
				return "", errors.New("dangling preparation conclusion")
			}
			deps = append(deps, target.Location)
		}
		sort.Strings(deps)
		var value map[string]any
		_ = json.Unmarshal(rawJSON(node), &value)
		value["depends_on"] = deps
		graph[node.Location] = value
	}
	content["graph"] = graph
	identity := map[string]any{}
	_ = json.Unmarshal(rawJSON(p.Identity), &identity)
	delete(identity, "head_sha")
	delete(identity, "decisions_hash")
	content["identity"] = identity
	inputSources := map[string]SourceInput{}
	for key, value := range p.Inputs.Sources {
		if strings.HasPrefix(key, "prior_preparation/") {
			continue
		}
		if p.Sources[key].Mode == "capture" {
			value.Identity = key
		}
		inputSources[key] = value
	}
	content["inputs"] = map[string]any{"policy": p.Inputs.Policy, "tree": p.Inputs.Tree, "sources": inputSources}
	reviewers := []any{}
	for _, name := range p.ReviewerOrder {
		d, ok := p.Reviewers[name]
		if !ok {
			return "", errors.New("missing ordered reviewer descriptor")
		}
		var value map[string]any
		_ = json.Unmarshal(rawJSON(d), &value)
		reviewers = append(reviewers, normalizeDescriptor(value))
	}
	content["reviewers"] = reviewers
	contracts := map[string]any{}
	for key, source := range p.Sources {
		if strings.HasPrefix(key, "prior_preparation/") {
			continue
		}
		contracts[key] = map[string]any{"mode": source.Mode}
	}
	content["source_contracts"] = contracts
	lineage := map[string]bool{}
	parent := p.Parent
	for parent != nil {
		if lineage[parent.PreparationID] {
			return "", errors.New("cyclic preparation lineage")
		}
		var ancestor Preparation
		if err := (Artifacts{Root: filepath.Dir(parent.Path)}).Load(Reference{parent.Path, parent.SHA256}, &ancestor); err != nil {
			return "", err
		}
		if ancestor.ID != parent.PreparationID {
			return "", errors.New("preparation parent fingerprint mismatch")
		}
		lineage[parent.PreparationID] = true
		parent = ancestor.Parent
	}
	governing := map[string]any{}
	for key, source := range p.Sources {
		if !strings.HasPrefix(key, "prior_preparation/") {
			continue
		}
		b, err := os.ReadFile(source.Snapshot)
		if err != nil || Hash(b) != source.SHA256 {
			return "", errors.New("prior preparation capture changed")
		}
		var value map[string]any
		if err = StrictDecode(b, &value); err != nil {
			return "", err
		}
		if _, ok := value["analysis"]; ok {
			var ancestor Preparation
			if err = StrictDecode(b, &ancestor); err != nil {
				return "", err
			}
			if !lineage[ancestor.ID] {
				id, err := EngineeringIdentity(ancestor)
				if err != nil {
					return "", err
				}
				governing[key] = map[string]any{"preparation": id}
			}
		} else {
			extensions := map[string]any{}
			for k, v := range value {
				if k != "verified_absence" && k != "reason" {
					extensions[k] = v
				}
			}
			if len(extensions) > 0 {
				governing[key] = map[string]any{"extensions": extensions}
			}
		}
	}
	content["prior_governing"] = governing
	return CompactDigest(content), nil
}
func (p Preparation) Validate(e Evidence, bindings []Binding) error {
	if p.Version != 3 || p.Mode != "standalone" || p.Inputs.Policy != DKPreparationPolicy || p.Readiness != p.Analysis.Readiness() || Digest(p.Bindings) != Digest(bindings) {
		return errors.New("preparation contract or bindings changed")
	}
	if err := p.Analysis.Validate(e, bindings, nil); err != nil {
		return err
	}
	d := preparedDecisions(e, p.Answers)
	expected := PreparationIdentity{e.Metadata.URL, e.Metadata.Base, e.Metadata.Head, d.Data.BodyHash, CompactDigest(d.Data)}
	if p.Identity != expected {
		return errors.New("preparation source identity changed")
	}
	copy := p
	copy.ID = ""
	if p.ID != CompactDigest(copy) {
		return errors.New("preparation artifact identity changed")
	}
	engineering, err := EngineeringIdentity(p)
	if err != nil || engineering != p.EngineeringID {
		return errors.New("preparation engineering identity changed")
	}
	entries, err := gitEntries(context.Background(), e)
	if err != nil {
		return err
	}
	if Digest(entries) != Digest(p.Inputs.Tree) {
		return errors.New("preparation tree changed")
	}
	for key, source := range p.Sources {
		if source.Mode != "capture" || source.Path != source.Snapshot || !filepath.IsAbs(source.Snapshot) {
			return errors.New("invalid preparation capture contract")
		}
		b, err := os.ReadFile(source.Snapshot)
		if err != nil || Hash(b) != source.SHA256 {
			return errors.New("preparation immutable capture changed")
		}
		input, ok := p.Inputs.Sources[key]
		if !ok || input.Identity != source.OriginIdentity || input.Governing != CompactDigest(sourceSemantic(key, b)) {
			return errors.New("preparation governing input changed")
		}
	}
	if len(p.Sources) != len(p.Inputs.Sources) || len(p.ReviewerOrder) != len(bindings) || len(p.Reviewers) != len(bindings) {
		return errors.New("incomplete preparation source/reviewer inventory")
	}
	for i, b := range bindings {
		if err = b.Validate(); err != nil {
			return err
		}
		if p.ReviewerOrder[i] != b.Reviewer.ID || Digest(p.Reviewers[b.Reviewer.ID].Binding) != Digest(b) {
			return errors.New("ordered reviewer descriptor changed")
		}
	}
	for _, ref := range []Reference{p.RecoveryManifest, p.RecoveryContext} {
		var v map[string]any
		if err := (Artifacts{Root: filepath.Dir(ref.Path)}).Load(ref, &v); err != nil {
			return err
		}
	}
	locations := map[string]bool{}
	for _, location := range analysisLocations(p.Analysis) {
		locations[location] = true
	}
	if len(locations) != len(p.Graph) {
		return errors.New("incomplete conclusion graph")
	}
	for id, node := range p.Graph {
		if !strings.HasPrefix(id, "c-") || !locations[node.Location] {
			return errors.New("invalid conclusion graph identity")
		}
		delete(locations, node.Location)
		for _, source := range node.Sources {
			if _, ok := p.Sources[source]; !ok {
				return errors.New("unknown conclusion source")
			}
		}
	}
	return nil
}
