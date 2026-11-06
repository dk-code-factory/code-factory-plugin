package factory

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

var errObservationBusy = errors.New("observation deferred while a conversation is busy")

type delivery struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Body    json.RawMessage `json:"body"`
	Digest  string          `json:"digest"`
	State   string          `json:"state"`
	Error   string          `json:"error,omitempty"`
	RetryAt time.Time       `json:"retryAt,omitempty"`
	At      time.Time       `json:"at"`
}

// Webhook validates GitHub's HMAC over the exact bytes, then persists the
// delivery before acknowledgement. Processing is restartable and transactional.
func (s *Service) Webhook(secret func(context.Context) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		key, err := secret(r.Context())
		if err != nil || key == "" {
			http.Error(w, "webhook is not configured", 503)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "invalid payload", 413)
			return
		}
		sig := r.Header.Get("X-Hub-Signature-256")
		provided, err := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write(body)
		if err != nil || !strings.HasPrefix(sig, "sha256=") || !hmac.Equal(provided, mac.Sum(nil)) {
			http.Error(w, "invalid signature", 401)
			return
		}
		id, kind := r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event")
		if id == "" || len(id) > 200 || kind == "" || !json.Valid(body) {
			http.Error(w, "invalid delivery", 400)
			return
		}
		d := delivery{ID: id, Type: kind, Body: body, Digest: digest(struct {
			Type string
			Body json.RawMessage
		}{kind, body}), State: "pending", At: s.Now()}
		err = s.Store.Write(r.Context(), func(t *store.Tx) error {
			var old delivery
			if e := t.Get("factory-delivery", id, &old); e == nil {
				if old.Digest != d.Digest {
					return fmt.Errorf("delivery identity collision")
				}
				return nil
			} else if e != store.ErrNotFound {
				return e
			}
			return t.Put("factory-delivery", id, d)
		})
		if err != nil {
			http.Error(w, "delivery could not be retained", 503)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
func event(item domain.Item, kind, action, facet string, value any) Event {
	// Keep one current full PR context in the batch. Each event carries its
	// own changed facet plus compact rule-matching identity, never N copies
	// of the complete source history during a polling catch-up.
	item = domain.Item{ID: item.ID, Repository: item.Repository, Number: item.Number, Kind: item.Kind,
		Title: item.Title, URL: item.URL, State: item.State, Author: item.Author, Labels: item.Labels,
		Draft: item.Draft, Head: item.Head, Branch: item.Branch, Revision: item.Revision, Updated: item.Updated,
		Assignees: item.Assignees, ReviewRequests: item.ReviewRequests, ReviewTeams: item.ReviewTeams}
	return Event{Repository: item.Repository, ItemID: item.ID, Type: kind, Action: action, Facet: facet, Value: digest(value), Details: bytes(value), Source: "poll", Context: item}
}
func sorted(v []string) []string { out := append([]string{}, v...); slices.Sort(out); return out }
func observations(item domain.Item) []Event {
	if item.Kind != "pr" {
		return nil
	}
	out := []Event{
		event(item, "pull_request", item.State, "lifecycle", item.State),
		event(item, "pull_request", "synchronize", "head", item.Head),
		event(item, "pull_request", "draft_changed", "draft", item.Draft),
		event(item, "pull_request", "labels_changed", "labels", sorted(item.Labels)),
		event(item, "pull_request", "review_requests_changed", "requests", []any{sorted(item.ReviewRequests), sorted(item.ReviewTeams)}),
		event(item, "pull_request", "edited", "content", []string{item.Title, item.Body}),
	}
	for _, a := range item.Activity {
		if a.Kind != "comment" && a.Kind != "review" && a.Kind != "review-comment" {
			continue
		}
		kind := "issue_comment"
		if strings.Contains(a.Kind, "review") {
			kind = "pull_request_review"
		}
		if a.Kind == "review-comment" {
			kind = "pull_request_review_comment"
		}
		out = append(out, event(item, kind, "observed", "activity:"+a.ID, []string{a.Body, a.Updated}))
	}
	for _, thread := range item.ReviewThreads {
		action := "unresolved"
		if thread.Resolved {
			action = "resolved"
		}
		out = append(out, event(item, "pull_request_review_thread", action, "thread:"+thread.ID, thread.Resolved))
	}
	if item.PR != nil && item.PR.Error == "" {
		out = append(out, event(item, "ci", "observed", "ci", []any{item.Head, item.PR.CI, item.PR.Checks}))
	}
	return out
}

// Poll reads saved provider observations. Initial import establishes a baseline;
// later newly discovered PRs produce observable current state, never past events.
func (s *Service) Poll(ctx context.Context) error {
	at, offset := s.Now(), 0
	for {
		ds, err := store.DueRecordsPage[delivery](ctx, s.Store, "factory-delivery", at, 64, offset)
		if err != nil {
			return err
		}
		for _, d := range ds {
			if d.State == "pending" && !s.Now().Before(d.RetryAt) {
				if err = s.processDelivery(ctx, d); err != nil {
					if errors.Is(err, errObservationBusy) {
						offset++
						continue
					}
					d.Error = err.Error()
					d.RetryAt = s.Now().Add(time.Minute)
					if e := s.Store.Put(ctx, "factory-delivery", d.ID, d, ""); e != nil {
						return e
					}
				}
			}
		}
		if len(ds) < 64 {
			break
		}
	}
	items, err := store.ListWithout[domain.Item](ctx, s.Store, "item", "$.body", "$.activity", "$.checks", "$.pr.checks")
	if err != nil {
		return err
	}
	c, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	for _, r := range c.Repositories {
		var repo domain.Repository
		if err = s.Store.Get(ctx, "repository", r.ID, &repo); err != nil {
			return err
		}
		if !repo.Imported {
			continue
		}
		if r.Paused {
			continue
		}
		var mark struct {
			Ready  bool     `json:"ready"`
			Items  []string `json:"items"`
			Cursor int      `json:"cursor"`
		}
		err = s.Store.Get(ctx, "factory-baseline", r.ID, &mark)
		if err != nil && err != store.ErrNotFound {
			return err
		}
		if err == store.ErrNotFound {
			for _, item := range items {
				if item.Repository == r.ID && item.Kind == "pr" {
					mark.Items = append(mark.Items, item.ID)
				}
			}
			if err = s.Store.Put(ctx, "factory-baseline", r.ID, mark, ""); err != nil {
				return err
			}
		}
		if !mark.Ready && s.App.GitHub != nil {
			for n := 0; mark.Cursor < len(mark.Items) && n < 8; n++ {
				id := mark.Items[mark.Cursor]
				release, ok := s.tryLockSession(id)
				if !ok {
					break
				}
				err = func() error {
					defer release()
					var item domain.Item
					if e := s.Store.Get(ctx, "item", id, &item); e != nil {
						return e
					}
					fresh, e := s.App.GitHub.ItemDetail(ctx, item)
					if e != nil {
						return e
					}
					if e = s.App.ImportObservations(ctx, repo, []domain.Item{fresh}); e != nil {
						return e
					}
					return s.Store.Write(ctx, func(t *store.Tx) error {
						var saved domain.Item
						if e := t.Get("item", id, &saved); e != nil {
							return e
						}
						o := s.observationWriter(t, c)
						for _, event := range observations(saved) {
							if e := o.observe(event, true); e != nil {
								return e
							}
						}
						if e := o.flush(); e != nil {
							return e
						}
						return t.Put("factory-item-observed", id, map[string]string{"revision": saved.Revision})
					})
				}()
				if err != nil {
					return err
				}
				mark.Cursor++
				if err = s.Store.Put(ctx, "factory-baseline", r.ID, mark, ""); err != nil {
					return err
				}
			}
			items, err = store.ListWithout[domain.Item](ctx, s.Store, "item", "$.body", "$.activity", "$.checks", "$.pr.checks")
			if err != nil {
				return err
			}
		}
		// Compact revision checks avoid even acquiring locks for unchanged rows.
		seenRows, e := s.Store.Records(ctx, "factory-item-observed")
		if e != nil {
			return e
		}
		for _, item := range items {
			if err = ctx.Err(); err != nil {
				return err
			}
			if item.Repository != r.ID || item.Kind != "pr" {
				continue
			}
			if !mark.Ready && s.App.GitHub != nil && contains(mark.Items[mark.Cursor:], item.ID) {
				continue
			}
			var seen struct{ Revision string }
			if raw, ok := seenRows[item.ID]; ok {
				if err = json.Unmarshal(raw, &seen); err != nil {
					return err
				}
				if seen.Revision == item.Revision {
					continue
				}
			}
			release, ok := s.tryLockSession(item.ID)
			if !ok {
				continue
			}
			err = func() error {
				defer release()
				currentSettings, e := s.Settings(ctx)
				if e != nil {
					return e
				}
				return s.Store.Write(ctx, func(t *store.Tx) error {
					var latest domain.Item
					if e := t.Get("item", item.ID, &latest); e != nil {
						return e
					}
					e := t.Get("factory-item-observed", item.ID, &seen)
					if e != nil && e != store.ErrNotFound {
						return e
					}
					if e == nil && seen.Revision == latest.Revision {
						return nil
					}
					o := s.observationWriter(t, currentSettings)
					for _, event := range observations(latest) {
						if e := o.observe(event, !mark.Ready && contains(mark.Items, item.ID)); e != nil {
							return e
						}
					}
					if e := o.flush(); e != nil {
						return e
					}
					return t.Put("factory-item-observed", item.ID, map[string]string{"revision": latest.Revision})
				})
			}()
			if err != nil {
				return err
			}
		}
		if !mark.Ready && (s.App.GitHub == nil || mark.Cursor >= len(mark.Items)) {
			mark.Ready = true
			for _, id := range mark.Items {
				var observed struct{ Revision string }
				if e := s.Store.Get(ctx, "factory-item-observed", id, &observed); e == store.ErrNotFound {
					mark.Ready = false
					break
				} else if e != nil {
					return e
				}
			}
		}
		if err = s.Store.Put(ctx, "factory-baseline", r.ID, mark, ""); err != nil {
			return err
		}
	}
	return s.refreshDetails(ctx, c)
}

// Reconcile detailed observable changes even when nobody is viewing a PR.
// Freshness and the existing request budget bound provider traffic.
func (s *Service) refreshDetails(ctx context.Context, c Settings) error {
	if s.App.GitHub == nil {
		return nil
	}
	items, err := store.ListWithout[domain.Item](ctx, s.Store, "item", "$.activity", "$.body", "$.checks", "$.pr.checks")
	if err != nil {
		return err
	}
	var cursor struct {
		After string `json:"after"`
	}
	if e := s.Store.Get(ctx, "factory-refresh", "details", &cursor); e != nil && e != store.ErrNotFound {
		return e
	}
	start := 0
	for i, item := range items {
		if item.ID > cursor.After {
			start = i
			break
		}
	}
	var firstErr error
	for n := 0; n < min(8, len(items)); n++ {
		item := items[(start+n)%len(items)]
		cursor.After = item.ID
		if err = s.Store.Put(ctx, "factory-refresh", "details", cursor, ""); err != nil {
			return err
		}
		r, ok := repository(c, item.Repository)
		if !ok || r.Paused || s.Now().Sub(item.DetailSyncedAt) < 30*time.Second {
			continue
		}
		var baseline struct{ Ready bool }
		if e := s.Store.Get(ctx, "factory-baseline", r.ID, &baseline); e != nil && e != store.ErrNotFound {
			return e
		}
		if !baseline.Ready {
			continue
		}
		started := s.Now()
		fresh, e := s.App.GitHub.ItemDetail(ctx, item)
		if e == nil {
			e = s.importDetail(ctx, r, fresh, started)
		}
		if e != nil && firstErr == nil {
			firstErr = fmt.Errorf("reconcile %s: %w", item.ID, e)
		}
	}
	return firstErr
}

// Import and route only a detail snapshot that is at least as recent as the
// saved observation. The final transaction also rejects concurrent replacements.
func (s *Service) importDetail(ctx context.Context, repo Repository, fresh domain.Item, started time.Time) error {
	release, ok := s.tryLockSession(fresh.ID)
	if !ok {
		return nil
	}
	defer release()
	if fresh.ReviewObservedAt.IsZero() {
		fresh.ReviewObservedAt = fresh.DetailSyncedAt
		if fresh.ReviewObservedAt.IsZero() {
			fresh.ReviewObservedAt = started
		}
	}
	var savedRepo domain.Repository
	if err := s.Store.Get(ctx, "repository", repo.ID, &savedRepo); err != nil {
		return err
	}
	if err := s.App.ImportObservations(ctx, savedRepo, []domain.Item{fresh}); err != nil {
		return err
	}
	var saved domain.Item
	if err := s.Store.Get(ctx, "item", fresh.ID, &saved); err != nil {
		return err
	}
	if saved.ReviewObservedAt.After(fresh.ReviewObservedAt) {
		return nil
	}
	removed, err := s.deletedActivities(ctx, saved, started)
	if err != nil {
		return err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	return s.Store.Write(ctx, func(t *store.Tx) error {
		var latest domain.Item
		if err := t.Get("item", saved.ID, &latest); err != nil {
			return err
		}
		if latest.Revision != saved.Revision || !latest.ReviewObservedAt.Equal(saved.ReviewObservedAt) {
			return nil
		}
		o := s.observationWriter(t, settings)
		for _, e := range append(observations(latest), removed...) {
			if err := o.observe(e, false); err != nil {
				return err
			}
		}
		return o.flush()
	})
}

type githubPR struct {
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	URL     string    `json:"html_url"`
	State   string    `json:"state"`
	Merged  bool      `json:"merged"`
	Draft   bool      `json:"draft"`
	Updated time.Time `json:"updated_at"`
	Head    struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Reviewers []struct {
		Login string `json:"login"`
	} `json:"requested_reviewers"`
	Teams []struct {
		Slug string `json:"slug"`
	} `json:"requested_teams"`
}

func (p githubPR) item(repo string) domain.Item {
	i := domain.Item{ID: repo + "#" + strconv.Itoa(p.Number), Repository: repo, Number: p.Number, Kind: "pr", Title: p.Title, Body: p.Body, URL: p.URL, State: p.State, Author: p.User.Login, Draft: p.Draft, Head: p.Head.SHA, Branch: p.Head.Ref, Updated: p.Updated}
	if p.Merged {
		i.State = "merged"
	}
	for _, l := range p.Labels {
		i.Labels = append(i.Labels, l.Name)
	}
	for _, r := range p.Reviewers {
		i.ReviewRequests = append(i.ReviewRequests, r.Login)
	}
	for _, t := range p.Teams {
		i.ReviewTeams = append(i.ReviewTeams, strings.SplitN(repo, "/", 2)[0]+"/"+t.Slug)
	}
	i.Revision = i.ContentRevision()
	return i
}
func (s *Service) processDelivery(ctx context.Context, d delivery) error {
	var p struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		PR    githubPR `json:"pull_request"`
		Issue struct {
			Number int              `json:"number"`
			PR     *json.RawMessage `json:"pull_request"`
		} `json:"issue"`
		Comment struct {
			ID      int64  `json:"id"`
			Body    string `json:"body"`
			Updated string `json:"updated_at"`
		} `json:"comment"`
		Review struct {
			ID        int64  `json:"id"`
			Body      string `json:"body"`
			Submitted string `json:"submitted_at"`
			State     string `json:"state"`
		} `json:"review"`
		Thread struct {
			ID       int64  `json:"id"`
			NodeID   string `json:"node_id"`
			Resolved bool   `json:"is_resolved"`
		} `json:"thread"`
		CheckRun struct {
			Head string `json:"head_sha"`
			PRs  []struct {
				Number int `json:"number"`
			} `json:"pull_requests"`
		} `json:"check_run"`
		CheckSuite struct {
			Head string `json:"head_sha"`
			PRs  []struct {
				Number int `json:"number"`
			} `json:"pull_requests"`
		} `json:"check_suite"`
		Workflow struct {
			Head string `json:"head_sha"`
			PRs  []struct {
				Number int `json:"number"`
			} `json:"pull_requests"`
		} `json:"workflow_run"`
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(d.Body, &p); err != nil {
		return err
	}
	c, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	r, configured := repository(c, p.Repository.FullName)
	if !configured || d.Type == "ping" {
		d.State = "processed"
		return s.Store.Put(ctx, "factory-delivery", d.ID, d, "")
	}
	items := []domain.Item{}
	if p.PR.Number > 0 {
		items = append(items, p.PR.item(r.ID))
	} else if p.Issue.PR != nil && p.Issue.Number > 0 {
		var i domain.Item
		if err = s.Store.Get(ctx, "item", r.ID+"#"+strconv.Itoa(p.Issue.Number), &i); err != nil {
			i, err = s.App.GitHub.PullRequest(providers.WithGitHubRead(ctx, providers.RequireLive), r.ID, p.Issue.Number)
			if err != nil {
				return err
			}
		}
		items = append(items, i)
	} else if d.Type == "check_run" || d.Type == "check_suite" || d.Type == "workflow_run" || d.Type == "status" {
		head := p.SHA
		switch d.Type {
		case "check_run":
			head = p.CheckRun.Head
		case "check_suite":
			head = p.CheckSuite.Head
		case "workflow_run":
			head = p.Workflow.Head
		}
		items, err = s.Store.PullRequestHeads(ctx, r.ID, head)
		if err != nil {
			return err
		}
		// Associate CI to every explicit PR, including PRs not imported yet.
		numbers := []int{}
		for _, v := range p.CheckRun.PRs {
			numbers = append(numbers, v.Number)
		}
		for _, v := range p.CheckSuite.PRs {
			numbers = append(numbers, v.Number)
		}
		for _, v := range p.Workflow.PRs {
			numbers = append(numbers, v.Number)
		}
		for _, n := range numbers {
			if n < 1 {
				continue
			}
			found := false
			for _, i := range items {
				found = found || i.Number == n
			}
			if !found {
				items = append(items, domain.Item{Number: n})
			}
		}
		candidates := items
		items = []domain.Item{}
		for _, i := range candidates {
			fresh, e := s.App.GitHub.PullRequest(providers.WithGitHubRead(ctx, providers.RequireLive), r.ID, i.Number)
			if e != nil {
				return e
			}
			// Saved head metadata can change while provider reads are in flight.
			if head != "" && fresh.Head == head {
				items = append(items, fresh)
			}
		}
	}
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	release, available := s.tryLockSessions(ids...)
	if !available {
		return errObservationBusy
	}
	defer release()
	c, err = s.Settings(ctx)
	if err != nil {
		return err
	}
	return s.Store.Write(ctx, func(t *store.Tx) error {
		var current delivery
		if e := t.Get("factory-delivery", d.ID, &current); e != nil {
			return e
		}
		if current.State == "processed" {
			return nil
		}
		o := s.observationWriter(t, c)
		for _, i := range items {
			var old domain.Item
			if e := t.Get("item", i.ID, &old); e == nil {
				if i.Activity == nil || d.Type == "check_run" || d.Type == "check_suite" || d.Type == "workflow_run" || d.Type == "status" {
					i.Activity = old.Activity
				}
				if i.PR == nil && i.Head == old.Head {
					i.PR = old.PR
				}
				if old.Updated.After(i.Updated) {
					i = old
				}
			}
			if i.ReviewThreads == nil {
				i.ReviewThreads = old.ReviewThreads
			}
			staleActivity := false
			if d.Type == "issue_comment" || d.Type == "pull_request_review_comment" || d.Type == "pull_request_review" {
				kind := "comment"
				id := p.Comment.ID
				body, updated := p.Comment.Body, p.Comment.Updated
				if d.Type == "pull_request_review_comment" {
					kind = "review-comment"
				}
				if d.Type == "pull_request_review" {
					kind = "review"
					id = p.Review.ID
					body = p.Review.Body + strings.ToLower(p.Review.State)
					updated = p.Review.Submitted
				}
				aid := kind + ":" + strconv.FormatInt(id, 10)
				kept := []domain.Activity{}
				for _, a := range i.Activity {
					if a.ID != aid {
						kept = append(kept, a)
					} else if newerActivity(a.Updated, updated) {
						// The enclosing PR timestamp does not version its comments.
						// Preserve a later version and its observation fingerprint.
						kept = append(kept, a)
						staleActivity = true
					}
				}
				if !staleActivity && p.Action != "deleted" {
					kept = append(kept, domain.Activity{ID: aid, Kind: kind, Body: body, Updated: updated})
				}
				i.Activity = kept
			}
			if d.Type == "pull_request_review_thread" {
				// GitHub's thread webhook omits is_resolved; its action is authoritative.
				if p.Action == "resolved" {
					p.Thread.Resolved = true
				}
				if p.Action == "unresolved" {
					p.Thread.Resolved = false
				}
				id := p.Thread.NodeID
				if id == "" {
					id = strconv.FormatInt(p.Thread.ID, 10)
				}
				kept := []domain.ReviewThread{}
				for _, thread := range i.ReviewThreads {
					if thread.ID != id {
						kept = append(kept, thread)
					}
				}
				i.ReviewThreads = append(kept, domain.ReviewThread{ID: id, Resolved: p.Thread.Resolved})
			}
			i.ReviewObservedAt = d.At
			i.Revision = i.ContentRevision()
			if e := t.Put("item", i.ID, i); e != nil {
				return e
			}
			if old.ID == "" || old.Revision != i.Revision {
				if e := t.Event("item.changed", i.ID, map[string]string{"id": i.ID}); e != nil {
					return e
				}
			}
			var user domain.UserState
			if e := t.Get("user", i.ID, &user); e == store.ErrNotFound {
				user = domain.UserState{ID: i.ID, New: true, Actions: []domain.Action{}}
				if e = t.Put("user", i.ID, user); e != nil {
					return e
				}
			} else if e != nil {
				return e
			}
			es := []Event{}
			switch d.Type {
			case "pull_request":
				facet := ""
				switch p.Action {
				case "opened", "closed", "reopened":
					facet = "lifecycle"
				case "synchronize":
					facet = "head"
				case "converted_to_draft", "ready_for_review":
					facet = "draft"
				case "labeled", "unlabeled":
					facet = "labels"
				case "review_requested", "review_request_removed":
					facet = "requests"
				case "edited":
					facet = "content"
				}
				for _, e := range observations(i) {
					if e.Facet == facet {
						e.Action = p.Action
						es = append(es, e)
					}
				}
				if facet == "" {
					es = append(es, event(i, d.Type, p.Action, "pr:"+p.Action, d.ID))
				}
			case "issue_comment", "pull_request_review_comment":
				prefix := "comment:"
				if d.Type == "pull_request_review_comment" {
					prefix = "review-comment:"
				}
				value := any([]string{p.Comment.Body, p.Comment.Updated})
				if p.Action == "deleted" {
					value = map[string]bool{"deleted": true}
				}
				es = append(es, event(i, d.Type, p.Action, "activity:"+prefix+strconv.FormatInt(p.Comment.ID, 10), value))
			case "pull_request_review":
				es = append(es, event(i, d.Type, p.Action, "activity:review:"+strconv.FormatInt(p.Review.ID, 10), []string{p.Review.Body + strings.ToLower(p.Review.State), p.Review.Submitted}))
			case "pull_request_review_thread":
				id := p.Thread.NodeID
				if id == "" {
					id = strconv.FormatInt(p.Thread.ID, 10)
				}
				es = append(es, event(i, d.Type, p.Action, "thread:"+id, p.Thread.Resolved))
			case "check_run", "check_suite", "workflow_run", "status":
				if i.PR != nil {
					es = append(es, event(i, "ci", "observed", "ci", []any{i.Head, i.PR.CI, i.PR.Checks}))
				}
			}
			for _, e := range es {
				if staleActivity {
					continue
				}
				e.Source = "webhook"
				e.Delivery = d.ID
				if err := o.observe(e, false); err != nil {
					return err
				}
			}
		}
		if err := o.flush(); err != nil {
			return err
		}
		d.State = "processed"
		d.Error = ""
		return t.Put("factory-delivery", d.ID, d)
	})
}

func newerActivity(retained, incoming string) bool {
	old, err := time.Parse(time.RFC3339Nano, retained)
	if err != nil {
		return false
	}
	next, err := time.Parse(time.RFC3339Nano, incoming)
	return err != nil || old.After(next)
}

// Missing previously observed comments are recoverable only after a successful
// complete detail read. Newer webhook observations cannot be erased by that read.
func (s *Service) deletedActivities(ctx context.Context, item domain.Item, started time.Time) ([]Event, error) {
	if item.Kind != "pr" || item.PR != nil && item.PR.Error != "" {
		return nil, nil
	}
	var rows []struct {
		Type       string    `json:"type"`
		Facet      string    `json:"facet"`
		Deleted    bool      `json:"deleted"`
		ObservedAt time.Time `json:"observedAt"`
	}
	var err error
	rows, err = store.Select[struct {
		Type       string    `json:"type"`
		Facet      string    `json:"facet"`
		Deleted    bool      `json:"deleted"`
		ObservedAt time.Time `json:"observedAt"`
	}](ctx, s.Store, "factory-observation", "itemId", item.ID)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, a := range item.Activity {
		present["activity:"+a.ID] = true
	}
	out := []Event{}
	for _, row := range rows {
		if (row.Type == "issue_comment" || row.Type == "pull_request_review_comment") && !row.Deleted && !row.ObservedAt.After(started) && !present[row.Facet] {
			out = append(out, event(item, row.Type, "deleted", row.Facet, map[string]bool{"deleted": true}))
		}
	}
	return out, nil
}
