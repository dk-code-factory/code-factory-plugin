package dependencies

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const CodexRepository = "dkropachev/codex"

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}
type Release struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Code-Factory-Dependency-Admin")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dependency upstream unavailable")
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("dependency upstream HTTP %d", res.StatusCode)
	}
	return res, nil
}

func LatestCodex(ctx context.Context, client *http.Client) (Release, error) {
	var release Release
	res, err := get(ctx, client, "https://api.github.com/repos/"+CodexRepository+"/releases/latest")
	if err != nil {
		return release, err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&release); err != nil {
		return release, fmt.Errorf("invalid Codex release metadata")
	}
	if release.Draft || release.Prerelease || !regexp.MustCompile(`^rust-v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(release.Tag) {
		return release, fmt.Errorf("Codex release must be stable")
	}
	return release, nil
}

// StageCodex verifies the configured fork package digest and keeps companion binaries
// beside the CLI. The caller probes compatibility before activating its path.
func StageCodex(ctx context.Context, client *http.Client, release Release, destination string) (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return "", fmt.Errorf("managed Codex updates currently support Linux amd64")
	}
	var asset *Asset
	for i := range release.Assets {
		if release.Assets[i].Name == "codex-package-x86_64-unknown-linux-musl.tar.gz" {
			asset = &release.Assets[i]
			break
		}
	}
	if asset == nil || !strings.HasPrefix(asset.URL, "https://github.com/"+CodexRepository+"/releases/download/"+release.Tag+"/") || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(asset.Digest) {
		return "", fmt.Errorf("Codex package has no verified configured-source digest")
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return "", err
	}
	archive, err := os.CreateTemp(destination, ".codex-download-")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	res, err := get(ctx, client, asset.URL)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(res.Body, (300<<20)+1))
	res.Body.Close()
	if copyErr != nil || n > 300<<20 {
		return "", fmt.Errorf("Codex download failed or exceeded limit")
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.TrimPrefix(asset.Digest, "sha256:") {
		return "", fmt.Errorf("Codex package checksum mismatch")
	}
	if _, err := archive.Seek(0, 0); err != nil {
		return "", err
	}
	if err := Extract(archive, destination, 1<<30); err != nil {
		return "", err
	}
	var binaries []string
	err = filepath.WalkDir(destination, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "codex" {
			binaries = append(binaries, p)
		}
		return nil
	})
	if err != nil || len(binaries) != 1 {
		return "", fmt.Errorf("Codex distribution must contain exactly one CLI")
	}
	return binaries[0], nil
}
