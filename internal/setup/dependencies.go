package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// InstallTunnel preserves existing clients. GitHub's release-asset digest verifies
// the download from the official upstream, over authenticated HTTPS transport.
func InstallTunnel(ctx context.Context) error {
	if _, e := exec.LookPath("tunnel-client"); e == nil {
		return nil
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("tunnel installation supports Linux x86-64 only")
	}
	client := &http.Client{Timeout: 90 * time.Second}
	get := func(url string) (*http.Response, error) {
		r, e := http.NewRequestWithContext(ctx, "GET", url, nil)
		if e != nil {
			return nil, e
		}
		r.Header.Set("Accept", "application/vnd.github+json")
		res, e := client.Do(r)
		if e != nil {
			return nil, e
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			return nil, fmt.Errorf("official tunnel download HTTP %d", res.StatusCode)
		}
		return res, nil
	}
	res, e := get("https://api.github.com/repos/openai/tunnel-client/releases/latest")
	if e != nil {
		return e
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name   string
			URL    string `json:"browser_download_url"`
			Digest string
		}
	}
	e = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&release)
	res.Body.Close()
	if e != nil {
		return e
	}
	for _, a := range release.Assets {
		name := strings.ToLower(a.Name)
		if !strings.HasPrefix(name, "tunnel-client-v") || !strings.Contains(name, "linux") || !(strings.Contains(name, "amd64") || strings.Contains(name, "x86_64")) || !strings.HasSuffix(name, ".zip") {
			continue
		}
		if !strings.HasPrefix(a.URL, "https://github.com/openai/tunnel-client/releases/download/") || !strings.HasPrefix(a.Digest, "sha256:") {
			return fmt.Errorf("official release asset has no verifiable SHA256 digest")
		}
		res, e = get(a.URL)
		if e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(res.Body, (100<<20)+1))
		res.Body.Close()
		if e != nil {
			return e
		}
		if len(b) > 100<<20 {
			return fmt.Errorf("tunnel archive too large")
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != strings.TrimPrefix(a.Digest, "sha256:") {
			return fmt.Errorf("tunnel archive checksum mismatch")
		}
		archive, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if e != nil {
			return e
		}
		for _, entry := range archive.File {
			if filepath.Base(entry.Name) != "tunnel-client" || !entry.Mode().IsRegular() {
				continue
			}
			reader, e := entry.Open()
			if e != nil {
				return e
			}
			bin, e := io.ReadAll(io.LimitReader(reader, (100<<20)+1))
			reader.Close()
			if e != nil {
				return e
			}
			if len(bin) > 100<<20 {
				return fmt.Errorf("tunnel binary too large")
			}
			home, _ := os.UserHomeDir()
			target := filepath.Join(home, ".local", "bin", "tunnel-client")
			if _, e = os.Lstat(target); !os.IsNotExist(e) {
				return fmt.Errorf("refusing to replace existing tunnel client")
			}
			if e = atomicFile(target, bin, 0700); e != nil {
				return e
			}
			fmt.Println("Installed verified official tunnel-client", release.Tag)
			return nil
		}

	}
	return fmt.Errorf("official release has no supported Linux x86-64 archive; use the download in Platform tunnel settings")
}
