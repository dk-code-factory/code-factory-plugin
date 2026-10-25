package transport

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var apiURL = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s<>"']+`)
var apiCredential = regexp.MustCompile(`(?i)(?:github_pat_[A-Za-z0-9_]+|gh[pousr]_[A-Za-z0-9]+|sk-[A-Za-z0-9_-]+|bearer\s+[A-Za-z0-9._~+/-]+=*)`)

type apiParameterSanitizer struct {
	secrets          []string
	remaining, nodes int
}

func newAPIParameterSanitizer(secrets []string) *apiParameterSanitizer {
	return &apiParameterSanitizer{secrets: secrets, remaining: 4096, nodes: 128}
}

func privateAPIParameter(key string) bool {
	key = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(key))
	if key == "meta" || key == "metadata" || key == "key" || key == "auth" || key == "nonce" || key == "env" || key == "headers" || key == "code" || key == "codeverifier" {
		return true
	}
	for _, part := range []string{"token", "secret", "password", "credential", "authorization", "cookie", "privatekey", "signingkey", "apikey", "answer", "prompt", "body", "content", "text", "message", "description", "reason"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func (s *apiParameterSanitizer) text(value string) string {
	for _, secret := range s.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	if strings.Contains(value, "-----BEGIN") && strings.Contains(value, "PRIVATE KEY") {
		return "[REDACTED]"
	}
	value = apiCredential.ReplaceAllString(value, "[REDACTED]")
	value = apiURL.ReplaceAllStringFunc(value, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[REDACTED URL]"
		}
		u.User = nil
		for _, secret := range s.secrets {
			if secret != "" {
				u.Path = strings.ReplaceAll(u.Path, secret, "[REDACTED]")
			}
		}
		u.Path = apiCredential.ReplaceAllString(u.Path, "[REDACTED]")
		u.RawPath = ""
		if u.RawQuery != "" {
			u.RawQuery = "[REDACTED]"
		}
		if u.Fragment != "" {
			u.Fragment = "[REDACTED]"
		}
		return u.String()
	})
	limit := min(512, s.remaining)
	if len(value) > limit {
		value = value[:limit] + "[TRUNCATED]"
	}
	s.remaining -= min(len(value), s.remaining)
	return value
}

func (s *apiParameterSanitizer) value(key string, value any, depth int) any {
	if privateAPIParameter(key) {
		return "[REDACTED]"
	}
	if depth > 8 || s.nodes <= 0 || s.remaining <= 0 {
		return "[TRUNCATED]"
	}
	s.nodes--
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s.nodes <= 0 || s.remaining <= 0 {
				out["_truncated"] = true
				break
			}
			out[s.text(k)] = s.value(k, v[k], depth+1)
		}
		return out
	case []any:
		out := []any{}
		for _, item := range v {
			if s.nodes <= 0 || s.remaining <= 0 {
				out = append(out, "[TRUNCATED]")
				break
			}
			out = append(out, s.value("", item, depth+1))
		}
		return out
	case []string:
		out := []any{}
		for _, item := range v {
			if s.nodes <= 0 || s.remaining <= 0 {
				out = append(out, "[TRUNCATED]")
				break
			}
			out = append(out, s.value("", item, depth+1))
		}
		return out
	case string:
		return s.text(v)
	case nil, bool, float64, int, int64, json.Number:
		return v
	default:
		return "[REDACTED]"
	}
}
