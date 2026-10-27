package reports

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type issueTemplate struct {
	ID          string          `json:"id"`
	Fields      []templateField `json:"fields"`
	Labels      []string        `json:"labels"`
	Unsupported string          `json:"unsupported,omitempty"`
}
type templateField struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Heading         string   `json:"heading"`
	Description     string   `json:"description,omitempty"`
	Required        bool     `json:"required"`
	Options         []string `json:"options,omitempty"`
	RequiredOptions []string `json:"requiredOptions,omitempty"`
	Multiple        bool     `json:"multiple,omitempty"`
	Render          string   `json:"render,omitempty"`
}
type fieldAnswer struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Choices []string `json:"choices"`
}
type reportContent struct {
	Environment  string `json:"environment"`
	Reproduction string `json:"reproduction"`
	Expected     string `json:"expected"`
	Actual       string `json:"actual"`
	Evidence     string `json:"evidence"`
	Findings     string `json:"findings"`
	Uncertainty  string `json:"uncertainty"`
}
type structuredDraft struct {
	Version            int           `json:"version"`
	Classification     string        `json:"classification"`
	Summary            string        `json:"summary"`
	MissingInformation string        `json:"missingInformation"`
	Component          string        `json:"component"`
	SourcePath         string        `json:"sourcePath"`
	SourceCommit       string        `json:"sourceCommit"`
	OwnershipReason    string        `json:"ownershipReason"`
	DuplicateNumber    int           `json:"duplicateNumber"`
	Title              string        `json:"title"`
	Content            reportContent `json:"content"`
	TemplateID         string        `json:"templateId"`
	Answers            []fieldAnswer `json:"answers"`
	Labels             []string      `json:"labels"`
	GuidanceReviewed   []string      `json:"guidanceReviewed"`
	Restriction        string        `json:"restriction"` // none, security, support; prose interpretation remains with AI
	RestrictionReason  string        `json:"restrictionReason"`
}

func strictYAML(data string, out any) error {
	d := yaml.NewDecoder(strings.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("multiple YAML documents")
	}
	return nil
}

var markdownHeading = regexp.MustCompile(`^(#{1,6}) ([^#].*?)\s*$`)
var complexMarkdownLine = regexp.MustCompile(`^\s*(?:[-*+] |[0-9]+[.)] |    |\t)`)
var formFieldID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func normalizeTemplate(id, content string) (t issueTemplate) {
	t.ID = id
	content = strings.ReplaceAll(content, "\r\n", "\n")
	fail := func(reason string) issueTemplate { t.Fields = nil; t.Unsupported = reason; return t }
	if strings.HasSuffix(strings.ToLower(id), ".md") {
		if strings.HasPrefix(content, "---\n") {
			_, tail, ok := strings.Cut(content[4:], "\n---\n")
			if !ok {
				return fail("malformed Markdown frontmatter")
			}
			front := content[4 : len(content)-len(tail)-5]
			var meta struct {
				Name      string `yaml:"name"`
				About     string `yaml:"about"`
				Title     string `yaml:"title"`
				Labels    any    `yaml:"labels"`
				Assignees any    `yaml:"assignees"`
			}
			if strictYAML(front, &meta) != nil {
				return fail("unsupported Markdown frontmatter")
			}
			switch labels := meta.Labels.(type) {
			case string:
				for _, l := range strings.Split(labels, ",") {
					if strings.TrimSpace(l) != "" {
						t.Labels = append(t.Labels, strings.TrimSpace(l))
					}
				}
			case []any:
				for _, l := range labels {
					s, ok := l.(string)
					if !ok {
						return fail("invalid template labels")
					}
					t.Labels = append(t.Labels, s)
				}
			case nil:
			default:
				return fail("invalid template labels")
			}
			content = tail
		}
		// Only simple heading layouts are safe to normalize. Lists, tables, HTML,
		// code blocks and nested headings can encode obligations we cannot preserve.
		level := ""
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			match := markdownHeading.FindStringSubmatch(line)
			if match != nil {
				if level != "" && level != match[1] {
					return fail("nested Markdown headings")
				}
				level = match[1]
				if seen[match[2]] {
					return fail("duplicate Markdown headings")
				}
				seen[match[2]] = true
				t.Fields = append(t.Fields, templateField{ID: "section-" + strconv.Itoa(len(t.Fields)+1), Kind: "textarea", Heading: line})
			} else {
				if len(t.Fields) == 0 || strings.ContainsAny(line, "|<>`") || complexMarkdownLine.MatchString(line) {
					return fail("complex Markdown layout")
				}
				t.Fields[len(t.Fields)-1].Description += line + "\n"
			}
		}
		if len(t.Fields) == 0 {
			return fail("no Markdown headings")
		}
		return t
	}
	var form struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Title       string   `yaml:"title"`
		Labels      []string `yaml:"labels"`
		Assignees   []string `yaml:"assignees"`
		Body        []struct {
			Type       string `yaml:"type"`
			ID         string `yaml:"id"`
			Attributes struct {
				Label       string      `yaml:"label"`
				Description string      `yaml:"description"`
				Placeholder string      `yaml:"placeholder"`
				Value       string      `yaml:"value"`
				Render      string      `yaml:"render"`
				Multiple    bool        `yaml:"multiple"`
				Options     []yaml.Node `yaml:"options"`
			} `yaml:"attributes"`
			Validations struct {
				Required bool `yaml:"required"`
			} `yaml:"validations"`
		} `yaml:"body"`
	}
	if strictYAML(content, &form) != nil || len(form.Body) == 0 {
		return fail("malformed or unsupported issue form")
	}
	t.Labels = form.Labels
	seen := map[string]bool{}
	for i, entry := range form.Body {
		a := entry.Attributes
		f := templateField{ID: entry.ID, Kind: entry.Type, Heading: "### " + a.Label, Description: a.Description, Required: entry.Validations.Required, Multiple: a.Multiple, Render: a.Render}
		if entry.Type == "markdown" {
			if a.Value == "" {
				return fail("empty form instructions")
			}
			f.ID = "@instructions-" + strconv.Itoa(i)
			f.Description = a.Value
			t.Fields = append(t.Fields, f)
			continue
		}
		if f.ID == "" {
			f.ID = "@field-" + strconv.Itoa(i)
		} else if !formFieldID.MatchString(f.ID) {
			return fail("invalid form field ID")
		}
		if seen[f.ID] || !validText(a.Label) || strings.ContainsAny(a.Label, "\r\n") {
			return fail("invalid form identity")
		}
		seen[f.ID] = true
		switch entry.Type {
		case "input", "textarea":
			if len(a.Options) > 0 || a.Multiple {
				return fail("invalid text attributes")
			}
		case "dropdown":
			for _, node := range a.Options {
				if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
					return fail("invalid dropdown option")
				}
				f.Options = append(f.Options, node.Value)
			}
		case "checkboxes":
			for _, node := range a.Options {
				var option struct {
					Label    string `yaml:"label"`
					Required bool   `yaml:"required"`
				}
				data, _ := yaml.Marshal(node)
				if strictYAML(string(data), &option) != nil || !validText(option.Label) {
					return fail("invalid checkbox option")
				}
				f.Options = append(f.Options, option.Label)
				if option.Required {
					f.RequiredOptions = append(f.RequiredOptions, option.Label)
				}
			}
		default:
			return fail("unsupported form field: " + entry.Type)
		}
		if (entry.Type == "dropdown" || entry.Type == "checkboxes") && len(f.Options) == 0 {
			return fail("empty choices")
		}
		options := map[string]bool{}
		for _, o := range f.Options {
			if options[o] || strings.ContainsAny(o, "\n\r") {
				return fail("ambiguous choices")
			}
			options[o] = true
		}
		if a.Render != "" && (entry.Type != "textarea" || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(a.Render)) {
			return fail("unsupported rendered field")
		}
		t.Fields = append(t.Fields, f)
	}
	return t
}

func decodeStructured(raw json.RawMessage) (structuredDraft, error) {
	var d structuredDraft
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return d, fmt.Errorf("trailing structured result")
	}
	return d, nil
}
func provenanceValid(f Findings) bool {
	return routes[f.Component] == f.Repository && f.Repository != "" && commitPattern.MatchString(f.SourceCommit) && validText(f.OwnershipReason) && validText(f.SourcePath) && path.Clean(f.SourcePath) == f.SourcePath && !strings.HasPrefix(f.SourcePath, "/") && !strings.HasPrefix(f.SourcePath, "..")
}
func validateFindings(f Findings) error {
	if !validText(f.Summary) {
		return fmt.Errorf("summary required")
	}
	switch f.Classification {
	case "insufficient_information":
		if !validText(f.MissingInformation) || f.DuplicateNumber != 0 {
			return fmt.Errorf("actionable missing information required")
		}
	case "explained":
		if f.DuplicateNumber != 0 || validText(f.MissingInformation) {
			return fmt.Errorf("contradictory explained result")
		}
	case "duplicate":
		if f.DuplicateNumber < 1 || validText(f.MissingInformation) || !provenanceValid(f) {
			return fmt.Errorf("verified duplicate identity and ownership required")
		}
	case "confirmed", "suspected":
		if f.DuplicateNumber != 0 || validText(f.MissingInformation) || !provenanceValid(f) || !validText(f.Title) || !validText(f.Body) || len(f.Title) > 256 || len(f.Body) > 60000 {
			return fmt.Errorf("complete draft and ownership required")
		}
	default:
		return fmt.Errorf("invalid classification")
	}
	return nil
}
func renderDraft(d structuredDraft, c *investigationContext, observation Submission, additions []string) (Findings, error) {
	f := Findings{Classification: d.Classification, Summary: d.Summary, MissingInformation: d.MissingInformation, Component: d.Component, Repository: routes[d.Component], SourcePath: d.SourcePath, SourceCommit: d.SourceCommit, OwnershipReason: d.OwnershipReason, DuplicateNumber: d.DuplicateNumber, Title: d.Title, Labels: d.Labels}
	if d.Version != structuredContract || c == nil || c.Version != 1 {
		return f, fmt.Errorf("unsupported draft/context version")
	}
	if f.Classification == "explained" || f.Classification == "insufficient_information" {
		return f, validateFindings(f)
	}
	// Validate the classification/ownership envelope before applying routing rules
	// or converting missing required facts into an information request.
	f.Body = "pending deterministic rendering"
	if err := validateFindings(f); err != nil {
		return f, err
	}
	f.Body = ""
	r := c.repository(f.Repository)
	if r == nil {
		return f, fmt.Errorf("unsupported ownership")
	}
	for _, doc := range r.Documents {
		if !strings.Contains(strings.ToLower(doc.Path), "issue_template") && !slices.Contains(d.GuidanceReviewed, doc.Path) {
			return f, fmt.Errorf("repository guidance must be reviewed")
		}
	}
	switch d.Restriction {
	case "security", "support":
		if !validText(d.RestrictionReason) {
			return f, fmt.Errorf("restriction explanation required")
		}
		f.Classification = "insufficient_information"
		f.DuplicateNumber = 0
		f.MissingInformation = "Use the repository's " + d.Restriction + " reporting channel: " + d.RestrictionReason
		return f, nil
	case "none":
		if d.RestrictionReason != "" {
			return f, fmt.Errorf("contradictory restriction")
		}
	default:
		return f, fmt.Errorf("repository reporting restriction disposition required")
	}
	for _, label := range f.Labels {
		if !slices.Contains(r.Labels, label) {
			return f, fmt.Errorf("unsupported label")
		}
	}
	if f.Classification == "duplicate" {
		found := false
		for _, candidate := range r.Candidates {
			if candidate.Number == f.DuplicateNumber {
				found = true
			}
		}
		if !found {
			return f, fmt.Errorf("duplicate was not supplied as a candidate")
		}
		return f, validateFindings(f)
	}
	// Require all standard facts even when a repository template omits them.
	content := []struct{ heading, text string }{{"Environment", d.Content.Environment}, {"Reproduction", d.Content.Reproduction}, {"Expected behavior", d.Content.Expected}, {"Actual behavior", d.Content.Actual}, {"Relevant evidence", d.Content.Evidence}, {"Findings", d.Content.Findings}, {"Uncertainty", d.Content.Uncertainty}}
	var selected *issueTemplate
	for i := range r.Templates {
		if r.Templates[i].ID == d.TemplateID {
			selected = &r.Templates[i]
		}
	}
	if selected == nil && (d.TemplateID != "" || len(r.Templates) > 0) {
		return f, fmt.Errorf("invented template identity")
	}
	reason := "Applicable repository issue layout is missing or unavailable."
	if selected != nil && selected.Unsupported != "" {
		reason = "Repository issue layout is unsupported: " + selected.Unsupported + "."
	}
	if selected != nil && selected.Unsupported == "" {
		body, missing, err := renderTemplate(*selected, d.Answers, observation, additions)
		if err != nil {
			return f, err
		}
		if len(missing) > 0 {
			f.Classification = "insufficient_information"
			f.MissingInformation = "Please provide the required repository fields: " + strings.Join(missing, ", ")
			return f, nil
		}
		f.Body = body
		for _, label := range selected.Labels {
			if slices.Contains(r.Labels, label) && !slices.Contains(f.Labels, label) {
				f.Labels = append(f.Labels, label)
			}
		}
	} else {
		if len(d.Answers) > 0 {
			return f, fmt.Errorf("answers supplied without a supported template")
		}
		f.Body = "Layout limitation: " + reason + "\n"
	}
	missingContent := []string{}
	for _, part := range content {
		if !validText(part.text) {
			missingContent = append(missingContent, part.heading)
		}
	}
	if len(missingContent) > 0 {
		f.Classification = "insufficient_information"
		f.MissingInformation = "Please provide the missing report facts: " + strings.Join(missingContent, ", ")
		f.Body = ""
		return f, nil
	}
	if selected != nil && selected.Unsupported == "" {
		return f, validateFindings(f)
	}
	for _, part := range content {
		f.Body += "\n## " + part.heading + "\n\n" + part.text + "\n"
	}
	return f, validateFindings(f)
}
func renderTemplate(t issueTemplate, answers []fieldAnswer, o Submission, additions []string) (string, []string, error) {
	byID := map[string]fieldAnswer{}
	for _, a := range answers {
		if _, ok := byID[a.ID]; ok {
			return "", nil, fmt.Errorf("duplicate field answer")
		}
		byID[a.ID] = a
	}
	host := []string{o.Expected, o.Actual, o.Errors, o.ToolEvidence, o.ReproductionUnknown, o.Impact, o.Frequency, o.SuspectedCause}
	host = append(host, o.Steps...)
	host = append(host, additions...)
	attestations := map[string]bool{}
	for _, text := range host {
		for _, line := range strings.Split(text, "\n") {
			attestations[strings.TrimSpace(line)] = true
		}
	}
	body := ""
	missing := []string{}
	for _, field := range t.Fields {
		a := byID[field.ID]
		delete(byID, field.ID)
		if field.Kind == "markdown" {
			if a.ID != "" {
				return "", nil, fmt.Errorf("answer to static instructions")
			}
			// GitHub form Markdown supplies instructions only; it is not submitted.
			continue
		}
		text := a.Text
		if field.Kind == "input" && strings.ContainsAny(text, "\r\n") {
			return "", nil, fmt.Errorf("multiline answer to single-line field")
		}
		if field.Kind == "dropdown" || field.Kind == "checkboxes" {
			if text != "" {
				return "", nil, fmt.Errorf("text supplied for choice field")
			}
			if field.Kind == "dropdown" && !field.Multiple && len(a.Choices) > 1 {
				return "", nil, fmt.Errorf("multiple answers to single choice")
			}
			seen := map[string]bool{}
			for _, choice := range a.Choices {
				if seen[choice] || !slices.Contains(field.Options, choice) {
					return "", nil, fmt.Errorf("unsupported choice")
				}
				seen[choice] = true
				if field.Kind == "checkboxes" && !attestations[choice] {
					return "", nil, fmt.Errorf("checkbox attestation lacks explicit host evidence")
				}
			}
			if field.Kind == "checkboxes" {
				for _, required := range field.RequiredOptions {
					if !seen[required] {
						missing = append(missing, field.Heading+": "+required)
					}
				}
				for _, option := range field.Options {
					mark := " "
					if seen[option] {
						mark = "x"
					}
					text += "- [" + mark + "] " + option + "\n"
				}
			} else {
				text = strings.Join(a.Choices, ", ")
			}
		} else if len(a.Choices) > 0 {
			return "", nil, fmt.Errorf("choices supplied for text field")
		}
		if field.Required && (!validText(text) || (field.Kind == "checkboxes" && len(a.Choices) == 0)) {
			missing = append(missing, field.Heading)
		}
		if !validText(text) {
			text = "Not provided."
		}
		if field.Render != "" {
			if strings.Contains(text, "```") {
				return "", nil, fmt.Errorf("invalid code field fence")
			}
			text = "```" + field.Render + "\n" + text + "\n```"
		}
		body += "\n" + field.Heading + "\n\n" + text + "\n"
	}
	if len(byID) > 0 {
		return "", nil, fmt.Errorf("unknown template field")
	}
	return strings.TrimSpace(body), missing, nil
}
