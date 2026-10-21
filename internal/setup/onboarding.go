package setup

import (
	"codefactory/internal/store"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type credentialForm struct {
	origin, nonce string
	expires       time.Time
	ready         atomic.Bool
	used          atomic.Bool
	save          func(context.Context, string) error
	done          chan error
}

func (f *credentialForm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; form-action 'none'; frame-ancestors 'none'")
	if r.Host != strings.TrimPrefix(f.origin, "http://") {
		http.Error(w, "Invalid local host", 403)
		return
	}
	if time.Now().After(f.expires) {
		http.Error(w, "Local setup form expired", 410)
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		fmt.Fprint(w, formHTML)
		return
	}
	if r.Method != "POST" || r.Header.Get("Origin") != f.origin || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+f.nonce)) != 1 {
		http.Error(w, "Invalid local session", 403)
		return
	}
	if r.URL.Path == "/ready" {
		f.ready.Store(true)
		fmt.Fprint(w, "Local browser reachability verified")
		return
	}
	if r.URL.Path != "/credential" {
		http.NotFound(w, r)
		return
	}
	if !f.ready.Load() {
		http.Error(w, "Verify local browser reachability first", 409)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if e := r.ParseForm(); e != nil {
		http.Error(w, "Invalid input", 400)
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		http.Error(w, "Runtime key required", 400)
		return
	}
	if !f.used.CompareAndSwap(false, true) {
		http.Error(w, "Local session already submitted; reopen onboarding to retry", 409)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e := f.save(ctx, key)
	if e != nil {
		http.Error(w, "Credential processing failed; rerun onboarding to check saved state", 500)
	} else {
		fmt.Fprint(w, "Credential saved. Tunnel service started. Return to Work to verify the connection.")
	}
	// Shutdown waits for this handler, flushing its complete response before closing.
	f.done <- e
}

// Onboard never accepts secrets through command arguments, chat, or terminal input.
func Onboard(ctx context.Context, vault *store.Vault, dir string, manual, replace bool) error {
	if !replace {
		if _, e := vault.Get(ctx, "tunnel-key"); e == nil {
			if e = exec.CommandContext(ctx, "systemctl", "--user", "enable", "--now", "code-factory-tunnel.service").Run(); e != nil {
				return fmt.Errorf("stored credential retained; tunnel service startup failed")
			}
			fmt.Fprintln(os.Stdout, "Reused the saved tunnel credential and started its service. Use --replace-key to replace a revoked key.")
			return nil
		} else if !errors.Is(e, store.ErrNotFound) {
			return fmt.Errorf("stored tunnel credential unavailable; repair the vault before onboarding")
		}
	}

	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return e
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	f := &credentialForm{origin: "http://" + listener.Addr().String(), nonce: hex.EncodeToString(b), expires: time.Now().Add(10 * time.Minute), done: make(chan error, 1)}
	f.save = func(ctx context.Context, key string) error {
		if e := vault.Set(ctx, "tunnel-key", key); e != nil {
			return e
		}
		if e := AtomicJSON(filepath.Join(dir, "credential-checkpoint.json"), map[string]any{"saved": true, "at": time.Now().UTC()}); e != nil {
			return e
		}
		return exec.CommandContext(ctx, "systemctl", "--user", "enable", "--now", "code-factory-tunnel.service").Run()
	}
	server := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
	}()
	url := f.origin + "/#" + f.nonce
	if manual {
		fmt.Fprintln(os.Stdout, "Open this short-lived URL in a browser on this computer:", url)
	} else {
		open, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if e = exec.CommandContext(open, "xdg-open", url).Run(); e != nil {
			return fmt.Errorf("setup blocker: browser could not open; rerun onboarding --manual-browser and open the loopback URL on this computer")
		}
	}
	timer := time.NewTimer(time.Until(f.expires))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("local setup form expired; reopen onboarding")
	case e := <-f.done:
		return e
	case e := <-serveErr:
		if e == http.ErrServerClosed {
			return nil
		}
		return e
	}
}

const formHTML = `<!doctype html><html lang="en"><meta charset="utf-8"><title>Code Factory · local setup</title><style>body{font:16px system-ui;max-width:520px;margin:70px auto;padding:24px;color:#244430}input{display:block;width:100%;padding:12px;margin:20px 0;box-sizing:border-box}button{padding:12px;background:#285d45;color:white;border:0;border-radius:6px}</style><h1>Connect your private tunnel</h1><p>Enter the restricted runtime key for your configured OpenAI Secure MCP Tunnel. It is encrypted on this computer and never displayed in Work.</p><form hidden><label>Tunnel runtime key<input name="key" type="password" autocomplete="off" required></label><button>Save and start tunnel</button></form><p id="status" role="status"></p><script>const nonce=location.hash.slice(1);history.replaceState(null,'','/');fetch('/ready',{method:'POST',headers:{Authorization:'Bearer '+nonce}}).then(r=>{if(r.ok)document.querySelector('form').hidden=false;else document.querySelector('#status').textContent='Local setup session unavailable';});document.querySelector('form').onsubmit=async e=>{e.preventDefault();const input=document.querySelector('input');const key=input.value;input.value='';const r=await fetch('/credential',{method:'POST',headers:{Authorization:'Bearer '+nonce,'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({key})});document.querySelector('#status').textContent=await r.text();if(r.ok)document.querySelector('form').remove()};</script></html>`
