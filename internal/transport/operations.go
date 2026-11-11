package transport

import (
	"codefactory/internal/app"
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Leave time for MCP encoding, authentication and the HTTP response within the
// 100 ms local response budget. Slow work keeps its original execution identity.
const operationInlineBudget = 25 * time.Millisecond
const operationTimeout = 5 * time.Minute
const operationRetention = 10 * time.Minute
const operationCapacity = 128

type operationResult struct {
	value any
	err   error
}
type pendingOperation struct {
	id, tool string
	done     chan struct{}
	result   operationResult
	finished time.Time
}
type operations struct {
	ctx     context.Context
	mu      sync.Mutex
	entries map[string]*pendingOperation
}

func (o *operations) prune(now time.Time) {
	for id, entry := range o.entries {
		if !entry.finished.IsZero() && now.Sub(entry.finished) >= operationRetention {
			delete(o.entries, id)
		}
	}
}

func toolResult(name string, result operationResult) (*mcp.CallToolResult, map[string]any, error) {
	if result.err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: result.err.Error()}}}, nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name + " returned saved state or operation result; inspect structuredContent.data."}}}, map[string]any{"data": result.value}, nil
}
func operationTicket(entry *pendingOperation) (*mcp.CallToolResult, map[string]any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Operation accepted and still running. Call operation_result with id " + entry.id + " to retrieve its result. Do not repeat the original action."}}}, map[string]any{"operation": map[string]any{"id": entry.id, "tool": entry.tool, "state": "running"}}, nil
}

func (o *operations) run(ctx context.Context, name string, work func(context.Context) (any, error)) (*mcp.CallToolResult, map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return toolResult(name, operationResult{err: err})
	}
	if err := o.ctx.Err(); err != nil {
		return toolResult(name, operationResult{err: err})
	}
	o.mu.Lock()
	o.prune(time.Now())
	if len(o.entries) >= operationCapacity {
		o.mu.Unlock()
		return toolResult(name, operationResult{err: fmt.Errorf("operation capacity reached; no action was started")})
	}
	entry := &pendingOperation{id: app.ID(), tool: name, done: make(chan struct{})}
	o.entries[entry.id] = entry
	o.mu.Unlock()
	// HTTP cancellation must never turn an accepted mutation into a replay. The
	// daemon lifetime and a fixed timeout, rather than a polling request, own it.
	background, cancel := context.WithTimeout(context.WithoutCancel(ctx), operationTimeout)
	stop := context.AfterFunc(o.ctx, cancel)
	if o.ctx.Err() != nil {
		cancel()
	}
	go func() {
		defer cancel()
		defer stop()
		result := operationResult{}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					// Owner diagnostics retain the failure site and identity without
					// formatting panic values, which may contain private payloads.
					slog.Error("Code Factory operation panicked", "operation_id", entry.id,
						"tool", entry.tool, "panic_type", fmt.Sprintf("%T", recovered),
						"stack", string(debug.Stack()))
					result = operationResult{err: fmt.Errorf("operation failed unexpectedly; inspect saved state before retrying")}
				}
			}()
			if err := background.Err(); err != nil {
				result.err = err
				return
			}
			result.value, result.err = work(background)
		}()
		o.mu.Lock()
		entry.result = result
		entry.finished = time.Now()
		close(entry.done)
		o.mu.Unlock()
	}()
	timer := time.NewTimer(operationInlineBudget)
	defer timer.Stop()
	select {
	case <-entry.done:
		o.mu.Lock()
		delete(o.entries, entry.id)
		o.mu.Unlock()
		return toolResult(name, entry.result)
	case <-timer.C:
		return operationTicket(entry)
	case <-ctx.Done():
		// The operation remains discoverable if its ticket was already received.
		return operationTicket(entry)
	}
}

func (o *operations) poll(id string) (*mcp.CallToolResult, map[string]any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.prune(time.Now())
	entry, ok := o.entries[id]
	if !ok {
		return toolResult("operation_result", operationResult{err: fmt.Errorf("operation result is unavailable (expired or daemon restarted); inspect saved state before retrying")})
	}
	select {
	case <-entry.done:
		return toolResult(entry.tool, entry.result)
	default:
		return operationTicket(entry)
	}
}

func registerOperations(s *Server, ctx context.Context) {
	s.operations = &operations{ctx: ctx, entries: map[string]*pendingOperation{}}
	mcp.AddTool(s.MCP, &mcp.Tool{Name: "operation_result", Description: "Read an accepted operation by its exact id. Pending results remain pending; completed results and errors can be read for ten minutes. Polling never repeats an action. After a daemon restart or expiry inspect saved state before retrying.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Meta: mcp.Meta{"ui": map[string]any{"visibility": []string{"app", "model"}}}}, func(_ context.Context, _ *mcp.CallToolRequest, in IDInput) (*mcp.CallToolResult, map[string]any, error) {
		return s.operations.poll(in.ID)
	})
}
