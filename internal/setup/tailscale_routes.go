package setup

import (
	"codefactory/internal/gateway"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type tailscaleHandler struct {
	Proxy string
}
type tailscaleWeb struct {
	Handlers map[string]tailscaleHandler
}
type tailscaleRoutes struct {
	TCP         map[string]json.RawMessage
	Web         map[string]tailscaleWeb
	AllowFunnel map[string]bool
}
type gatewayRoute struct {
	host, port, path, target string
	public                   bool
}

func gatewayRoutes(cfg gateway.Config) []gatewayRoute {
	public, publicErr := url.Parse(cfg.PublicURL)
	private, privateErr := url.Parse(cfg.ApprovalURL)
	if publicErr != nil || privateErr != nil || public.Hostname() == "" || private.Hostname() == "" {
		return nil
	}
	port := func(u *url.URL) string {
		if u.Port() == "" {
			return "443"
		}
		return u.Port()
	}
	newRoute := func(u *url.URL, path, target string, isPublic bool) gatewayRoute {
		return gatewayRoute{u.Hostname() + ":" + port(u), port(u), path, target, isPublic}
	}
	routes := []gatewayRoute{newRoute(private, "/", "http://"+cfg.ApprovalListen, false)}
	if public.Path == "" {
		return append(routes, newRoute(public, "/", "http://"+cfg.PublicListen, true))
	}
	return append(routes,
		newRoute(public, public.Path, "http://"+cfg.PublicListen, true),
		newRoute(public, "/.well-known/oauth-authorization-server"+public.Path, "http://"+cfg.PublicListen+"/.well-known/oauth-authorization-server", true),
		newRoute(public, "/.well-known/oauth-protected-resource"+public.Path+"/mcp", "http://"+cfg.PublicListen+"/.well-known/oauth-protected-resource/mcp", true),
	)
}

func inspectTailscaleRoutes(ctx context.Context, binary string) (tailscaleRoutes, error) {
	var routes tailscaleRoutes
	b, err := exec.CommandContext(ctx, binary, "serve", "status", "--json").Output()
	if err != nil {
		return routes, fmt.Errorf("cannot inspect Tailscale routes")
	}
	err = json.Unmarshal(b, &routes)
	return routes, err
}

func validateGatewayRoutes(routes tailscaleRoutes, desired []gatewayRoute) error {
	for _, route := range desired {
		web, exists := routes.Web[route.host]
		if _, occupied := routes.TCP[route.port]; occupied && !exists {
			return fmt.Errorf("Tailscale port %s is already used by another service", route.port)
		}
		if exists && routes.AllowFunnel[route.host] != route.public {
			return fmt.Errorf("Tailscale port %s has a different public/private boundary; refusing to expose or replace its services", route.port)
		}
		if handler, exists := web.Handlers[route.path]; exists && handler.Proxy != route.target {
			return fmt.Errorf("Tailscale path %s on port %s is already used by another service", route.path, route.port)
		}
		for path := range web.Handlers {
			if route.path != "/" && strings.HasPrefix(path, route.path+"/") {
				return fmt.Errorf("Tailscale path under %s on port %s belongs to another handler", route.path, route.port)
			}
		}
		if !route.public && exists && (len(web.Handlers) != 1 || web.Handlers["/"].Proxy != route.target) {
			return fmt.Errorf("private Tailscale port %s is already used by another service", route.port)
		}
	}
	return nil
}

func (route gatewayRoute) arguments(remove bool) []string {
	command := "serve"
	if route.public {
		command = "funnel"
	}
	args := []string{command, "--https=" + route.port}
	if route.path != "/" {
		args = append(args, "--set-path="+route.path)
	}
	if remove {
		return append(args, "off")
	}
	return append(args, "--bg", "--yes", route.target)
}

// Re-read ownership before removal so another application's replacement wins.
func removeGatewayRoute(ctx context.Context, binary string, route gatewayRoute) error {
	routes, err := inspectTailscaleRoutes(ctx, binary)
	if err != nil {
		return err
	}
	web := routes.Web[route.host]
	handler, exists := web.Handlers[route.path]
	if !exists {
		return nil
	}
	if handler.Proxy != route.target || (route.path == "/" && len(web.Handlers) != 1) {
		return fmt.Errorf("Tailscale route changed by another service; refusing to remove it")
	}
	return runTailscale(ctx, binary, route.arguments(true)...)
}
