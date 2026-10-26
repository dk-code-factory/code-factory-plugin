package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"codefactory/internal/store"
)

const GitHubDetailTTL = 15 * time.Minute

type GitHubPolicy struct {
	RequestsPerHour int
	Reserve         int
	MinInterval     time.Duration
}
type githubRemoteLimit struct {
	Remaining int
	Reset     time.Time
}
type githubUsage struct {
	PollUntil      map[string]time.Time
	ActiveScope    string
	Calls          []time.Time
	Next           time.Time
	SecondaryUntil time.Time
	Retry          map[string]time.Time
	Limits         map[string]githubRemoteLimit
	Strikes        int
}
type GitHubUsage struct {
	RequestsLastHour int       `json:"requestsLastHour"`
	Budget           int       `json:"budget"`
	Reserve          int       `json:"reserve"`
	NextRequest      time.Time `json:"nextRequest"`
	RESTRemaining    *int      `json:"restRemaining,omitempty"`
	RESTReset        time.Time `json:"restReset"`
	GraphQLRemaining *int      `json:"graphqlRemaining,omitempty"`
	GraphQLReset     time.Time `json:"graphqlReset"`
}

// ConfigureUsage attaches private durable state to the one application provider.
// Every managed REST and GraphQL request shares its serialized, fail-closed gate.
func (g *GitHub) ConfigureUsage(s *store.Store, policy func(context.Context) (GitHubPolicy, error)) {
	g.usageStore = s
	g.policy = policy
}
func (g *GitHub) Managed() bool { return g.policy != nil }

// All managed transports, including publication subprocesses, hold the same
// request lock through reservation, HTTP response and durable observations.
func (g *GitHub) lockUsage(ctx context.Context) (func(), error) {
	g.requestMu.Lock()
	if g.usageStore == nil {
		return g.requestMu.Unlock, nil
	}
	unlock, err := g.usageStore.ProcessLock(ctx, "github-request")
	if err != nil {
		g.requestMu.Unlock()
		return nil, err
	}
	return func() { unlock(); g.requestMu.Unlock() }, nil
}
func (g *GitHub) usage(ctx context.Context) (githubUsage, error) {
	u := githubUsage{Retry: map[string]time.Time{}, Limits: map[string]githubRemoteLimit{}}
	if g.usageStore != nil {
		err := g.usageStore.Get(ctx, "github-usage", "application", &u)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return u, err
		}
	} else if g.usageMemory != nil {
		u = *g.usageMemory
	}
	if u.PollUntil == nil {
		u.PollUntil = map[string]time.Time{}
	}
	if u.Retry == nil {
		u.Retry = map[string]time.Time{}
	}
	if u.Limits == nil {
		u.Limits = map[string]githubRemoteLimit{}
	}
	return u, nil
}
func (g *GitHub) saveUsage(ctx context.Context, u githubUsage) error {
	if g.usageStore != nil {
		return g.usageStore.Put(ctx, "github-usage", "application", u, "")
	}
	g.usageMemory = &u
	return nil
}
func (g *GitHub) now() time.Time {
	if g.clock != nil {
		return g.clock()
	}
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}
func usageKey(token, resource string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x:%s", h, resource)
}
func recentCalls(calls []time.Time, now time.Time) []time.Time {
	out := make([]time.Time, 0, len(calls))
	for _, t := range calls {
		if t.After(now.Add(-time.Hour)) {
			out = append(out, t)
		}
	}
	return out
}
func (g *GitHub) beginRequest(ctx context.Context, token, resource, route string) error {
	if !g.Managed() {
		return nil
	}
	for {
		p, err := g.policy(ctx)
		if err != nil {
			return fmt.Errorf("GitHub request budget unavailable")
		}
		if p.RequestsPerHour <= 0 {
			return fmt.Errorf("GitHub request budget unavailable")
		}
		u, err := g.usage(ctx)
		if err != nil {
			return fmt.Errorf("GitHub request budget unavailable")
		}
		now := g.now()
		key := usageKey(token, resource)
		until := u.SecondaryUntil
		if t := u.PollUntil[usageKey(token, route)]; t.After(until) {
			until = t
		}
		if u.Retry[key].After(until) {
			until = u.Retry[key]
		}
		if limit, ok := u.Limits[key]; ok && limit.Reset.After(now) && limit.Remaining <= p.Reserve && limit.Reset.After(until) {
			until = limit.Reset.Add(time.Second)
		}
		u.Calls = recentCalls(u.Calls, now)
		if len(u.Calls) >= p.RequestsPerHour {
			t := u.Calls[len(u.Calls)-p.RequestsPerHour].Add(time.Hour + time.Second)
			if t.After(until) {
				until = t
			}
		}
		if until.After(now) {
			reason := ""
			if len(u.Calls) >= p.RequestsPerHour {
				reason = "Code Factory GitHub budget reached; synchronization will retry"
			} else if limit, ok := u.Limits[key]; ok && limit.Reset.After(now) && limit.Remaining <= p.Reserve {
				reason = "GitHub request reserve reached; synchronization will retry"
			}
			return &RateLimitError{Until: until, Reason: reason}
		}
		if delay := u.Next.Sub(now); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			timer.Stop()
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		u.ActiveScope = strings.TrimSuffix(usageKey(token, resource), ":"+resource)
		u.Calls = append(u.Calls, now)
		u.Next = now.Add(p.MinInterval)
		// Reserve durably before sending. A crash or an uncertain response never
		// restores a potentially consumed request to the local budget.
		if err = g.saveUsage(ctx, u); err != nil {
			return fmt.Errorf("GitHub request budget unavailable")
		}
		return nil
	}
}
func (g *GitHub) observe(ctx context.Context, token, resource, route string, h http.Header) error {
	if !g.Managed() {
		return nil
	}
	u, err := g.usage(ctx)
	if err != nil {
		return err
	}
	changed := false
	if seconds, e := strconv.Atoi(h.Get("X-Poll-Interval")); e == nil && seconds > 0 {
		u.PollUntil[usageKey(token, route)] = g.now().Add(time.Duration(seconds) * time.Second)
		changed = true
	}
	remaining, e := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	epoch, resetErr := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if e == nil && resetErr == nil {
		u.Limits[usageKey(token, resource)] = githubRemoteLimit{Remaining: remaining, Reset: time.Unix(epoch, 0)}
		changed = true
	}
	if changed {
		return g.saveUsage(ctx, u)
	}
	return nil
}
func retryTime(h http.Header, now time.Time) time.Time {
	until := now.Add(time.Minute)
	if h.Get("X-RateLimit-Remaining") == "0" {
		if n, e := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); e == nil && time.Unix(n, 0).After(now) {
			until = time.Unix(n, 0).Add(time.Second)
		}
	}
	if raw := h.Get("Retry-After"); raw != "" {
		if n, e := strconv.Atoi(raw); e == nil && n >= 0 {
			if t := now.Add(time.Duration(n) * time.Second); t.After(until) {
				until = t
			}
		}
		if t, e := http.ParseTime(raw); e == nil && t.After(until) {
			until = t
		}
	}
	return until
}
func (g *GitHub) rateLimited(ctx context.Context, token, resource string, h http.Header) error {
	until := retryTime(h, g.now())
	if g.Managed() {
		u, err := g.usage(ctx)
		if err != nil {
			return fmt.Errorf("GitHub request budget unavailable")
		}
		if h.Get("X-RateLimit-Remaining") == "0" {
			u.Retry[usageKey(token, resource)] = until
		} else {
			u.Strikes = min(u.Strikes+1, 6)
			exponential := g.now().Add(time.Minute * time.Duration(1<<uint(u.Strikes-1)))
			if exponential.After(until) {
				until = exponential
			}
			u.SecondaryUntil = until
		}
		if err = g.saveUsage(ctx, u); err != nil {
			return fmt.Errorf("GitHub request budget unavailable")
		}
	}
	return &RateLimitError{Until: until}
}
func rateBody(body []byte) bool {
	var d struct{ Message string }
	if json.Unmarshal(body, &d) != nil {
		return false
	}
	m := strings.ToLower(d.Message)
	return strings.Contains(m, "rate limit") || strings.Contains(m, "abuse detection")
}

// UsageStatus is cached local accounting, never a call to /rate_limit.
func (g *GitHub) UsageStatus(ctx context.Context) (GitHubUsage, error) {
	if g.usageStore == nil {
		g.requestMu.Lock()
		defer g.requestMu.Unlock()
	}
	p := GitHubPolicy{RequestsPerHour: 600, Reserve: 100}
	if g.policy != nil {
		var err error
		p, err = g.policy(ctx)
		if err != nil {
			return GitHubUsage{}, err
		}
	}
	u, err := g.usage(ctx)
	if err != nil {
		return GitHubUsage{}, err
	}
	now := g.now()
	out := GitHubUsage{RequestsLastHour: len(recentCalls(u.Calls, now)), Budget: p.RequestsPerHour, Reserve: p.Reserve, NextRequest: u.SecondaryUntil}
	for key, l := range u.Limits {
		if u.ActiveScope != "" && !strings.HasPrefix(key, u.ActiveScope+":") {
			continue
		}
		if !l.Reset.After(now) {
			continue
		}
		if strings.HasSuffix(key, ":core") {
			v := l.Remaining
			if out.RESTRemaining == nil || v < *out.RESTRemaining {
				out.RESTRemaining = &v
				out.RESTReset = l.Reset
			}
		}
		if strings.HasSuffix(key, ":graphql") {
			v := l.Remaining
			if out.GraphQLRemaining == nil || v < *out.GraphQLRemaining {
				out.GraphQLRemaining = &v
				out.GraphQLReset = l.Reset
			}
		}
	}
	for key, t := range u.PollUntil {
		if (u.ActiveScope == "" || strings.HasPrefix(key, u.ActiveScope+":")) && t.After(out.NextRequest) {
			out.NextRequest = t
		}
	}
	for key, t := range u.Retry {
		if u.ActiveScope != "" && !strings.HasPrefix(key, u.ActiveScope+":") {
			continue
		}
		if t.After(out.NextRequest) {
			out.NextRequest = t
		}
	}
	calls := recentCalls(u.Calls, now)
	if len(calls) >= p.RequestsPerHour {
		t := calls[len(calls)-p.RequestsPerHour].Add(time.Hour + time.Second)
		if t.After(out.NextRequest) {
			out.NextRequest = t
		}
	}
	return out, nil
}

func (g *GitHub) cached(ctx context.Context, key string) (cacheEntry, bool, error) {
	g.mu.Lock()
	v, ok := g.cache[key]
	g.mu.Unlock()
	if ok {
		return v, true, nil
	}
	if g.usageStore == nil {
		return v, false, nil
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	err := g.usageStore.Get(ctx, "github-cache", id, &v)
	if errors.Is(err, store.ErrNotFound) {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if g.now().Sub(v.CheckedAt) > 24*time.Hour {
		return cacheEntry{}, false, nil
	}
	return v, true, nil
}
func (g *GitHub) remember(ctx context.Context, key string, v cacheEntry) error {
	g.cachePut(key, v)
	if g.usageStore != nil {
		// Keep the complete private persistent cache bounded as well as memory.
		id := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
		entries, err := g.usageStore.JSONByteSizes(ctx, "github-cache", "$.Body")
		if err != nil {
			return err
		}
		total := int64(len(v.Body))
		for entryID, size := range entries {
			if entryID != id {
				total += size
			}
		}
		if total > 64<<20 {
			if err := g.usageStore.Write(ctx, func(t *store.Tx) error {
				for entryID := range entries {
					if err := t.Delete("github-cache", entryID); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		v.ID = id
		return g.usageStore.Put(ctx, "github-cache", id, v, "")
	}
	return nil
}
func safeCacheHeader(h http.Header) http.Header {
	out := http.Header{}
	for _, k := range []string{"ETag", "Last-Modified", "Link", "X-Poll-Interval"} {
		if v := h.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	return out
}

func restTTL(path string, h http.Header) time.Duration {
	var ttl time.Duration
	if path == "/user" {
		ttl = 5 * time.Minute
	}
	if path == "/user/teams" {
		ttl = 15 * time.Minute
	}
	if n, e := strconv.Atoi(h.Get("X-Poll-Interval")); e == nil && n > 0 && time.Duration(n)*time.Second > ttl {
		ttl = time.Duration(n) * time.Second
	}
	return ttl
}

func (g *GitHub) SeedCooldown(ctx context.Context, until time.Time) error {
	unlock, err := g.lockUsage(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	u, err := g.usage(ctx)
	if err != nil {
		return err
	}
	if until.After(u.SecondaryUntil) {
		u.SecondaryUntil = until
		return g.saveUsage(ctx, u)
	}
	return nil
}

type readObservation struct{}

func observeReads(ctx context.Context, now time.Time) (context.Context, *time.Time) {
	observed := now
	return context.WithValue(ctx, readObservation{}, &observed), &observed
}
func noteRead(ctx context.Context, at time.Time) {
	if observed, ok := ctx.Value(readObservation{}).(*time.Time); ok && at.Before(*observed) {
		*observed = at
	}
}
func (g *GitHub) requestSucceeded(ctx context.Context) error {
	if !g.Managed() {
		return nil
	}
	u, err := g.usage(ctx)
	if err != nil {
		return err
	}
	if u.Strikes == 0 {
		return nil
	}
	u.Strikes = 0
	u.SecondaryUntil = time.Time{}
	return g.saveUsage(ctx, u)
}
