package reports

import (
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const markerIndexKind = "report-github-marker-index"
const markerIndexVersion = 1

var errIndexConflict = errors.New("Report marker index changed concurrently; resume reconciliation")
var reportMarkerPattern = regexp.MustCompile(`<!-- code-factory-report:[^\s<>]+ -->`)

type markerIndex struct {
	Version    int              `json:"version"`
	Generation uint64           `json:"generation"`
	Complete   bool             `json:"complete"`
	Watermark  time.Time        `json:"watermark"`
	CompleteAt time.Time        `json:"complete_at"`
	Issues     map[int][]string `json:"issues"`
}

func (g *GitHubAdapter) indexTime() time.Time {
	if g.Client.Now != nil {
		return g.Client.Now().UTC()
	}
	return time.Now().UTC()
}
func (g *GitHubAdapter) loadIndex(ctx context.Context, key string) (markerIndex, error) {
	var idx markerIndex
	if g.Store == nil {
		return idx, errors.New("Report marker index storage unavailable; restore private storage and resume")
	}
	var raw json.RawMessage
	e := g.Store.Get(ctx, markerIndexKind, key, &raw)
	if errors.Is(e, store.ErrNotFound) {
		return markerIndex{}, nil
	}
	if e == nil {
		var header struct {
			Version    int    `json:"version"`
			Generation uint64 `json:"generation"`
		}
		e = json.Unmarshal(raw, &header)
		if e == nil && header.Version != markerIndexVersion {
			return markerIndex{Version: header.Version, Generation: header.Generation}, nil
		}
		if e == nil {
			e = json.Unmarshal(raw, &idx)
		}
	}
	if e != nil {
		return idx, errors.New("Report marker index unreadable; restore private storage and resume")
	}
	return idx, nil
}
func (g *GitHubAdapter) commitIndex(ctx context.Context, key string, old uint64, idx markerIndex) error {
	var raw json.RawMessage
	e := g.Store.Get(ctx, markerIndexKind, key, &raw)
	if e != nil && !errors.Is(e, store.ErrNotFound) {
		return e
	}
	var current struct {
		Generation uint64 `json:"generation"`
	}
	if raw != nil {
		if e = json.Unmarshal(raw, &current); e != nil {
			return e
		}
	}
	if current.Generation != old {
		return errIndexConflict
	}
	idx.Generation = old + 1
	swapped, e := g.Store.CompareAndSwap(ctx, markerIndexKind, key, raw, idx)
	if e != nil {
		if strings.Contains(e.Error(), "SQLITE_BUSY") {
			return errIndexConflict
		}
		return e
	}
	if !swapped {
		return errIndexConflict
	}
	return nil
}

func (g *GitHubAdapter) refreshIndex(ctx context.Context, repo, key string, rebuild bool) (markerIndex, error) {
	for attempt := 0; attempt < 3; attempt++ {
		idx, e := g.loadIndex(ctx, key)
		if e != nil {
			return idx, e
		}
		generation := idx.Generation
		started := g.indexTime()
		full := rebuild || idx.Version != markerIndexVersion || !idx.Complete || idx.Watermark.IsZero() || started.Sub(idx.CompleteAt) >= time.Hour
		endpoint := "/repos/" + repo + "/issues?state=all&sort=updated&direction=asc"
		if full {
			endpoint = "/repos/" + repo + "/issues?state=all&sort=created&direction=desc"
		}
		if !full {
			endpoint += "&since=" + url.QueryEscape(idx.Watermark.Add(-2*time.Minute).Truncate(2*time.Minute).Format(time.RFC3339))
		}
		// A retry/rebuild must dispatch again, even within one publication operation.
		rows, e := listPages[githubIssue](providers.Revalidate(ctx), g, endpoint)
		if e != nil {
			return idx, e
		}
		if full {
			idx = markerIndex{Generation: generation, Issues: map[int][]string{}}
		}
		if idx.Issues == nil {
			idx.Issues = map[int][]string{}
		}
		for _, row := range rows {
			if row.Number < 1 {
				return idx, errors.New("Report marker history contains an invalid issue identity")
			}
			delete(idx.Issues, row.Number)
			if markers := reportMarkerPattern.FindAllString(row.Body, -1); len(markers) > 0 {
				idx.Issues[row.Number] = markers
			}
		}
		idx.Version = markerIndexVersion
		idx.Complete = true
		idx.Watermark = started
		if full {
			idx.CompleteAt = started
		}
		e = g.commitIndex(ctx, key, generation, idx)
		if errors.Is(e, errIndexConflict) {
			continue
		}
		return idx, e
	}
	return markerIndex{}, errIndexConflict
}
func (g *GitHubAdapter) FindMarker(ctx context.Context, repo, marker string) (*Issue, error) {
	if !allowedRepo(repo) || reportMarkerPattern.FindString(marker) != marker || marker == "" {
		return nil, fmt.Errorf("Invalid marker repository or marker")
	}
	ctx = g.operation(ctx)
	scope, e := g.Client.Scope(ctx)
	if e != nil {
		return nil, e
	}
	key := scope + ":" + repo
	for rebuild := 0; rebuild < 2; rebuild++ {
		idx, e := g.refreshIndex(ctx, repo, key, rebuild == 1)
		if e != nil {
			return nil, e
		}
		var found *Issue
		inconsistent := false
		for number, markers := range idx.Issues {
			match := false
			for _, m := range markers {
				if m == marker {
					match = true
					break
				}
			}
			if !match {
				continue
			}
			var current githubIssue
			e = g.get(providers.Revalidate(ctx), fmt.Sprintf("/repos/%s/issues/%d", repo, number), &current)
			if e != nil {
				var outcome *providers.RequestError
				if errors.As(e, &outcome) && outcome.Status == http.StatusNotFound {
					inconsistent = true
					continue
				}
				return nil, e
			}
			issue, e := current.issue(repo)
			if e != nil || current.Number != number || !containsMarker(current.Body, marker) {
				inconsistent = true
				continue
			}
			if found != nil {
				return nil, errors.New("Multiple issues contain the report marker; reconcile manually")
			}
			found = &issue
		}
		if !inconsistent {
			return found, nil
		}
	}
	return nil, errors.New("Report marker candidates remain inconsistent; resume reconciliation")
}
func containsMarker(body, marker string) bool {
	for _, m := range reportMarkerPattern.FindAllString(body, -1) {
		if m == marker {
			return true
		}
	}
	return false
}
func (g *GitHubAdapter) recordCreation(ctx context.Context, repo, marker string, issue githubIssue) error {
	scope, e := g.Client.Scope(ctx)
	if e != nil {
		return e
	}
	key := scope + ":" + repo
	for attempt := 0; attempt < 3; attempt++ {
		idx, e := g.loadIndex(ctx, key)
		if e != nil {
			return e
		}
		old := idx.Generation
		if idx.Version != markerIndexVersion {
			idx = markerIndex{Version: markerIndexVersion, Generation: old}
		}
		if idx.Issues == nil {
			idx.Issues = map[int][]string{}
		}
		idx.Issues[issue.Number] = []string{marker}
		if e = g.commitIndex(ctx, key, old, idx); errors.Is(e, errIndexConflict) {
			continue
		}
		return e
	}
	return errIndexConflict
}
