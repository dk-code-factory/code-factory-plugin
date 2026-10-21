package setup

import (
	"codefactory/internal/gateway"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func prefixTestConfig() gateway.Config {
	return gateway.Config{PublicURL: "https://pilot.ts.net/code-factory", ApprovalURL: "https://pilot.ts.net:8444", Owner: "owner@test", PublicListen: "127.0.0.1:8788", ApprovalListen: "127.0.0.1:8789", Daemon: "127.0.0.1:8787"}
}

func TestGatewayPrefixRoutePlanPreservesSharedRoot(t *testing.T) {
	cfg := prefixTestConfig()
	want := []gatewayRoute{
		{"pilot.ts.net:8444", "8444", "/", "http://127.0.0.1:8789", false},
		{"pilot.ts.net:443", "443", "/code-factory", "http://127.0.0.1:8788", true},
		{"pilot.ts.net:443", "443", "/.well-known/oauth-authorization-server/code-factory", "http://127.0.0.1:8788/.well-known/oauth-authorization-server", true},
		{"pilot.ts.net:443", "443", "/.well-known/oauth-protected-resource/code-factory/mcp", "http://127.0.0.1:8788/.well-known/oauth-protected-resource/mcp", true},
	}
	if got := gatewayRoutes(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("route plan differs: %#v", got)
	}
	routes := prefixTestLegacyRoutes()
	if err := validateGatewayRoutes(routes, want); err != nil {
		t.Fatalf("shared public root should be preserved: %v", err)
	}
	for _, route := range want[1:] {
		if !strings.Contains(strings.Join(route.arguments(false), " "), "--set-path="+route.path) {
			t.Fatalf("route addition lost its path: %#v", route)
		}
		if !reflect.DeepEqual(route.arguments(true), []string{"funnel", "--https=443", "--set-path=" + route.path, "off"}) {
			t.Fatalf("removal would affect more than the owned path: %#v", route.arguments(true))
		}
	}
}

func TestGatewayPrefixRouteConflictsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*tailscaleRoutes)
	}{
		{"private443", func(r *tailscaleRoutes) { r.AllowFunnel["pilot.ts.net:443"] = false }},
		{"publicConsent", func(r *tailscaleRoutes) { r.AllowFunnel["pilot.ts.net:8444"] = true }},
		{"foreignExactPrefix", func(r *tailscaleRoutes) {
			r.Web["pilot.ts.net:443"].Handlers["/code-factory"] = tailscaleHandler{Proxy: "http://127.0.0.1:9000"}
		}},
		{"foreignChildShadowsMCP", func(r *tailscaleRoutes) {
			r.Web["pilot.ts.net:443"].Handlers["/code-factory/mcp"] = tailscaleHandler{Proxy: "http://127.0.0.1:9000"}
		}},
		{"foreignChildShadowsMetadata", func(r *tailscaleRoutes) {
			r.Web["pilot.ts.net:443"].Handlers["/.well-known/oauth-authorization-server/code-factory/other"] = tailscaleHandler{Proxy: "http://127.0.0.1:9000"}
		}},
		{"extraPrivateHandler", func(r *tailscaleRoutes) {
			r.Web["pilot.ts.net:8444"].Handlers["/other"] = tailscaleHandler{Proxy: "http://127.0.0.1:9000"}
		}},
		{"tcp443WithoutHTTPSProxy", func(r *tailscaleRoutes) { delete(r.Web, "pilot.ts.net:443") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routes := prefixTestLegacyRoutes()
			tc.modify(&routes)
			if err := validateGatewayRoutes(routes, gatewayRoutes(prefixTestConfig())); err == nil {
				t.Fatal("conflicting route was accepted")
			}
		})
	}
}

func prefixTestLegacyRoutes() tailscaleRoutes {
	return tailscaleRoutes{
		TCP: map[string]json.RawMessage{"443": json.RawMessage(`{"HTTPS":true}`), "8443": json.RawMessage(`{"HTTPS":true}`), "8444": json.RawMessage(`{"HTTPS":true}`), "10000": json.RawMessage(`{"HTTPS":true}`)},
		Web: map[string]tailscaleWeb{
			"pilot.ts.net:443":   {Handlers: map[string]tailscaleHandler{"/": {Proxy: "http://127.0.0.1:4440"}, "/code-factory-other": {Proxy: "http://127.0.0.1:4441"}}},
			"pilot.ts.net:8443":  {Handlers: map[string]tailscaleHandler{"/": {Proxy: "http://127.0.0.1:8440"}}},
			"pilot.ts.net:8444":  {Handlers: map[string]tailscaleHandler{"/": {Proxy: "http://127.0.0.1:8789"}}},
			"pilot.ts.net:10000": {Handlers: map[string]tailscaleHandler{"/": {Proxy: "http://127.0.0.1:8788"}}},
		},
		AllowFunnel: map[string]bool{"pilot.ts.net:443": true, "pilot.ts.net:10000": true},
	}
}

type prefixFixture struct {
	dir, fakeDir, configPath string
	legacy                   gateway.Config
}

func newPrefixFixture(t *testing.T) prefixFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home with spaces"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config with spaces"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data with spaces"))
	fakeDir := filepath.Join(root, "fake")
	bin := filepath.Join(root, "bin with spaces")
	for _, dir := range []string{fakeDir, bin} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"tailscale", "systemctl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(prefixFakeCommand), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("CF_PREFIX_FAKE", fakeDir)
	t.Setenv("CF_PREFIX_FAIL_PATH", "")
	fixture := prefixFixture{dir: DataDir(), fakeDir: fakeDir, legacy: prefixTestConfig()}
	fixture.configPath = filepath.Join(fixture.dir, "tailscale.json")
	fixture.legacy.PublicURL = "https://pilot.ts.net:10000"
	// Existing profile marks the fixed listeners as this installation's even
	// when the real developer host already has a daemon listening on those ports.
	for path, value := range map[string]any{fixture.configPath: fixture.legacy, filepath.Join(fakeDir, "routes.json"): prefixTestLegacyRoutes(), filepath.Join(fakeDir, "calls.json"): [][]string{}} {
		if err := AtomicJSON(path, value); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (f prefixFixture) routes(t *testing.T) tailscaleRoutes {
	t.Helper()
	var value tailscaleRoutes
	b, err := os.ReadFile(filepath.Join(f.fakeDir, "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func (f prefixFixture) calls(t *testing.T) [][]string {
	t.Helper()
	var calls [][]string
	b, err := os.ReadFile(filepath.Join(f.fakeDir, "calls.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &calls); err != nil {
		t.Fatal(err)
	}
	return calls
}

func prefixSameRoutes(a, b tailscaleRoutes) bool {
	// Raw TCP JSON can be reformatted by the CLI without changing its meaning.
	aJSON, aErr := json.Marshal(a)
	bJSON, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && string(aJSON) == string(bJSON)
}

func TestConfigureTailscaleMigratesRerunsAndRemovesOnlyOwnedRoutes(t *testing.T) {
	f := newPrefixFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := ConfigureTailscale(ctx, f.dir); err != nil {
		t.Fatal(err)
	}
	want := prefixTestConfig()
	var installed, backup gateway.Config
	for name, target := range map[string]*gateway.Config{f.configPath: &installed, filepath.Join(f.dir, "tailscale-before-443.json"): &backup} {
		b, err := os.ReadFile(name)
		if err != nil || json.Unmarshal(b, target) != nil {
			t.Fatalf("profile missing or invalid: %s, %v", name, err)
		}
	}
	if installed != want || backup != f.legacy {
		t.Fatalf("migration profiles differ: installed=%#v backup=%#v", installed, backup)
	}
	after := f.routes(t)
	for _, route := range gatewayRoutes(want) {
		if after.Web[route.host].Handlers[route.path].Proxy != route.target {
			t.Fatalf("missing planned route: %#v", route)
		}
	}
	for host, original := range prefixTestLegacyRoutes().Web {
		for path, originalHandler := range original.Handlers {
			if !reflect.DeepEqual(after.Web[host].Handlers[path], originalHandler) {
				t.Fatalf("migration changed existing route %s%s", host, path)
			}
		}
	}
	beforeRerun := len(f.calls(t))
	if err := ConfigureTailscale(ctx, f.dir); err != nil {
		t.Fatal(err)
	}
	if !prefixSameRoutes(after, f.routes(t)) {
		t.Fatal("rerun changed routes")
	}
	for _, call := range f.calls(t)[beforeRerun:] {
		if call[0] == "tailscale" && len(call) > 2 && call[2] != "status" && call[1] != "status" {
			t.Fatalf("rerun mutated Tailscale: %#v", call)
		}
	}
	if err := RemoveTailscale(ctx, f.dir); err != nil {
		t.Fatal(err)
	}
	remaining := f.routes(t)
	baseline := prefixTestLegacyRoutes()
	for _, host := range []string{"pilot.ts.net:8444", "pilot.ts.net:10000"} {
		delete(baseline.Web, host)
		delete(baseline.TCP, strings.Split(host, ":")[1])
		delete(baseline.AllowFunnel, host)
	}
	if !prefixSameRoutes(remaining, baseline) {
		t.Fatalf("uninstall changed unrelated routes: got=%#v want=%#v", remaining, baseline)
	}
}

func TestConfigureTailscaleFailedMigrationRollsBackOnlyAdditions(t *testing.T) {
	f := newPrefixFixture(t)
	original, err := os.ReadFile(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CF_PREFIX_FAIL_PATH", "/.well-known/oauth-authorization-server/code-factory")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := ConfigureTailscale(ctx, f.dir); err == nil {
		t.Fatal("induced route failure unexpectedly succeeded")
	}
	restored, err := os.ReadFile(f.configPath)
	if err != nil || string(restored) != string(original) {
		t.Fatal("failed migration did not restore the exact old profile", err)
	}
	if !prefixSameRoutes(f.routes(t), prefixTestLegacyRoutes()) {
		t.Fatal("failed migration did not preserve all original routes")
	}
	var removed []string
	restarts := 0
	for _, call := range f.calls(t) {
		if call[0] == "tailscale" && call[len(call)-1] == "off" {
			removed = append(removed, strings.Join(call, " "))
		}
		if reflect.DeepEqual(call, []string{"systemctl", "--user", "restart", "code-factory-gateway.service"}) {
			restarts++
		}
	}
	if !reflect.DeepEqual(removed, []string{"tailscale funnel --https=443 --set-path=/code-factory off"}) || restarts < 2 {
		t.Fatalf("rollback did not scope removal/restart: removals=%#v restarts=%d", removed, restarts)
	}
}

func TestRemoveTailscaleRefusesRouteReplacedByAnotherService(t *testing.T) {
	f := newPrefixFixture(t)
	routes := prefixTestLegacyRoutes()
	routes.Web["pilot.ts.net:10000"].Handlers["/"] = tailscaleHandler{Proxy: "http://127.0.0.1:9000"}
	if err := AtomicJSON(filepath.Join(f.fakeDir, "routes.json"), routes); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := RemoveTailscale(ctx, f.dir); err == nil {
		t.Fatal("replacement service's route was removed")
	}
	if !prefixSameRoutes(routes, f.routes(t)) {
		t.Fatal("route ownership conflict mutated routes")
	}
	for _, call := range f.calls(t) {
		if call[len(call)-1] == "off" {
			t.Fatalf("route ownership conflict still issued removal: %#v", call)
		}
	}
}

const prefixFakeCommand = `#!/usr/bin/python3
import json,os,pathlib,sys
root=pathlib.Path(os.environ['CF_PREFIX_FAKE'])
name=pathlib.Path(sys.argv[0]).name
args=sys.argv[1:]
log=root/'calls.json'
calls=json.loads(log.read_text())
calls.append([name]+args)
log.write_text(json.dumps(calls))
if name=='systemctl': sys.exit(0)
if args==['status','--json']:
 print(json.dumps({'BackendState':'Running','Self':{'DNSName':'pilot.ts.net.','UserID':1},'User':{'1':{'LoginName':'owner@test'}}}))
 sys.exit(0)
statefile=root/'routes.json'
state=json.loads(statefile.read_text())
if args==['serve','status','--json']:
 print(json.dumps(state));sys.exit(0)
assert args[0] in ['serve','funnel']
port=next(a.split('=',1)[1] for a in args if a.startswith('--https='))
route=next((a.split('=',1)[1] for a in args if a.startswith('--set-path=')),'/')
host='pilot.ts.net:'+port
if args[-1]=='off':
 handlers=state['Web'].get(host,{}).get('Handlers',{})
 handlers.pop(route,None)
 if not handlers:
  state['Web'].pop(host,None);state['TCP'].pop(port,None);state['AllowFunnel'].pop(host,None)
else:
 if route==os.environ.get('CF_PREFIX_FAIL_PATH'): sys.exit(42)
 state['TCP'][port]={'HTTPS':True}
 state['Web'].setdefault(host,{'Handlers':{}})['Handlers'][route]={'Proxy':args[-1]}
 if args[0]=='funnel': state['AllowFunnel'][host]=True
 else: state['AllowFunnel'].pop(host,None)
statefile.write_text(json.dumps(state))
`
