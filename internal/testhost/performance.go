package main

import (
	"codefactory/internal/app"
	"codefactory/internal/domain"
	"codefactory/internal/events"
	"codefactory/internal/providers"
	"codefactory/internal/store"
	"codefactory/internal/transport"
	"codefactory/internal/web"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// This listener exists only in the fixture executable. It serves the exact
// production-embedded resource and sends all MCP calls through owner auth.
func servePerformance(ctx context.Context, dir string, port int) error {
	type fixture struct {
		app     *app.App
		once    sync.Once
		handler http.Handler
		work    sync.Mutex
		pending int
	}
	fixtures := map[string]*fixture{}
	var mu sync.Mutex
	delays := []int64{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		delays = append(delays, time.Since(start).Microseconds())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"pilot"}`)
		case "/repos/pilot/fixture/issues":
			fmt.Fprint(w, `[{"number":1,"title":"Background synchronization persisted","body":"Saved fixture","state":"open","html_url":"https://github.com/pilot/fixture/issues/1","updated_at":"2026-10-27T12:00:00Z","user":{"login":"pilot"},"labels":[]}]`)
		default:
			fmt.Fprint(w, "[]")
		}
	}))
	defer provider.Close()
	binary, _ := filepath.Abs("bin/fake-codex")
	delayRecord := filepath.Join(dir, "ai-delays")
	os.Setenv("CF_FAKE_DELAY_RECORD", delayRecord)
	os.Setenv("CF_FAKE_OPERATION_DELAY_MS", "100")
	os.Setenv("CF_FAKE_MODE", "inference")
	checkout := filepath.Join(dir, "checkout")
	if err := os.MkdirAll(checkout, 0700); err != nil {
		return err
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	for _, name := range []string{"empty", "cold", "retained"} {
		db, err := store.Open(filepath.Join(dir, name, "state.db"))
		if err != nil {
			return err
		}
		defer db.Close()
		vault, err := store.NewVault(db, filepath.Join(dir, name))
		if err != nil {
			return err
		}
		gh := providers.NewGitHub()
		gh.BaseURL = provider.URL
		gh.Token = func(context.Context) (string, error) { return "fixture", nil }
		a := &app.App{Store: db, Vault: vault, DataDir: filepath.Join(dir, name), GitHub: gh}
		a.Runs = app.NewManager(ctx, a, binary)
		defer a.Runs.Close()
		if err := a.Init(ctx); err != nil {
			return err
		}
		if name != "empty" {
			if err := a.SaveRepository(ctx, domain.Repository{ID: "pilot/fixture", Checkout: checkout, Base: "main"}); err != nil {
				return err
			}
			count := 1
			if name == "retained" {
				count = 1000
			}
			if err := db.Write(ctx, func(tx *store.Tx) error {
				if err := tx.Put("identity", "github", domain.GitHubViewer{Login: "pilot"}); err != nil {
					return err
				}
				for n := 1; n <= count; n++ {
					id := fmt.Sprintf("pilot/fixture#%d", n)
					item := domain.Item{ID: id, Number: n, Repository: "pilot/fixture", Kind: "issue", Title: fmt.Sprintf("Retained item %04d", n), Body: "Saved searchable body", Author: "pilot", State: "open", Revision: "v1", Updated: time.Now().UTC(), Labels: []string{}, Activity: []domain.Activity{{Body: strings.Repeat("retained history ", 2000)}}}
					if err := tx.Put("item", id, item); err != nil {
						return err
					}
					if err := tx.Put("user", id, domain.UserState{ID: id, New: true, Actions: []domain.Action{}}); err != nil {
						return err
					}
				}
				if name == "retained" {
					for n := 0; n < 3000; n++ {
						link := domain.Relationship{ID: fmt.Sprintf("link-%04d", n), Source: fmt.Sprintf("pilot/fixture#%d", n%1000+1), Target: fmt.Sprintf("pilot/fixture#%d", (n+1)%1000+1)}
						if err := tx.Put("relationship", link.ID, link); err != nil {
							return err
						}
					}
					for n := 0; n < 100; n++ {
						run := domain.Run{ID: fmt.Sprintf("run-%03d", n), ItemID: fmt.Sprintf("pilot/fixture#%d", n+1), Status: "idle", Created: time.Now().UTC(), Skills: []domain.Skill{{Files: map[string]string{"large": strings.Repeat("private skill", 8000)}}}, SourceSnapshot: &domain.Item{Body: strings.Repeat("source", 8000)}}
						if err := tx.Put("run", run.ID, run); err != nil {
							return err
						}
						if err := tx.Event("run.output", run.ID, map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"delta": strings.Repeat("history", 8000)}}); err != nil {
							return err
						}
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		f := &fixture{app: a}
		init := func() { f.handler = transport.New(a, events.New(db, vault)).Handler(strings.Repeat("a", 64)) }
		if name != "cold" {
			f.once.Do(init)
		}
		fixtures[name] = f
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ready") })
	mux.HandleFunc("/fixture-host", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, performanceHostHTML)
	})
	mux.HandleFunc("/fixture-ui", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := web.HTML
		if r.URL.Query().Get("view") == "settings" {
			html = web.SettingsHTML()
		}
		fmt.Fprint(w, html)
	})
	mux.HandleFunc("/fixture-mcp", func(w http.ResponseWriter, r *http.Request) {
		f := fixtures[r.URL.Query().Get("scenario")]
		if f == nil {
			http.NotFound(w, r)
			return
		}
		f.once.Do(func() {
			f.handler = transport.New(f.app, events.New(f.app.Store, f.app.Vault)).Handler(strings.Repeat("a", 64))
		})
		copy := r.Clone(r.Context())
		copy.URL.Path = "/mcp"
		f.handler.ServeHTTP(w, copy)
	})
	mux.HandleFunc("/fixture-work", func(w http.ResponseWriter, r *http.Request) {
		f := fixtures[r.URL.Query().Get("scenario")]
		if f == nil {
			http.NotFound(w, r)
			return
		}
		f.work.Lock()
		f.pending += 2
		f.work.Unlock()
		for _, job := range []func(context.Context) error{f.app.Sync, f.app.AssessNext} {
			go func(job func(context.Context) error) { _ = job(ctx); f.work.Lock(); f.pending--; f.work.Unlock() }(job)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/fixture-metrics", func(w http.ResponseWriter, r *http.Request) {
		pending := 0
		for _, f := range fixtures {
			f.work.Lock()
			pending += f.pending
			f.work.Unlock()
		}
		mu.Lock()
		github := append([]int64{}, delays...)
		mu.Unlock()
		ai, _ := os.ReadFile(delayRecord)
		_ = json.NewEncoder(w).Encode(map[string]any{"githubDelayUs": github, "aiDelays": string(ai), "pending": pending})
	})
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// performanceHostHTML is served by the fixture itself so timed resource
// requests do not pass through Playwright routing or its cache override.
const performanceHostHTML = `<!doctype html><button id="launch">Open Code Factory</button><script>
window.addEventListener("message",async e=>{
 const m=e.data;
 if(m.shellPainted){
 const notificationAt=performance.timeOrigin+performance.now();
 window.launchResult={ms:m.paintedAt-window.startedEpoch,readyMs:m.readyAt-window.startedEpoch,firstFrameMs:m.firstFrameAt-window.startedEpoch,notificationMs:notificationAt-window.startedEpoch,messageLagMs:notificationAt-m.paintedAt,fcpMs:m.fcpAt-window.startedEpoch,doubleFrameMs:m.doubleFrameAt-window.startedEpoch,navigationPaints:m.navigationPaints.map(p=>({id:p.id,label:p.label,atMs:p.at-window.startedEpoch,area:p.area})),usable:m.usable,pending:window.pending};return
 }
 if(m.longTasks){window.tasks=(window.tasks||[]).concat(m.longTasks);return}
 if(!m||m.id===undefined)return;
 try{
 let result={};
 if(m.method==="ui/initialize")result={protocolVersion:m.params.protocolVersion,hostInfo:{name:"Fixture MCP host",version:"1"},hostCapabilities:{serverTools:{},serverResources:{}},hostContext:{theme:"light",displayMode:"fullscreen"}};
 else if(m.method==="tools/call")result=await window.fixtureCall(m.params);
 e.source.postMessage({jsonrpc:"2.0",id:m.id,result},"*");
 }catch(error){e.source.postMessage({jsonrpc:"2.0",id:m.id,error:{code:-32000,message:String(error)}},"*")}
});
document.getElementById("launch").onclick=()=>{
 window.started=performance.now();window.startedEpoch=performance.timeOrigin+window.started;window.launchResult=undefined;
 document.querySelector("iframe")?.remove();
 const frame=document.createElement("iframe");frame.title="Code Factory";frame.style="width:100%;height:90vh;border:0";frame.src="/fixture-ui";document.body.appendChild(frame);
};
</script>`
