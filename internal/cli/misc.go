package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

func domainCmd() *command {
	var portID string
	return &command{
		name:    "domain",
		summary: "Add and remove service domains",
		help: `A name without dots (my-app) is a generated domain under the platform domain, e.g. my-app.zenavo.app.
A full name (www.example.com) is your own domain: point its DNS at the server, then deploy; HTTPS is automatic.`,
		sub: []*command{
			{
				name: "ls", aliases: []string{"list"}, summary: "List the project's domains",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					type row struct {
						Domain  string `json:"domain"`
						Service string `json:"service"`
						Port    int    `json:"port"`
					}
					var list []row
					rows := [][]string{}
					for _, s := range p.Services {
						for _, port := range s.Ports {
							for _, d := range port.AllDomains() {
								list = append(list, row{d, s.Name, port.Port})
								rows = append(rows, []string{"https://" + d, s.Name, fmt.Sprint(port.Port)})
							}
						}
					}
					return a.table(list, []string{"DOMAIN", "SERVICE", "PORT"}, rows)
				},
			},
			{
				name: "add", args: "<name|domain>", summary: "Attach a domain to a service's HTTP port",
				flags: func(fs *flag.FlagSet) {
					fs.StringVar(&portID, "port", "", "port `ID` (default: the service's only HTTP port)")
				},
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if len(args) != 1 {
						return usagef("give a name (my-app) or a domain (www.example.com)")
					}
					domain := strings.ToLower(strings.TrimSpace(args[0]))
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					name, err := a.currentService(p)
					if err != nil {
						return err
					}
					m, err := a.loadManifest(p)
					if err != nil {
						return err
					}
					port, err := m.port(name, portID)
					if err != nil {
						return err
					}
					if !strings.Contains(domain, ".") {
						var out api.Project
						if err := a.post("/projects/"+api.P(p.ID)+"/services/"+api.P(name)+"/domains", map[string]any{"name": domain, "port_id": port["id"]}, &out); err != nil {
							return err
						}
						// The port may have other domains: the new one is <name>.<platform domain>.
						for _, s := range out.Services {
							for _, pt := range s.Ports {
								for _, d := range pt.AllDomains() {
									if s.Key() == name && pt.ID == port["id"] && strings.HasPrefix(d, domain+".") {
										domain = d
									}
								}
							}
						}
					} else {
						if slices.Contains(rawDomains(port), domain) {
							return fmt.Errorf("%s already serves %s", name, domain)
						}
						setRawDomains(port, append(rawDomains(port), domain))
						if err := a.saveManifest(m); err != nil {
							return err
						}
						a.info("Point an A/AAAA record for %s at the server's address.", domain)
					}
					a.info("Added https://%s to %s. Apply with: zenavo deploy %s", domain, name, name)
					return nil
				},
			},
			{
				name: "rm", aliases: []string{"remove"}, args: "<domain>", summary: "Remove a domain",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if len(args) != 1 {
						return usagef("name the domain to remove")
					}
					domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(args[0]), "https://"))
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					var providers struct {
						AppDomain string `json:"app_domain"`
					}
					_ = a.client.Get(a.ctx, "/auth/providers", &providers)
					if providers.AppDomain != "" && strings.HasSuffix(domain, "."+providers.AppDomain) {
						if err := a.do("DELETE", "/projects/"+api.P(p.ID)+"/domains/"+api.P(domain), nil, nil); err != nil {
							return err
						}
						a.info("Removed %s", domain)
						return nil
					}
					m, err := a.loadManifest(p)
					if err != nil {
						return err
					}
					found := ""
					services, _ := m.manifest["services"].([]any)
					for _, raw := range services {
						svc, _ := raw.(map[string]any)
						ports, _ := svc["ports"].([]any)
						for _, rp := range ports {
							if port, ok := rp.(map[string]any); ok && slices.Contains(rawDomains(port), domain) {
								setRawDomains(port, slices.DeleteFunc(rawDomains(port), func(d string) bool { return d == domain }))
								found, _ = svc["name"].(string)
							}
						}
					}
					if found == "" {
						return fmt.Errorf("no service in %s uses %s", p.Name, domain)
					}
					if err := a.saveManifest(m); err != nil {
						return err
					}
					a.info("Removed %s from %s. Apply with: zenavo deploy %s", domain, found, found)
					return nil
				},
			},
		},
	}
}

func templateCmd() *command {
	var file, server string
	var vars multiFlag
	var deploy bool
	return &command{
		name:    "template",
		summary: "Create projects from Zeabur templates or Zenavo exports",
		sub: []*command{
			{
				name: "deploy", summary: "Create a project from a template file",
				help: `The file is a Zeabur template (YAML) or a project exported from Zenavo (JSON).
Template variables are given with --var KEY=value (repeatable). Use -f - to read stdin.`,
				flags: func(fs *flag.FlagSet) {
					fs.StringVar(&file, "file", "", "template `file`, - for stdin (-f)")
					fs.StringVar(&file, "f", "", "same as --file")
					fs.StringVar(&server, "server", "", "server `name or ID` (default: the only server, or ask)")
					fs.Var(&vars, "var", "template variable `KEY=value` (repeatable)")
					fs.BoolVar(&deploy, "deploy", false, "deploy the new project right away")
				},
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					if file == "" {
						return usagef("pass -f <file>")
					}
					var r io.Reader = a.In
					if file != "-" {
						f, err := os.Open(file)
						if err != nil {
							return err
						}
						defer f.Close()
						r = f
					}
					raw, err := io.ReadAll(io.LimitReader(r, 2<<20+1))
					if err != nil {
						return err
					}
					if len(raw) > 2<<20 {
						return errors.New("the template is larger than 2 MB")
					}
					variables := map[string]string{}
					for _, kv := range vars {
						k, v, ok := strings.Cut(kv, "=")
						if !ok {
							return usagef("--var needs KEY=value, got %q", kv)
						}
						variables[k] = v
					}
					tid, err := a.teamID()
					if err != nil {
						return err
					}
					var pv api.ImportPreview
					if err := a.post("/imports/preview", map[string]any{"text": string(raw), "team_id": tid, "variables": variables}, &pv); err != nil {
						return err
					}
					if !a.jsonOut {
						a.info("Template %s:", pv.Name)
						for _, s := range pv.Services {
							a.info("  %-20s %s", s.Name, s.Source)
						}
						for _, n := range pv.Notes {
							a.info("  note: %s", n)
						}
					}
					if !pv.Ready {
						return fmt.Errorf("the template cannot be deployed yet:\n  %s", strings.Join(pv.Blockers, "\n  "))
					}
					srv, err := a.pickServer(server)
					if err != nil {
						return err
					}
					if err := a.confirm(fmt.Sprintf("Create project %s on %s?", pv.Name, srv.Name)); err != nil {
						return err
					}
					var p api.Project
					if err := a.post("/imports/"+api.P(pv.DraftID)+"/commit", map[string]string{"server_id": srv.ID}, &p); err != nil {
						return err
					}
					a.cfg.SetTeam(p.TeamID)
					a.cfg.SetProject(p.ID)
					if err := a.save(); err != nil {
						return err
					}
					a.info("Created project %s (%s)", p.Name, p.ID)
					if deploy {
						return a.startDeploy(&p, "", false, false)
					}
					if a.jsonOut {
						return a.printJSON(p)
					}
					a.info("Deploy it with: zenavo deploy")
					return nil
				},
			},
		},
	}
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func backupCmd() *command {
	var engine string
	var detach bool
	return &command{
		name:    "backup",
		summary: "List and create backups",
		help:    "Download and restore backups in the console, where restores ask for typed confirmation.",
		sub: []*command{
			{
				name: "ls", aliases: []string{"list"}, summary: "List the project's backups",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					var list []api.Backup
					if err := a.get("/backups"+api.Q("team_id", p.TeamID), &list); err != nil {
						return err
					}
					mine := []api.Backup{}
					rows := [][]string{}
					for _, b := range list {
						if b.ProjectID != p.ID {
							continue
						}
						mine = append(mine, b)
						rows = append(rows, []string{b.ID, b.Service, b.Engine, fmt.Sprintf("%.1f MB", float64(b.Size)/1e6), ago(b.Created)})
					}
					return a.table(mine, []string{"ID", "SERVICE", "ENGINE", "SIZE", "CREATED"}, rows)
				},
			},
			{
				name: "create", summary: "Back up a service's database or volume",
				flags: func(fs *flag.FlagSet) {
					fs.StringVar(&engine, "engine", "", "postgres, mysql or volume:<volume-id>")
					fs.BoolVar(&detach, "detach", false, "do not wait for the job")
				},
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					if engine == "" {
						return usagef("pass --engine postgres, mysql or volume:<id>")
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					name, err := a.currentService(p)
					if err != nil {
						return err
					}
					var q api.Queued
					if err := a.post("/projects/"+api.P(p.ID)+"/backups", map[string]string{"service": name, "engine": engine}, &q); err != nil {
						return err
					}
					a.info("Backing up %s (%s)", name, engine)
					return a.followJob(q.ID, detach)
				},
			},
		},
	}
}

// rawDomains is every domain of a port in a manifest loaded as JSON: "domain" first, then "domains".
func rawDomains(port map[string]any) []string {
	first, _ := port["domain"].(string)
	if first == "" {
		return nil
	}
	out := []string{first}
	more, _ := port["domains"].([]any)
	for _, d := range more {
		if s, ok := d.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// setRawDomains makes domains a port's domains: the first is "domain", the rest "domains".
func setRawDomains(port map[string]any, domains []string) {
	port["domain"], port["domains"] = nil, []string{}
	if len(domains) > 0 {
		port["domain"], port["domains"] = domains[0], domains[1:]
	}
}
