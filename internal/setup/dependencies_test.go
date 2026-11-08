package setup

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"codefactory/internal/providers"
)

func TestTunnelReleaseDiscoveryUsesPublicGitHubCache(t *testing.T) {
	calls, validations := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repos/openai/tunnel-client/releases/latest" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected release request or authentication")
		}
		w.Header().Set("ETag", `"release"`)
		if r.Header.Get("If-None-Match") == `"release"` {
			validations++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fmt.Fprint(w, `{"tag_name":"v1","assets":[{"name":"fixture.zip","browser_download_url":"https://github.com/openai/tunnel-client/releases/download/v1/fixture.zip","digest":"sha256:fixture"}]}`)
	}))
	defer server.Close()
	g := providers.NewPublicGitHub()
	g.BaseURL, g.Client = server.URL, server.Client()
	ctx := context.Background()
	for range 2 {
		release, err := latestTunnelRelease(ctx, g)
		if err != nil || release.Tag != "v1" || len(release.Assets) != 1 || release.Assets[0].Digest != "sha256:fixture" {
			t.Fatal(release, err)
		}
	}
	if calls != 1 {
		t.Fatal("installer bypassed release cache", calls)
	}
	if _, err := latestTunnelRelease(providers.WithGitHubRead(ctx, providers.RequireLive), g); err != nil || calls != 2 || validations != 1 {
		t.Fatal("live release lookup lost conditional validation", calls, validations, err)
	}
	if _, _, err := g.Request(ctx, http.MethodPost, "/mutation", nil, ""); err == nil || calls != 2 {
		t.Fatal("anonymous client dispatched a mutation", calls, err)
	}
}
