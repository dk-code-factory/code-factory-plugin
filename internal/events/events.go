// Package events implements the Work MCP Events webhook contract, independently of domain state.
package events

import (
	"bytes"
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Subscription struct {
	VerifiedAt  time.Time         `json:"verifiedAt"`
	DeliveredAt time.Time         `json:"deliveredAt"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Arguments   map[string]string `json:"arguments"`
	URL         string            `json:"url"`
	Expires     time.Time         `json:"expires"`
	Cursor      int64             `json:"cursor"`
	Attempts    int               `json:"attempts"`
	RetryAt     time.Time         `json:"retryAt"`
	Disabled    bool              `json:"disabled"`
	Error       string            `json:"error,omitempty"`
	RotateUntil time.Time         `json:"rotateUntil"`
}
type Delivery struct {
	Mode   string `json:"mode"`
	URL    string `json:"url"`
	Secret string `json:"secret"`
}
type SubscribeInput struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments"`
	Delivery  Delivery          `json:"delivery"`
	TTL       *int64            `json:"ttlMs"`
	Cursor    *string           `json:"cursor"`
}
type Service struct {
	Store    *store.Store
	Vault    *store.Vault
	Client   *http.Client
	mu       sync.Mutex
	verified map[string]time.Time
}

func New(s *store.Store, v *store.Vault) *Service {
	return &Service{Store: s, Vault: v, Client: SafeClient(), verified: map[string]time.Time{}}
}

var denied = []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/23", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"}

func PublicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range denied {
		if netip.MustParsePrefix(s).Contains(a) {
			return false
		}
	}
	return true
}
func ValidateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("callback must be a public HTTPS URL on port 443")
	}
	return nil
}
func SafeClient() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, fmt.Errorf("callback DNS unavailable")
		}
		for _, ip := range ips {
			if !PublicIP(ip) {
				return nil, fmt.Errorf("callback resolves to a non-public address")
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func key(secret string) ([]byte, error) {
	if !strings.HasPrefix(secret, "whsec_") {
		return nil, fmt.Errorf("invalid signing key")
	}
	b, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if e != nil || len(b) < 24 || len(b) > 64 {
		return nil, fmt.Errorf("signing key must decode to 24–64 bytes")
	}
	return b, nil
}
func Sign(secret, id, ts string, body []byte) (string, error) {
	k, e := key(secret)
	if e != nil {
		return "", e
	}
	h := hmac.New(sha256.New, k)
	h.Write([]byte(id + "." + ts + "."))
	h.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
func identity(in SubscribeInput) string {
	b, _ := json.Marshal(in.Arguments)
	h := sha256.Sum256([]byte("owner\n" + in.Delivery.URL + "\n" + in.Name + "\n" + string(b)))
	return "sub_" + hex.EncodeToString(h[:16])
}
func validate(in SubscribeInput) error {
	if in.Name != "inbox.changed" && in.Name != "attention.required" && in.Name != "run.state" {
		return fmt.Errorf("unknown event")
	}
	if in.Delivery.Mode != "webhook" {
		return fmt.Errorf("only webhook delivery is supported")
	}
	for k := range in.Arguments {
		if k != "entity" {
			return fmt.Errorf("unsupported event filter")
		}
	}
	return ValidateURL(in.Delivery.URL)
}
func (s *Service) post(ctx context.Context, sub Subscription, secret, id string, body []byte) (*http.Response, error) {
	if len(body) > 256<<10 {
		return nil, fmt.Errorf("event too large")
	}
	if e := ValidateURL(sub.URL); e != nil {
		return nil, e
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig, e := Sign(secret, id, ts, body)
	if e != nil {
		return nil, e
	}
	if time.Now().Before(sub.RotateUntil) {
		old, e := s.Vault.Get(ctx, sub.ID+":previous")
		if e == nil {
			extra, e := Sign(old, id, ts, body)
			if e == nil {
				sig += " " + extra
			}
		}
	}
	req, e := http.NewRequestWithContext(ctx, "POST", sub.URL, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", ts)
	req.Header.Set("webhook-signature", sig)
	req.Header.Set("X-MCP-Subscription-Id", sub.ID)
	return s.Client.Do(req)
}
func (s *Service) Subscribe(ctx context.Context, in SubscribeInput) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Arguments == nil {
		in.Arguments = map[string]string{}
	}
	if e := validate(in); e != nil {
		return nil, e
	}
	if _, e := key(in.Delivery.Secret); e != nil {
		return nil, e
	}
	ttl := 24 * time.Hour
	if in.TTL != nil {
		if *in.TTL <= 0 {
			return nil, fmt.Errorf("ttlMs must be positive")
		}
		if *in.TTL < int64(ttl/time.Millisecond) {
			ttl = time.Duration(*in.TTL) * time.Millisecond
		}
		if ttl < time.Minute {
			ttl = time.Minute
		}
	}
	sub := Subscription{ID: identity(in), Name: in.Name, Arguments: in.Arguments, URL: in.Delivery.URL, Expires: time.Now().UTC().Add(ttl), Cursor: s.Store.Latest(ctx)}
	var old Subscription
	if e := s.Store.Get(ctx, "subscription", sub.ID, &old); e == nil {
		sub.Cursor = old.Cursor
		sub.DeliveredAt = old.DeliveredAt
	}
	cacheHash := sha256.Sum256([]byte(in.Delivery.URL + "\n" + in.Delivery.Secret))
	cacheKey := hex.EncodeToString(cacheHash[:])
	if time.Now().After(s.verified[cacheKey]) {
		challengeBytes := make([]byte, 32)
		if _, e := rand.Read(challengeBytes); e != nil {
			return nil, e
		}
		challenge := hex.EncodeToString(challengeBytes)
		body, _ := json.Marshal(map[string]string{"type": "verification", "challenge": challenge})
		res, e := s.post(ctx, sub, in.Delivery.Secret, "verify_"+challenge, body)
		if e != nil {
			return nil, &CallbackError{Reason: "connection_failed"}
		}
		defer res.Body.Close()
		var got struct{ Challenge string }
		if res.StatusCode < 200 || res.StatusCode >= 300 || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&got) != nil || subtle.ConstantTimeCompare([]byte(got.Challenge), []byte(challenge)) != 1 {
			return nil, &CallbackError{Reason: "challenge_failed"}
		}
		if s.verified == nil {
			s.verified = map[string]time.Time{}
		}
		for k, until := range s.verified {
			if time.Now().After(until) {
				delete(s.verified, k)
			}
		}
		if len(s.verified) >= 128 {
			for k := range s.verified {
				delete(s.verified, k)
				break
			}
		}
		s.verified[cacheKey] = time.Now().Add(10 * time.Minute)
	}

	sub.VerifiedAt = time.Now().UTC()
	if previous, e := s.Vault.Get(ctx, sub.ID); e == nil && previous != in.Delivery.Secret {
		if e = s.Vault.Set(ctx, sub.ID+":previous", previous); e != nil {
			return nil, e
		}
		sub.RotateUntil = time.Now().Add(5 * time.Minute)
	}
	if e := s.Vault.Set(ctx, sub.ID, in.Delivery.Secret); e != nil {
		return nil, e
	}
	if e := s.Store.Put(ctx, "subscription", sub.ID, sub, ""); e != nil {
		return nil, e
	}
	return map[string]any{"id": sub.ID, "refreshBefore": sub.Expires, "cursor": nil, "truncated": false}, nil
}
func (s *Service) Unsubscribe(ctx context.Context, in SubscribeInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Arguments == nil {
		in.Arguments = map[string]string{}
	}
	if e := validate(in); e != nil {
		return e
	}
	return s.Store.Write(ctx, func(t *store.Tx) error { return t.Delete("subscription", identity(in)) })
}
func Definitions() []map[string]any {
	out := []map[string]any{}
	for _, v := range []struct{ name, desc string }{{"inbox.changed", "An issue or PR changed in the local inbox."}, {"attention.required", "An agent needs an answer or approval."}, {"run.state", "An agent changed execution state."}} {
		out = append(out, map[string]any{"name": v.name, "description": v.desc, "delivery": []string{"webhook"}, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"entity": map[string]string{"type": "string", "description": "Optional exact item or run ID"}}, "additionalProperties": false}, "payloadSchema": map[string]any{"type": "object", "properties": map[string]any{"entity": map[string]string{"type": "string"}, "kind": map[string]string{"type": "string"}}, "required": []string{"entity", "kind"}, "additionalProperties": false}})
	}
	return out
}
func (s *Service) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs, e := store.List[Subscription](ctx, s.Store, "subscription")
	if e != nil {
		return e
	}
	var config domain.Config
	if e = s.Store.Get(ctx, "config", "settings", &config); e != nil {
		return e
	}
	for _, sub := range subs {
		if sub.Disabled || time.Now().After(sub.Expires) || time.Now().Before(sub.RetryAt) {
			continue
		}
		evs, e := s.Store.Events(ctx, sub.Cursor, 100)
		if e != nil {
			return e
		}
		for _, ev := range evs {
			name := ev.Kind
			if name == "item.changed" {
				name = "inbox.changed"
			}
			match := name == sub.Name && (sub.Arguments["entity"] == "" || sub.Arguments["entity"] == ev.Entity) && config.Notifications
			if match {
				secret, e := s.Vault.Get(ctx, sub.ID)
				if e != nil {
					return e
				}
				id := fmt.Sprintf("evt_%d_%s", ev.Seq, sub.ID)
				body, _ := json.Marshal(map[string]any{"eventId": id, "name": name, "timestamp": ev.At, "data": map[string]string{"entity": ev.Entity, "kind": ev.Kind}, "cursor": nil})
				res, err := s.post(ctx, sub, secret, id, body)
				status := 0
				if res != nil {
					status = res.StatusCode
					io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
					res.Body.Close()
				}
				if err == nil && status >= 200 && status < 300 {
					sub.DeliveredAt = time.Now().UTC()
					sub.Error = ""
				}
				if err != nil || status < 200 || status >= 300 {
					sub.Attempts++
					sub.Error = "Webhook delivery failed"
					if status == 410 {
						sub.Disabled = true
					}
					if (err != nil || status == 429 || status >= 500) && !sub.Disabled && sub.Attempts < 8 {
						sub.RetryAt = time.Now().Add(time.Second * time.Duration(1<<sub.Attempts))
						break
					}
				}
			}
			sub.Cursor = ev.Seq
			if sub.Disabled {
				break
			}
			sub.Attempts = 0
			sub.RetryAt = time.Time{}
		}
		if e := s.Store.Put(ctx, "subscription", sub.ID, sub, ""); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) Loop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.Tick(ctx)
		}
	}
}

type CallbackError struct{ Reason string }

func (e *CallbackError) Error() string { return "callback verification failed: " + e.Reason }
