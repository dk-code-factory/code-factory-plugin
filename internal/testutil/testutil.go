package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var once sync.Once
var binary string
var buildErr error

func FakeCodex(t *testing.T) string {
	t.Helper()
	once.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		dir, e := os.MkdirTemp("", "code-factory-fixture-")
		if e != nil {
			buildErr = e
			return
		}
		binary = filepath.Join(dir, "codex")
		cmd := exec.Command("go", "build", "-o", binary, filepath.Join(filepath.Dir(file), "fakecodex"))
		buildErr = cmd.Run()
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binary
}
