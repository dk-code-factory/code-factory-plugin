package prreview

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/dk-code-factory/agent-workflow/pkg/workflows"
)

const (
	EngineRevision   = "0606a1b9d16e8741bab684233ab49b96f34ea02e"
	GoActions        = workflows.RegisteredGoActionsV1
	OrderedIteration = "ordered-dynamic-iteration-v1"
	ExternalWaits    = "durable-external-input-v1"
)

var RequiredCapabilities = []string{GoActions, OrderedIteration, ExternalWaits}

// EngineAdapter owns the upstream execution boundary. Implementations must pin
// actions and typed schemas, checkpoint ordered child workflows and suspend waits
// without occupying workers. Native turn completion is never parent completion.
// Runtime dependencies are checked before evidence capture or model execution.
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

// FixtureAdapter executes the application stages without the SDK journal.
// It is opt-in from tests, never selected by fixture-mode environment variables.
type FixtureAdapter struct{}

func (FixtureAdapter) Check(context.Context) error { return nil }
func (FixtureAdapter) Execute(ctx context.Context, w *Workflow, s *Session) (Result, error) {
	return w.execute(ctx, s)
}

// The implementation identity is embedded at build time. Checkpoints cannot
// silently cross deterministic action implementations after an application update.
//
//go:embed artifacts.go contracts.go dk_preparation.go decisions.go evidence.go git.go patch.go preparation.go publication.go workflow.go engine.go native.go workflows/*.yml
var implementation embed.FS

func ImplementationIdentity() string {
	sources := map[string]string{"engine-revision": EngineRevision}
	_ = fs.WalkDir(implementation, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			data, err := implementation.ReadFile(path)
			if err != nil {
				return err
			}
			sources[path] = Hash(data)
		}
		return nil
	})
	return Digest(sources)
}
