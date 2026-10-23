package cli

import (
	"flag"
	"fmt"
	"slices"
	"strings"
)

// icons are the built-in service icons, the engine's manifest.Icons: a service's "icon" is one of these or unset
// (inferred from its image). The control plane checks the key; this list is for --list and early errors.
var icons = []struct{ group, keys string }{
	{"Languages", "nodejs bun deno python go php ruby rust java dotnet kotlin elixir html"},
	{"Frameworks", "nextjs react vue nuxt svelte astro angular vite express nestjs laravel django flask fastapi spring rails"},
	{"Servers & middleware", "docker nginx caddy traefik rabbitmq elasticsearch minio"},
	{"Databases", "postgresql mysql mariadb mongodb redis valkey clickhouse"},
	{"Applications", "wordpress ghost n8n grafana prometheus"},
	{"Generic", "box globe server database cpu bot mail cog"},
}

func isIcon(key string) bool {
	for _, g := range icons {
		if slices.Contains(strings.Fields(g.keys), key) {
			return true
		}
	}
	return false
}

// providerIcons maps the build plan's provider to an icon, as the console does; a Dockerfile build is docker.
var providerIcons = map[string]string{"nodejs": "nodejs", "bun": "bun", "deno": "deno", "python": "python", "go": "go",
	"golang": "go", "php": "php", "ruby": "ruby", "rust": "rust", "java": "java", "dotnet": "dotnet", "kotlin": "kotlin",
	"elixir": "elixir", "static": "html"}

func checkIcon(key string) error {
	if key != "" && !isIcon(key) {
		return usagef("unknown icon %q; see zenavo service icon --list", key)
	}
	return nil
}

func serviceIconCmd() *command {
	var list bool
	return &command{
		name: "icon", args: "[key|auto]", summary: "Show or change the service's icon in the console",
		help: `Without an argument, prints the service's icon. A key from --list sets it; auto goes back to the icon
inferred from the image (node, php, postgres and other known images get their logo). The icon is only
shown in the console, so changing it needs no deploy.

  zenavo service icon laravel -s web
  zenavo service icon auto -s web
  zenavo service icon --list`,
		flags: func(fs *flag.FlagSet) { fs.BoolVar(&list, "list", false, "list the built-in icons") },
		run: func(a *App, _ *flag.FlagSet, args []string) error {
			if list {
				if err := noArgs(args); err != nil {
					return err
				}
				out := map[string][]string{}
				rows := [][]string{}
				for _, g := range icons {
					out[g.group] = strings.Fields(g.keys)
					rows = append(rows, []string{g.group, strings.ReplaceAll(g.keys, " ", ", ")})
				}
				return a.table(out, []string{"GROUP", "ICONS"}, rows)
			}
			if err := maxArgs(args, 1); err != nil {
				return err
			}
			key := ""
			if len(args) == 1 {
				if key = strings.ToLower(strings.TrimSpace(args[0])); key == "" {
					return usagef("give an icon key, or auto")
				}
				if key != "auto" {
					if err := checkIcon(key); err != nil {
						return err
					}
				}
			}
			p, err := a.currentProject()
			if err != nil {
				return err
			}
			name, err := a.currentService(p)
			if err != nil {
				return err
			}
			if key == "" {
				for _, s := range p.Services {
					if s.Key() != name {
						continue
					}
					if a.jsonOut {
						return a.printJSON(map[string]any{"service": name, "icon": s.Icon})
					}
					if s.Icon == "" {
						fmt.Fprintln(a.Out, "none (a generic icon; set one with zenavo service icon <key>)")
					} else {
						fmt.Fprintln(a.Out, s.Icon)
					}
				}
				return nil
			}
			m, err := a.loadManifest(p)
			if err != nil {
				return err
			}
			svc, err := m.service(name)
			if err != nil {
				return err
			}
			if key == "auto" {
				delete(svc, "icon")
			} else {
				svc["icon"] = key
			}
			if err := a.saveManifest(m); err != nil {
				return err
			}
			if key == "auto" {
				a.info("%s's icon is inferred from its image again", svc["name"])
			} else {
				a.info("Set %s's icon to %s", svc["name"], key)
			}
			return nil
		},
	}
}
