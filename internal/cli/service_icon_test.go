package cli

import (
	"strings"
	"testing"
)

func TestServiceAddIcon(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--image", "ghcr.io/acme/app:1", "--port", "8080", "--icon", "laravel")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if svc := added(t, f); svc["icon"] != "laravel" {
		t.Errorf("icon: %v", svc)
	}

	// An unknown icon fails before anything is saved; a URL is not an icon.
	for _, bad := range []string{"https://example.com/logo.svg", "Laravel", "nope"} {
		f, cfg := setup(t)
		r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--image", "nginx", "--icon", bad)
		if r.code == 0 || !strings.Contains(r.stderr, "unknown icon") || called(f, "PUT /api/projects/proj_shop") {
			t.Errorf("%s: %+v", bad, r)
		}
	}

	// A chosen icon wins over the build plan's; without a port the plan is still read for the port.
	f, cfg = setup(t)
	if r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "add", "--git", "acme/api", "--branch", "main", "--icon", "go"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if svc := added(t, f); svc["icon"] != "go" || svc["ports"].([]any)[0].(map[string]any)["port"] != float64(3000) {
		t.Errorf("git with icon: %v", svc)
	}
}

func TestServiceIcon(t *testing.T) {
	f, cfg := setup(t)
	r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "icon", "-s", "db")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "postgresql" {
		t.Fatalf("show: %+v", r)
	}
	if r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "icon", "-s", "web"); r.code != 0 || !strings.HasPrefix(r.stdout, "none") {
		t.Fatalf("show generic: %+v", r)
	}

	r = zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "icon", "PHP", "-s", "web")
	if r.code != 0 {
		t.Fatalf("set: %+v", r)
	}
	web := func() map[string]any {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.manifest["x_unknown"] != "keep me" {
			t.Errorf("unknown field lost: %v", f.manifest)
		}
		return f.manifest["services"].([]any)[0].(map[string]any)
	}
	if w := web(); w["icon"] != "php" || w["env"].(map[string]any)["SECRET"] != "hunter2" {
		t.Errorf("after set: %v", w)
	}
	if r := zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "icon", "auto", "-s", "web"); r.code != 0 {
		t.Fatalf("auto: %+v", r)
	}
	if _, ok := web()["icon"]; ok {
		t.Errorf("auto kept the icon: %v", web())
	}

	r = zenavo(t, cfg, "", nil, "-p", "proj_shop", "service", "icon", "https://example.com/x.svg", "-s", "web")
	if r.code == 0 || !strings.Contains(r.stderr, "unknown icon") {
		t.Fatalf("bad key: %+v", r)
	}

	r = zenavo(t, cfg, "", nil, "service", "icon", "--list")
	if r.code != 0 || !strings.Contains(r.stdout, "nodejs") || !strings.Contains(r.stdout, "postgresql") || !strings.Contains(r.stdout, "Databases") {
		t.Fatalf("list: %+v", r)
	}
}

// The CLI's icon list is the engine's manifest.Icons, in order.
func TestIconsMatchEngine(t *testing.T) {
	var keys []string
	for _, g := range icons {
		keys = append(keys, strings.Fields(g.keys)...)
	}
	engine := "nodejs bun deno python go php ruby rust java dotnet kotlin elixir html nextjs react vue nuxt svelte astro angular vite express nestjs laravel django flask fastapi spring rails docker nginx caddy traefik rabbitmq elasticsearch minio postgresql mysql mariadb mongodb redis valkey clickhouse wordpress ghost n8n grafana prometheus box globe server database cpu bot mail cog"
	if strings.Join(keys, " ") != engine {
		t.Errorf("icons %v", keys)
	}
}
