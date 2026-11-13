package factory

import (
	"codefactory/internal/codex"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func nativeCredentialFixture(file string) map[string]any {
	config := map[string]any{
		"model": "fixture-model", "cli_auth_credentials_store": "file",
		"history":  map[string]any{"max_bytes": int64(9223372036854775807)},
		"profiles": map[string]any{"pilot": map[string]any{"sandbox_mode": "read-only", "approval_policy": "never"}},
		"mcp_servers": map[string]any{"fixture": map[string]any{
			"env":          map[string]any{"CUSTOM_ACCESS": "fixture-env-credential", "SHORT": "x"},
			"http_headers": map[string]any{"Authorization": "fixture-header-credential", "X-Auth": "fixture-custom-header-credential", "X-Workspace": "fixture-opaque-header-credential"},
			"args":         []any{"--credential", "fixture-argument-credential"},
			"url":          "https://user:fixture-url-password@example.test/mcp?token=fixture-query-credential#access_token=fixture-fragment",
		}},
		"password": "fixture-password-credential", "custom_note": "copied fixture-env-credential",
		"access_token": "fixture-token-credential", "unknown": "ghp_fixturePatternCredential",
		"access_key":              "fixture-access-credential",
		"developer_instructions":  "copied fixture-custom-header-credential",
		"custom_header_copy":      "copied fixture-opaque-header-credential",
		"opaque_pattern":          "sk-fixtureOpaquePatternCredential",
		"ordinary_query_copy":     "fixture-malformed-query-credential;part",
		"ordinary_fragment_copy":  "&fixture-encoded-fragment-credential",
		"ordinary_quote_copy":     "'fixture-quoted-url-credential",
		"ordinary_parse_copy":     "fixture-unparseable-url-credential",
		"ordinary_separator_copy": "fixture-separator-url-credential",
	}
	servers := config["mcp_servers"].(map[string]any)
	servers["query-boundary"] = map[string]any{"url": "https://example.test/mcp?token=fixture-malformed-query-credential;part"}
	servers["fragment-boundary"] = map[string]any{"url": "https://example.test/mcp#access_token=%26fixture-encoded-fragment-credential"}
	servers["quote-boundary"] = map[string]any{"url": "https://example.test/mcp?token='fixture-quoted-url-credential"}
	servers["parse-boundary"] = map[string]any{"url": "https://example.test/mcp?token=fixture-unparseable-url-credential#bad%zz"}
	servers["separator-boundary"] = map[string]any{"url": "https://example.test/mcp?mode=read-only;token=fixture-separator-url-credential"}
	return map[string]any{"config": config, "layers": []any{map[string]any{
		"name": map[string]any{"type": "user", "file": file}, "version": "v1", "config": config,
	}}}
}

func assertNativeCredentialsHidden(t *testing.T, value any) {
	t.Helper()
	data := string(bytes(value))
	for _, private := range []string{"fixture-env-credential", "fixture-header-credential", "fixture-custom-header-credential", "fixture-opaque-header-credential", "fixture-argument-credential", "fixture-url-password", "fixture-query-credential", "fixture-fragment", "fixture-password-credential", "fixture-token-credential", "fixture-access-credential", "ghp_fixturePatternCredential", "sk-fixtureOpaquePatternCredential", "fixture-malformed-query-credential", "fixture-encoded-fragment-credential", "fixture-quoted-url-credential", "fixture-unparseable-url-credential", "fixture-separator-url-credential"} {
		if strings.Contains(data, private) {
			t.Fatal("native credential escaped its projection")
		}
	}
	if !strings.Contains(data, "[redacted]") {
		t.Fatal("native projection did not identify redacted values")
	}
}

func TestNativeInspectionRedactsUnmanagedCredentials(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	file := filepath.Join(s.Dir, "config.toml")
	rpc.native = nativeCredentialFixture(file)
	raw := string(bytes(rpc.native))
	view, err := s.NativeConfig(ctx, s.Dir)
	must(t, err)
	assertNativeCredentialsHidden(t, view)
	for _, visible := range []string{"fixture-model", "pilot", "read-only", "approval_policy", "never", "https://example.test/mcp", file, `"version":"v1"`, `"cli_auth_credentials_store":"file"`, `"max_bytes":9223372036854775807`} {
		if !strings.Contains(string(view), visible) {
			t.Fatalf("native inspection lost nonsecret setting/identity %q", visible)
		}
	}
	if string(bytes(rpc.native)) != raw {
		t.Fatal("inspection mutated the actual native configuration")
	}
	v, err := s.Chat(ctx, "configuration", "Inspect", nil)
	must(t, err)
	result, suspended, err := s.dynamic(ctx, &v, "code_factory_inspect_configuration", bytes(map[string]string{"cwd": s.Dir}), codex.Message{})
	must(t, err)
	if suspended {
		t.Fatal("inspection suspended")
	}
	assertNativeCredentialsHidden(t, result)
}

func TestNativeInspectionPreservesSelectorsAndRedactsURLCopies(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	file := filepath.Join(s.Dir, "user", "config.toml")
	rpc.native = nativeCredentialFixture(file)
	config := rpc.native["config"].(map[string]any)
	server := config["mcp_servers"].(map[string]any)["fixture"].(map[string]any)
	server["env"].(map[string]any)["HOME"] = filepath.Dir(filepath.Dir(file))
	server["env"].(map[string]any)["MODE"] = "read-only"
	server["args"] = []any{"user", "--model", "fixture-model", "--sandbox", "read-only", "--credential", "fixture-argument-credential"}
	server["url"] = "https://user:p4ss@example.test/mcp?token=fixture-query-credential&mode=read-only#access_token=fixture-fragment-credential"
	config["ordinary_url_copy"] = "copied p4ss"
	config["copied_url_query"] = "copied fixture-query-credential"
	config["copied_url_fragment"] = "copied fixture-fragment-credential"
	view, err := s.NativeConfig(ctx, s.Dir)
	must(t, err)
	for _, secret := range []string{"p4ss", "fixture-query-credential", "fixture-fragment-credential"} {
		if strings.Contains(string(view), secret) {
			t.Fatal("copied URL credential was not masked")
		}
	}
	var projected struct {
		Config map[string]any
		Layers []struct {
			Name    struct{ File, Type string }
			Version string
		}
	}
	must(t, json.Unmarshal(view, &projected))
	if projected.Config["model"] != "fixture-model" || projected.Layers[0].Name.File != file || projected.Layers[0].Name.Type != "user" || projected.Layers[0].Version != "v1" {
		t.Fatal("nonsecret arguments/environment changed profile settings or exact layer selectors")
	}
	profile := projected.Config["profiles"].(map[string]any)["pilot"].(map[string]any)
	if profile["sandbox_mode"] != "read-only" || profile["approval_policy"] != "never" {
		t.Fatal("nonsecret arguments/environment changed native profile settings")
	}
	_, err = s.Propose(ctx, nil, &NativeEdit{CWD: s.Dir, File: projected.Layers[0].Name.File, Version: projected.Layers[0].Version, Edits: []ConfigEdit{{KeyPath: "model", Value: "next-model", MergeStrategy: "replace"}}})
	must(t, err) // The displayed selector must remain usable for a safe proposal.
}

func TestNativeNonsecretURLsRemainEditable(t *testing.T) {
	for i, publicURL := range []string{
		"https://example.test/mcp?transport=sse&mode=read-only&profile=sk-dev#secret-guide",
		"https://example.test/mcp?transport=sse#section;two",
		"https://example.test/mcp?transport=sse#next=section%26two&label=space+title",
		"https://example.test/mcp?label=quote'\"<>&mode=read-only#section'\"<>",
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, rpc, _ := fixture(t)
			ctx := context.Background()
			file := filepath.Join(s.Dir, "config.toml")
			rpc.native = nativeCredentialFixture(file)
			config := rpc.native["config"].(map[string]any)
			server := config["mcp_servers"].(map[string]any)["fixture"].(map[string]any)
			server["url"] = publicURL
			view, err := s.NativeConfig(ctx, s.Dir)
			must(t, err)
			var projected struct{ Config map[string]any }
			must(t, json.Unmarshal(view, &projected))
			if projected.Config["mcp_servers"].(map[string]any)["fixture"].(map[string]any)["url"] != publicURL {
				t.Fatal("native inspection changed a nonsecret URL")
			}
			p, err := s.Propose(ctx, nil, &NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{{KeyPath: "mcp_servers.fixture.url", Value: publicURL, MergeStrategy: "replace"}}})
			must(t, err)
			if p.Native.Edits[0].Value != publicURL || len(rpc.writes) != 0 {
				t.Fatal("ordinary URL proposal changed its value or wrote before confirmation")
			}
			_, err = s.Confirm(ctx, p.ID, true)
			must(t, err)
			if len(rpc.writes) != 1 {
				t.Fatal("confirmed ordinary URL did not produce one native write")
			}
			var write struct{ Edits []ConfigEdit }
			must(t, json.Unmarshal(rpc.writes[0], &write))
			if len(write.Edits) != 1 || write.Edits[0].Value != publicURL {
				t.Fatal("native write changed the ordinary URL")
			}
		})
	}
}

func TestNativeURLParameterBoundariesRejectCredentials(t *testing.T) {
	for _, tc := range []struct{ name, url, decoded, encoded, marker string }{
		{"query-semicolon", "https://example.test/mcp?token=opaque-query-value;part&mode=read-only", "opaque-query-value;part", "opaque-query-value;part", "opaque-query-value"},
		{"query-invalid-escape", "https://example.test/mcp?token=opaque%zzquery-value", "opaque%zzquery-value", "opaque%zzquery-value", "query-value"},
		{"fragment-semicolon", "https://example.test/mcp#access_token=opaque-fragment-value;part", "opaque-fragment-value;part", "opaque-fragment-value;part", "opaque-fragment-value"},
		{"fragment-escaped-ampersand", "https://example.test/mcp#access_token=%26opaque-fragment-value&next=section%26two", "&opaque-fragment-value", "%26opaque-fragment-value", "opaque-fragment-value"},
		{"fragment-escaped-semicolon", "https://example.test/mcp#access_token=%3Bopaque-fragment-value", ";opaque-fragment-value", "%3Bopaque-fragment-value", "opaque-fragment-value"},
		{"query-escaped-ampersand", "https://example.test/mcp?token=%26opaque-query-value", "&opaque-query-value", "%26opaque-query-value", "opaque-query-value"},
		{"userinfo-escaped-password", "https://user:%6Fpaque-userinfo-value@example.test/mcp", "opaque-userinfo-value", "%6Fpaque-userinfo-value", "opaque-userinfo-value"},
		{"userinfo-escaped-username", "https://%6Fpaque-userinfo-value@example.test/mcp", "opaque-userinfo-value", "%6Fpaque-userinfo-value", "opaque-userinfo-value"},
		{"query-apostrophe", "https://example.test/mcp?token='opaque-query-value", "'opaque-query-value", "'opaque-query-value", "opaque-query-value"},
		{"fragment-apostrophe", "https://example.test/mcp#access_token='opaque-fragment-value", "'opaque-fragment-value", "'opaque-fragment-value", "opaque-fragment-value"},
		{"userinfo-apostrophe", "https://user:alpha'omega@example.test/mcp", "alpha'omega", "alpha'omega", "omega"},
		{"query-double-quote", "https://example.test/mcp?token=\"opaque-query-value", "\"opaque-query-value", "\"opaque-query-value", "opaque-query-value"},
		{"query-invalid-fragment", "https://example.test/mcp?token=opaque-query-value#bad%zz", "opaque-query-value", "opaque-query-value", "opaque-query-value"},
		{"query-invalid-path", "https://example.test/bad%zz?token=opaque-query-value", "opaque-query-value", "opaque-query-value", "opaque-query-value"},
		{"fragment-invalid-path", "https://example.test/bad%zz#access_token=opaque-fragment-value", "opaque-fragment-value", "opaque-fragment-value", "opaque-fragment-value"},
		{"userinfo-invalid-fragment", "https://user:opaque-userinfo-value@example.test/mcp#bad%zz", "opaque-userinfo-value", "opaque-userinfo-value", "opaque-userinfo-value"},
		{"query-angle-brackets", "https://example.test/mcp?token=<opaque-query-value>", "<opaque-query-value>", "<opaque-query-value>", "opaque-query-value"},
		{"userinfo-literal-plus", "https://user:alpha+omega@example.test/mcp", "alpha+omega", "alpha+omega", "omega"},
		{"query-semicolon-before-token", "https://example.test/mcp?mode=read-only;token=opaque-query-value", "opaque-query-value", "opaque-query-value", "opaque-query-value"},
		{"fragment-semicolon-before-token", "https://example.test/mcp#mode=read-only;access_token=opaque-fragment-value", "opaque-fragment-value", "opaque-fragment-value", "opaque-fragment-value"},
		{"query-multiple-semicolons", "https://example.test/mcp?mode=read-only;transport=sse;token=opaque-query-value", "opaque-query-value", "opaque-query-value", "opaque-query-value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rpc, _ := fixture(t)
			ctx := context.Background()
			file := filepath.Join(s.Dir, "config.toml")
			credentialURL := tc.url
			config := map[string]any{"model": "fixture-model", "sandbox_mode": "read-only", "mcp_servers": map[string]any{"fixture": map[string]any{"url": credentialURL}}, "ordinary_copy": tc.decoded, "encoded_copy": tc.encoded}
			rpc.native = map[string]any{"config": config, "layers": []any{map[string]any{"name": map[string]any{"type": "user", "file": file}, "version": "v1", "config": config}}}
			assertHidden := func(label string, value any) {
				t.Helper()
				data := string(bytes(value))
				for _, secret := range []string{tc.marker, tc.decoded, tc.encoded} {
					encoded := bytes(secret)
					if strings.Contains(data, string(encoded[1:len(encoded)-1])) {
						t.Errorf("%s retained a URL credential or its copied value", label)
						break
					}
				}
			}
			view, err := s.NativeConfig(ctx, s.Dir)
			must(t, err)
			assertHidden("inspection", view)
			var projected struct{ Config map[string]any }
			must(t, json.Unmarshal(view, &projected))
			if projected.Config["sandbox_mode"] != "read-only" {
				t.Error("malformed credential parameters changed an unrelated ordinary setting")
			}
			if tc.name == "fragment-escaped-ampersand" {
				if projected.Config["mcp_servers"].(map[string]any)["fixture"].(map[string]any)["url"] != "https://example.test/mcp#next=section%26two" {
					t.Error("redaction changed the remaining fragment parameter encoding")
				}
			}
			edit := ConfigEdit{KeyPath: "model", Value: "next-model", MergeStrategy: "replace"}
			p, err := s.Propose(ctx, nil, &NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{edit}})
			must(t, err)
			assertHidden("proposal", p)
			var saved Proposal
			must(t, s.Store.Get(ctx, "factory-proposal", p.ID, &saved))
			assertHidden("persisted proposal", saved)
			legacy := Proposal{State: "proposed", Native: &NativeEdit{Before: bytes(rpc.native), Edits: []ConfigEdit{edit}}, Diff: string(bytes(rpc.native))}
			event, err := sanitizeNativeProposalEvent(bytes(legacy))
			must(t, err)
			assertHidden("legacy event", event)
			sanitizeNativeProposal(&legacy)
			assertHidden("legacy proposal", legacy)
			_, err = s.Propose(ctx, nil, &NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{{KeyPath: "mcp_servers.fixture.url", Value: credentialURL, MergeStrategy: "replace"}}})
			if err == nil {
				t.Error("credential-bearing URL edit was accepted")
			}
			for _, copied := range []string{tc.decoded, tc.encoded} {
				_, err = s.Propose(ctx, nil, &NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{{KeyPath: "ordinary_copy", Value: copied, MergeStrategy: "replace"}}})
				if err == nil {
					t.Error("copied URL credential edit was accepted")
				}
			}
			proposals, err := store.List[Proposal](ctx, s.Store, "factory-proposal")
			must(t, err)
			if len(proposals) != 1 || len(rpc.writes) != 0 {
				t.Fatal("URL edit persisted or native configuration was written")
			}
		})
	}
}

func TestNativeCredentialWordsInNamesRemainEditable(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	file := filepath.Join(s.Dir, "config.toml")
	rpc.native = nativeCredentialFixture(file)
	config := rpc.native["config"].(map[string]any)
	profiles := config["profiles"].(map[string]any)
	for _, name := range []string{"secret-scan", "token", "secret.scan", "sk-dev"} {
		profiles[name] = map[string]any{"model": "fixture-model", "sandbox_mode": "read-only", "approval_policy": "never"}
	}
	project := filepath.Join(s.Dir, ".secrets", "project")
	config["projects"] = map[string]any{project: map[string]any{"trust_level": "trusted"}}
	config["plugins"] = map[string]any{"secret-scan@market": map[string]any{"enabled": true}}
	config["model_provider"] = "sk-dev"
	config["model_providers"] = map[string]any{"sk-dev": map[string]any{"name": "Development provider"}}
	config["mcp_servers"].(map[string]any)["secret-server"] = map[string]any{"enabled": true, "env": map[string]any{"API_KEY": "fixture-named-server-credential"}}
	view, err := s.NativeConfig(ctx, s.Dir)
	must(t, err)
	var projected struct{ Config map[string]json.RawMessage }
	must(t, json.Unmarshal(view, &projected))
	if string(projected.Config["profiles"]) != string(bytes(profiles)) || string(projected.Config["projects"]) != string(bytes(config["projects"])) || string(projected.Config["plugins"]) != string(bytes(config["plugins"])) || string(projected.Config["model_provider"]) != `"sk-dev"` || string(projected.Config["model_providers"]) != string(bytes(config["model_providers"])) {
		t.Fatal("credential words in user-defined names hid ordinary settings")
	}
	if strings.Contains(string(view), "fixture-named-server-credential") {
		t.Fatal("a named server bypassed credential masking inside its settings")
	}
	for _, edit := range []ConfigEdit{
		{KeyPath: "profiles.secret-scan", Value: profiles["secret-scan"], MergeStrategy: "upsert"},
		{KeyPath: "profiles.token.model", Value: "next-model", MergeStrategy: "replace"},
		{KeyPath: "profiles.sk-dev.model", Value: "next-model", MergeStrategy: "replace"},
		{KeyPath: "model_provider", Value: "sk-dev", MergeStrategy: "replace"},
		{KeyPath: `profiles."secret.scan".model`, Value: "next-model", MergeStrategy: "replace"},
		{KeyPath: "profiles", Value: profiles, MergeStrategy: "upsert"},
		{KeyPath: fmt.Sprintf("projects.%q.trust_level", project), Value: "trusted", MergeStrategy: "replace"},
		{KeyPath: `mcp_servers."secret-server".enabled`, Value: true, MergeStrategy: "replace"},
	} {
		_, err = s.Propose(ctx, nil, &NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{edit}})
		must(t, err)
	}
	for _, path := range []string{`profiles.token.api_key`, `profiles.sk-dev.api_key`, `profiles."secret.scan".password`, `mcp_servers."secret-server".env.API_KEY`} {
		_, err = s.Propose(ctx, nil, &NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{{KeyPath: path, Value: "new-credential", MergeStrategy: "replace"}}})
		if err == nil {
			t.Fatal("a named-entry path bypassed rejection of a credential setting")
		}
	}
}

func TestNativeProposalRetainsRedactedSnapshotAndRawFreshness(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	file := filepath.Join(s.Dir, "config.toml")
	rpc.native = nativeCredentialFixture(file)
	n := NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{{KeyPath: "profiles.extra", Value: map[string]any{"sandbox_mode": "workspace-write", "approval_policy": "on-request"}, MergeStrategy: "upsert"}}}
	p, err := s.Propose(ctx, nil, &n)
	must(t, err)
	assertNativeCredentialsHidden(t, p)
	var saved Proposal
	must(t, s.Store.Get(ctx, "factory-proposal", p.ID, &saved))
	assertNativeCredentialsHidden(t, saved)
	if saved.Native.BeforeDigest != digest(bytes(rpc.native)) || len(rpc.writes) != 0 {
		t.Fatal("proposal lost its raw fingerprint or wrote before confirmation")
	}
	before, err := s.NativeConfig(ctx, s.Dir)
	must(t, err)
	// Even a credential-only change, invisible in the projection, must invalidate
	// confirmation. Preserve the layer version to test the full fingerprint.
	rpc.native["config"].(map[string]any)["password"] = "fixture-rotated-credential"
	after, err := s.NativeConfig(ctx, s.Dir)
	must(t, err)
	if string(before) != string(after) {
		t.Fatal("credential rotation changed the public projection")
	}
	if _, err = s.Confirm(ctx, p.ID, true); err == nil || len(rpc.writes) != 0 {
		t.Fatal("hidden native change did not reject stale confirmation")
	}
	p, err = s.Propose(ctx, nil, &n)
	must(t, err)
	p, err = s.Confirm(ctx, p.ID, true)
	must(t, err)
	if p.State != "applied" || len(rpc.writes) != 1 {
		t.Fatal("fresh scoped edit did not apply exactly once")
	}
	var write struct{ Edits []ConfigEdit }
	must(t, json.Unmarshal(rpc.writes[0], &write))
	if string(bytes(write.Edits)) != string(bytes(n.Edits)) {
		t.Fatal("confirmed edit changed or included unrelated native settings")
	}
}

func TestNativeCredentialEditsAreRejectedWithoutPersistence(t *testing.T) {
	s, rpc, _ := fixture(t)
	ctx := context.Background()
	file := filepath.Join(s.Dir, "config.toml")
	rpc.native = nativeCredentialFixture(file)
	for _, edit := range []ConfigEdit{
		{KeyPath: "mcp_servers.fixture.env.NEW_KEY", Value: "new-credential", MergeStrategy: "upsert"},
		{KeyPath: "mcp_servers.fixture", Value: map[string]any{"env": map[string]any{"CUSTOM": "new-credential"}}, MergeStrategy: "replace"},
		{KeyPath: "model", Value: "fixture-env-credential", MergeStrategy: "replace"},
		{KeyPath: "developer_instructions", Value: "copied fixture-custom-header-credential", MergeStrategy: "replace"},
		{KeyPath: "model", Value: "fixture-opaque-header-credential", MergeStrategy: "replace"},
		{KeyPath: "model", Value: "ghp_fixtureNewCredential", MergeStrategy: "replace"},
		{KeyPath: "model", Value: "sk-fixtureOpaquePatternCredential", MergeStrategy: "replace"},
		{KeyPath: "mcp_servers.fixture.url", Value: "https://user:pass@example.test?key=new-credential", MergeStrategy: "replace"},
		{KeyPath: "mcp_servers.fixture.url", Value: "https://example.test?transport=sse&token=new-credential", MergeStrategy: "replace"},
		{KeyPath: "mcp_servers.fixture.url", Value: "https://example.test#access_token=new-credential", MergeStrategy: "replace"},
		{KeyPath: "[profiles.pilot]\nmodel", Value: "next-model", MergeStrategy: "replace"},
		{KeyPath: "profiles.extra", Value: map[string]any{"model": "[redacted]"}, MergeStrategy: "upsert"},
	} {
		_, err := s.Propose(ctx, nil, &NativeEdit{CWD: s.Dir, File: file, Version: "v1", Edits: []ConfigEdit{edit}})
		if err == nil {
			t.Fatal("credential edit or redacted placeholder was accepted")
		}
	}
	proposals, err := store.List[Proposal](ctx, s.Store, "factory-proposal")
	must(t, err)
	if len(proposals) != 0 || len(rpc.writes) != 0 {
		t.Fatal("rejected credential edits persisted or wrote configuration")
	}
}

func TestNativeLegacyProposalMigrationPreservesStaleness(t *testing.T) {
	for _, credentialEdit := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-edit", true: "credential-edit"}[credentialEdit], func(t *testing.T) {
			s, rpc, _ := fixture(t)
			ctx := context.Background()
			file := filepath.Join(s.Dir, "config.toml")
			rpc.native = nativeCredentialFixture(file)
			settings, err := s.Settings(ctx)
			must(t, err)
			edits := []ConfigEdit{{KeyPath: "model", Value: "next-model", MergeStrategy: "replace"}}
			if credentialEdit {
				edits = []ConfigEdit{{KeyPath: "mcp_servers.fixture.env.NEW", Value: "fixture-env-credential", MergeStrategy: "upsert"}}
			}
			p := Proposal{ID: "legacy-native", State: "proposed", Base: digest(settings), Before: settings, After: settings,
				Native: &NativeEdit{CWD: s.Dir, File: file, Version: "v1", Before: bytes(rpc.native), Edits: edits},
				Diff:   "--- native configuration layers\n" + string(bytes(rpc.native)), Created: s.Now()}
			must(t, s.Store.Put(ctx, "factory-proposal", p.ID, p, "configuration.proposed"))
			must(t, s.Store.Write(ctx, func(tx *store.Tx) error {
				historical := p
				historical.State = "confirmed"
				if err := tx.Event("configuration.confirmed", p.ID, historical); err != nil {
					return err
				}
				// More than one migration page, including events whose proposal
				// entity is no longer present, must lose their legacy snapshots.
				for i := 0; i < 35; i++ {
					historical.ID, historical.State = fmt.Sprintf("historical-%d", i), "rejected"
					if err := tx.Event("configuration.rejected", historical.ID, historical); err != nil {
						return err
					}
				}
				return tx.Event("unrelated.fixture", "other", map[string]string{"marker": "keep-history"})
			}))
			originalEvents, err := s.Store.Events(ctx, 0, 200)
			must(t, err)
			calls := 0
			err = s.Store.RedactNativeProposalEvents(ctx, func(data json.RawMessage) (json.RawMessage, error) {
				calls++
				if calls == 2 {
					return nil, fmt.Errorf("fixture migration failure")
				}
				return sanitizeNativeProposalEvent(data)
			})
			if err == nil || calls != 2 {
				t.Fatal("fixture did not exercise migration failure after one rewrite")
			}
			failedEvents, err := s.Store.Events(ctx, 0, 200)
			must(t, err)
			if string(bytes(originalEvents)) != string(bytes(failedEvents)) {
				t.Fatal("failed journal sanitization left a partial update")
			}
			// Startup sanitizes retained proposals before API projections are built.
			must(t, s.Init(ctx))
			must(t, s.Store.Get(ctx, "factory-proposal", p.ID, &p))
			assertNativeCredentialsHidden(t, p)
			cleanEvents, err := s.Store.Events(ctx, 0, 200)
			must(t, err)
			if len(cleanEvents) != len(originalEvents) {
				t.Fatal("sanitization emitted or removed an event")
			}
			for i, event := range cleanEvents {
				old := originalEvents[i]
				if event.Seq != old.Seq || event.Kind != old.Kind || event.Entity != old.Entity || !event.At.Equal(old.At) {
					t.Fatal("sanitization changed historical event identity")
				}
				if strings.HasPrefix(event.Kind, "configuration.") {
					assertNativeCredentialsHidden(t, event.Data)
					var payload Proposal
					must(t, json.Unmarshal(event.Data, &payload))
					var original Proposal
					must(t, json.Unmarshal(old.Data, &original))
					if payload.State != original.State {
						t.Fatal("sanitization rewrote historical event state")
					}
				} else if string(event.Data) != string(old.Data) {
					t.Fatal("sanitization changed an unrelated event")
				}
			}
			if p.Native.BeforeDigest != digest(bytes(rpc.native)) {
				t.Fatal("legacy migration replaced the original freshness fingerprint")
			}
			if credentialEdit {
				if p.State != "stale" {
					t.Fatal("legacy credential edit remained eligible for application")
				}
			} else {
				if p.State != "proposed" {
					t.Fatal("ordinary legacy proposal lost its pending decision")
				}
				rpc.native["config"].(map[string]any)["password"] = "fixture-rotated-credential"
				if _, err = s.Confirm(ctx, p.ID, true); err == nil {
					t.Fatal("migrated proposal accepted a hidden native change")
				}
			}
			if len(rpc.writes) != 0 {
				t.Fatal("migration or stale confirmation wrote native configuration")
			}
			first := string(bytes(p))
			must(t, s.Init(ctx))
			must(t, s.Store.Get(ctx, "factory-proposal", p.ID, &p))
			if string(bytes(p)) != first {
				t.Fatal("native proposal migration was not idempotent")
			}
			repeatedEvents, err := s.Store.Events(ctx, 0, 200)
			must(t, err)
			if string(bytes(repeatedEvents)) != string(bytes(cleanEvents)) {
				t.Fatal("journal sanitization was not idempotent")
			}
		})
	}
}
