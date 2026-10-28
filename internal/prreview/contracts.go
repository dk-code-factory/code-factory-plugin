// Package prreview owns deterministic PR review and publication contracts.
// Models supply analysis and findings; neither output supplies mutation authority.
package prreview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const Upstream = "1b0019767a29142bbb4b3855c2fc9f1e4af75b71"
const Contract = "code-factory-pr-review-v1"

type Reviewer struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	SkillID string   `json:"skillId,omitempty"`
	Argv    []string `json:"argv,omitempty"`
}
type Policy struct {
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"`
}

func ResolvePolicy(global bool, repository, pr, launch *bool) Policy {
	for _, entry := range []struct {
		name  string
		value *bool
	}{{"launch", launch}, {"pr", pr}, {"repository", repository}} {
		if entry.value != nil {
			return Policy{*entry.value, entry.name}
		}
	}
	return Policy{global, "global"}
}
func ValidateReviewers(reviewers []Reviewer) error {
	seen := map[string]bool{}
	for _, r := range reviewers {
		if r.ID == "" || seen[r.ID] {
			return errors.New("reviewer IDs must be nonempty and unique")
		}
		seen[r.ID] = true
		switch r.Kind {
		case "skill":
			if r.SkillID == "" || len(r.Argv) != 0 {
				return errors.New("skill reviewer requires only a skillId")
			}
		case "command":
			if len(r.Argv) == 0 || r.Argv[0] == "" || r.SkillID != "" {
				return errors.New("command reviewer requires only an argument vector")
			}
			hasHandoff := false
			for _, arg := range r.Argv {
				if strings.Contains(arg, "{handoff}") {
					hasHandoff = true
				}
				if arg == "" || strings.ContainsRune(arg, 0) {
					return errors.New("invalid reviewer argument")
				}
			}
			if !hasHandoff {
				return errors.New("command reviewer must receive a {handoff} argument")
			}
		default:
			return fmt.Errorf("reviewer %s: expected skill or command binding", r.ID)
		}
	}
	return nil
}

// CanonicalJSON matches DK's json.dumps(sort_keys=True, ensure_ascii=False).
func CanonicalJSON(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&v); err != nil {
		return nil, err
	}
	var write func(any) error
	var out bytes.Buffer
	write = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteString(", ")
				}
				if err := write(k); err != nil {
					return err
				}
				out.WriteString(": ")
				if err := write(x[k]); err != nil {
					return err
				}
			}
			out.WriteByte('}')
		case []any:
			out.WriteByte('[')
			for i, item := range x {
				if i > 0 {
					out.WriteString(", ")
				}
				if err := write(item); err != nil {
					return err
				}
			}
			out.WriteByte(']')
		case string:
			var s bytes.Buffer
			e := json.NewEncoder(&s)
			e.SetEscapeHTML(false)
			if err := e.Encode(x); err != nil {
				return err
			}
			str := strings.TrimSuffix(s.String(), "\n")
			str = strings.ReplaceAll(strings.ReplaceAll(str, `\u2028`, "\u2028"), `\u2029`, "\u2029")
			out.WriteString(str)
		default:
			b, err := json.Marshal(x)
			if err != nil {
				return err
			}
			out.Write(b)
		}
		return nil
	}
	if err = write(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func Digest(value any) string {
	b, err := CanonicalJSON(value)
	if err != nil {
		panic(err)
	}
	return Hash(b)
}
func Hash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

// StrictDecode rejects duplicate keys, unknown fields and trailing JSON.
func StrictDecode(b []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var scan func() error
	scan = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, err := d.Token()
					if err != nil {
						return err
					}
					s, ok := k.(string)
					if !ok || seen[s] {
						return errors.New("duplicate JSON member")
					}
					seen[s] = true
					if err = scan(); err != nil {
						return err
					}
				}
				_, err = d.Token()
				return err
			case '[':
				for d.More() {
					if err = scan(); err != nil {
						return err
					}
				}
				_, err = d.Token()
				return err
			default:
				return errors.New("unexpected JSON delimiter")
			}
		}
		return nil
	}
	if err := scan(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

type Finding struct {
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}
type Response struct {
	Findings []Finding `json:"findings"`
	Status   string    `json:"status,omitempty"`
	Message  string    `json:"message,omitempty"`
}

func ValidateResponse(b []byte, patch string) (Response, error) {
	var response Response
	if err := StrictDecode(b, &response); err != nil {
		return response, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	// encoding/json matches struct fields without regard to case. The pinned
	// response schema requires exact member names, including nested findings.
	for name := range fields {
		if name != "findings" && name != "status" && name != "message" {
			return response, fmt.Errorf("$: unexpected member %q", name)
		}
	}
	if response.Findings == nil {
		return response, errors.New("/findings: expected an array")
	}
	if response.Status != "" {
		if (response.Status != "incomplete" && response.Status != "tokens_exhausted") || len(fields) != 3 || len(response.Findings) != 0 || strings.TrimSpace(response.Message) == "" {
			return response, errors.New("incomplete response requires status, empty findings and a reason")
		}
		return response, nil
	}
	if len(fields) != 1 {
		return response, errors.New("$: expected only findings")
	}
	var findings []map[string]json.RawMessage
	_ = json.Unmarshal(fields["findings"], &findings)
	for i, finding := range findings {
		if len(finding) != 5 {
			return response, fmt.Errorf("/findings/%d: expected severity, path, line, message and fix", i)
		}
		for name := range finding {
			switch name {
			case "severity", "path", "line", "message", "fix":
			default:
				return response, fmt.Errorf("/findings/%d: unexpected member %q", i, name)
			}
		}
	}
	lines, err := ParsePatch(patch)
	if err != nil {
		return response, err
	}
	for i, f := range response.Findings {
		if f.Severity != "Major" && f.Severity != "Minor" && f.Severity != "Nit" {
			return response, fmt.Errorf("/findings/%d/severity: expected Major, Minor or Nit", i)
		}
		if !lines.Added[Location{f.Path, f.Line, "RIGHT"}] {
			return response, fmt.Errorf("/findings/%d/line: requires an added line in the pinned diff", i)
		}
		if strings.TrimSpace(f.Message) == "" || strings.TrimSpace(f.Fix) == "" {
			return response, fmt.Errorf("/findings/%d: message and fix are required", i)
		}
	}
	return response, nil
}

// CompactDigest is DK preparation_graph/engineering_identity hashing. Session
// and result provenance use Digest's spaced representation instead.
func CompactDigest(v any) string {
	b, err := CanonicalJSON(v)
	if err != nil {
		panic(err)
	}
	var compact bytes.Buffer
	if err = json.Compact(&compact, b); err != nil {
		panic(err)
	}
	return Hash(compact.Bytes())
}
