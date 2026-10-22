package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
)

// Record only a fixed failure class. Raw errors can contain URLs, addresses or
// request details and must never enter the gateway's diagnostic log.
func proxyFailure(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "client_canceled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "daemon_unavailable"
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return "connection_reset"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "upstream_eof"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "upstream_timeout"
	}
	return "upstream_transport"
}

func proxyError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("Code Factory upstream connection failed", "reason", proxyFailure(r.Context(), err))
	http.Error(w, "Local daemon unavailable", http.StatusBadGateway)
}
