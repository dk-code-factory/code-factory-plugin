package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/store"
	"codefactory/internal/testutil"
)

func TestDiagnosticsUseLocalGitHubCredentialAndRespectReconciliationCadence(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "api-attempt")
	t.Setenv("CF_GH_API_ATTEMPT", marker)
	script := `#!/bin/sh
if [ "$1" = auth ] && [ "$2" = token ]; then
printf 'fixture-private-credential\n'
exit 0
fi
: > "$CF_GH_API_ATTEMPT"
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, r := range []domain.Repository{{ID: "o/recent", LastSync: time.Now().Add(-10 * time.Minute)}, {ID: "o/paused", Paused: true}, {ID: "o/deferred", Error: "GitHub reserve reached", RetryAt: time.Now().Add(time.Hour)}} {
		if err := db.Put(ctx, "repository", r.ID, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	result := Diagnose(ctx, dir, testutil.FakeCodex(t), "127.0.0.1:1", db)
	states := map[string]State{}
	for _, check := range result.Checks {
		states[check.ID] = check.State
	}
	if states["provider.github"] != Verified || states["sync.o/recent"] != Verified || states["sync.o/paused"] != Unverified || states["sync.o/deferred"] != Unverified {
		t.Fatal("local/cached diagnostic state incorrect", states)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("diagnostics attempted an unbudgeted GitHub command")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "fixture-private-credential") {
		t.Fatal("diagnostics exposed local credential")
	}
}
