package providers

import (
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var JiraKey = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`)

func ValidJiraURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && strings.HasSuffix(u.Hostname(), ".atlassian.net") && u.Port() == "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}
func JiraTicket(ctx context.Context, base, email, token, key string) (domain.Ticket, error) {
	v := domain.Ticket{ID: key, URL: strings.TrimRight(base, "/") + "/browse/" + key}
	if !ValidJiraURL(base) || !JiraKey.MatchString(key) {
		return v, fmt.Errorf("invalid Jira Cloud configuration")
	}
	r, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/rest/api/3/issue/"+url.PathEscape(key)+"?fields=summary,description,status", nil)
	if e != nil {
		return v, e
	}
	r.SetBasicAuth(email, token)
	c := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := c.Do(r)
	if e != nil {
		return v, fmt.Errorf("Jira connection failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return v, fmt.Errorf("Jira ticket unavailable (HTTP %d)", res.StatusCode)
	}
	var body struct {
		Fields struct {
			Summary     string
			Description json.RawMessage
			Status      struct{ Name string }
		}
	}
	e = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&body)
	v.Summary = body.Fields.Summary
	v.Description = body.Fields.Description
	v.Status = body.Fields.Status.Name
	return v, e
}
