package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// EnsureRepositoryWebhook reconciles only the hook at this exact callback URL.
// Live listing before every write also recovers a POST whose response was lost.
func (g *GitHub) EnsureRepositoryWebhook(ctx context.Context, repo, callback, secret string) (int64, error) {
	ctx = WithGitHubRead(g.StartOperation(ctx, nil, false), RequireLive)
	path := "/repos/" + repo + "/hooks"
	type hook struct {
		ID     int64 `json:"id"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	var match int64
	seen := map[string]bool{}
	for next := path + "?per_page=100"; next != ""; {
		if seen[next] {
			return 0, fmt.Errorf("GitHub webhook pagination repeated")
		}
		seen[next] = true
		body, headers, err := g.Request(ctx, http.MethodGet, next, nil, "")
		if err != nil {
			return 0, err
		}
		var hooks []hook
		if err = json.Unmarshal(body, &hooks); err != nil {
			return 0, fmt.Errorf("GitHub webhook list is invalid")
		}
		for _, h := range hooks {
			if h.Config.URL == callback {
				if h.ID <= 0 || match != 0 {
					return 0, fmt.Errorf("GitHub callback has ambiguous webhook identities")
				}
				match = h.ID
			}
		}
		next, err = g.NextPage(headers)
		if err != nil {
			return 0, err
		}
	}
	// CI subscriptions match the receiver's four supported CI event types.
	events := []string{"pull_request", "pull_request_review", "pull_request_review_comment", "pull_request_review_thread", "issue_comment", "check_run", "check_suite", "status", "workflow_run"}
	input := map[string]any{"active": true, "config": map[string]string{"url": callback, "content_type": "json", "insecure_ssl": "0", "secret": secret}}
	method := http.MethodPost
	if match == 0 {
		input["name"], input["events"] = "web", events
	} else {
		method, path = http.MethodPatch, fmt.Sprintf("%s/%d", path, match)
		input["add_events"] = events
	}
	body, _, err := g.Request(ctx, method, path, input, "")
	if err != nil {
		return 0, err
	}
	var saved hook
	if json.Unmarshal(body, &saved) != nil || saved.ID <= 0 || saved.Config.URL != callback || match != 0 && saved.ID != match {
		return 0, fmt.Errorf("GitHub webhook response is invalid; reconciliation will retry")
	}
	return saved.ID, nil
}
