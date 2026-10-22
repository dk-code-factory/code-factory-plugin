package transport

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestUIResourceContentIdentityAndCompatibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	previousURI := ""
	for _, html := range []string{
		"<!doctype html><style>body{background:white}</style><script>const revision=1</script>",
		"<!doctype html><style>body{background:var(--background)}</style><script>const revision=2</script>",
	} {
		server := mcp.NewServer(&mcp.Implementation{Name: "ui-resource-test", Version: "1"}, nil)
		uri := registerUIResource(server, html)
		repeated := registerUIResource(mcp.NewServer(&mcp.Implementation{Name: "repeated-resource-test", Version: "1"}, nil), html)
		want := fmt.Sprintf("ui://code-factory/inbox-%x.html", sha256.Sum256([]byte(html)))
		if uri != want || repeated != uri || resourceURI(html) != uri || uri == legacyResourceURI || uri == previousURI {
			t.Fatalf("UI content did not receive its deterministic identity: %q", uri)
		}
		bind(server, uri, "open_factory", "Open the UI fixture.", false, true, func(context.Context, Empty) (any, error) { return nil, nil })
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, serverTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "ui-resource-client", Version: "1"}, nil)
		session, err := client.Connect(ctx, clientTransport, nil)
		if err != nil {
			serverSession.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close(); serverSession.Close() })
		resources, err := session.ListResources(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(resources.Resources) != 1 || resources.Resources[0].URI != uri {
			t.Fatalf("only current content may be advertised: %+v", resources.Resources)
		}
		templates, err := session.ListResourceTemplates(ctx, nil)
		if err != nil || len(templates.ResourceTemplates) != 0 {
			t.Fatalf("compatibility resource must not be advertised as a template: %+v, %v", templates, err)
		}
		tools, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tools.Tools) != 1 {
			t.Fatalf("expected one current UI tool: %+v", tools.Tools)
		}
		metadata := tools.Tools[0].Meta
		if metadata["ui"].(map[string]any)["resourceUri"] != uri || metadata["openai/outputTemplate"] != uri {
			t.Fatalf("tool metadata does not use current content identity: %v", metadata)
		}
		for _, requestedURI := range []string{uri, legacyResourceURI} {
			resource, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: requestedURI})
			if err != nil {
				t.Fatal(err)
			}
			if len(resource.Contents) != 1 || resource.Contents[0].URI != requestedURI || resource.Contents[0].Text != html || resource.Contents[0].MIMEType != "text/html;profile=mcp-app" {
				t.Fatalf("resource read does not match requested identity: %+v", resource.Contents)
			}
			csp := resource.Contents[0].Meta["ui"].(map[string]any)["csp"].(map[string]any)
			if len(csp["connectDomains"].([]any)) != 0 || len(csp["resourceDomains"].([]any)) != 0 {
				t.Fatalf("UI resource gained external access: %v", csp)
			}
		}
		unknown := []string{"ui://code-factory/inbox-" + strings.Repeat("0", 64) + ".html", legacyResourceURI + "?revision=unknown", uri + "?revision=unknown"}
		if previousURI != "" {
			unknown = append(unknown, previousURI)
		}
		for _, requestedURI := range unknown {
			_, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: requestedURI})
			var protocolError *jsonrpc.Error
			if !errors.As(err, &protocolError) || protocolError.Code != mcp.CodeResourceNotFound {
				t.Fatalf("unregistered URI %q must fail closed: %v", requestedURI, err)
			}
		}
		previousURI = uri
	}
}

func TestSettingsResourceIsIndependentFromInboxAndLegacyAlias(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	previousSettings := ""
	for _, main := range []string{`<!doctype html><html><body>revision one</body></html>`, `<!doctype html><html><body>revision two</body></html>`} {
		settings := strings.Replace(main, "<html", `<html data-code-factory-view="settings"`, 1)
		server := mcp.NewServer(&mcp.Implementation{Name: "independent-settings-test", Version: "1"}, nil)
		inboxURI := registerUIResource(server, main)
		settingsURI := registerSettingsUIResource(server, settings)
		repeated := registerSettingsUIResource(mcp.NewServer(&mcp.Implementation{Name: "repeated-settings-test", Version: "1"}, nil), settings)
		if inboxURI == settingsURI || settingsURI == previousSettings || settingsURI != repeated || settingsURI != fmt.Sprintf("ui://code-factory/settings-%x.html", sha256.Sum256([]byte(settings))) {
			t.Fatal("settings identity is not independent and content-specific")
		}
		bind(server, inboxURI, "open_factory", "Inbox", false, true, func(context.Context, Empty) (any, error) { return "inbox", nil })
		bind(server, settingsURI, "open_settings", "Settings", false, true, func(context.Context, Empty) (any, error) { return "settings", nil })
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "separate-settings-client", Version: "1"}, nil)
		session, err := client.Connect(ctx, ct, nil)
		if err != nil {
			ss.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close(); ss.Close() })
		listed, err := session.ListResources(ctx, nil)
		if err != nil || len(listed.Resources) != 2 {
			t.Fatal("both surfaces must be advertised", err)
		}
		tools, err := session.ListTools(ctx, nil)
		if err != nil || len(tools.Tools) != 2 {
			t.Fatal("both UI entry tools must be advertised", err)
		}
		expected := map[string]string{"open_factory": inboxURI, "open_settings": settingsURI}
		for _, tool := range tools.Tools {
			uri := expected[tool.Name]
			if tool.Meta["ui"].(map[string]any)["resourceUri"] != uri || tool.Meta["openai/outputTemplate"] != uri {
				t.Fatal("entry tool points at the wrong surface", tool.Name)
			}
		}
		for uri, body := range map[string]string{inboxURI: main, settingsURI: settings, legacyResourceURI: main} {
			result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Contents) != 1 || result.Contents[0].URI != uri || result.Contents[0].Text != body {
				t.Fatal("surface content crossed identities", uri)
			}
			csp := result.Contents[0].Meta["ui"].(map[string]any)["csp"].(map[string]any)
			if len(csp["connectDomains"].([]any)) != 0 || len(csp["resourceDomains"].([]any)) != 0 {
				t.Fatal("settings gained direct iframe network access")
			}
		}
		invalid := []string{"ui://code-factory/settings.html", settingsURI + "?view=inbox", resourceURI(settings), settingsResourceURI(main)}
		if previousSettings != "" {
			invalid = append(invalid, previousSettings)
		}
		for _, uri := range invalid {
			_, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
			var protocolError *jsonrpc.Error
			if !errors.As(err, &protocolError) || protocolError.Code != mcp.CodeResourceNotFound {
				t.Fatal("unregistered cross-surface URI accepted", uri, err)
			}
		}
		previousSettings = settingsURI
	}
}
