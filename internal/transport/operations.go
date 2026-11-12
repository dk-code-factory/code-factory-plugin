package transport

import (
	"codefactory/internal/app"
	"codefactory/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

const APIVersion = 2
const pendingLimit = 256
const payloadLimit = 64 << 20
const ordinaryWorkers = 8
const controlWorkers = 2

type operation struct {
	RequestID         string `json:"requestId"`
	InstanceID        string `json:"instanceId"`
	Tool              string `json:"tool"`
	State             string `json:"state"`
	Result            any    `json:"result,omitempty"`
	Error             string `json:"error,omitempty"`
	ResultUnavailable bool   `json:"resultUnavailable,omitempty"`
	ErrorDocument     any    `json:"errorDocument,omitempty"`
	ErrorUnavailable  bool   `json:"errorUnavailable,omitempty"`
	retainedBytes     int
}

// Journal records deliberately exclude arguments, answer bodies, errors and results.
type operationMetadata struct {
	RequestID  string `json:"requestId"`
	InstanceID string `json:"instanceId"`
	Tool       string `json:"tool"`
	State      string `json:"state"`
}
type operationTask struct {
	key, scope, target string
	control            bool
	size               int
	run                func(context.Context) (any, error)
}
type operationQueue struct {
	mu               sync.Mutex
	instance         string
	db               *store.Store
	ctx              context.Context
	operations       map[string]operation
	pending          []operationTask
	active           map[string]bool
	bytes            int
	wake             chan struct{}
	recovered        bool
	documents        *documentCache
	resultBytes      int
	resultOrder      []string
	executionTimeout time.Duration
}

func newOperationQueue(db *store.Store, documents *documentCache) *operationQueue {
	q := &operationQueue{instance: app.ID(), db: db, ctx: db.Context(), operations: map[string]operation{}, active: map[string]bool{}, wake: make(chan struct{}), documents: documents, executionTimeout: 5 * time.Minute}
	go q.recover()
	for n := 0; n < ordinaryWorkers; n++ {
		go q.worker(false)
	}
	for n := 0; n < controlWorkers; n++ {
		go q.worker(true)
	}
	return q
}
func (q *operationQueue) signalLocked() { close(q.wake); q.wake = make(chan struct{}) }
func (q *operationQueue) signal()       { q.mu.Lock(); q.signalLocked(); q.mu.Unlock() }

func operationKey(instance, id string) string { return instance + "/" + id }
func validRequestID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (q *operationQueue) submit(instance, id, name, scope string, control bool, size int, run func(context.Context) (any, error), target ...string) (any, error) {
	if !validRequestID(id) || !validRequestID(instance) {
		return nil, fmt.Errorf("requestId and instanceId from bootstrap are required (1–128 ASCII letters, digits, hyphen or underscore)")
	}
	if !q.mu.TryLock() {
		return map[string]any{"state": "busy"}, nil
	}
	defer q.mu.Unlock()
	key := operationKey(instance, id)
	if op, ok := q.operations[key]; ok {
		return q.receiptLocked(op), nil
	}
	if instance != q.instance {
		return operation{RequestID: id, InstanceID: instance, State: q.missingState()}, nil
	}
	if q.ctx.Err() != nil || len(q.pending) >= pendingLimit || q.bytes+size > payloadLimit {
		return map[string]any{"state": "busy"}, nil
	}
	op := operation{RequestID: id, InstanceID: instance, Tool: name, State: "queued"}
	q.operations[key] = op
	subject := scope
	if len(target) > 0 {
		subject = target[0]
	}
	q.pending = append(q.pending, operationTask{key: key, scope: scope, target: subject, control: control, size: size, run: run})
	q.bytes += size
	q.signalLocked()
	return op, nil
}
func (q *operationQueue) missingState() string {
	if !q.recovered {
		return "pending"
	}
	return "lost"
}
func (q *operationQueue) status(instance, id string) any {
	if !q.mu.TryLock() {
		return map[string]any{"state": "busy"}
	}
	defer q.mu.Unlock()
	if op, ok := q.operations[operationKey(instance, id)]; ok {
		return q.receiptLocked(op)
	}
	return operation{RequestID: id, InstanceID: instance, State: q.missingState()}
}
func (q *operationQueue) receiptLocked(op operation) any {
	available, busy := q.documents.availability(op.Result)
	if busy {
		return map[string]any{"state": "busy"}
	}
	if !available {
		op.Result = nil
		op.ResultUnavailable = true
	}
	available, busy = q.documents.availability(op.ErrorDocument)
	if busy {
		return map[string]any{"state": "busy"}
	}
	if !available {
		op.Error, op.ErrorDocument, op.ErrorUnavailable = "", nil, true
	}
	return op
}
func (q *operationQueue) recover() {
	rows, err := store.List[operationMetadata](q.ctx, q.db, "plugin-api-operation")
	if err != nil {
		return
	} // Unknown recovery stays pending; never authorize a replay.
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, row := range rows {
		if row.InstanceID == q.instance {
			continue
		}
		state := row.State
		if state == "running" || state == "queued" {
			state = "uncertain"
		}
		q.operations[operationKey(row.InstanceID, row.RequestID)] = operation{RequestID: row.RequestID, InstanceID: row.InstanceID, Tool: row.Tool, State: state, ResultUnavailable: true, ErrorUnavailable: state == "failed" || state == "uncertain"}
	}
	q.recovered = true
}
func (q *operationQueue) take(control bool) (operationTask, bool, <-chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for n, task := range q.pending {
		if task.control != control || (!control && q.active[task.scope]) {
			continue
		}
		copy(q.pending[n:], q.pending[n+1:])
		last := len(q.pending) - 1
		q.pending[last] = operationTask{}
		q.pending = q.pending[:last]
		if !control {
			q.active[task.scope] = true
		}
		op := q.operations[task.key]
		op.State = "running"
		q.operations[task.key] = op
		return task, true, q.wake
	}
	return operationTask{}, false, q.wake
}
func (q *operationQueue) worker(control bool) {
	for {
		if q.ctx.Err() != nil {
			return
		}
		task, ok, wake := q.take(control)
		if !ok {
			select {
			case <-q.ctx.Done():
				return
			case <-wake:
			}
			continue
		}
		q.execute(task)
	}
}
func (q *operationQueue) execute(task operationTask) {
	q.mu.Lock()
	op := q.operations[task.key]
	timeout := q.executionTimeout
	q.mu.Unlock()
	ctx, cancel := context.WithTimeout(q.ctx, timeout)
	defer cancel()
	meta := operationMetadata{op.RequestID, op.InstanceID, op.Tool, "running"}
	// A durable intent precedes every possible external effect. No mutation replay.
	err := q.db.Put(ctx, "plugin-api-operation", task.key, meta, "")
	var result any
	state := "failed"
	if err == nil {
		state = "uncertain" // panic/cancellation after intent is conservatively uncertain.
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.Error("Code Factory operation panicked", "operation_id", op.RequestID, "tool", op.Tool, "panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
					err = fmt.Errorf("operation interrupted; inspect saved state before retrying")
				}
			}()
			result, err = task.run(ctx)
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			if err == nil {
				state = "succeeded"
			} else {
				state = "failed"
			}
		}()
		// Factory delivery state is authoritative for ambiguous provider responses.
		if err != nil && (ctx.Err() != nil || uncertainOperation(q.ctx, q.db, op, task.target)) {
			state = "uncertain"
		}
	}
	if state == "succeeded" {
		result, err = q.documents.prepare(result)
		if err != nil {
			state = "uncertain"
		}
	}
	meta.State = state
	if persistErr := q.db.Put(q.ctx, "plugin-api-operation", task.key, meta, ""); persistErr != nil && state == "succeeded" {
		state = "uncertain"
		err = fmt.Errorf("completion metadata unavailable; inspect saved state")
	}
	op.State = state
	op.Result = result
	if err != nil {
		result = nil
		op.Result = nil
		if len(err.Error()) > 4096 {
			op.Error = "Operation failed; inspect errorDocument for the complete error."
			op.ErrorDocument, _ = q.documents.prepare(map[string]string{"error": err.Error()})
			if op.ErrorDocument == nil {
				op.Error = "Operation failed; error detail unavailable; inspect saved state."
			}
		} else {
			op.Error = err.Error()
		}
	}
	op.retainedBytes = operationPayloadBytes(op)
	q.mu.Lock()
	q.operations[task.key] = op
	if op.retainedBytes > 0 {
		q.resultBytes += op.retainedBytes
		q.resultOrder = append(q.resultOrder, task.key)
		q.pruneResultsLocked(payloadLimit)
	}
	q.bytes -= task.size
	if !task.control {
		delete(q.active, task.scope)
	}
	q.mu.Unlock()
	q.signal()
}

func operationPayloadBytes(op operation) int {
	if op.Result == nil && op.Error == "" && op.ErrorDocument == nil {
		return 0
	}
	data, err := json.Marshal(struct {
		Result        any    `json:"result,omitempty"`
		Error         string `json:"error,omitempty"`
		ErrorDocument any    `json:"errorDocument,omitempty"`
	}{op.Result, op.Error, op.ErrorDocument})
	if err != nil {
		// Prepared values are serializable; discard unexpected payloads safely.
		return payloadLimit + 1
	}
	return len(data)
}

func (q *operationQueue) pruneResultsLocked(limit int) {
	for q.resultBytes > limit && len(q.resultOrder) > 0 {
		oldest := q.resultOrder[0]
		q.resultOrder = q.resultOrder[1:]
		saved := q.operations[oldest]
		q.resultBytes -= saved.retainedBytes
		saved.retainedBytes = 0
		if saved.Result != nil {
			saved.Result, saved.ResultUnavailable = nil, true
		}
		if saved.Error != "" || saved.ErrorDocument != nil {
			saved.Error, saved.ErrorDocument, saved.ErrorUnavailable = "", nil, true
		}
		q.operations[oldest] = saved
	}
}
func uncertainOperation(ctx context.Context, db *store.Store, op operation, scope string) bool {
	// The journal never interprets an error string as proof of delivery.
	if op.Tool == "session_chat" || op.Tool == "configuration_chat" || op.Tool == "session_control" {
		var session struct {
			ThreadPending bool   `json:"threadPending"`
			BatchID       string `json:"batchId"`
		}
		if db.Get(ctx, "factory-session", scope, &session) == nil {
			if session.ThreadPending {
				return true
			}
			var batch struct {
				State string `json:"state"`
			}
			if session.BatchID != "" && db.Get(ctx, "factory-batch", session.BatchID, &batch) == nil && (batch.State == "claimed" || batch.State == "uncertain") {
				return true
			}
		}
	}
	if op.Tool == "answer_session_question" || op.Tool == "confirm_configuration" {
		kind := "factory-question"
		if op.Tool == "confirm_configuration" {
			kind = "factory-proposal"
		}
		var v struct {
			State  string `json:"state"`
			Status string `json:"status"`
		}
		if db.Get(ctx, kind, scope, &v) == nil {
			return v.State == "uncertain" || v.Status == "uncertain"
		}
	}
	return false
}
func operationScope(name string, raw json.RawMessage) (string, bool) {
	var in struct {
		ItemID string `json:"itemId"`
		ID     string `json:"id"`
		Source string `json:"source"`
		Action string `json:"action"`
	}
	_ = json.Unmarshal(raw, &in)
	switch name {
	case "configuration_chat", "propose_configuration", "confirm_configuration", "inspect_configuration":
		return "configuration", false
	case "sync_repositories":
		return "sync", false
	}
	scope := in.ItemID
	if scope == "" {
		scope = in.ID
	}
	if scope == "" {
		scope = in.Source
	}
	if scope == "" {
		scope = name
	}
	return scope, name == "session_control" || name == "answer_session_question" || name == "resolve_session_delivery"
}
