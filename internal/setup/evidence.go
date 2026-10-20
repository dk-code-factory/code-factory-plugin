package setup

import (
	"codefactory/internal/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Evidence is an operator-recorded checkpoint, never a conclusion inferred from
// local health. Every record is bound to the installed build and dated source.
type Evidence struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	State     State     `json:"state"`
	At        time.Time `json:"at"`
	Reference string    `json:"reference"`
}

var AcceptanceChecks = []string{"packaging", "provider", "ownership", "tunnel.readiness", "plugin.fresh-chat", "inbox", "priority", "launch", "question.answer", "follow-up", "callbacks", "subscription.lifecycle", "recovery", "rollback", "credentials", "cleanup", "soak", "login"}

func RecordEvidence(dir, id string, state State, reference string) error {
	valid := false
	for _, name := range AcceptanceChecks {
		valid = valid || name == id
	}
	if !valid {
		return fmt.Errorf("unknown acceptance checkpoint")
	}
	if state != Verified && state != Failed && state != Unverified {
		return fmt.Errorf("use verified, failed, or unverified")
	}
	if len(reference) == 0 || len(reference) > 1000 {
		return fmt.Errorf("provide a nonsecret dated evidence reference of at most 1000 characters")
	}
	return AtomicJSON(filepath.Join(dir, "acceptance", id+".json"), Evidence{ID: id, Version: buildinfo.Version, State: state, At: time.Now().UTC(), Reference: reference})
}
func EvidenceDir(dir string) string { return filepath.Join(dir, "acceptance") }
func readEvidence(dir, id string) (Evidence, error) {
	var e Evidence
	b, err := os.ReadFile(filepath.Join(EvidenceDir(dir), id+".json"))
	if err != nil {
		return e, err
	}
	err = json.Unmarshal(b, &e)
	return e, err
}
