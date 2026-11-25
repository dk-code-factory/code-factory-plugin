package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestEnsureRepositoryWebhookReusesPaginatedCallback(t *testing.T) {
	const callback = "https://plugin.example/code-factory/github/webhook"
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing CLI authentication")
		}
		switch r.Method + " " + r.URL.RequestURI() {
		case "GET /repos/o/r/hooks?per_page=100":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/hooks?per_page=100&page=2>; rel="next"`, "http://"+r.Host))
			fmt.Fprint(w, `[{"id":1,"config":{"url":"https://unrelated.example/hook"}}]`)
		case "GET /repos/o/r/hooks?per_page=100&page=2":
			fmt.Fprintf(w, `[{"id":42,"config":{"url":%q}}]`, callback)
		case "PATCH /repos/o/r/hooks/42":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			config := body["config"].(map[string]any)
			if config["secret"] != "private-secret" || config["content_type"] != "json" || config["insecure_ssl"] != "0" || body["active"] != true {
				t.Error("unsafe or incomplete webhook config", body)
			}
			if body["events"] != nil || len(body["add_events"].([]any)) != 9 {
				t.Error("must preserve extra events while subscribing to receiver events")
			}
			fmt.Fprintf(w, `{"id":42,"config":{"url":%q}}`, callback)
		default:
			t.Error("unexpected request", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	g := &GitHub{BaseURL: srv.URL, Client: srv.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	id, err := g.EnsureRepositoryWebhook(context.Background(), "o/r", callback, "private-secret")
	if err != nil || id != 42 {
		t.Fatal(id, err)
	}
	if !reflect.DeepEqual(requests, []string{"GET /repos/o/r/hooks?per_page=100", "GET /repos/o/r/hooks?per_page=100&page=2", "PATCH /repos/o/r/hooks/42"}) {
		t.Fatal(requests)
	}
}

func TestEnsureRepositoryWebhookRecoversLostCreateResponse(t *testing.T) {
	const callback = "https://plugin.example/github/webhook"
	created, posts, patches := false, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			if created {
				fmt.Fprintf(w, `[{"id":8,"config":{"url":%q}}]`, callback)
			} else {
				fmt.Fprint(w, `[]`)
			}
		case "POST":
			posts++
			created = true
			var input struct {
				Events []string
				Config map[string]string
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			if len(input.Events) != 9 || input.Config["url"] != callback || input.Config["secret"] != "secret" {
				t.Error("incomplete create request")
			}
			// Commit the hook, then return a malformed response, as if it was lost.
			fmt.Fprint(w, `{`)
		case "PATCH":
			patches++
			fmt.Fprintf(w, `{"id":8,"config":{"url":%q}}`, callback)
		}
	}))
	defer srv.Close()
	g := &GitHub{BaseURL: srv.URL, Client: srv.Client(), Token: func(context.Context) (string, error) { return "fixture", nil }}
	if _, err := g.EnsureRepositoryWebhook(context.Background(), "o/r", callback, "secret"); err == nil {
		t.Fatal("lost response accepted")
	}
	if id, err := g.EnsureRepositoryWebhook(context.Background(), "o/r", callback, "secret"); err != nil || id != 8 {
		t.Fatal(id, err)
	}
	if posts != 1 || patches != 1 {
		t.Fatal("duplicate hook creation", posts, patches)
	}
}
