package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ProbeDynamicTools checks the experimental schema without creating a thread or
// making a model call. A stable version number alone does not prove this surface.
func ProbeDynamicTools(ctx context.Context, binary string) error {
	dir, err := os.MkdirTemp("", "code-factory-protocol-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err = exec.CommandContext(ctx, binary, "app-server", "generate-json-schema", "--experimental", "--out", dir).Run(); err != nil {
		return fmt.Errorf("Codex experimental protocol schema unavailable: %w", err)
	}
	for name, fields := range map[string][]string{"ThreadStartParams.json": {"dynamicTools"}, "DynamicToolCallParams.json": {"threadId", "turnId", "callId", "tool", "arguments"}, "DynamicToolCallResponse.json": {"contentItems", "success"}, "ConfigBatchWriteParams.json": {"expectedVersion", "edits", "reloadUserConfig"}} {
		path := filepath.Join(dir, "v2", name)
		b, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			b, e = os.ReadFile(filepath.Join(dir, name))
		}
		if e != nil {
			return fmt.Errorf("Codex schema %s unavailable", name)
		}
		var schema struct{ Properties map[string]json.RawMessage }
		if json.Unmarshal(b, &schema) != nil {
			return fmt.Errorf("invalid Codex schema %s", name)
		}
		for _, field := range fields {
			if _, ok := schema.Properties[field]; !ok {
				return fmt.Errorf("Codex schema lacks %s.%s", name, field)
			}
		}
	}
	return nil
}
