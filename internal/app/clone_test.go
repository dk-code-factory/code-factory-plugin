package app

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestManagedCloneUsesGitTransportAndCredentialHelperWithoutAPILookup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Fixture")
	t.Setenv("GIT_COMMITTER_NAME", "Fixture")
	t.Setenv("GIT_AUTHOR_EMAIL", "fixture@example.invalid")
	t.Setenv("GIT_COMMITTER_EMAIL", "fixture@example.invalid")
	run := func(args ...string) string {
		t.Helper()
		b, e := exec.Command("git", args...).CombinedOutput()
		if e != nil {
			t.Fatal("fixture Git command failed", e)
		}
		return strings.TrimSpace(string(b))
	}
	work := filepath.Join(root, "work")
	run("init", "--initial-branch=main", work)
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("private fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("-C", work, "add", "README")
	run("-C", work, "commit", "-m", "fixture")
	project := filepath.Join(root, "served")
	if err := os.MkdirAll(filepath.Join(project, "owner"), 0700); err != nil {
		t.Fatal(err)
	}
	run("clone", "--bare", work, filepath.Join(project, "owner", "repo.git"))
	backend := &cgi.Handler{Path: filepath.Join(run("--exec-path"), "git-http-backend"), Env: []string{"GIT_PROJECT_ROOT=" + project, "GIT_HTTP_EXPORT_ALL=1"}}
	var authenticated atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture-user" || password != "fixture-pass" {
			w.Header().Set("WWW-Authenticate", `Basic realm="private fixture"`)
			w.WriteHeader(401)
			return
		}
		authenticated.Add(1)
		backend.ServeHTTP(w, r)
	}))
	defer server.Close()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// Any attempted gh API/repository lookup fails; only Git credential exchange
	// is available. All HTTP traffic is rewritten to the private local fixture.
	helper := `#!/bin/sh
if [ "$1" = auth ] && [ "$2" = git-credential ]; then
cat >/dev/null
printf 'username=fixture-user\npassword=fixture-pass\n'
exit 0
fi
exit 97
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+server.URL+"/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/")
	checkout := filepath.Join(root, "checkout")
	if err := cloneRepository(context.Background(), "owner/repo", checkout); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(checkout, "README"))
	if err != nil || string(body) != "private fixture\n" || authenticated.Load() == 0 {
		t.Fatal("authenticated clone did not preserve fixture contents", err)
	}
}
