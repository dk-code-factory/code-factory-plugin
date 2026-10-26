package reports

import (
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrackingBatchSharesAndRevalidatesEvidence(t *testing.T) {
	s, ctx := fixture(t)
	sha, fix := strings.Repeat("a", 40), strings.Repeat("b", 40)
	p := Provenance{Version: 1, Commit: sha, Components: map[string]ComponentProvenance{"code-factory": {Repository: DefaultRepository, Version: "1.0.0", Commit: sha}}, Fixes: []FixedIssue{{Issue: DefaultRepository + "#17", Component: "code-factory", Commit: fix}, {Issue: DefaultRepository + "#18", Component: "code-factory", Commit: fix}}, Files: []Artifact{{Name: "app.tar.gz", Size: 3, SHA256: strings.Repeat("c", 64)}, {Name: "app.zip", Size: 4, SHA256: strings.Repeat("d", 64)}}}
	manifest, _ := json.Marshal(p)
	sum := sha256.Sum256(manifest)
	sums := []byte(p.Files[0].SHA256 + "  app.tar.gz\n" + p.Files[1].SHA256 + "  app.zip\n" + hex.EncodeToString(sum[:]) + "  CI-MANIFEST.json\n")
	published := time.Now()
	release := githubRelease{Tag: "v1.0.0", URL: "https://github.com/" + DefaultRepository + "/releases/tag/v1.0.0", PublishedAt: &published}
	release.Assets = []githubAsset{{ID: 1, Name: "app.tar.gz", Size: 3, State: "uploaded", Digest: "sha256:" + p.Files[0].SHA256}, {ID: 2, Name: "app.zip", Size: 4, State: "uploaded", Digest: "sha256:" + p.Files[1].SHA256}}
	payloads := map[int64][]byte{3: manifest, 4: sums}
	for _, id := range []int64{3, 4} {
		h := sha256.Sum256(payloads[id])
		name := "CI-MANIFEST.json"
		if id == 4 {
			name = "SHA256SUMS"
		}
		release.Assets = append(release.Assets, githubAsset{ID: id, Name: name, State: "uploaded", Size: int64(len(payloads[id])), Digest: "sha256:" + hex.EncodeToString(h[:])})
	}
	calls := map[string]int{}
	tag := sha
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		switch {
		case strings.HasSuffix(r.URL.Path, "/timeline"):
			reply(w, []any{map[string]any{"event": "closed", "commit_id": fix, "commit_url": "https://api.github.com/repos/" + DefaultRepository + "/commits/" + fix}})
		case strings.HasSuffix(r.URL.Path, "/commits/"+fix):
			reply(w, map[string]any{"sha": fix, "html_url": "https://github.com/" + DefaultRepository + "/commit/" + fix, "commit": map[string]string{"message": "Fixes #17 and fixes #18"}})
		case strings.HasSuffix(r.URL.Path, "/commits/v1.0.0"):
			reply(w, map[string]string{"sha": tag})
		case strings.Contains(r.URL.Path, "/compare/"):
			reply(w, map[string]string{"status": "ahead"})
		case strings.HasSuffix(r.URL.Path, "/releases"):
			w.Header().Set("ETag", `"listing"`)
			if r.Header.Get("If-None-Match") == `"listing"` {
				w.WriteHeader(304)
			} else {
				reply(w, []githubRelease{release})
			}
		case strings.Contains(r.URL.Path, "/assets/"):
			var id int64
			fmt.Sscanf(r.URL.Path, "/repos/"+DefaultRepository+"/releases/assets/%d", &id)
			_, _ = w.Write(payloads[id])
		default:
			var number int
			fmt.Sscanf(r.URL.Path, "/repos/"+DefaultRepository+"/issues/%d", &number)
			reply(w, githubIssue{Number: number, URL: fmt.Sprintf("https://github.com/%s/issues/%d", DefaultRepository, number), State: "open"})
		}
	})
	g.Store = s.Store
	s.GitHub = g
	var tokens int
	g.Client.Token = func(context.Context) (string, error) { tokens++; return "fixture-token", nil }
	for i, n := range []int{17, 18, 17} {
		r := Report{ID: fmt.Sprint(i), Component: "code-factory", Issue: &Issue{Repository: DefaultRepository, Number: n}}
		if e := s.Store.Put(ctx, "issue-report", r.ID, r, ""); e != nil {
			t.Fatal(e)
		}
	}
	// A reused caller context must never make manual refresh reuse a prior batch.
	ctx = g.BeginOperation(ctx)
	if e := s.Refresh(ctx, true); e != nil {
		t.Fatal(e)
	}
	prefix := "/repos/" + DefaultRepository
	for _, endpoint := range []string{"/issues/17", "/issues/18", "/issues/17/timeline", "/issues/18/timeline", "/releases", "/commits/v1.0.0", "/commits/" + fix, "/compare/" + fix + "..." + sha, "/releases/assets/3", "/releases/assets/4"} {
		if calls[prefix+endpoint] != 1 {
			t.Fatal("batch duplicated evidence", endpoint, calls)
		}
	}
	if tokens != 1 {
		t.Fatal("refresh resolved token repeatedly", tokens)
	}
	reports, e := store.List[Report](ctx, s.Store, "issue-report")
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range reports {
		if len(r.Releases) != 1 || len(r.Fixes) != 1 || r.ReleaseSync.LastSuccess.IsZero() {
			t.Fatal("report-specific matching failed", r.ID, r.Releases)
		}
	}
	if e = s.Refresh(ctx, true); e != nil {
		t.Fatal(e)
	}
	if tokens != 2 || calls[prefix+"/releases"] != 2 || calls[prefix+"/commits/v1.0.0"] != 2 || calls[prefix+"/releases/assets/3"] != 1 {
		t.Fatal("new refresh did not revalidate mutable evidence/reuse verified bytes", tokens, calls)
	}
	tag = fix
	if e = s.Refresh(ctx, true); e != nil {
		t.Fatal(e)
	}
	reports, _ = store.List[Report](ctx, s.Store, "issue-report")
	for _, r := range reports {
		if len(r.Releases) != 0 {
			t.Fatal("changed tag reused verification", r.ID)
		}
	}
}

func TestReportingLinkTraversalAndCachedPagination(t *testing.T) {
	for _, count := range []int{100, 200} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			calls := 0
			var g *GitHubAdapter
			g = adapter(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if count == 200 && r.URL.Query().Get("page") == "1" {
					w.Header().Set("Link", `<`+g.Client.BaseURL+`/rows?per_page=100&page=2>; type="application/json"; rel="next"`)
				}
				if r.Header.Get("If-None-Match") != "" {
					w.WriteHeader(304)
					return
				}
				w.Header().Set("ETag", `"page"`)
				rows := make([]int, 100)
				reply(w, rows)
			})
			for pass := 0; pass < 2; pass++ {
				rows, e := listPages[int](context.Background(), g, "/rows")
				if e != nil || len(rows) != count {
					t.Fatal("pagination lost cached links", len(rows), e)
				}
			}
			if calls != count/100*2 {
				t.Fatal("extra empty page probe", calls)
			}
		})
	}
	for _, kind := range []string{"cycle", "cross-origin"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			var g *GitHubAdapter
			g = adapter(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				link := g.Client.BaseURL + r.URL.String()
				if kind == "cross-origin" {
					link = "https://untrusted.invalid/rows"
				}
				w.Header().Set("Link", "<"+link+">; rel=\"next\"")
				reply(w, []int{1})
			})
			if _, e := listPages[int](context.Background(), g, "/rows"); e == nil || calls != 1 {
				t.Fatal("unsafe pagination traversed", e, calls)
			}
		})
	}
}

func TestMarkerIndexIncrementalSafetyAndRecovery(t *testing.T) {
	ledger, ctx := fixture(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	rows := map[int]githubIssue{7: {Number: 7, URL: "https://github.com/" + DefaultRepository + "/issues/7", Body: marker("one"), State: "open"}}
	scans, incrementals, candidates, posts := 0, 0, 0, 0
	partial, omitUpdate := false, false
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			t.Error("lookup posted issue")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/issues") {
			if r.URL.Query().Get("since") == "" {
				scans++
			} else {
				incrementals++
			}
			if partial {
				w.Header().Set("Link", `<https://untrusted.invalid/issues?page=2>; rel="next"`)
			}
			out := []githubIssue{}
			if !omitUpdate || r.URL.Query().Get("since") == "" {
				for _, row := range rows {
					out = append(out, row)
				}
			}
			reply(w, out)
			return
		}
		candidates++
		var n int
		fmt.Sscanf(r.URL.Path, "/repos/"+DefaultRepository+"/issues/%d", &n)
		row, ok := rows[n]
		if !ok {
			w.WriteHeader(404)
			return
		}
		reply(w, row)
	})
	g.Client.Now = func() time.Time { return now }
	g.Store = ledger.Store
	lookup := func(m string) *Issue {
		t.Helper()
		i, e := g.FindMarker(ctx, DefaultRepository, marker(m))
		if e != nil {
			t.Fatal(e)
		}
		return i
	}
	if lookup("one") == nil || scans != 1 || candidates != 1 {
		t.Fatal("cold scan", scans, candidates)
	}
	now = now.Add(time.Minute)
	if lookup("one") == nil || incrementals != 1 {
		t.Fatal("incremental lookup")
	}
	// Reopened storage retains the private index across process lifetime.
	if e := g.Store.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := store.Open(ledger.Dir + "/state.db")
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	g = &GitHubAdapter{Client: g.Client, Store: reopened}
	if lookup("one") == nil || scans != 1 {
		t.Fatal("restart rescanned history")
	}
	row := rows[7]
	row.Body = marker("two")
	rows[7] = row
	if lookup("one") != nil || lookup("two") == nil {
		t.Fatal("marker edit left stale association")
	}
	row.Body = "removed"
	rows[7] = row
	if lookup("two") != nil {
		t.Fatal("marker removal retained match")
	}
	row.Body = marker("two")
	rows[7] = row
	row.Number = 8
	row.URL = "https://github.com/" + DefaultRepository + "/issues/8"
	rows[8] = row
	if _, e := g.FindMarker(ctx, DefaultRepository, marker("two")); e == nil {
		t.Fatal("duplicate marker accepted")
	}
	delete(rows, 8)
	lookup("two")
	omitUpdate = true
	delete(rows, 7)
	before := scans
	if lookup("two") != nil || scans != before+1 {
		t.Fatal("deleted candidate did not rebuild", scans, before)
	}
	scope, e := g.Client.Scope(ctx)
	if e != nil {
		t.Fatal(e)
	}
	key := scope + ":" + DefaultRepository
	prior, e := g.loadIndex(ctx, key)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(time.Minute)
	partial = true
	if _, e = g.FindMarker(ctx, DefaultRepository, marker("absent")); e == nil {
		t.Fatal("partial scan established absence")
	}
	retained, e := g.loadIndex(ctx, key)
	if e != nil || retained.Generation != prior.Generation || !retained.Watermark.Equal(prior.Watermark) {
		t.Fatal("failed scan advanced watermark", retained, e)
	}
	partial = false
	omitUpdate = false
	now = now.Add(time.Hour)
	before = scans
	lookup("absent")
	if scans != before+1 {
		t.Fatal("hourly rebuild missing")
	}
	idx, _ := g.loadIndex(ctx, key)
	idx.Version = 99
	if e = g.Store.Put(ctx, markerIndexKind, key, idx, ""); e != nil {
		t.Fatal(e)
	}
	before = scans
	lookup("absent")
	if scans != before+1 {
		t.Fatal("unknown version trusted")
	}
	if e = g.Store.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = g.FindMarker(ctx, DefaultRepository, marker("absent")); e == nil || posts != 0 {
		t.Fatal("unreadable index authorized creation", e, posts)
	}
}

func TestMarkerIndexGenerationConflictsAndCreationCheckpoint(t *testing.T) {
	var g *GitHubAdapter
	var conflicts atomic.Int32
	g = adapter(t, func(w http.ResponseWriter, r *http.Request) {
		scope := providers.CooldownKey(g.Client.BaseURL, "fixture-token")
		var e error
		key := scope + ":" + DefaultRepository
		if conflicts.Add(1) <= 3 {
			idx, _ := g.loadIndex(context.Background(), key)
			idx.Version = 1
			idx.Generation++
			if e = g.Store.Put(context.Background(), markerIndexKind, key, idx, ""); e != nil {
				t.Error(e)
			}
		}
		reply(w, []githubIssue{})
	})
	// The callback uses the configured credential scope directly because the
	// transport holds the client dispatch lock during fixture handling.
	ctx := context.Background()
	_, e := g.FindMarker(ctx, DefaultRepository, marker("unknown"))
	if !errors.Is(e, errIndexConflict) || conflicts.Load() != 3 {
		t.Fatal("generation conflicts overwrote a newer index", e, conflicts.Load())
	}
	if _, e = g.FindMarker(ctx, DefaultRepository, marker("unknown")); e != nil {
		t.Fatal(e)
	}
	scope := providers.CooldownKey(g.Client.BaseURL, "fixture-token")
	key := scope + ":" + DefaultRepository
	old, _ := g.loadIndex(ctx, key)
	op := g.BeginOperation(ctx)
	if e = g.recordCreation(op, DefaultRepository, marker("created"), githubIssue{Number: 42}); e != nil {
		t.Fatal(e)
	}
	updated, _ := g.loadIndex(ctx, key)
	if updated.Generation != old.Generation+1 || !updated.Watermark.Equal(old.Watermark) || len(updated.Issues[42]) != 1 {
		t.Fatal("creation advanced synchronization or lost association", updated)
	}
}

func TestCrossConsumerCooldownStopsInboxAndPublication(t *testing.T) {
	for _, origin := range []string{"inbox", "report"} {
		t.Run(origin, func(t *testing.T) {
			calls := 0
			g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(429)
			})
			ctx := context.Background()
			var e error
			if origin == "inbox" {
				_, e = g.Client.User(g.Client.Operation(ctx, g.Store, false))
			} else {
				_, e = g.ReadIssue(ctx, DefaultRepository, 17)
			}
			var rate *providers.RateLimitError
			if !errors.As(e, &rate) {
				t.Fatal(e)
			}
			_, e = g.ReadIssue(ctx, DefaultRepository, 17)
			if !errors.As(e, &rate) {
				t.Fatal("report ignored shared limit", e)
			}
			_, e = g.Client.User(g.Client.Operation(ctx, g.Store, false))
			if !errors.As(e, &rate) {
				t.Fatal("inbox ignored shared limit", e)
			}
			_, e = g.Create(ctx, findings(), marker("blocked"))
			var rejected *publicationRejected
			if !errors.As(e, &rejected) || calls != 1 {
				t.Fatal("cross-consumer cooldown dispatched", e, calls)
			}
		})
	}
}

func TestMarkerIndexConcurrentWriters(t *testing.T) {
	// Two clients and database connections model daemon/publication processes.
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			entered <- struct{}{}
			<-release
		}
		reply(w, []githubIssue{})
	})
	s, ctx := fixture(t)
	g.Store = s.Store
	// Reopen the same private database rather than sharing Store's process mutex.
	otherDB, e := store.Open(s.Dir + "/state.db")
	if e != nil {
		t.Fatal(e)
	}
	defer otherDB.Close()
	otherClient := providers.NewGitHub()
	otherClient.BaseURL = g.Client.BaseURL
	otherClient.Client = g.Client.Client
	otherClient.Token = g.Client.Token
	other := &GitHubAdapter{Client: otherClient, Store: otherDB}
	done := make(chan error, 2)
	for _, client := range []*GitHubAdapter{g, other} {
		go func() { _, e := client.FindMarker(ctx, DefaultRepository, marker("absent")); done <- e }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent scans did not start")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent scan stalled")
		}
	}
	scope := providers.CooldownKey(g.Client.BaseURL, "fixture-token")
	idx, e := g.loadIndex(ctx, scope+":"+DefaultRepository)
	if e != nil || idx.Generation != 2 || !idx.Complete || (calls.Load() < 3 || calls.Load() > 6) {
		t.Fatal("concurrent generation was lost", idx, e, calls.Load())
	}
}

func TestLostCreationResponseRecoversThroughMarkerIndexWithoutReplay(t *testing.T) {
	s, ctx := fixture(t)
	engine, _ := setupEngine(s)
	engine.findings.SourceCommit = strings.Repeat("a", 40)
	engine.findings.SourcePath = "internal/app/app.go"
	engine.findings.OwnershipReason = "Implementation is in this source path"
	var posts atomic.Int32
	var created githubIssue
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			posts.Add(1)
			var body struct{ Body string }
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			created = githubIssue{Number: 31, URL: "https://github.com/" + DefaultRepository + "/issues/31", State: "open", Body: body.Body}
			c, _, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			_ = c.Close()
		case strings.HasSuffix(r.URL.Path, "/issues"):
			rows := []githubIssue{}
			if created.Number != 0 {
				rows = append(rows, created)
			}
			reply(w, rows)
		case strings.HasSuffix(r.URL.Path, "/issues/31"):
			reply(w, created)
		case strings.Contains(r.URL.Path, "/contents/"):
			reply(w, map[string]string{"type": "file"})
		default:
			reply(w, map[string]any{"full_name": DefaultRepository, "has_issues": true})
		}
	})
	g.Store = s.Store
	s.GitHub = g
	r := submit(t, s, ctx)
	step(t, s, ctx)
	step(t, s, ctx)
	if get(t, s, ctx, r.ID).Processing != "needs_attention" || posts.Load() != 1 {
		t.Fatal("lost response did not retain uncertainty")
	}
	if _, e := s.ResumeReport(ctx, ResumeInput{ID: r.ID}); e != nil {
		t.Fatal(e)
	}
	recovered := get(t, s, ctx, r.ID)
	if recovered.Processing != "complete" || recovered.Issue == nil || recovered.Issue.Number != 31 || posts.Load() != 1 {
		t.Fatal("index recovery replayed/lost creation", recovered.Processing, recovered.Issue, posts.Load())
	}
	if e := s.PublishReport(ctx, r.ID); e != nil || posts.Load() != 1 {
		t.Fatal("completed publication replayed", e, posts.Load())
	}
}

func TestAssetCacheRequiresDigestAcrossOperations(t *testing.T) {
	calls := 0
	g := adapter(t, func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, "one") })
	a := githubAsset{ID: 7, Name: "proof", State: "uploaded", Size: 3}
	ctx := g.BeginOperation(context.Background())
	for i := 0; i < 2; i++ {
		if _, e := g.releaseAsset(ctx, DefaultRepository, a); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 1 {
		t.Fatal("digestless bytes not shared within batch", calls)
	}
	if _, e := g.releaseAsset(context.Background(), DefaultRepository, a); e != nil || calls != 2 {
		t.Fatal("digestless bytes reused across operations", e, calls)
	}
	digest := sha256.Sum256([]byte("one"))
	a.Digest = "sha256:" + hex.EncodeToString(digest[:])
	if _, e := g.releaseAsset(context.Background(), DefaultRepository, a); e != nil {
		t.Fatal(e)
	}
	if _, e := g.releaseAsset(context.Background(), DefaultRepository, a); e != nil || calls != 3 {
		t.Fatal("verified immutable bytes not reused", e, calls)
	}
	a.Size = 4
	if _, e := g.releaseAsset(context.Background(), DefaultRepository, a); e == nil || calls != 4 {
		t.Fatal("changed asset metadata reused verification", e, calls)
	}
}
