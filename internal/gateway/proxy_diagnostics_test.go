package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
)

func TestProxyDiagnosticsClassifyWithoutLeakingRequestOrError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		cancel bool
	}{
		{"daemon_unavailable", &net.OpError{Op: "hidden-operation", Err: syscall.ECONNREFUSED}, false},
		{"client_canceled", errors.New("hidden-upstream-error"), true},
		{"deadline_exceeded", context.DeadlineExceeded, false},
		{"connection_reset", syscall.ECONNRESET, false},
		{"upstream_eof", io.ErrUnexpectedEOF, false},
		{"upstream_timeout", &net.DNSError{Err: "hidden-dns-error", Name: "hidden-host", IsTimeout: true}, false},
		{"upstream_transport", errors.New("hidden-upstream-error https://hidden-host.example/?token=hidden-token"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(previous)
			req := httptest.NewRequest("POST", "https://hidden-request.example/mcp?code=hidden-code", strings.NewReader("hidden-body"))
			req.Header.Set("Authorization", "Bearer hidden-auth")
			if tc.cancel {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			response := httptest.NewRecorder()
			proxyError(response, req, tc.err)
			log := output.String()
			if !strings.Contains(log, `"reason":"`+tc.name+`"`) {
				t.Fatal("missing failure class", log)
			}
			if strings.Contains(log, "hidden-") {
				t.Fatal("diagnostics leaked private details", log)
			}
			if response.Code != 502 || response.Body.String() != "Local daemon unavailable\n" {
				t.Fatal("diagnostics changed proxy behavior")
			}
		})
	}
}
