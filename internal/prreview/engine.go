package prreview

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/dk-code-factory/agent-workflow/pkg/workflows"
)

const (
	GoActions        = workflows.RegisteredGoActionsV1
	OrderedIteration = "ordered-dynamic-iteration-v1"
	ExternalWaits    = "durable-external-input-v1"
)

var RequiredCapabilities = []string{GoActions, OrderedIteration, ExternalWaits}

// EngineAdapter is the sole assumed upstream boundary. Implementations must pin
// actions and typed schemas, checkpoint ordered child workflows and suspend waits
// without occupying workers. Native turn completion is never parent completion.
// Until upstream implements these contracts the production adapter fails closed.
type EngineAdapter interface {
	Check(context.Context) error
	Execute(context.Context, *Workflow, *Session) (Result, error)
}
type NativeAdapter struct{}

func (NativeAdapter) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return requireCapabilities(workflows.Capabilities())
}
func requireCapabilities(available []string) error {
	supported := map[string]bool{}
	for _, capability := range available {
		supported[capability] = true
	}
	missing, issues := []string{}, []string{}
	issue := map[string]int{GoActions: 25, OrderedIteration: 26, ExternalWaits: 27}
	for _, capability := range RequiredCapabilities {
		if !supported[capability] {
			missing = append(missing, capability)
			issues = append(issues, fmt.Sprintf("#%d", issue[capability]))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("PR review workflow dependency unavailable: agent-workflow requires %s (upstream issues %s); no review actions or model turns started", strings.Join(missing, ", "), strings.Join(issues, ", "))
}
func (a NativeAdapter) Execute(ctx context.Context, _ *Workflow, _ *Session) (Result, error) {
	if err := a.Check(ctx); err != nil {
		return Result{}, err
	}
	return Result{}, errors.New("native PR review execution adapter is not configured; no review actions or model turns started")
}

// FixtureAdapter executes the assumed application contract deterministically.
// It is opt-in from tests, never selected by fixture-mode environment variables.
type FixtureAdapter struct{}

func (FixtureAdapter) Check(context.Context) error { return nil }
func (FixtureAdapter) Execute(ctx context.Context, w *Workflow, s *Session) (Result, error) {
	return w.execute(ctx, s)
}

// The implementation identity is embedded at build time. Checkpoints cannot
// silently cross deterministic action implementations after an application update.
//
//go:embed artifacts.go contracts.go dk_preparation.go decisions.go evidence.go git.go patch.go preparation.go publication.go workflow.go engine.go
var implementation embed.FS

func ImplementationIdentity() string {
	entries, _ := fs.ReadDir(implementation, ".")
	sources := map[string]string{}
	for _, entry := range entries {
		b, _ := implementation.ReadFile(entry.Name())
		sources[entry.Name()] = Hash(b)
	}
	return Digest(sources)
}
