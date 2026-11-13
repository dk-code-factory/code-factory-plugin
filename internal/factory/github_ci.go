package factory

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
)

type ciRefreshContext struct{}
type ciRefreshKey struct {
	Scope, Repository, Head string
	Number                  int
}
type ciRefreshVersion struct {
	Revision string
	Observed time.Time
}
type ciRefreshSnapshot struct {
	Body    []byte
	Started time.Time
	Saved   ciRefreshVersion
	Applied bool
}
type ciRefreshBatch struct {
	Snapshots map[ciRefreshKey]*ciRefreshSnapshot
	Bytes     int
}
type ciRefreshRead struct {
	Snapshot *ciRefreshSnapshot
	Reused   bool
}

func isCIDelivery(kind string) bool {
	return kind == "check_run" || kind == "check_suite" || kind == "workflow_run" || kind == "status"
}

func ciVersion(i domain.Item) ciRefreshVersion {
	return ciRefreshVersion{Revision: i.Revision, Observed: i.ReviewObservedAt}
}

// A batch belongs to one Poll invocation, never to a later provider observation.
// Only deliveries received strictly before a read started can share that read.
func (s *Service) refreshCI(ctx context.Context, d delivery, repo string, number int, head string) (domain.Item, ciRefreshRead, error) {
	// Resolve one operation's credentials for both the snapshot identity and
	// its live request; a second token lookup could label another user's read.
	ctx, err := providers.PinGitHubRead(ctx, s.App.GitHub, s.Store)
	if err != nil {
		return domain.Item{}, ciRefreshRead{}, err
	}
	batch, _ := ctx.Value(ciRefreshContext{}).(*ciRefreshBatch)
	var key ciRefreshKey
	if batch != nil && head != "" {
		scope, err := s.App.GitHub.CacheScope(ctx)
		if err != nil {
			return domain.Item{}, ciRefreshRead{}, err
		}
		key = ciRefreshKey{Scope: scope, Repository: repo, Head: head, Number: number}
		if cached := batch.Snapshots[key]; cached != nil && cached.Applied && !d.At.IsZero() && d.At.Before(cached.Started) {
			var saved domain.Item
			err := s.Store.Get(ctx, "item", repo+"#"+strconv.Itoa(number), &saved)
			if err != nil && err != store.ErrNotFound {
				return domain.Item{}, ciRefreshRead{}, err
			}
			if err == nil && saved.Head == head && ciVersion(saved) == cached.Saved {
				var item domain.Item
				if err := json.Unmarshal(cached.Body, &item); err != nil {
					return domain.Item{}, ciRefreshRead{}, err
				}
				return item, ciRefreshRead{Snapshot: cached, Reused: true}, nil
			}
		}
	}
	started := s.Now()
	item, err := s.App.GitHub.PullRequest(providers.WithGitHubRead(ctx, providers.RequireLive), repo, number)
	if err != nil || batch == nil || head == "" || item.Head != head || item.PR == nil || item.PR.Error != "" {
		return item, ciRefreshRead{}, err
	}
	body, err := json.Marshal(item)
	if err != nil {
		return domain.Item{}, ciRefreshRead{}, err
	}
	// Retained snapshots are private to this pass and bounded independently of
	// the provider caches. Oversized or high-cardinality batches simply read live.
	if old := batch.Snapshots[key]; old != nil {
		batch.Bytes -= len(old.Body)
		delete(batch.Snapshots, key)
	}
	if len(batch.Snapshots) >= 128 || len(body)+batch.Bytes > 8<<20 {
		return item, ciRefreshRead{}, nil
	}
	snapshot := &ciRefreshSnapshot{Body: body, Started: started}
	batch.Snapshots[key] = snapshot
	batch.Bytes += len(body)
	return item, ciRefreshRead{Snapshot: snapshot}, nil
}
