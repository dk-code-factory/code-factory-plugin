package providers

// Transport mechanics are shared; publication policy belongs to reports.
import (
	"bytes"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const CooldownKind = "report-github-rate-limit"
const apiVersion = "2022-11-28"
const cacheLimit = 64 << 20
const cacheEntries = 4096

type RequestPhase int

const (
	BeforeDispatch RequestPhase = iota
	HTTPRejected
	Uncertain
)

type RequestError struct {
	Phase  RequestPhase
	Status int
	Cause  error
}

func (e *RequestError) Error() string          { return e.Cause.Error() }
func (e *RequestError) Unwrap() error          { return e.Cause }
func (e *RateLimitError) RetryTime() time.Time { return e.Until }
func CooldownKey(base, token string) string {
	h := sha256.Sum256([]byte(base + "\x00" + token))
	return hex.EncodeToString(h[:])
}

type revalidateKey struct{}

// Revalidate bypasses batch memoization while retaining operation credentials.
func Revalidate(ctx context.Context) context.Context {
	return context.WithValue(ctx, revalidateKey{}, true)
}

type operationKey struct{}
type operation struct {
	client                           *GitHub
	ledger                           *store.Store
	requireLedger, memoize, resolved bool
	token                            string
	err                              error
	memo                             map[string]bool
}

// Operation pins authentication for one batch. New operations resolve it again.
func (g *GitHub) Operation(ctx context.Context, ledger *store.Store, memoize bool) context.Context {
	if op, _ := ctx.Value(operationKey{}).(*operation); op != nil && op.client == g {
		return ctx
	}
	return g.StartOperation(ctx, ledger, memoize)
}

// StartOperation always starts a new top-level sync, refresh or publication,
// even if its caller reused a context from an earlier operation.
func (g *GitHub) StartOperation(ctx context.Context, ledger *store.Store, memoize bool) context.Context {
	return context.WithValue(ctx, operationKey{}, &operation{client: g, ledger: ledger, requireLedger: memoize, memoize: memoize, memo: map[string]bool{}})
}
func (g *GitHub) resolve(ctx context.Context, op *operation) error {
	if !op.resolved {
		op.resolved = true
		if g.Token != nil {
			op.token, op.err = g.Token(ctx)
		}
		if g.Token == nil || op.err != nil || strings.TrimSpace(op.token) == "" {
			op.err = errors.New("GitHub authentication unavailable; restore GitHub CLI authentication and resume")
		}
	}
	return op.err
}
func (g *GitHub) Scope(ctx context.Context) (string, error) {
	ctx = g.Operation(ctx, nil, false)
	op := ctx.Value(operationKey{}).(*operation)
	g.dispatch.Lock()
	defer g.dispatch.Unlock()
	if err := g.resolve(ctx, op); err != nil {
		return "", err
	}
	return CooldownKey(g.BaseURL, op.token), nil
}

// RequestURL canonicalizes a same-origin API target before pagination traversal.
func (g *GitHub) RequestURL(endpoint string) (string, error) { return g.endpoint(endpoint) }
func (g *GitHub) endpoint(endpoint string) (string, error) {
	base, err := url.Parse(g.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		return "", errors.New("GitHub request configuration is invalid; restore the adapter settings and resume")
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		return "", errors.New("invalid GitHub pagination URL")
	}
	target = base.ResolveReference(target)
	if target.Host != base.Host || target.Scheme != base.Scheme || target.User != nil || target.Fragment != "" {
		return "", errors.New("invalid GitHub pagination URL")
	}
	return target.String(), nil
}
func (g *GitHub) NextPage(h http.Header) (string, error) {
	next := ""
	for _, link := range strings.Split(h.Get("Link"), ",") {
		parts := strings.Split(strings.TrimSpace(link), ";")
		for _, param := range parts[1:] {
			kv := strings.SplitN(strings.TrimSpace(param), "=", 2)
			if len(kv) != 2 || kv[0] != "rel" {
				continue
			}
			for _, rel := range strings.Fields(strings.Trim(kv[1], `"`)) {
				if rel != "next" {
					continue
				}
				if next != "" || !strings.HasPrefix(parts[0], "<") || !strings.HasSuffix(parts[0], ">") {
					return "", errors.New("invalid GitHub pagination link")
				}
				var err error
				next, err = g.endpoint(strings.TrimSuffix(strings.TrimPrefix(parts[0], "<"), ">"))
				if err != nil {
					return "", err
				}
			}
		}
	}
	return next, nil
}
func (g *GitHub) cacheGet(key string) (cacheEntry, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.cache[key]
	if ok && !c.Expires.IsZero() && !g.now().Before(c.Expires) {
		g.cacheBytes -= c.Size
		delete(g.cache, key)
		return cacheEntry{}, false
	}
	if ok {
		g.tick++
		c.Used = g.tick
		g.cache[key] = c
	}
	return c, ok
}
func (g *GitHub) cachePut(key string, c cacheEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cache == nil {
		g.cache = map[string]cacheEntry{}
	}
	if old, ok := g.cache[key]; ok {
		g.cacheBytes -= old.Size
		delete(g.cache, key)
	}
	c.Size = len(key) + len(c.Body)
	for k, v := range c.Header {
		c.Size += len(k)
		for _, s := range v {
			c.Size += len(s)
		}
	}
	if c.Size > cacheLimit {
		return
	}
	for len(g.cache) >= cacheEntries || g.cacheBytes+c.Size > cacheLimit {
		oldest := ""
		var used uint64 = ^uint64(0)
		for k, v := range g.cache {
			if v.Used < used {
				oldest, used = k, v.Used
			}
		}
		g.cacheBytes -= g.cache[oldest].Size
		delete(g.cache, oldest)
	}
	g.tick++
	c.Used = g.tick
	g.cache[key] = c
	g.cacheBytes += c.Size
}

// Verified blobs share the same bounded LRU as conditional responses.
func (g *GitHub) CachedBlob(key string) ([]byte, bool) {
	c, ok := g.cacheGet("blob:" + key)
	return append([]byte(nil), c.Body...), ok
}
func (g *GitHub) CacheBlob(key string, b []byte, ttl time.Duration) {
	c := cacheEntry{Body: append([]byte(nil), b...)}
	if ttl > 0 {
		c.Expires = g.now().Add(ttl)
	}
	g.cachePut("blob:"+key, c)
}
func retryDeadline(h http.Header, now time.Time) time.Time {
	until := now
	if sec, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && time.Unix(sec, 0).After(until) {
		until = time.Unix(sec, 0)
	}
	if sec, err := strconv.ParseInt(h.Get("Retry-After"), 10, 32); err == nil && sec >= 0 {
		if t := now.Add(time.Duration(sec) * time.Second); t.After(until) {
			until = t
		}
	} else if t, err := http.ParseTime(h.Get("Retry-After")); err == nil && t.After(until) {
		until = t
	}
	if !until.After(now) {
		until = now.Add(time.Minute)
	}
	return until
}
func (g *GitHub) Request(ctx context.Context, method, endpoint string, input any, accept string) ([]byte, http.Header, error) {
	fail := func(phase RequestPhase, status int, err error) ([]byte, http.Header, error) {
		return nil, nil, &RequestError{Phase: phase, Status: status, Cause: err}
	}
	target, err := g.endpoint(endpoint)
	if err != nil {
		return fail(BeforeDispatch, 0, err)
	}
	var body []byte
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return fail(BeforeDispatch, 0, errors.New("GitHub request could not be encoded; correct the draft and resume"))
		}
	}
	ctx = g.Operation(ctx, nil, false)
	op := ctx.Value(operationKey{}).(*operation)
	g.dispatch.Lock()
	defer g.dispatch.Unlock()
	if err = g.resolve(ctx, op); err != nil {
		return fail(BeforeDispatch, 0, err)
	}
	scope := CooldownKey(g.BaseURL, op.token)
	if op.requireLedger && op.ledger == nil {
		return fail(BeforeDispatch, 0, errors.New("GitHub cooldown storage unavailable; restore the private ledger and resume"))
	}
	until := g.cooldowns[scope]
	if op.ledger != nil {
		var saved time.Time
		if err = op.ledger.Get(ctx, CooldownKind, scope, &saved); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fail(BeforeDispatch, 0, errors.New("GitHub cooldown state unavailable; restore readable private ledger storage and resume"))
		}
		if saved.After(until) {
			until = saved
		}
	}
	if g.now().Before(until) {
		return fail(BeforeDispatch, 0, &RateLimitError{Until: until})
	}
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	key := target + "#" + scope + "#" + accept + "#" + apiVersion
	if method == http.MethodGet && op.memoize && ctx.Value(revalidateKey{}) != true {
		if op.memo[key] {
			if c, ok := g.cacheGet(key); ok {
				return c.Body, c.Header.Clone(), nil
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return fail(BeforeDispatch, 0, errors.New("GitHub request configuration is invalid; restore settings and resume"))
	}
	req.Header.Set("Authorization", "Bearer "+op.token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	c, cached := g.cacheGet(key)
	if method == http.MethodGet && cached && c.ETag != "" {
		req.Header.Set("If-None-Match", c.ETag)
	}
	if g.Client == nil {
		return fail(BeforeDispatch, 0, errors.New("GitHub HTTP client unavailable; restore settings and resume"))
	}
	if g.Managed() {
		unlock, err := g.lockUsage(ctx)
		if err != nil {
			return fail(BeforeDispatch, 0, err)
		}
		defer unlock()
	}
	safe := *g.Client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err = g.beginRequest(ctx, op.token, "core", req.URL.Path); err != nil {
		return fail(BeforeDispatch, 0, err)
	}
	res, err := safe.Do(req)
	if err != nil {
		return fail(Uncertain, 0, errors.New("GitHub connection failed"))
	}
	defer res.Body.Close()
	if err = g.observe(ctx, op.token, "core", req.URL.Path, res.Header); err != nil {
		return fail(Uncertain, res.StatusCode, errors.New("GitHub request budget unavailable"))
	}
	if res.StatusCode == 429 || res.StatusCode == 403 && (res.Header.Get("X-RateLimit-Remaining") == "0" || res.Header.Get("Retry-After") != "") {
		if g.Managed() {
			return fail(HTTPRejected, res.StatusCode, g.rateLimited(ctx, op.token, "core", res.Header))
		}
		deadline := retryDeadline(res.Header, g.now())
		if until.After(deadline) {
			deadline = until
		}
		if g.cooldowns == nil {
			g.cooldowns = map[string]time.Time{}
		}
		g.cooldowns[scope] = deadline
		if op.ledger != nil {
			err = op.ledger.Write(ctx, func(t *store.Tx) error {
				var retained time.Time
				if e := t.Get(CooldownKind, scope, &retained); e != nil && !errors.Is(e, store.ErrNotFound) {
					return e
				}
				if retained.After(deadline) {
					deadline = retained
				}
				return t.Put(CooldownKind, scope, deadline)
			})
			if err != nil {
				return fail(HTTPRejected, res.StatusCode, fmt.Errorf("GitHub retry deadline could not be retained: %w", &RateLimitError{Until: deadline}))
			}
		}
		return fail(HTTPRejected, res.StatusCode, &RateLimitError{Until: deadline})
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		op.err = errors.New("GitHub access revoked or insufficient repository permissions; restore authentication and resume")
		op.memo = map[string]bool{}
		g.mu.Lock()
		for k, v := range g.cache {
			if strings.Contains(k, scope) {
				g.cacheBytes -= v.Size
				delete(g.cache, k)
			}
		}
		g.mu.Unlock()
		return fail(HTTPRejected, res.StatusCode, op.err)
	}
	if res.StatusCode == 304 && method == http.MethodGet && cached {
		merged := c.Header.Clone()
		for k, v := range res.Header {
			merged[k] = v
		}
		c.Header = merged
		c.ETag = merged.Get("ETag")
		g.cachePut(key, c)
		if op.memoize {
			if len(op.memo) >= cacheEntries {
				op.memo = map[string]bool{}
			}
			op.memo[key] = true
		}
		return c.Body, c.Header.Clone(), nil
	}
	if method == http.MethodGet && accept == "application/octet-stream" && res.StatusCode == 302 {
		u, e := url.Parse(res.Header.Get("Location"))
		if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Hostname() != "release-assets.githubusercontent.com" && u.Hostname() != "objects.githubusercontent.com") {
			return fail(HTTPRejected, 302, errors.New("Untrusted release asset redirect"))
		}
		download, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return fail(Uncertain, 0, errors.New("Release asset unavailable"))
		}
		res, err = (&http.Client{Timeout: 30 * time.Second, CheckRedirect: safe.CheckRedirect}).Do(download)
		if err != nil {
			return fail(Uncertain, 0, errors.New("Release asset unavailable"))
		}
		defer res.Body.Close()
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fail(HTTPRejected, res.StatusCode, fmt.Errorf("GitHub operation failed (HTTP %d)", res.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if err != nil {
		return fail(Uncertain, res.StatusCode, errors.New("GitHub response unavailable"))
	}
	if len(b) > 16<<20 {
		return fail(Uncertain, res.StatusCode, errors.New("GitHub response exceeds limit"))
	}
	entry := cacheEntry{ETag: res.Header.Get("ETag"), Body: b, Header: res.Header.Clone()}
	if method == http.MethodGet {
		if strings.Contains(target, "/issues?") && strings.Contains(target, "since=") {
			entry.Expires = g.now().Add(10 * time.Minute)
		}
		g.cachePut(key, entry)
		if op.memoize {
			if len(op.memo) >= cacheEntries {
				op.memo = map[string]bool{}
			}
			op.memo[key] = true
		}
	}
	return b, res.Header.Clone(), nil
}

// Index hydration may overlap independent repositories, while retaining the
// same authentication and cooldown boundary as publication requests.
func (g *GitHub) indexCooldown(ctx context.Context, token string) error {
	g.dispatch.Lock()
	defer g.dispatch.Unlock()
	until := g.cooldowns[CooldownKey(g.BaseURL, token)]
	if op, _ := ctx.Value(operationKey{}).(*operation); op != nil && op.ledger != nil {
		var saved time.Time
		if err := op.ledger.Get(ctx, CooldownKind, CooldownKey(g.BaseURL, token), &saved); err != nil && !errors.Is(err, store.ErrNotFound) {
			return errors.New("GitHub cooldown state unavailable")
		}
		if saved.After(until) {
			until = saved
		}
	}
	if g.now().Before(until) {
		return &RateLimitError{Until: until}
	}
	return nil
}
func (g *GitHub) indexRateLimited(ctx context.Context, token string, h http.Header) error {
	err := g.rateLimited(ctx, token, "core", h)
	if g.Managed() {
		return err
	}
	var rate *RateLimitError
	if !errors.As(err, &rate) {
		return err
	}
	g.dispatch.Lock()
	defer g.dispatch.Unlock()
	key := CooldownKey(g.BaseURL, token)
	if g.cooldowns == nil {
		g.cooldowns = map[string]time.Time{}
	}
	if rate.Until.After(g.cooldowns[key]) {
		g.cooldowns[key] = rate.Until
	}
	if op, _ := ctx.Value(operationKey{}).(*operation); op != nil && op.ledger != nil {
		if saveErr := op.ledger.Write(ctx, func(t *store.Tx) error {
			var saved time.Time
			if e := t.Get(CooldownKind, key, &saved); e != nil && !errors.Is(e, store.ErrNotFound) {
				return e
			}
			if saved.After(rate.Until) {
				rate.Until = saved
			}
			return t.Put(CooldownKind, key, rate.Until)
		}); saveErr != nil {
			return errors.New("GitHub retry deadline could not be retained")
		}
	}
	return err
}
