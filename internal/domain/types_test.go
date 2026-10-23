package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestItemFreshnessKeepsLegacyContentRevision(t *testing.T) {
	i := Item{ID: "o/r#1", Repository: "o/r", Number: 1, Kind: "issue", Title: "Existing item", Body: "Retained body", State: "open", Labels: []string{}, Assignees: []string{}, Activity: []Activity{}}
	raw, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	// This is precisely the pre-freshness serialized item; retain its field order.
	legacy := bytes.ReplaceAll(raw, []byte(`"detailSyncedAt":"0001-01-01T00:00:00Z",`), nil)
	legacy = bytes.ReplaceAll(legacy, []byte(`"reviewObservedAt":"0001-01-01T00:00:00Z",`), nil)
	sum := sha256.Sum256(legacy)
	expected := hex.EncodeToString(sum[:])
	if got := i.ContentRevision(); got != expected {
		t.Fatal("metadata changed pre-existing revision", got, expected)
	}
	i.DetailSyncedAt = time.Now()
	i.ReviewObservedAt = time.Now()
	i.SummaryOnly = true
	if i.ContentRevision() != expected {
		t.Fatal("freshness or read projection changed content identity")
	}
	i.Body += " updated"
	if i.ContentRevision() == expected {
		t.Fatal("actual content change did not advance revision")
	}
}
