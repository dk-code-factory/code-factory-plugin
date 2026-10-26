package reports

import (
	"codefactory/internal/providers"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sync/atomic"
)

type trackingKey struct{}
type trackingBatch struct {
	client *providers.GitHub
	id     uint64
}

var batchSequence atomic.Uint64

// Requests (including listings, tags and comparisons) are memoized only for the
// current operation. Verified immutable evidence can survive it when every asset
// has a digest. Tag targets are always revalidated before consulting that cache.
func (g *GitHubAdapter) proofKey(ctx context.Context, repo string, r githubRelease) (string, error) {
	scope, e := g.Client.Scope(ctx)
	if e != nil {
		return "", e
	}
	var tag struct {
		SHA string `json:"sha"`
	}
	if e = g.get(ctx, "/repos/"+repo+"/commits/"+url.PathEscape(r.Tag), &tag); e != nil {
		return "", e
	}
	b, _ := json.Marshal(struct {
		Repo    string
		Release githubRelease
		Tag     string
	}{repo, r, tag.SHA})
	h := sha256.Sum256(b)
	key := "provenance:" + scope + ":" + hex.EncodeToString(h[:])
	if !digestAssets(r) {
		key += fmt.Sprintf(":batch:%d", ctx.Value(trackingKey{}).(*trackingBatch).id)
	}
	return key, nil
}
func digestAssets(r githubRelease) bool {
	if len(r.Assets) == 0 {
		return false
	}
	for _, a := range r.Assets {
		if a.Digest == "" {
			return false
		}
	}
	return true
}
func (g *GitHubAdapter) releaseProvenance(ctx context.Context, repo string, r githubRelease) (Provenance, bool, error) {
	ctx = g.operation(ctx)
	// Preserve cheap rejection of absent manifests without additional requests.
	hasManifest := false
	for _, a := range r.Assets {
		if a.Name == "CI-MANIFEST.json" {
			hasManifest = true
		}
	}
	if !hasManifest {
		return g.verifyReleaseProvenance(ctx, repo, r)
	}
	key, e := g.proofKey(ctx, repo, r)
	if e != nil {
		return Provenance{}, false, e
	}
	{
		if b, ok := g.Client.CachedBlob(key); ok {
			var p Provenance
			if json.Unmarshal(b, &p) == nil {
				return p, true, nil
			}
		}
	}
	p, ok, e := g.verifyReleaseProvenance(ctx, repo, r)
	if e == nil && ok {
		b, _ := json.Marshal(p)
		g.Client.CacheBlob(key, b, 0)
	}
	return p, ok, e
}
func (g *GitHubAdapter) workflowReleaseProvenance(ctx context.Context, repo string, r githubRelease) (ComponentProvenance, bool, error) {
	ctx = g.operation(ctx)
	if r.Tag == "" || r.URL != "https://github.com/"+repo+"/releases/tag/"+r.Tag {
		return ComponentProvenance{}, false, nil
	}
	key, e := g.proofKey(ctx, repo, r)
	if e != nil {
		return ComponentProvenance{}, false, e
	}
	{
		if b, ok := g.Client.CachedBlob(key); ok {
			var p ComponentProvenance
			if json.Unmarshal(b, &p) == nil {
				return p, true, nil
			}
		}
	}
	p, ok, e := g.verifyWorkflowReleaseProvenance(ctx, repo, r)
	if e == nil && ok {
		b, _ := json.Marshal(p)
		g.Client.CacheBlob(key, b, 0)
	}
	return p, ok, e
}
