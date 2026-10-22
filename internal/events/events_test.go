package events

import (
	"codefactory/internal/domain"
	"codefactory/internal/store"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func eventFixture(t *testing.T) (*Service, context.Context) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	v, e := store.NewVault(s, dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Put(ctx, "config", "settings", domain.Defaults(), ""); e != nil {
		t.Fatal(e)
	}
	return New(s, v), ctx
}
func TestSubscriptionVerificationRetryDedupRefreshAndExpiry(t *testing.T) {
	s, ctx := eventFixture(t)
	secret := "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	var delivered []string
	fail := true
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		expected, e := Sign(secret, r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), b)
		if e != nil || expected != r.Header.Get("webhook-signature") {
			t.Fatal("invalid signature", e)
		}
		var body map[string]any
		if e = json.Unmarshal(b, &body); e != nil {
			t.Fatal(e)
		}
		status := 200
		response := `{}`
		if body["type"] == "verification" {
			response = fmt.Sprintf(`{"challenge":%q}`, body["challenge"])
		} else {
			delivered = append(delivered, body["eventId"].(string))
			if fail {
				status = 503
				fail = false
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	in := SubscribeInput{Name: "attention.required", Arguments: map[string]string{"entity": "run-1"}, Delivery: Delivery{Mode: "webhook", URL: "https://callback.example/events", Secret: secret}}
	res, e := s.Subscribe(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Subscribe(ctx, in)
	if e != nil || again["id"] != res["id"] {
		t.Fatal("idempotency", e)
	}
	id := res["id"].(string)
	if e = s.Store.Write(ctx, func(tx *store.Tx) error {
		if e := tx.Event("attention.required", "run-2", nil); e != nil {
			return e
		}
		return tx.Event("attention.required", "run-1", nil)
	}); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	var sub Subscription
	_ = s.Store.Get(ctx, "subscription", id, &sub)
	if len(delivered) != 1 || sub.Attempts != 1 {
		t.Fatal("retry missing", sub, delivered)
	}
	sub.RetryAt = time.Now().Add(-time.Second)
	if e = s.Store.Put(ctx, "subscription", id, sub, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	if len(delivered) != 2 || delivered[0] != delivered[1] {
		t.Fatal("retry event ID changed", delivered)
	}
	if e = s.Tick(ctx); e != nil || len(delivered) != 2 {
		t.Fatal("acknowledged delivery repeated", e)
	}
	_ = s.Store.Get(ctx, "subscription", id, &sub)
	sub.Expires = time.Now().Add(-time.Second)
	_ = s.Store.Put(ctx, "subscription", id, sub, "")
	_ = s.Store.Write(ctx, func(tx *store.Tx) error { return tx.Event("attention.required", "run-1", nil) })
	_ = s.Tick(ctx)
	if len(delivered) != 2 {
		t.Fatal("expired subscription delivered")
	}
	if e = s.Unsubscribe(ctx, in); e != nil {
		t.Fatal(e)
	}
	if e = s.Unsubscribe(ctx, in); e != nil {
		t.Fatal("unsubscribe must be idempotent", e)
	}
}
func TestCallbackSafetyAndFailedChallenge(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "2001:db8::1", "224.0.0.1"} {
		if PublicIP(net.ParseIP(ip)) {
			t.Fatal("allowed non-public address", ip)
		}
	}
	if !PublicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("blocked public address")
	}
	for _, u := range []string{"http://example.com", "https://user@example.com", "https://example.com:8443"} {
		if ValidateURL(u) == nil {
			t.Fatal("invalid URL accepted", u)
		}
	}
	s, ctx := eventFixture(t)
	s.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"challenge":"wrong"}`))}, nil
	})}
	_, e := s.Subscribe(ctx, SubscribeInput{Name: "inbox.changed", Delivery: Delivery{Mode: "webhook", URL: "https://callback.example/", Secret: "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))}})
	if e == nil {
		t.Fatal("wrong challenge accepted")
	}
	subs, _ := store.List[Subscription](ctx, s.Store, "subscription")
	if len(subs) != 0 {
		t.Fatal("failed verification persisted subscription")
	}
}

func TestPRMonitoringAndRecoveredQuestionsUseMetadataOnlyNotifications(t *testing.T) {
	s, ctx := eventFixture(t)
	secret := "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	names := map[string]int{}
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var body map[string]any
		if err = json.Unmarshal(b, &body); err != nil {
			return nil, err
		}
		response := "{}"
		if body["type"] == "verification" {
			response = fmt.Sprintf(`{"challenge":%q}`, body["challenge"])
		} else {
			if strings.Contains(string(b), "private-question-context") {
				t.Error("notification included private content")
			}
			names[body["name"].(string)]++
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	for _, name := range []string{"inbox.changed", "attention.required"} {
		if _, err := s.Subscribe(ctx, SubscribeInput{Name: name, Delivery: Delivery{Mode: "webhook", URL: "https://callback.example/events", Secret: secret}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Event("pr.changed", "org/repo#1", map[string]string{"context": "private-question-context"}); err != nil {
			return err
		}
		return tx.Event("attention.required", "run-1", map[string]string{"origin": "text", "context": "private-question-context"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if names["inbox.changed"] != 1 || names["attention.required"] != 1 {
		t.Fatal("monitoring or recovered question alert was lost or duplicated")
	}
}
