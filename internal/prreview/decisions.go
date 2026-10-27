package prreview

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var historyBlock = regexp.MustCompile(`(?s)<!-- dk-pr-decisions:history ([A-Za-z0-9_=+\-]+) -->.*?<!-- /dk-pr-decisions:history -->`)

// PublishedDecisions preserves active description choices and superseded history.
// Discussion suggestions remain evidence for preparation, never inferred agreement.
func PublishedDecisions(m Metadata) (Decisions, error) {
	records := []json.RawMessage{}
	section := ""
	inside := false
	headings := 0
	for _, line := range strings.Split(m.Body, "\n") {
		if strings.TrimSpace(line) == "## Decisions" {
			headings++
			inside = true
			continue
		}
		if inside && strings.HasPrefix(line, "## ") {
			inside = false
		}
		if inside {
			section += line + "\n"
		}
	}
	if headings > 1 {
		return Decisions{}, errors.New("ambiguous Decisions sections")
	}
	history := []json.RawMessage{}
	for _, match := range historyBlock.FindAllStringSubmatch(section, -1) {
		b, err := base64.URLEncoding.DecodeString(match[1])
		if err != nil {
			return Decisions{}, err
		}
		var v map[string]any
		if err = StrictDecode(b, &v); err != nil {
			return Decisions{}, err
		}
		if v["status"] != "superseded" {
			return Decisions{}, errors.New("invalid Decisions history")
		}
		history = append(history, b)
	}
	section = historyBlock.ReplaceAllString(section, "")
	if strings.Contains(section, "dk-pr-decisions:history") {
		return Decisions{}, errors.New("malformed Decisions history")
	}
	blocks := []string{}
	current := ""
	flush := func() {
		if strings.TrimSpace(current) != "" {
			blocks = append(blocks, current)
		}
		current = ""
	}
	for _, line := range strings.Split(section, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			flush()
			current = line[2:]
		} else {
			if current != "" {
				current += "\n"
			}
			current += line
		}
	}
	flush()
	sourceRefs := regexp.MustCompile(` Source: (https?://\S+(?:, https?://\S+)*)$`)
	seen := map[string]bool{}
	for _, block := range blocks {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(block), "**Choice:**"))
		refs := []string{m.URL + "#decisions"}
		if match := sourceRefs.FindStringSubmatch(text); match != nil {
			refs = strings.Split(match[1], ", ")
			text = text[:len(text)-len(match[0])]
		}
		id := Hash([]byte(text))[:20]
		if seen[id] {
			continue
		}
		seen[id] = true
		records = append(records, rawJSON(map[string]any{"id": id, "text": text, "source_type": "description", "references": refs, "source": nil, "status": "active", "publication_status": "published", "superseded_by": nil}))
	}
	records = append(records, history...)
	return Decisions{SchemaVersion: 2, Command: "context", Status: "ok", Data: DecisionData{Verified: true, PRURL: m.URL, Head: m.Head, BodyHash: Hash([]byte(m.Body)), LiveDecisions: decisionSection(m.Body), DiscussionDecisions: []DiscussionDecision{}, LocalDecisions: []LocalDecision{}, Decisions: records}}, nil
}

func decisionSection(body string) string {
	start := -1
	offset := 0
	for _, line := range strings.SplitAfter(body, "\n") {
		trimmed := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.TrimRight(trimmed, " \t") == "## Decisions" {
			start = offset
			break
		}
		offset += len(line)
	}
	if start < 0 {
		return ""
	}
	end := len(body)
	offset = start
	for _, line := range strings.SplitAfter(body[start:], "\n") {
		if offset > start && strings.HasPrefix(line, "## ") {
			end = offset
			break
		}
		offset += len(line)
	}
	return strings.TrimRight(body[start:end], " \t\r\n")
}
func preparedDecisions(e Evidence, answers []Gate) Decisions {
	d := cloneJSON(e.Decisions)
	seen := map[string]bool{}
	for _, record := range d.Data.Decisions {
		var v map[string]any
		_ = json.Unmarshal(record, &v)
		if id, ok := v["id"].(string); ok {
			seen[id] = true
		}
	}
	for _, gate := range answers {
		var values map[string]string
		_ = json.Unmarshal(gate.Answer, &values)
		for _, q := range gate.Questions {
			answer := values[q.ID]
			text := q.ID + ": " + answer
			source := string(rawJSON(map[string]any{"gate_id": gate.ID, "engineering_id": gate.EvidenceID, "question": q, "answer": answer}))
			d.Data.LocalDecisions = append(d.Data.LocalDecisions, LocalDecision{text, source})
			id := Hash([]byte(text))[:20]
			if !seen[id] {
				d.Data.Decisions = append(d.Data.Decisions, rawJSON(map[string]any{"id": id, "text": text, "source_type": "local", "references": []string{}, "source": source, "status": "active", "publication_status": "unpublished", "superseded_by": nil}))
				seen[id] = true
			}
		}
	}
	return d
}

func validateDecisionContext(m Metadata, d Decisions) error {
	if d.Data.LiveDecisions != decisionSection(m.Body) || d.Data.DiscussionDecisions == nil || d.Data.LocalDecisions == nil {
		return errors.New("incomplete live Decisions context")
	}
	type record struct {
		ID                string   `json:"id"`
		Text              string   `json:"text"`
		SourceType        string   `json:"source_type"`
		References        []string `json:"references"`
		Source            *string  `json:"source"`
		Status            string   `json:"status"`
		PublicationStatus string   `json:"publication_status"`
		SupersededBy      *string  `json:"superseded_by"`
	}
	records := map[string]record{}
	for _, raw := range d.Data.Decisions {
		var r record
		if err := StrictDecode(raw, &r); err != nil {
			return err
		}
		if r.ID != Hash([]byte(r.Text))[:20] || strings.TrimSpace(r.Text) == "" || r.References == nil || (r.Source == nil && len(r.References) == 0) {
			return errors.New("invalid unified decision record")
		}
		switch r.SourceType {
		case "description", "discussion", "code", "user", "local":
		default:
			return errors.New("invalid decision source")
		}
		if r.PublicationStatus != "published" && r.PublicationStatus != "unpublished" {
			return errors.New("invalid decision publication state")
		}
		if (r.Status != "active" && r.Status != "superseded") || (r.Status == "active" && r.SupersededBy != nil) || (r.Status == "superseded" && r.SupersededBy == nil) {
			return errors.New("invalid decision supersession")
		}
		if _, ok := records[r.ID]; ok {
			return errors.New("duplicate unified decision")
		}
		records[r.ID] = r
	}
	for _, r := range records {
		if r.SupersededBy != nil {
			if _, ok := records[*r.SupersededBy]; !ok {
				return errors.New("missing replacement decision")
			}
		}
	}
	published, err := PublishedDecisions(m)
	if err != nil {
		return err
	}
	for _, raw := range published.Data.Decisions {
		var r record
		_ = json.Unmarshal(raw, &r)
		if _, ok := records[r.ID]; !ok {
			return errors.New("unified Decisions omit published choice")
		}
	}
	for _, local := range d.Data.LocalDecisions {
		if strings.TrimSpace(local.Text) == "" || strings.TrimSpace(local.Source) == "" {
			return errors.New("local choice requires exact instruction evidence")
		}
		if _, ok := records[Hash([]byte(local.Text))[:20]]; !ok {
			return errors.New("unified Decisions omit local choice")
		}
	}
	return nil
}
