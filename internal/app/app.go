package app

import (
	"codefactory/internal/domain"
	"codefactory/internal/providers"
	"codefactory/internal/prreview"
	"codefactory/internal/reports"
	"codefactory/internal/store"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type App struct {
	ReviewEngine    prreview.EngineAdapter
	ReviewCollector prreview.Collector
	ReviewWorkers   prreview.Workers
	ReviewRemote    prreview.PublicationRemote
	// SavedOnly disables observation scheduling and provider status probes for
	// projection builders and UI reads. Background workers use the live App.
	SavedOnly        bool
	refreshMu        sync.Mutex
	refreshing       map[string]bool
	refreshAgain     map[string]bool
	autoReviewMu     sync.Mutex
	providerMu       sync.Mutex
	prRefreshAfter   string
	observationAfter string
	Store            *store.Store
	Vault            *store.Vault
	GitHub           providers.GitHubSource
	Runs             *Manager
	DataDir          string
	Reports          *reports.Service
}

func ID() string {
	var b [16]byte
	_, e := rand.Read(b[:])
	if e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func (a *App) Init(ctx context.Context) error {
	if a.Reports == nil {
		a.Reports = &reports.Service{Store: a.Store, Vault: a.Vault, Dir: a.DataDir, Engine: &reports.NativeEngine{Dir: a.DataDir}, GitHub: &reports.GitHubAdapter{Client: providers.LiveGitHub(a.GitHub), Store: a.Store}}
		if a.Runs != nil {
			a.Reports.Engine = &reports.NativeEngine{Dir: a.DataDir, Codex: a.Runs.Binary}
		}
	}
	var c domain.Config
	if e := a.Store.Get(ctx, "config", "settings", &c); errors.Is(e, store.ErrNotFound) {
		if e = a.Store.Put(ctx, "config", "settings", domain.Defaults(), ""); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	for _, p := range domain.Profiles() {
		var old domain.Profile
		if e := a.Store.Get(ctx, "profile", p.ID, &old); errors.Is(e, store.ErrNotFound) {
			if e = a.Store.Put(ctx, "profile", p.ID, p, ""); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
	}
	return nil
}
func (a *App) Config(ctx context.Context) (domain.Config, error) {
	var c domain.Config
	e := a.Store.Get(ctx, "config", "settings", &c)
	return c, e
}
func (a *App) SaveConfig(ctx context.Context, c domain.Config) error {
	if err := prreview.ValidateReviewers(c.Reviewers); err != nil {
		return err
	}
	if c.GitHubRequestsPerHour != 0 && (c.GitHubRequestsPerHour < 60 || c.GitHubRequestsPerHour > 5000) {
		return fmt.Errorf("GitHub budget must be 60–5000 requests per hour")
	}
	if c.GitHubReserve != nil && (*c.GitHubReserve < 0 || *c.GitHubReserve > 5000) {
		return fmt.Errorf("GitHub reserve must be 0–5000")
	}
	if c.GitHubPollSeconds != 0 && (c.GitHubPollSeconds < 60 || c.GitHubPollSeconds > 3600) {
		return fmt.Errorf("GitHub polling must be 60–3600 seconds")
	}

	for _, id := range []string{c.ReviewProfileID, c.ImplementationProfileID} {
		if id != "" {
			var p domain.Profile
			if err := a.Store.Get(ctx, "profile", id, &p); err != nil {
				return fmt.Errorf("default pipeline is unavailable")
			}
		}
	}
	if c.PluginCheckMinutes < 0 || c.PluginCheckMinutes > 10080 || c.PluginCheckMinutes > 0 && c.PluginCheckMinutes < 15 {
		return fmt.Errorf("plugin update checks must be manual (0) or 15–10080 minutes")
	}
	if c.JiraURL != "" && !providers.ValidJiraURL(c.JiraURL) {
		return fmt.Errorf("Jira URL must be an HTTPS atlassian.net tenant")
	}
	for _, p := range c.PriorityLabels {
		if p < 0 || p > 3 {
			return fmt.Errorf("priority must be P0–P3")
		}
	}
	if c.AssessmentPrompt == "" {
		return fmt.Errorf("assessment prompt required")
	}
	c.ID = "settings"
	old, err := a.Config(ctx)
	if err != nil {
		return err
	}
	c.ReviewersConfigured = c.ReviewersConfigured || old.ReviewersConfigured || c.Reviewers != nil
	c.ReviewRulesConfigured = c.ReviewRulesConfigured || old.ReviewRulesConfigured || c.ReviewRules != ""
	c.PolicyVersion = old.PolicyVersion
	if old.AssessmentPrompt != c.AssessmentPrompt || string(raw(old.PriorityLabels)) != string(raw(c.PriorityLabels)) {
		c.PolicyVersion = ID()
	}
	return a.Store.Put(ctx, "config", c.ID, c, "config.changed")
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func (a *App) SaveRepository(ctx context.Context, r domain.Repository) error {
	if r.Reviewers != nil {
		if err := prreview.ValidateReviewers(*r.Reviewers); err != nil {
			return err
		}
	}
	if !repoPattern.MatchString(r.ID) || strings.Contains(r.ID, "..") || strings.HasPrefix(r.Base, "-") {
		return fmt.Errorf("invalid repository or base")
	}
	for _, id := range []string{r.ReviewProfileID, r.ImplementationProfileID} {
		if id != "" {
			var p domain.Profile
			if err := a.Store.Get(ctx, "profile", id, &p); err != nil {
				return fmt.Errorf("repository pipeline is unavailable")
			}
		}
	}
	var old domain.Repository
	if e := a.Store.Get(ctx, "repository", r.ID, &old); e == nil {
		old.Checkout = r.Checkout
		old.Base = r.Base
		old.Paused = r.Paused
		old.AutoReview = r.AutoReview
		old.AutoPostReview = r.AutoPostReview
		old.Reviewers = r.Reviewers
		old.ReviewRules = r.ReviewRules
		old.ReviewPrompt = r.ReviewPrompt
		old.ReviewError = ""
		old.ReviewProfileID = r.ReviewProfileID
		old.ImplementationProfileID = r.ImplementationProfileID
		r = old
	} else if !errors.Is(e, store.ErrNotFound) {
		return e
	}
	return a.Store.Put(ctx, "repository", r.ID, r, "repository.changed")
}
func (a *App) SaveProfile(ctx context.Context, p domain.Profile) error {
	if p.ID == "" {
		p.ID = ID()
	}
	if p.Name == "" || p.Prompt == "" {
		return fmt.Errorf("name and prompt required")
	}
	if p.Mode != "" && p.Mode != "default" && p.Mode != "plan" {
		return fmt.Errorf("invalid collaboration mode")
	}
	if p.Sandbox != "read-only" && p.Sandbox != "workspace-write" && p.Sandbox != "danger-full-access" {
		return fmt.Errorf("invalid sandbox")
	}
	if p.Approval != "on-request" && p.Approval != "untrusted" && p.Approval != "never" {
		return fmt.Errorf("invalid approval policy")
	}
	if p.Publishing != "ask" && p.Publishing != "authorized" {
		return fmt.Errorf("invalid publishing authority")
	}
	return a.Store.Put(ctx, "profile", p.ID, p, "profile.changed")
}
func (a *App) Inbox(ctx context.Context, state string) ([]domain.InboxEntry, error) {
	return a.inbox(ctx, state, false)
}

// InboxSummaries avoids loading historical activity/check blobs for list views.
// Full detail and assessment inputs continue to use the retained source records.
func (a *App) InboxSummaries(ctx context.Context, state string) ([]domain.InboxEntry, error) {
	return a.inbox(ctx, state, true)
}

func (a *App) inbox(ctx context.Context, state string, compact bool) ([]domain.InboxEntry, error) {
	var items []domain.Item
	var e error
	if compact {
		items, e = store.ListWithout[domain.Item](ctx, a.Store, "item", "$.activity", "$.checks", "$.pr.checks")
	} else {
		items, e = store.List[domain.Item](ctx, a.Store, "item")
	}
	if e != nil {
		return nil, e
	}
	users, e := a.Store.Records(ctx, "user")
	if e != nil {
		return nil, e
	}
	assessments, e := a.Store.Records(ctx, "assessment")
	if e != nil {
		return nil, e
	}
	tickets, e := a.Store.Records(ctx, "ticket")
	if e != nil {
		return nil, e
	}
	byID := map[string]domain.Item{}
	for _, i := range items {
		byID[i.ID] = i
	}
	c, e := a.Config(ctx)
	if e != nil {
		return nil, e
	}
	links, e := store.List[domain.Relationship](ctx, a.Store, "relationship")
	if e != nil {
		return nil, e
	}
	outgoing := map[string][]domain.Relationship{}
	for _, l := range links {
		outgoing[l.Source] = append(outgoing[l.Source], l)
	}
	kinds := map[string]string{}
	for _, item := range items {
		kinds[item.ID] = item.Kind
	}
	hasPR := map[string]bool{}
	for _, link := range links {
		if kinds[link.Source] == "pr" {
			hasPR[link.Target] = true
		}
		if kinds[link.Target] == "pr" || strings.Contains(link.Evidence, "github.com/") && strings.Contains(link.Evidence, "/pull/") {
			hasPR[link.Source] = true
		}
	}
	out := []domain.InboxEntry{}
	for _, i := range items {
		if state != "all" && i.State != "open" {
			continue
		}
		var u domain.UserState
		if e = decodeInboxRecord(users, i.ID, &u); e != nil {
			return nil, e
		}
		var assess domain.Assessment
		if e = decodeInboxRecord(assessments, i.ID, &assess); e != nil && !errors.Is(e, store.ErrNotFound) {
			return nil, e
		}
		entry := domain.InboxEntry{HasPullRequest: hasPR[i.ID], Item: i, User: u, Unread: u.ReadRevision != i.Revision, Assessment: assess, PrioritySource: "pending"}
		if u.Override != nil {
			entry.Priority = u.Override
			entry.PrioritySource = "override"
		} else {
			for _, l := range i.Labels {
				if p, ok := c.PriorityLabels[l]; ok {
					if entry.Priority == nil || p < *entry.Priority {
						x := p
						entry.Priority = &x
						entry.PrioritySource = "label"
					}
				}
			}
			if entry.Priority == nil && assess.Status == "complete" && assess.Revision == i.Revision && assess.Policy == c.PolicyVersion && assess.Context == assessmentContext(i, outgoing[i.ID], func(id string) (domain.Item, error) {
				v, ok := byID[id]
				if !ok {
					return v, store.ErrNotFound
				}
				return v, nil
			}, func(id string) (domain.Ticket, error) {
				var v domain.Ticket
				err := decodeInboxRecord(tickets, id, &v)
				return v, err
			}) {
				x := assess.Priority
				entry.Priority = &x
				entry.PrioritySource = "inference"
			}
		}
		out = append(out, entry)
	}
	if e = a.projectWork(ctx, items, links, out); e != nil {
		return nil, e
	}
	sort.SliceStable(out, func(i, j int) bool {
		p, q := 4, 4
		if out[i].Priority != nil {
			p = *out[i].Priority
		}
		if out[j].Priority != nil {
			q = *out[j].Priority
		}
		if p != q {
			return p < q
		}
		return out[i].Item.Updated.After(out[j].Item.Updated)
	})
	return out, nil
}

func decodeInboxRecord(rows map[string]json.RawMessage, id string, out any) error {
	b, ok := rows[id]
	if !ok {
		return store.ErrNotFound
	}
	return json.Unmarshal(b, out)
}
func (a *App) UserAction(ctx context.Context, id, kind, revision string, priority *int) error {
	return a.Store.Write(ctx, func(t *store.Tx) error {
		var i domain.Item
		if e := t.Get("item", id, &i); e != nil {
			return e
		}
		var u domain.UserState
		if e := t.Get("user", id, &u); e != nil {
			return e
		}
		switch kind {
		case "read":
			if revision == "" {
				return fmt.Errorf("displayed revision required")
			}
			u.ReadRevision = revision
		case "unread":
			u.ReadRevision = ""
		case "acknowledge":
			u.New = false
		case "priority":
			if priority != nil && (*priority < 0 || *priority > 3) {
				return fmt.Errorf("invalid priority")
			}
			u.Override = priority
		case "acted":
			u.Actions = append(u.Actions, domain.Action{ID: ID(), Kind: "plugin", At: time.Now().UTC()})
		default:
			return fmt.Errorf("unknown action")
		}
		if e := t.Put("user", id, u); e != nil {
			return e
		}
		return t.Event("item.state", id, u)
	})
}
func (a *App) Import(ctx context.Context, r domain.Repository, items []domain.Item, user string, checkpoint time.Time, full bool) error {
	return a.importItems(ctx, r, items, user, &checkpoint, full)
}
func (a *App) importItems(ctx context.Context, r domain.Repository, items []domain.Item, user string, checkpoint *time.Time, full bool) error {
	previousLinks, err := store.List[domain.Relationship](ctx, a.Store, "relationship")
	if err != nil {
		return err
	}
	err = a.Store.Write(ctx, func(t *store.Tx) error {
		for _, i := range items {
			var old domain.Item
			e := t.Get("item", i.ID, &old)
			if e != nil && !errors.Is(e, store.ErrNotFound) {
				return e
			}
			created := errors.Is(e, store.ErrNotFound)
			if i.SummaryOnly {
				if i.Kind == "pr" && old.ReviewObservedAt.After(i.ReviewObservedAt) {
					continue
				}
				i.SummaryOnly = false
				if !created {
					i.Activity = old.Activity
					i.DetailSyncedAt = old.DetailSyncedAt
					if i.Head != old.Head || i.Updated.After(old.Updated) || i.State != old.State {
						i.DetailSyncedAt = time.Time{}
					}
					i.ClosingIssues = old.ClosingIssues
				}
				if i.Kind == "pr" {
					i.PR = &domain.PullRequest{ID: i.ID, Title: i.Title, URL: i.URL, State: i.State, Draft: i.Draft, Head: i.Head, CI: "unknown", ReviewDecision: "unknown", Checks: []domain.Check{}, Error: "PR details have not been refreshed"}
					if !created && old.Head == i.Head {
						i.Checks = old.Checks
						if old.PR != nil {
							p := *old.PR
							p.Title = i.Title
							p.State = i.State
							p.Draft = i.Draft
							i.PR = &p
							if i.DetailSyncedAt.IsZero() || time.Since(p.SyncedAt) > providers.GitHubDetailTTL {
								i.PR.Error = "PR details need refreshing"
							}
						}
					}
				}
			}

			if i.PR != nil {
				copy := *i.PR
				i.PR = &copy
			}
			retainPRMetadata(old, &i)
			i.Revision = i.ContentRevision()
			var u domain.UserState
			if created {
				u = domain.UserState{ID: i.ID, New: r.Imported, Actions: []domain.Action{}}
			} else if e = t.Get("user", i.ID, &u); e != nil {
				return e
			}
			for _, act := range i.Activity {
				if user != "" && act.Author == user && (act.Kind == "comment" || act.Kind == "review" || act.Kind == "review-comment") {
					found := false
					for _, x := range u.Actions {
						if x.ID == act.ID {
							found = true
						}
					}
					if !found {
						u.Actions = append(u.Actions, domain.Action{ID: act.ID, Kind: "github:" + act.Kind, At: time.Now().UTC()})
					}
				}
			}
			if e = t.Put("user", i.ID, u); e != nil {
				return e
			}
			if e = t.Put("item", i.ID, i); e != nil {
				return e
			}
			if old.Revision != i.Revision {
				if e = t.Event("item.changed", i.ID, map[string]any{"id": i.ID, "new": u.New, "title": i.Title}); e != nil {
					return e
				}
				assess := domain.Assessment{ID: i.ID, Revision: i.Revision, Status: "pending"}
				if e = t.Put("assessment", i.ID, assess); e != nil {
					return e
				}
			}
			for _, l := range Discover(i) {
				if e = t.Put("relationship", l.ID, l); e != nil {
					return e
				}
			}
			for _, l := range previousLinks {
				if l.Source == i.ID && l.Kind == "implementation" && !l.Manual {
					if e = t.Delete("relationship", l.ID); e != nil {
						return e
					}
				}
			}
			for _, issue := range i.ClosingIssues {
				l := domain.Relationship{ID: "implementation:" + i.ID + "->" + issue, Source: i.ID, Target: issue, Kind: "implementation", Evidence: i.URL}
				if e = t.Put("relationship", l.ID, l); e != nil {
					return e
				}
			}
		}
		if checkpoint == nil {
			return t.Event("item.detail", r.ID, map[string]any{"repository": r.ID})
		}
		var current domain.Repository
		if e := t.Get("repository", r.ID, &current); e != nil {
			return e
		}
		if checkpoint.After(current.LastSync) {
			current.LastSync = *checkpoint
		}
		if full && checkpoint.After(current.LastFull) {
			current.LastFull = *checkpoint
		}
		current.Imported = true
		current.Error = ""
		current.SyncFailures = 0
		current.RetryAt = time.Time{}
		if e := t.Put("repository", r.ID, current); e != nil {
			return e
		}
		return t.Event("sync.completed", r.ID, map[string]any{"repository": r.ID, "at": checkpoint})
	})
	if err != nil {
		return err
	}
	if a.Runs != nil {
		for _, item := range items {
			if closedWork(item) {
				var saved domain.Item
				if err := a.Store.Get(ctx, "item", item.ID, &saved); err != nil {
					return err
				}
				if err := a.Runs.stopClosedItem(ctx, saved); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (a *App) Sync(ctx context.Context) error { return a.sync(ctx, false) }
func (a *App) sync(ctx context.Context, dueOnly bool) error {
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	repos, e := store.List[domain.Repository](ctx, a.Store, "repository")
	if e != nil {
		return e
	}
	if len(repos) == 0 {
		return nil
	}
	if dueOnly {
		selected := repos[:0]
		for _, r := range repos {
			if !r.Paused && (r.LastSync.IsZero() || time.Since(r.LastSync) >= 15*time.Minute) {
				selected = append(selected, r)
			}
		}
		repos = selected
		if len(repos) == 0 {
			return a.SyncNotifications(ctx)
		}
	}
	ctx = providers.StartGitHubOperation(ctx, a.GitHub, a.Store, false)
	identityCtx := ctx
	if !a.GitHub.Managed() {
		identityCtx = providers.WithGitHubRead(ctx, providers.RequireLive)
	}
	user, e := a.GitHub.User(identityCtx)
	if e != nil {
		for _, r := range repos {
			if err := a.saveSyncError(ctx, r.ID, e); err != nil {
				return err
			}
		}
		return e
	}
	viewer := domain.GitHubViewer{Login: user, Teams: []string{}}
	var previous domain.GitHubViewer
	if err := a.Store.Get(ctx, "identity", "github", &previous); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if previous.Login == user {
		viewer = previous
	}
	saveViewer := func() error {
		if string(raw(viewer)) == string(raw(previous)) {
			return nil
		}
		if err := a.Store.Put(ctx, "identity", "github", viewer, "identity.changed"); err != nil {
			return err
		}
		previous = viewer
		return nil
	}
	if err := saveViewer(); err != nil {
		return err
	}
	var teamsOnce sync.Once
	var teamErr error
	syncRepo := func(r domain.Repository) error {
		if r.Paused {
			return nil
		}
		derivedLimit := strings.HasPrefix(r.Error, "Code Factory GitHub budget") || strings.HasPrefix(r.Error, "GitHub request reserve")
		if time.Now().Before(r.RetryAt) && (dueOnly || !derivedLimit) {
			return nil
		}
		at := time.Now().UTC()
		full := !r.Imported || time.Since(r.LastFull) > time.Hour
		items, observed, err := a.GitHub.SyncIndex(ctx, r, full)
		if !observed.IsZero() && observed.Before(at) {
			at = observed
		}
		if err != nil {
			return a.saveSyncError(ctx, r.ID, err)
		}

		for _, item := range items {
			if len(item.ReviewTeams) == 0 {
				continue
			}
			teamsOnce.Do(func() {
				teams, err := a.GitHub.Teams(ctx)
				viewer.Teams = teams
				viewer.TeamError = ""
				if err != nil {
					viewer.TeamError = "Team review requests unavailable: " + err.Error()
				}
				teamErr = saveViewer()
			})
			if teamErr != nil {
				return teamErr
			}
			break
		}
		if e := a.Import(ctx, r, items, user, at, full); e != nil {
			return e
		}
		return nil
	}
	// An initial import in a large repository must not hold up every other
	// repository. Up to three workers synchronize repository indexes; any
	// fallback PR metadata is processed sequentially within each index.
	jobs := make(chan domain.Repository)
	var workers sync.WaitGroup
	var firstErr error
	var failed sync.Once
	workerCount := min(3, len(repos))
	if a.GitHub.Managed() {
		// Managed requests already serialize at the transport budget. Preserve
		// repository order so a limit stops later checkpoint advancement.
		workerCount = min(1, len(repos))
	}
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for repo := range jobs {
				if err := syncRepo(repo); err != nil {
					failed.Do(func() { firstErr = err })
				}
			}
		}()
	}
	for _, repo := range repos {
		jobs <- repo
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return firstErr
	}
	if a.Runs != nil {
		return a.ProcessReviewRequests(ctx)
	}
	return nil
}

const SyncInterval = 30 * time.Second

func (a *App) SyncLoop(ctx context.Context) {
	ticker := time.NewTicker(SyncInterval)
	defer ticker.Stop()
	a.syncLoop(ctx, ticker.C)
}
func (a *App) syncLoop(ctx context.Context, ticks <-chan time.Time) {
	for {
		if ctx.Err() != nil {
			return
		}
		_ = a.sync(ctx, true)
		if a.Runs != nil {
			_ = a.Runs.RetryDeferredLaunches(ctx)
		}
		// Reference backfill yields to the next repository poll and resumes from
		// its last attempted PR, rather than restarting a large reference list.
		references, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = a.RefreshPullRequests(references)
		cancel()
		_ = a.RefreshJira(ctx)
		a.RefreshSavedObservations(ctx)
		if a.Runs != nil {
			a.Runs.maybeCheckPlugins(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

var ghRef = regexp.MustCompile(`https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/(?:issues|pull)/([0-9]+)`)
var shortRef = regexp.MustCompile(`(?:\b([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+))?#([0-9]+)\b`)

func Discover(i domain.Item) []domain.Relationship {
	text := i.Title + "\n" + i.Body + "\n" + i.Branch
	for _, a := range i.Activity {
		text += "\n" + a.Body + " " + a.URL
	}
	out := []domain.Relationship{}
	seen := map[string]bool{}
	add := func(target, evidence, kind string) {
		if target == i.ID || seen[target] {
			return
		}
		seen[target] = true
		out = append(out, domain.Relationship{ID: i.ID + "->" + target, Source: i.ID, Target: target, Kind: kind, Evidence: evidence})
	}
	for _, m := range ghRef.FindAllStringSubmatch(text, -1) {
		add(m[1]+"#"+m[2], m[0], "github")
	}
	for _, m := range shortRef.FindAllStringSubmatch(text, -1) {
		repo := m[1]
		if repo == "" {
			repo = i.Repository
		}
		add(repo+"#"+m[2], m[0], "github")
	}
	for _, m := range providers.JiraKey.FindAllString(text, -1) {
		add(m, m, "jira")
	}
	return out
}
func (a *App) Link(ctx context.Context, l domain.Relationship, remove bool) error {
	if l.Source == "" || l.Target == "" || l.Source == l.Target {
		return fmt.Errorf("source and different target required")
	}
	l.ID = "local:" + l.Source + "->" + l.Target
	l.Manual = true
	l.Kind = "local"
	return a.Store.Write(ctx, func(t *store.Tx) error {
		if remove {
			if e := t.Delete("relationship", l.ID); e != nil {
				return e
			}
		} else if e := t.Put("relationship", l.ID, l); e != nil {
			return e
		}
		return t.Event("relationship.changed", l.Source, l)
	})
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func (a *App) AssessmentContext(ctx context.Context, item domain.Item) string {
	links, err := store.List[domain.Relationship](ctx, a.Store, "relationship")
	if err != nil {
		return "unavailable"
	}
	return assessmentContext(item, links, func(id string) (domain.Item, error) {
		var v domain.Item
		err := a.Store.Get(ctx, "item", id, &v)
		return v, err
	}, func(id string) (domain.Ticket, error) {
		var v domain.Ticket
		err := a.Store.Get(ctx, "ticket", id, &v)
		return v, err
	})
}

func assessmentContext(item domain.Item, links []domain.Relationship, getItem func(string) (domain.Item, error), getTicket func(string) (domain.Ticket, error)) string {
	data := []any{item.Revision}
	for _, l := range links {
		if l.Source != item.ID {
			continue
		}
		data = append(data, l)
		if linked, err := getItem(l.Target); err == nil {
			data = append(data, linked.Revision)
		}
		if ticket, err := getTicket(l.Target); err == nil {
			data = append(data, ticket)
		}
	}
	b, _ := json.Marshal(data)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (a *App) RefreshJira(ctx context.Context) error {
	c, e := a.Config(ctx)
	if e != nil || c.JiraURL == "" {
		return e
	}
	token, e := a.Vault.Get(ctx, "jira")
	if errors.Is(e, store.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	links, e := store.List[domain.Relationship](ctx, a.Store, "relationship")
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, l := range links {
		if providers.JiraKey.FindString(l.Target) != l.Target || l.Target == "" || seen[l.Target] {
			continue
		}
		seen[l.Target] = true
		ticket, e := providers.JiraTicket(ctx, c.JiraURL, c.JiraEmail, token, l.Target)
		if e != nil {
			ticket.Error = e.Error()
		}
		var old domain.Ticket
		_ = a.Store.Get(ctx, "ticket", l.Target, &old)
		if string(raw(old)) != string(raw(ticket)) {
			if e = a.Store.Put(ctx, "ticket", l.Target, ticket, "ticket.changed"); e != nil {
				return e
			}
		}
	}
	return nil
}
