package factory

import (
	"encoding/json"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Native configuration can include credentials that are not managed by our vault.
// Keep those values out of inspection results, proposals and retained snapshots.
var nativeCredential = regexp.MustCompile(`(?i)(?:github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{20,}|bearer\s+[A-Za-z0-9._~+/-]+=*)`)

var nativeURLStart = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://`)
var nativePercentEscape = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)

// Credential discovery must retain independently readable escapes when another
// escape is malformed. URL rendering still uses the strict standard parser.
func nativeURLPercentDecode(raw string) string {
	decoded, _ := nativeURLPercentDecodePositions(raw)
	return decoded
}

func nativeURLPercentDecodePositions(raw string) (string, []int) {
	var decoded strings.Builder
	var positions []int
	offset := 0
	for _, escape := range nativePercentEscape.FindAllStringIndex(raw, -1) {
		decoded.WriteString(raw[offset:escape[0]])
		positions = append(positions, decoded.Len())
		value, _ := url.PathUnescape(raw[escape[0]:escape[1]])
		decoded.WriteString(value)
		offset = escape[1]
	}
	if len(positions) == 0 {
		return raw, nil
	}
	decoded.WriteString(raw[offset:])
	return decoded.String(), positions
}

// Keep whitespace inside a URL candidate, stopping at the next separate URL.
// A nested URL without a whitespace separator remains part of its outer value.
func nativeURLCandidates(value string) [][2]int {
	var spans [][2]int
	for _, match := range nativeURLStart.FindAllStringIndex(value, -1) {
		if len(spans) > 0 {
			previous, _ := utf8.DecodeLastRuneInString(value[:match[0]])
			if !unicode.IsSpace(previous) {
				continue
			}
		}
		spans = append(spans, [2]int{match[0], 0})
	}
	for i := range spans {
		end := len(value)
		if i+1 < len(spans) {
			end = spans[i+1][0]
		}
		start := spans[i][0]
		spans[i][1] = start + len(strings.TrimRightFunc(value[start:end], unicode.IsSpace))
	}
	return spans
}

func nativeURLRanges(value string) [][2]int {
	spans := nativeURLCandidates(value)
	for i, span := range spans {
		raw := value[span[0]:span[1]]
		// Ordinary prose after a public URL is not part of that URL. Extend
		// through whitespace only when it is needed to retain a credential.
		if len(nativeURLSecrets(raw)) == 0 {
			if space := strings.IndexFunc(raw, unicode.IsSpace); space >= 0 {
				raw = raw[:space]
			}
		}
		spans[i][1] = span[0] + len(raw)
	}
	return spans
}

func privateNativeKey(key string) bool {
	key = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(key))
	switch key {
	case "cliauthcredentialsstore", "mcpoauthcredentialsstore":
		return false // Native storage selectors, not credential values.
	case "env", "headers", "httpheaders", "args", "auth", "authorization", "cookie", "key":
		return true
	}
	for _, part := range []string{"secret", "password", "credential", "privatekey", "signingkey", "encryptionkey", "sessionkey", "accesskey", "apikey", "accesstoken", "refreshtoken", "bearertoken", "idtoken"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return key == "token" || strings.HasSuffix(key, "token")
}

type nativeRedactor struct{ secrets []string }

// These maps contain user-defined identifiers, not native setting names.
func nativeNamedEntries(key string) bool {
	switch key {
	case "profiles", "agents", "mcp_servers", "model_providers", "projects", "plugins", "marketplaces", "directories", "github_repos":
		return true
	}
	return false
}

func newNativeRedactor(data json.RawMessage) nativeRedactor {
	r := nativeRedactor{}
	var value any
	_ = json.Unmarshal(data, &value)
	var collect func(any, bool, bool)
	collect = func(value any, private, named bool) {
		switch v := value.(type) {
		case map[string]any:
			for k, child := range v {
				if !private && !named && (k == "env" || k == "headers" || k == "http_headers") {
					if env, ok := child.(map[string]any); ok {
						for name, entry := range env {
							// Header credentials can use arbitrary names.
							credential := k != "env" || privateNativeKey(name)
							if k == "env" {
								credential = credential || strings.Contains(strings.ToLower(name), "access_key") || strings.HasSuffix(strings.ToLower(name), "_access")
							}
							collect(entry, credential, false)
						}
						continue
					}
				}
				if !private && !named && k == "args" {
					if args, ok := child.([]any); ok {
						privateNext := false
						for _, entry := range args {
							text, _ := entry.(string)
							collect(entry, privateNext, false)
							if privateNext {
								privateNext = false
								continue
							}
							flag, val, inline := strings.Cut(text, "=")
							if strings.HasPrefix(flag, "-") && privateNativeKey(strings.TrimLeft(flag, "-")) {
								if inline {
									collect(val, true, false)
								} else {
									privateNext = true
								}
							}
						}
						continue
					}
				}
				collect(child, private || (!named && privateNativeKey(k)), !named && nativeNamedEntries(k))
			}
		case []any:
			for _, child := range v {
				collect(child, private, false)
			}
		case string:
			for _, span := range nativeURLRanges(v) {
				r.secrets = append(r.secrets, nativeURLSecrets(v[span[0]:span[1]])...)
			}
			if private && v != "" {
				r.secrets = append(r.secrets, v)
			}
		}
	}
	collect(value, false, false)
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}

// Collect raw components independently: an invalid path or fragment must not
// suppress a valid credential in another component or its copied values.
func nativeURLSecrets(raw string) []string {
	var secrets []string
	type urlCandidate struct {
		value  string
		nested bool
	}
	pending, seen := []urlCandidate{{value: raw}}, map[urlCandidate]bool{}
	for len(pending) > 0 {
		entry := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[entry] {
			continue
		}
		seen[entry] = true
		candidate := entry.value
		// Decoded values obey the same separate-URL boundaries as readable
		// input, including whitespace within a credential before the next URL.
		if spans := nativeURLCandidates(candidate); len(spans) > 1 {
			for _, span := range spans {
				pending = append(pending, urlCandidate{candidate[span[0]:span[1]], entry.nested})
			}
			continue
		}
		// Discover encoded schemes in every component, even beside readable
		// schemes. Revisit only newly decoded schemes: decoding an existing
		// URL's escaped parameter delimiters would invent shorter credentials.
		for encoded := candidate; ; {
			decoded, positions := nativeURLPercentDecodePositions(encoded)
			if decoded == encoded {
				break
			}
			for _, start := range nativeURLStart.FindAllStringIndex(decoded, -1) {
				index := sort.SearchInts(positions, start[0])
				if index < len(positions) && positions[index] < start[1] {
					pending = append(pending, urlCandidate{decoded[start[0]:], entry.nested || nativeURLStart.MatchString(decoded[:start[0]])})
				}
			}
			encoded = decoded
		}
		for _, start := range nativeURLStart.FindAllStringIndex(candidate, -1) {
			nested := entry.nested || nativeURLStart.MatchString(candidate[:start[0]])
			_, rest, _ := strings.Cut(candidate[start[0]:], "://")
			beforeFragment, fragment, _ := strings.Cut(rest, "#")
			beforeQuery, query, _ := strings.Cut(beforeFragment, "?")
			authority, path, hasPath := strings.Cut(beforeQuery, "/")
			// A nested path URL may encode only its query/fragment syntax.
			// Decode its path until that syntax appears, preserving authority
			// escapes, existing parameter values and any deeper readable URL.
			if hasPath && nested {
				suffix := ""
				if next := nativeURLStart.FindStringIndex(path); next != nil {
					path, suffix = path[:next[0]], path[next[0]:]
				}
				for encoded := path; ; {
					decoded := nativeURLPercentDecode(encoded)
					if decoded == encoded {
						break
					}
					if strings.ContainsAny(decoded, "?#") {
						pending = append(pending, urlCandidate{candidate[start[0]:start[1]] + authority + "/" + decoded + suffix, true})
						break
					}
					encoded = decoded
				}
			}
			// A readable scheme can still introduce an encoded authority,
			// including inside another URL's path. Decode that component until
			// its userinfo delimiter is visible. Once @ is readable, retain the
			// password's own escapes rather than reinterpreting them as syntax.
			// Exclude query/fragment values so escaped delimiters there cannot
			// create shorter credential prefixes.
			if !strings.Contains(authority, "@") {
				if decoded := nativeURLPercentDecode(authority); decoded != authority {
					pending = append(pending, urlCandidate{candidate[start[0]:start[1]] + decoded, nested})
				}
			}
			if at := strings.LastIndexByte(authority, '@'); at >= 0 {
				credential := authority[:at]
				username, password, hasPassword := strings.Cut(credential, ":")
				if hasPassword && password != "" {
					credential = password
				} else {
					credential = username
				}
				if credential != "" {
					secrets = append(secrets, credential)
					if decoded := nativeURLPercentDecode(credential); decoded != credential {
						secrets = append(secrets, decoded)
					}
				}
			}
			for _, parameters := range []string{query, fragment} {
				secrets = append(secrets, nativeURLParameterSecrets(parameters, nested)...)
				for _, pair := range strings.FieldsFunc(parameters, func(r rune) bool { return r == '&' || r == ';' }) {
					_, value, _ := strings.Cut(pair, "=")
					if decoded := nativeURLPercentDecode(strings.ReplaceAll(value, "+", " ")); decoded != value {
						pending = append(pending, urlCandidate{decoded, true})
					}
				}
			}
		}
	}
	return secrets
}

func (r nativeRedactor) text(value string) string {
	if strings.Contains(value, "-----BEGIN") && strings.Contains(value, "PRIVATE KEY") {
		return "[redacted]"
	}
	redactURL := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[redacted URL]"
		}
		changed := u.User != nil
		u.User = nil
		if query, redacted := redactNativeURLParameters(u.RawQuery); redacted {
			u.RawQuery, changed = query, true
		}
		fragment := u.EscapedFragment()
		if strings.Contains(fragment, "=") {
			if clean, redacted := redactNativeURLParameters(fragment); redacted {
				u.Fragment, _ = url.PathUnescape(clean)
				u.RawFragment, changed = clean, true
			}
		}
		if !changed {
			if len(nativeURLSecrets(raw)) > 0 {
				return "[redacted URL]"
			}
			return raw
		}
		return u.String()
	}
	var clean strings.Builder
	offset := 0
	for _, span := range nativeURLRanges(value) {
		clean.WriteString(value[offset:span[0]])
		raw := value[span[0]:span[1]]
		// Redact inner URLs first, including URLs embedded in a path. Earlier
		// scheme offsets remain valid when only their suffix has changed.
		starts := nativeURLStart.FindAllStringIndex(raw, -1)
		for i := len(starts) - 1; i >= 0; i-- {
			start := starts[i][0]
			raw = raw[:start] + redactURL(raw[start:])
		}
		clean.WriteString(raw)
		offset = span[1]
	}
	clean.WriteString(value[offset:])
	value = clean.String()
	for _, secret := range r.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	for encoded := value; ; {
		decoded := nativeURLPercentDecode(encoded)
		if decoded == encoded {
			break
		}
		for _, secret := range r.secrets {
			if secret != "" && strings.Contains(decoded, secret) {
				return "[redacted]"
			}
		}
		encoded = decoded
	}
	return nativeCredential.ReplaceAllString(value, "[redacted]")
}

func redactNativeURLParameters(raw string) (string, bool) {
	parameters, err := url.ParseQuery(raw)
	if err != nil {
		return "", true // A partial parse cannot establish that omitted pairs are safe.
	}
	changed := false
	for key, values := range parameters {
		for _, value := range values {
			if value != "" && (privateNativeKey(key) || len(nativeURLSecrets(value)) > 0) {
				parameters.Del(key)
				changed = true
				break
			}
		}
	}
	if !changed {
		return raw, false
	}
	return parameters.Encode(), true
}

func nativeURLParameterSecrets(raw string, nested bool) []string {
	var secrets []string
	pairs := strings.Split(raw, "&")
	if strings.Contains(raw, ";") {
		// Retain whole credential values and also inspect ambiguous separator
		// chunks: an ordinary prefix must not hide a later credential pair.
		pairs = append(pairs, strings.FieldsFunc(raw, func(r rune) bool { return r == '&' || r == ';' })...)
	}
	for _, pair := range pairs {
		// Nested syntax can have a deeper-encoded assignment separator. Keep
		// the original pair boundary while exposing that separator, so decoded
		// ampersands inside the credential never create a shorter secret.
		if nested {
			for !strings.Contains(pair, "=") {
				decoded := nativeURLPercentDecode(pair)
				if decoded == pair {
					break
				}
				pair = decoded
			}
		}
		key, value, _ := strings.Cut(pair, "=")
		decodedKey, keyErr := url.QueryUnescape(key)
		for keyErr == nil && !privateNativeKey(decodedKey) {
			decoded := nativeURLPercentDecode(decodedKey)
			if decoded == decodedKey {
				break
			}
			decodedKey = decoded
		}
		if keyErr == nil && !strings.Contains(key, ";") && !privateNativeKey(decodedKey) {
			continue
		}
		if value != "" {
			secrets = append(secrets, value)
			if decoded := nativeURLPercentDecode(strings.ReplaceAll(value, "+", " ")); decoded != value {
				secrets = append(secrets, decoded)
			}
		}
	}
	return secrets
}

func (r nativeRedactor) value(value any, private, named bool) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			key := k
			if !named {
				key = nativeCredential.ReplaceAllString(k, "[redacted]")
			}
			out[key] = r.value(child, private || (!named && privateNativeKey(k)), !named && nativeNamedEntries(k))
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = r.value(child, private, false)
		}
		return out
	case nil:
		return nil
	default:
		if private {
			return "[redacted]"
		}
		if text, ok := v.(string); ok {
			return r.text(text)
		}
		return v
	}
}

func (r nativeRedactor) json(data json.RawMessage) json.RawMessage {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if !json.Valid(data) || decoder.Decode(&value) != nil {
		return bytes(map[string]string{"error": "invalid native configuration"})
	}
	return bytes(r.value(value, false, false))
}

func nativeSnapshot(data json.RawMessage) json.RawMessage {
	var original, projected map[string]json.RawMessage
	r := newNativeRedactor(data)
	clean := r.json(data)
	if json.Unmarshal(data, &original) != nil || json.Unmarshal(clean, &projected) != nil {
		return clean
	}
	// Native-generated layer identities are selectors for checkLayer, not
	// configuration values. Never substitute private arguments into them.
	var layers []map[string]json.RawMessage
	if json.Unmarshal(original["layers"], &layers) == nil {
		for _, layer := range layers {
			for key, value := range layer {
				if key != "name" && key != "version" {
					layer[key] = r.json(value)
				}
			}
		}
		projected["layers"] = bytes(layers)
	}
	return bytes(projected)
}

func validateNativeEdits(actual json.RawMessage, edits []ConfigEdit) error {
	r := newNativeRedactor(actual)
	for _, edit := range edits {
		path, err := nativeKeyPath(edit.KeyPath)
		if err != nil {
			return err
		}
		private, named, credentialPath := false, false, false
		for _, key := range path {
			private = private || (!named && privateNativeKey(key))
			credentialPath = credentialPath || (!named && nativeCredential.MatchString(key))
			named = !named && nativeNamedEntries(key)
		}
		var value any
		_ = json.Unmarshal(bytes(edit.Value), &value)
		encodedValue := string(bytes(value))
		if private || credentialPath || strings.Contains(encodedValue, "[redacted]") || strings.Contains(encodedValue, "[redacted URL]") || encodedValue != string(bytes(r.value(value, false, named))) {
			return fmt.Errorf("credential-bearing native edits must be made outside Code Factory")
		}
	}
	return nil
}

func nativeKeyPath(path string) ([]string, error) {
	var tree map[string]any
	if strings.ContainsAny(path, "\r\n") || toml.Unmarshal([]byte(path+" = 0"), &tree) != nil {
		return nil, fmt.Errorf("native edits require a valid dotted key path")
	}
	keys := []string{}
	for len(tree) == 1 {
		for key, value := range tree {
			keys = append(keys, key)
			if child, ok := value.(map[string]any); ok {
				tree = child
			} else if value == int64(0) {
				return keys, nil
			} else {
				return nil, fmt.Errorf("native edits require a valid dotted key path")
			}
		}
	}
	return nil, fmt.Errorf("native edits require a valid dotted key path")
}

func nativeDiff(n NativeEdit) string {
	return "--- native configuration layers (credentials redacted)\n" + string(n.Before) + "\n+++ proposed native key edits\n" + string(bytes(n.Edits))
}

// Older proposals stored the complete native response. Rebuild their display
// from a sanitized snapshot without weakening the original freshness check.
func sanitizeNativeProposal(p *Proposal) {
	if p.Native == nil {
		return
	}
	n := p.Native
	r := newNativeRedactor(n.Before)
	if n.BeforeDigest == "" {
		n.BeforeDigest = digest(n.Before)
	}
	if validateNativeEdits(n.Before, n.Edits) != nil {
		for i := range n.Edits {
			n.Edits[i].Value = "[redacted]"
			n.Edits[i].KeyPath = r.text(n.Edits[i].KeyPath)
		}
		if p.State == "proposed" || p.State == "confirmed" {
			p.State = "stale"
		}
	}
	n.Before = nativeSnapshot(n.Before)
	p.Diff = nativeDiff(*n)
}

func sanitizeNativeProposalEvent(data json.RawMessage) (json.RawMessage, error) {
	var proposal Proposal
	if err := json.Unmarshal(data, &proposal); err != nil {
		return nil, err
	}
	sanitizeNativeProposal(&proposal)
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	// Keep the historical event's state and any unrelated metadata verbatim.
	record["native"], record["diff"] = bytes(proposal.Native), bytes(proposal.Diff)
	return bytes(record), nil
}
