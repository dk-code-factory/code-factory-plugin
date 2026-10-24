package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStableCodexVersionBoundary(t *testing.T) {
	for _, tc := range []struct {
		version string
		valid   bool
	}{{"0.160.0", false}, {"0.161.0", true}, {"0.164.0", true}, {"1.0.0", true}, {"0.164.0-alpha", false}, {"unrelated", false}} {
		t.Run(tc.version, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "codex")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' 'codex-cli "+tc.version+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			_, err := Version(context.Background(), binary)
			if (err == nil) != tc.valid {
				t.Fatal(tc, err)
			}
		})
	}
	if CompareVersions("0.164.0", "0.161.0") <= 0 || CompareVersions("0.9.0", "0.10.0") >= 0 {
		t.Fatal("versions compared lexically")
	}
}
