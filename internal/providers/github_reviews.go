package providers

import (
	"codefactory/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// PublishedReviews uses the same authenticated, paginated and budgeted reads as
// inbox synchronization. Pending drafts are not publication receipts.
func (g *GitHub) PublishedReviews(ctx context.Context, item domain.Item) ([]domain.PublishedReview, time.Time, error) {
	ctx, observed := observeReads(ctx, g.now())
	path := fmt.Sprintf("/repos/%s/pulls/%d", item.Repository, item.Number)
	rows, err := g.pages(ctx, path+"/reviews?per_page=100")
	if err != nil {
		return nil, time.Time{}, err
	}
	reviews := []domain.PublishedReview{}
	byID := map[string]int{}
	for _, row := range rows {
		var r struct {
			ID          json.Number
			HTMLURL     string `json:"html_url"`
			User        domain.Actor
			State, Body string
			CommitID    string `json:"commit_id"`
			SubmittedAt string `json:"submitted_at"`
		}
		if err := json.Unmarshal(row, &r); err != nil {
			return nil, time.Time{}, err
		}
		if r.SubmittedAt == "" || r.State == "PENDING" {
			continue
		}
		switch r.State {
		case "COMMENTED", "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
		default:
			continue
		}
		if r.ID.String() == "" || r.HTMLURL == "" || r.CommitID == "" {
			return nil, time.Time{}, fmt.Errorf("Incomplete GitHub review receipt")
		}
		byID[r.ID.String()] = len(reviews)
		reviews = append(reviews, domain.PublishedReview{ID: r.ID.String(), URL: r.HTMLURL, Author: r.User.Login, State: r.State, Body: r.Body, Head: r.CommitID, SubmittedAt: r.SubmittedAt, Comments: []domain.ReviewComment{}})
	}
	rows, err = g.pages(ctx, path+"/comments?per_page=100")
	if err != nil {
		return nil, time.Time{}, err
	}
	for _, row := range rows {
		var c struct {
			ID         json.Number
			ReviewID   json.Number `json:"pull_request_review_id"`
			HTMLURL    string      `json:"html_url"`
			User       domain.Actor
			Body, Path string
			Line       int
		}
		if err := json.Unmarshal(row, &c); err != nil {
			return nil, time.Time{}, err
		}
		if n, ok := byID[c.ReviewID.String()]; ok {
			reviews[n].Comments = append(reviews[n].Comments, domain.ReviewComment{ID: c.ID.String(), URL: c.HTMLURL, Author: c.User.Login, Body: c.Body, Path: c.Path, Line: c.Line})
		}
	}
	sort.SliceStable(reviews, func(i, j int) bool { return reviews[i].SubmittedAt > reviews[j].SubmittedAt })
	return reviews, *observed, nil
}
