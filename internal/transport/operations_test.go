package transport

import (
	"bytes"
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/factory"
	"codefactory/internal/store"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitOperation(t *testing.T, q *operationQueue, instance, id, state string) operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if op, ok := q.status(instance, id).(operation); ok && op.State == state {
			return op
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("operation %s never reached %s: %+v", id, state, q.status(instance, id))
	return operation{}
}

func TestOperationFailurePayloadsUseRetentionBudget(t *testing.T) {
	f := newTrackingFixture(t)
	settings, err := f.native.Settings(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	kind := strings.Repeat("x", 40000)
	settings.Rules = []factory.Rule{{ID: "bad", Repositories: []string{"*"}, Types: []string{kind}, Instructions: "fixture"}}
	response, failed := f.call(t, "propose_configuration", map[string]any{"settings": settings})
	expected := fmt.Sprintf("unsupported event type %q", kind)
	if !failed || response["state"] != "failed" || response["errorDocument"].(map[string]any)["error"] != expected {
		t.Fatal("did not exercise the complete inline configuration error")
	}
	q := f.server.operations
	q.mu.Lock()
	retained, entries := q.resultBytes, len(q.resultOrder)
	q.mu.Unlock()
	if retained < len(expected) || entries != 1 {
		t.Fatalf("terminal error escaped retention accounting: bytes=%d entries=%d error=%d", retained, entries, len(expected))
	}
	var effects atomic.Int64
	for _, example := range []struct {
		id, message string
		value       any
	}{
		{"short-error", strings.Repeat("e", 1024), nil},
		{"inline-result", "", map[string]string{"value": strings.Repeat("r", 40000)}},
		{"chunked-result", "", map[string]string{"value": strings.Repeat("r", 60000)}},
		{"chunked-error", strings.Repeat("e", 60000), nil},
	} {
		submitUntilAvailable(t, q, q.instance, example.id, "fixture", example.id, false, 1, func(context.Context) (any, error) {
			effects.Add(1)
			if example.message != "" {
				return nil, fmt.Errorf("%s", example.message)
			}
			return example.value, nil
		})
		state := "succeeded"
		if example.message != "" {
			state = "failed"
		}
		op := waitOperation(t, q, q.instance, example.id, state)
		if op.retainedBytes == 0 || op.retainedBytes < len(op.Error) {
			t.Fatal("terminal payload kind was not accounted", example.id)
		}
	}
	q.documents.mu.Lock()
	q.documents.chunks, q.documents.order, q.documents.bytes = nil, nil, 0
	q.documents.mu.Unlock()
	missingError := waitOperation(t, q, q.instance, "chunked-error", "failed")
	missingResult := waitOperation(t, q, q.instance, "chunked-result", "succeeded")
	if !missingError.ErrorUnavailable || missingError.ErrorDocument != nil || missingError.Error != "" || !missingResult.ResultUnavailable || missingResult.Result != nil {
		t.Fatal("evicted document details were not explicit in status")
	}
	q.mu.Lock()
	before := map[string]operation{}
	for key, op := range q.operations {
		before[key] = op
	}
	// Exercise the same oldest-first pruning with a smaller retained-body budget.
	// Production publication always uses the selected 64 MiB ceiling.
	q.pruneResultsLocked(q.resultBytes - 1)
	first := q.operations[operationKey(q.instance, response["requestId"].(string))]
	if !first.ErrorUnavailable || first.Error != "" || first.ErrorDocument != nil || first.State != "failed" {
		q.mu.Unlock()
		t.Fatal("oldest failure details were not explicitly evicted", first.State)
	}
	q.pruneResultsLocked(0)
	if q.resultBytes != 0 || len(q.resultOrder) != 0 {
		q.mu.Unlock()
		t.Fatal("terminal payload pruning retained body bytes")
	}
	for key, previous := range before {
		op := q.operations[key]
		if op.RequestID != previous.RequestID || op.InstanceID != previous.InstanceID || op.Tool != previous.Tool || op.State != previous.State || op.Result != nil || op.Error != "" || op.ErrorDocument != nil || op.retainedBytes != 0 || (previous.Result != nil && !op.ResultUnavailable) || (previous.Error != "" && !op.ErrorUnavailable) {
			q.mu.Unlock()
			t.Fatal("eviction lost terminal identity/state or availability", previous.RequestID)
		}
	}
	q.mu.Unlock()
	duplicate, err := submitUntilAvailable(t, q, q.instance, first.RequestID, "different-tool", "other", false, 1, func(context.Context) (any, error) {
		effects.Add(100)
		return nil, nil
	})
	if err != nil || duplicate.(operation).Tool != "propose_configuration" || duplicate.(operation).State != "failed" || !duplicate.(operation).ErrorUnavailable || effects.Load() != 4 {
		t.Fatal("evicted failure identity permitted replay", duplicate, err, effects.Load())
	}
}
func TestOperationsDeduplicateBoundAdmissionAndReserveControl(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := newOperationQueue(db, &documentCache{})
	var effects atomic.Int64
	entered := make(chan struct{}, ordinaryWorkers)
	release := make(chan struct{})
	defer close(release)
	for n := 0; n < ordinaryWorkers; n++ {
		id := fmt.Sprint(n)
		result, err := submitUntilAvailable(t, q, q.instance, id, "fixture", id, false, 100, func(ctx context.Context) (any, error) {
			entered <- struct{}{}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
			}
			effects.Add(1)
			return map[string]bool{"complete": true}, nil
		})
		if err != nil || result.(operation).State != "queued" {
			t.Fatal(result, err)
		}
	}
	for n := 0; n < ordinaryWorkers; n++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("ordinary workers did not start")
		}
	}
	duplicate, err := submitUntilAvailable(t, q, q.instance, "0", "different-tool", "different-scope", false, 100, func(context.Context) (any, error) { effects.Add(100); return nil, nil })
	if err != nil || duplicate.(operation).Tool != "fixture" {
		t.Fatal("duplicate was replaced", duplicate, err)
	}
	for n := 0; n < pendingLimit; n++ {
		result, err := submitUntilAvailable(t, q, q.instance, fmt.Sprint(n+100), "fixture", "blocked", false, 100, func(context.Context) (any, error) { return nil, nil })
		if err != nil || result.(operation).State != "queued" {
			t.Fatal("bounded queue refused capacity", n, result, err)
		}
	}
	full, err := submitUntilAvailable(t, q, q.instance, "overflow", "fixture", "other", false, 1, func(context.Context) (any, error) { t.Error("overflow executed"); return nil, nil })
	if err != nil || full.(map[string]any)["state"] != "busy" {
		t.Fatal("full queue admitted work", full, err)
	}
	// Control workers stay available when all ordinary workers are occupied. Free
	// one pending slot; a full shared queue still rejects controls immediately.
	q.mu.Lock()
	q.pending = q.pending[1:]
	q.bytes -= 100
	q.mu.Unlock()
	control := make(chan struct{})
	_, err = submitUntilAvailable(t, q, q.instance, "interrupt", "session_control", "0", true, 1, func(context.Context) (any, error) { close(control); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-control:
	case <-time.After(time.Second):
		t.Fatal("control queued behind blocked ordinary workers")
	}
	waitOperation(t, q, q.instance, "interrupt", "succeeded")
	if effects.Load() != 0 {
		t.Fatal("unexpected completed delivery")
	}
}
func TestOperationJournalPrecedesEffectsAndNeverStoresAnswers(t *testing.T) {
	f := newTrackingFixture(t)
	q := f.server.operations
	const secret = "answer-body-must-never-enter-operation-journal"
	type input struct {
		ID     string            `json:"id"`
		Answer map[string]string `json:"answer"`
	}
	// Exercise authenticated HTTP decoding and the real asynchronous tool binding.
	bind(f.server, "", "answer_session_question", "fixture", false, false, func(ctx context.Context, in input) (any, error) {
		if in.Answer["private"] != secret {
			return nil, fmt.Errorf("answer did not reach worker")
		}
		var metadata operationMetadata
		if err := f.a.Store.Get(ctx, "plugin-api-operation", operationKey(q.instance, "answer"), &metadata); err != nil || metadata.State != "running" {
			return nil, fmt.Errorf("effect preceded journal: %v", err)
		}
		return map[string]bool{"delivered": true}, nil
	})
	deadline := time.Now().Add(time.Second)
	for {
		wire, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "answer_session_question", map[string]any{"requestId": "answer", "instanceId": q.instance, "id": "question", "answer": map[string]string{"private": secret}})
		if err != nil || wire["isError"] == true {
			t.Fatal(wire, err)
		}
		response := wire["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)
		if response["state"] != "busy" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("answer admission remained busy")
		}
	}
	waitOperation(t, q, q.instance, "answer", "succeeded")
	records, err := f.a.Store.Records(context.Background(), "plugin-api-operation")
	if err != nil || len(records) == 0 {
		t.Fatal("missing journal", err)
	}
	for _, record := range records {
		if strings.Contains(string(record), secret) || strings.Contains(string(record), "delivered") || strings.Contains(string(record), "result") {
			t.Fatal("journal contains command data", string(record))
		}
	}
}

func TestOperationDeadlineReleasesScopeWithoutReplay(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := newOperationQueue(db, &documentCache{})
	q.mu.Lock()
	q.executionTimeout = 50 * time.Millisecond
	q.mu.Unlock()
	var effects atomic.Int64
	submitUntilAvailable(t, q, q.instance, "stalled", "fixture", "same-session", false, 1, func(ctx context.Context) (any, error) { effects.Add(1); <-ctx.Done(); return nil, ctx.Err() })
	submitUntilAvailable(t, q, q.instance, "healthy", "fixture", "same-session", false, 1, func(context.Context) (any, error) { return map[string]bool{"complete": true}, nil })
	waitOperation(t, q, q.instance, "stalled", "uncertain")
	waitOperation(t, q, q.instance, "healthy", "succeeded")
	value, err := submitUntilAvailable(t, q, q.instance, "stalled", "fixture", "same-session", false, 1, func(context.Context) (any, error) { effects.Add(100); return nil, nil })
	if err != nil || value.(operation).State != "uncertain" || effects.Load() != 1 {
		t.Fatal("deadline authorized replay", value, err, effects.Load())
	}
	var metadata operationMetadata
	if err := db.Get(context.Background(), "plugin-api-operation", operationKey(q.instance, "stalled"), &metadata); err != nil || metadata.State != "uncertain" {
		t.Fatal("timeout outcome was not durable", metadata, err)
	}
}

func TestOperationRestartReportsUncertainAndLostWithoutReplay(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := app.ID()
	if err := db.Put(context.Background(), "plugin-api-operation", operationKey(old, "persisted"), operationMetadata{"persisted", old, "session_chat", "running"}, ""); err != nil {
		t.Fatal(err)
	}
	q := newOperationQueue(db, &documentCache{})
	waitOperation(t, q, old, "persisted", "uncertain")
	waitOperation(t, q, old, "memory-only", "lost")
	var replay atomic.Bool
	duplicate, err := submitUntilAvailable(t, q, old, "persisted", "session_chat", "session", false, 1, func(context.Context) (any, error) { replay.Store(true); return nil, nil })
	if err != nil || duplicate.(operation).State != "uncertain" {
		t.Fatal(duplicate, err)
	}
	lost, err := submitUntilAvailable(t, q, old, "memory-only", "session_chat", "session", false, 1, func(context.Context) (any, error) { replay.Store(true); return nil, nil })
	if err != nil || lost.(operation).State != "lost" || replay.Load() {
		t.Fatal("restart replayed work", lost, err)
	}
}
func TestProjectionCurrentReadsChunksAndUnrelatedTranscript(t *testing.T) {
	f := newTrackingFixture(t)
	item := domain.Item{ID: "o/r#1", Kind: "issue", State: "open", Title: "current", Body: strings.Repeat("界<&", 40000), Revision: "v1"}
	if err := f.a.Store.Put(f.ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Store.Put(f.ctx, "user", item.ID, domain.UserState{ID: item.ID}, ""); err != nil {
		t.Fatal(err)
	}
	before, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed {
		t.Fatal(before)
	}
	detail, failed := f.call(t, "factory_item", map[string]any{"id": item.ID})
	if failed || detail["item"].(map[string]any)["body"] != item.Body {
		t.Fatal("document chunks lost exact source")
	}
	for n := 0; n < 20; n++ {
		if err := f.a.Store.Write(f.ctx, func(tx *store.Tx) error { return tx.Event("session.output", "unrelated", map[string]int{"n": n}) }); err != nil {
			t.Fatal(err)
		}
	}
	wire, bytes, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "factory_state", map[string]any{"view": "issues"})
	if err != nil || bytes > responseLimit {
		t.Fatal("unbounded response", bytes, err)
	}
	response := wire["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)
	if response["state"] != "ready" {
		t.Fatal("unrelated transcript invalidated inbox", response)
	}
	item.Title = "new committed state"
	item.Revision = "v2"
	if err := f.a.Store.Put(f.ctx, "item", item.ID, item, ""); err != nil {
		t.Fatal(err)
	}
	wire, _, _, err = trackingCall(f.http.Client(), f.http.URL, f.token, "factory_state", map[string]any{"view": "issues", "sinceVersion": before["version"], "sinceInstanceId": f.server.operations.instance})
	if err != nil {
		t.Fatal(err)
	}
	response = wire["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)
	if response["state"] == "ready" {
		t.Fatal("old projection claimed current", response)
	}
	after, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
	if failed || after["inbox"].([]any)[0].(map[string]any)["item"].(map[string]any)["title"] != item.Title {
		t.Fatal("worker never published current result", after)
	}
	link := domain.Relationship{ID: "linked", Source: item.ID, Target: "external/r#2", Kind: "implementation", Manual: true}
	if err := f.a.Store.Put(f.ctx, "relationship", link.ID, link, ""); err != nil {
		t.Fatal(err)
	}
	pr := domain.Item{ID: link.Target, Kind: "pr", State: "open"}
	if err := f.a.Store.Put(f.ctx, "linked-pr", pr.ID, pr, ""); err != nil {
		t.Fatal(err)
	}
	conversation, failed := f.call(t, "factory_state", map[string]any{"view": "conversation", "itemId": item.ID})
	if failed || conversation["selectedEntry"].(map[string]any)["work"].(map[string]any)["complete"] != false {
		t.Fatal("initial linked PR state was not projected", conversation)
	}
	pr.State = "merged"
	if err := f.a.Store.Put(f.ctx, "linked-pr", pr.ID, pr, ""); err != nil {
		t.Fatal(err)
	}
	conversation, failed = f.call(t, "factory_state", map[string]any{"view": "conversation", "itemId": item.ID, "sinceVersion": conversation["version"], "sinceInstanceId": f.server.operations.instance})
	if failed || conversation["selectedEntry"].(map[string]any)["work"].(map[string]any)["complete"] != true {
		t.Fatal("linked PR update left the selected projection stale", conversation)
	}
}
func TestOperationPayloadCeiling(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := newOperationQueue(db, &documentCache{})
	out, err := submitUntilAvailable(t, q, q.instance, "too-large", "fixture", "fixture", false, payloadLimit+1, func(context.Context) (any, error) { t.Error("oversized work ran"); return nil, nil })
	if err != nil || out.(map[string]any)["state"] != "busy" {
		t.Fatal(out, err)
	}
}
func TestProjectionFailedRefreshDoesNotReturnOldData(t *testing.T) {
	f := newTrackingFixture(t)
	for _, title := range []any{"valid", map[string]any{"bad": "shape"}, "recovered"} {
		if err := f.a.Store.Put(f.ctx, "item", "x", map[string]any{"id": "x", "title": title}, ""); err != nil {
			t.Fatal(err)
		}
		if err := f.a.Store.Put(f.ctx, "user", "x", domain.UserState{ID: "x"}, ""); err != nil {
			t.Fatal(err)
		}
		result, failed := f.call(t, "factory_state", map[string]any{"view": "issues"})
		if _, bad := title.(map[string]any); bad {
			if !failed {
				t.Fatal("failed refresh returned old data")
			}
		} else if failed {
			t.Fatal(result)
		}
	}
}

func submitUntilAvailable(t *testing.T, q *operationQueue, instance, id, name, scope string, control bool, size int, run func(context.Context) (any, error)) (any, error) {
	t.Helper()
	for attempt := 0; attempt < 1000; attempt++ {
		value, err := q.submit(instance, id, name, scope, control, size, run)
		if _, busy := value.(map[string]any); !busy || err != nil {
			return value, err
		}
		if name == "fixture" && (id == "overflow" || id == "too-large") {
			return value, err
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("admission remained busy")
	return nil, nil
}

type privateOperationPanic string

func TestOperationPanicRetainsOwnerDiagnosticsWithoutPrivatePayload(t *testing.T) {
	var diagnostics bytes.Buffer
	previous := slog.Default()
	writer, flags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&diagnostics, nil)))
	t.Cleanup(func() { slog.SetDefault(previous); log.SetOutput(writer); log.SetFlags(flags) })
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := newOperationQueue(db, &documentCache{})
	submitUntilAvailable(t, q, q.instance, "panic-id", "private-tool", "fixture", false, 1, func(context.Context) (any, error) {
		panic(privateOperationPanic("owner_token=private-credential request=private-body"))
	})
	op := waitOperation(t, q, q.instance, "panic-id", "uncertain")
	for _, expected := range []string{"operation_id", "panic-id", "private-tool", "transport.privateOperationPanic", "stack", "runtime/debug.Stack"} {
		if !strings.Contains(diagnostics.String(), expected) {
			t.Fatalf("diagnostics missing %s", expected)
		}
	}
	for _, private := range []string{"private-credential", "private-body", "owner_token="} {
		if strings.Contains(op.Error, private) || strings.Contains(diagnostics.String(), private) {
			t.Fatal("panic payload escaped")
		}
	}
}

func TestQueuedConfigurationRejectsStaleConfirmation(t *testing.T) {
	f := newTrackingFixture(t)
	initial, err := f.native.Settings(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := initial
	first.QuietSeconds++
	proposal, err := f.native.Propose(f.ctx, &first, nil)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	submitUntilAvailable(t, f.server.operations, f.server.operations.instance, "hold-config", "fixture", "configuration", false, 1, func(context.Context) (any, error) { close(entered); <-release; return nil, nil })
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("configuration barrier did not start: %+v", f.server.operations.status(f.server.operations.instance, "hold-config"))
	}
	id := app.ID()
	input := map[string]any{"id": proposal.ID, "confirm": true, "instanceId": f.server.operations.instance, "requestId": id}
	deadline := time.Now().Add(time.Second)
	for {
		wire, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "confirm_configuration", input)
		if err != nil {
			t.Fatal(err)
		}
		if wire["isError"] == true {
			t.Fatal(wire)
		}
		state := wire["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)["state"]
		if state == "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("confirmation was not queued")
		}
	}
	second := initial
	second.QuietSeconds += 2
	newer, err := f.native.Propose(f.ctx, &second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.native.Confirm(f.ctx, newer.ID, true); err != nil {
		t.Fatal(err)
	}
	close(release)
	failed := waitOperation(t, f.server.operations, f.server.operations.instance, id, "failed")
	if !strings.Contains(failed.Error, "stale") {
		t.Fatal("stale proposal was not rejected", failed)
	}
	current, err := f.native.Settings(f.ctx)
	if err != nil || current.QuietSeconds != second.QuietSeconds {
		t.Fatal("queued confirmation overwrote current settings", current, err)
	}
}

func TestQuestionPagesKeepExactLargePayloads(t *testing.T) {
	f := newTrackingFixture(t)
	payload := strings.Repeat("exact option 界<&", 10000)
	err := f.a.Store.Write(f.ctx, func(tx *store.Tx) error {
		for n := 0; n < 55; n++ {
			id := fmt.Sprintf("q%02d", n)
			data := map[string]any{"questions": []map[string]any{{"id": "choice", "question": payload}}}
			raw, _ := json.Marshal(data)
			if err := tx.Put("factory-question", id, map[string]any{"id": id, "sessionId": "session", "status": "pending", "params": json.RawMessage(raw)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, failed := f.call(t, "session_questions", map[string]any{"sessionId": "session", "offset": 50, "limit": 5})
	if failed || page["total"] != float64(55) || len(page["questions"].([]any)) != 5 {
		t.Fatal("question page lost collection entries", page)
	}
	question := page["questions"].([]any)[4].(map[string]any)
	if question["id"] != "q54" || question["params"].(map[string]any)["questions"].([]any)[0].(map[string]any)["question"] != payload {
		t.Fatal("exact large request payload changed")
	}
}

func TestCompletedOperationQueueClearsObsoleteInputClosures(t *testing.T) {
	f := newTrackingFixture(t)
	q := &operationQueue{instance: app.ID(), db: f.a.Store, ctx: f.ctx, operations: map[string]operation{}, active: map[string]bool{}, wake: make(chan struct{}), documents: f.server.documents, executionTimeout: time.Second}
	var effects int
	for _, count := range []int{8, 5, 3} {
		for n := 0; n < count; n++ {
			payload := strings.Repeat(fmt.Sprintf("private-answer-%d-%d", count, n), 10000)
			id := fmt.Sprintf("burst-%d-%d", count, n)
			_, err := q.submit(q.instance, id, "fixture", id, false, len(payload), func(context.Context) (any, error) {
				effects++
				sum := sha256.Sum256([]byte(payload))
				return fmt.Sprintf("%x", sum), nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		backing := q.pending[:cap(q.pending)]
		for n := 0; n < count; n++ {
			task, ok, _ := q.take(false)
			if !ok {
				t.Fatal("queued task was not taken")
			}
			q.execute(task)
		}
		if len(q.pending) != 0 || q.bytes != 0 || len(q.active) != 0 {
			t.Fatal("queue did not drain", len(q.pending), q.bytes, len(q.active))
		}
		for n, slot := range backing {
			if !reflect.ValueOf(slot).IsZero() {
				t.Errorf("drained burst%d retained obsolete input closure/metadata at backing slot%d", count, n)
			}
		}
	}
	if effects != 16 {
		t.Fatal("tasks replayed or omitted", effects)
	}
}

func TestDuplicateOperationReceiptsReflectIndependentDocumentEviction(t *testing.T) {
	f := newTrackingFixture(t)
	q := f.server.operations
	var effects atomic.Int64
	for _, example := range []struct {
		id, state string
		failure   bool
	}{{"evicted-success", "succeeded", false}, {"evicted-error", "failed", true}} {
		submitUntilAvailable(t, q, q.instance, example.id, "fixture", example.id, false, 1, func(context.Context) (any, error) {
			effects.Add(1)
			if example.failure {
				return nil, fmt.Errorf("%s", strings.Repeat("private-error", 6000))
			}
			return map[string]string{"body": strings.Repeat("result", 12000)}, nil
		})
		op := waitOperation(t, q, q.instance, example.id, example.state)
		if example.failure && op.ErrorDocument == nil || !example.failure && op.Result == nil {
			t.Fatal("terminal fixture did not produce payload", example.id)
		}
	}
	// Two distinct encoded documents exceed the production cache capacity and evict
	// both smaller terminal documents through the actual oldest-first prepare path.
	for n := 0; n < 2; n++ {
		if _, err := q.documents.prepare(map[string]any{"value": strings.Repeat("x", 25<<20), "version": n}); err != nil {
			t.Fatal(err)
		}
	}
	for _, example := range []struct {
		id, state string
		failure   bool
	}{{"evicted-success", "succeeded", false}, {"evicted-error", "failed", true}} {
		var response map[string]any
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			result, _, _, err := trackingCall(f.http.Client(), f.http.URL, f.token, "session_chat", map[string]any{"itemId": "missing", "text": "must never deliver", "instanceId": q.instance, "requestId": example.id})
			if err != nil || result["isError"] == true {
				t.Fatal("duplicate receipt failed", err, result)
			}
			response = result["structuredContent"].(map[string]any)["data"].(map[string]any)["response"].(map[string]any)
			if response["state"] != "busy" {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if response["requestId"] != example.id || response["instanceId"] != q.instance || response["tool"] != "fixture" || response["state"] != example.state {
			t.Fatal("duplicate changed original identity/state", response)
		}
		if example.failure {
			if response["errorUnavailable"] != true || response["errorDocument"] != nil || response["error"] != nil {
				t.Error("duplicate advertises unavailable error details", response)
			}
		} else if response["resultUnavailable"] != true || response["result"] != nil {
			t.Error("duplicate advertises unavailable result details", response)
		}
	}
	if effects.Load() != 2 {
		t.Fatal("duplicate replayed", effects.Load())
	}
	q.mu.Lock()
	count := len(q.operations)
	q.mu.Unlock()
	if count != 2 {
		t.Fatal("duplicate admitted another operation", count)
	}
}
