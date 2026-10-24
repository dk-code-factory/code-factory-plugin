package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDependencyAdminIsAbsentFromPublicGateway(t *testing.T) {
	g, err := New(Config{PublicURL: "https://public.example/code-factory", ApprovalURL: "https://private.example:8444", Owner: "owner@example.com", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: "127.0.0.1:8787"}, t.TempDir(), strings.Repeat("x", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/dependencies", "/code-factory/admin/dependencies", "/code-factory/admin/dependencies/operations"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 64))
		w := httptest.NewRecorder()
		g.Public().ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatal("owner admin route reachable through public gateway", path, w.Code)
		}
	}
}
