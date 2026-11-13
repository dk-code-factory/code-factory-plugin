package httpbudget

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var slowRequests, droppedLatencyRecords atomic.Uint64
var activeSlowRequests atomic.Int64
var latencyWorkers atomic.Int64

type latencyRecord struct {
	RequestID          uint64
	API, Method, Phase string
	StartedAt          time.Time
	Duration           time.Duration
	Status             int
}

// One blocked sink and at most two records per admitted worker are retained.
// Counters remain exact when the diagnostic queue overflows.
var latencyAudit struct {
	sync.Mutex
	queue      [2 * Capacity]latencyRecord
	head, size int
	running    bool
}

// RequestID correlates local boundary warnings with the final API/audit record.
// It is generated locally, never accepted from an untrusted header.
func RequestID(ctx context.Context) uint64 {
	if state, _ := ctx.Value(key{}).(*requestState); state != nil {
		return state.id
	}
	return 0
}

// ResponseDuration measures finite completion or the initial stream acknowledgement,
// excluding the lifetime of an already acknowledged subscription.
func ResponseDuration(ctx context.Context) time.Duration {
	if state, _ := ctx.Value(key{}).(*requestState); state != nil {
		if elapsed := state.elapsed.Load(); elapsed != 0 {
			return time.Duration(elapsed)
		}
		return time.Since(state.started)
	}
	return 0
}

func safeMethod(method string) string {
	switch method {
	case http.MethodGet:
		return http.MethodGet
	case http.MethodPost:
		return http.MethodPost
	case http.MethodPut:
		return http.MethodPut
	case http.MethodPatch:
		return http.MethodPatch
	case http.MethodDelete:
		return http.MethodDelete
	case http.MethodHead:
		return http.MethodHead
	case http.MethodOptions:
		return http.MethodOptions
	default:
		return "other"
	}
}

// Even a short parsed method or endpoint label can share a large request-line
// allocation. Retain fixed methods and bounded owned labels, never substrings.
func safeLabel(label string) string {
	if len(label) == 0 || len(label) > 64 {
		return "other"
	}
	for _, char := range label {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
			return "other"
		}
	}
	return strings.Clone(label)
}

func observeLatency(record latencyRecord) {
	record.API = safeLabel(record.API)
	record.Method = safeMethod(record.Method)
	latencyAudit.Lock()
	defer latencyAudit.Unlock()
	if latencyAudit.size == len(latencyAudit.queue) {
		droppedLatencyRecords.Add(1)
		return
	}
	latencyAudit.queue[(latencyAudit.head+latencyAudit.size)%len(latencyAudit.queue)] = record
	latencyAudit.size++
	if latencyAudit.running {
		return
	}
	latencyAudit.running = true
	latencyWorkers.Add(1)
	go func() {
		defer latencyWorkers.Add(-1)
		for {
			latencyAudit.Lock()
			if latencyAudit.size == 0 {
				latencyAudit.running = false
				latencyAudit.Unlock()
				return
			}
			record := latencyAudit.queue[latencyAudit.head]
			latencyAudit.queue[latencyAudit.head] = latencyRecord{}
			latencyAudit.head = (latencyAudit.head + 1) % len(latencyAudit.queue)
			latencyAudit.size--
			latencyAudit.Unlock()
			// Diagnostics cannot alter the response or retain request bodies,
			// URLs, headers, credentials, contexts or arbitrary error strings.
			func() {
				defer func() {
					if recover() != nil {
						droppedLatencyRecords.Add(1)
					}
				}()
				slog.Warn("Code Factory HTTP latency exceeded", "request_id", record.RequestID,
					"api", record.API, "http_method", record.Method, "phase", record.Phase,
					"started_at", record.StartedAt, "duration_ms", float64(record.Duration)/float64(time.Millisecond),
					"threshold_ms", Duration.Milliseconds(), "status", record.Status,
					"slow_requests", slowRequests.Load(), "active_slow_requests", activeSlowRequests.Load(),
					"dropped_latency_records", droppedLatencyRecords.Load())
			}()
		}
	}()
}
