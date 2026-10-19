package cli

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

func serverCmd() *command {
	return &command{
		name:    "server",
		summary: "List your servers",
		help:    "Add servers in the console: setup needs an SSH password or key, which the CLI does not handle.",
		sub: []*command{
			{
				name: "ls", aliases: []string{"list"}, summary: "List the team's servers",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					list, err := a.servers()
					if err != nil {
						return err
					}
					rows := [][]string{}
					for _, s := range list {
						rows = append(rows, []string{s.Name, s.ID, s.Host, s.Status, deref(s.K3sVersion)})
					}
					return a.table(list, []string{"NAME", "ID", "HOST", "STATUS", "K3S"}, rows)
				},
			},
			{
				name: "get", args: "<name|id>", summary: "Show one server",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if len(args) != 1 {
						return usagef("name the server")
					}
					s, err := a.pickServer(args[0])
					if err != nil {
						return err
					}
					var full map[string]any
					if err := a.get("/servers/"+api.P(s.ID), &full); err != nil {
						return err
					}
					if a.jsonOut {
						return a.printJSON(full)
					}
					fmt.Fprintf(a.Out, "%s (%s)\n  host     %s\n  status   %s %s\n  os       %s %s\n  k3s      %s\n  seen     %s\n",
						s.Name, s.ID, s.Host, s.Status, deref(s.StatusMessage), deref(s.OS), deref(s.Arch), deref(s.K3sVersion), agoPtr(s.LastSeen))
					return nil
				},
			},
		},
	}
}

func agoPtr(f *float64) string {
	if f == nil {
		return "-"
	}
	return ago(*f)
}

func projectCmd() *command {
	var server, image, git, serviceName, confirmName string
	var detach bool
	return &command{
		name:    "project",
		summary: "Create, list and delete projects",
		sub: []*command{
			{
				name: "ls", aliases: []string{"list"}, summary: "List the team's projects",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					list, err := a.projects()
					if err != nil {
						return err
					}
					rows := [][]string{}
					for _, p := range list {
						mark := " "
						if p.ID == a.cfg.Project {
							mark = "*"
						}
						rows = append(rows, []string{mark, p.Name, p.ID, fmt.Sprint(len(p.Services)), projectState(&p), ago(p.Created)})
					}
					return a.table(list, []string{"", "NAME", "ID", "SERVICES", "STATE", "CREATED"}, rows)
				},
			},
			{
				name: "get", args: "[name|id]", summary: "Show a project and its services",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := maxArgs(args, 1); err != nil {
						return err
					}
					if len(args) == 1 {
						a.project = args[0]
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					if a.jsonOut {
						return a.printJSON(p)
					}
					fmt.Fprintf(a.Out, "%s (%s)\n  server   %s\n  state    %s\n", p.Name, p.ID, p.ServerID, projectState(p))
					for _, b := range p.Blockers {
						fmt.Fprintf(a.Out, "  blocker  %s\n", b)
					}
					fmt.Fprintln(a.Out)
					rows := [][]string{}
					for _, s := range p.Services {
						rows = append(rows, []string{s.Name, s.Key(), s.Source, portsText(s.Ports)})
					}
					return a.table(nil, []string{"SERVICE", "ID", "SOURCE", "PORTS"}, rows)
				},
			},
			{
				name: "create", args: "[name]", summary: "Create a project on one of your servers",
				help: `With --image or --git the project starts with one service, as in the console's
"Deploy" box; otherwise it is empty. The new project becomes the saved context.`,
				flags: func(fs *flag.FlagSet) {
					fs.StringVar(&server, "server", "", "server `name or ID` (default: the only server, or ask)")
					fs.StringVar(&image, "image", "", "first service from a Docker `image`")
					fs.StringVar(&git, "git", "", "first service from a Git repository `URL`")
					fs.StringVar(&serviceName, "service-name", "", "name of the first service (default: from the image or repository)")
				},
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := maxArgs(args, 1); err != nil {
						return err
					}
					if image != "" && git != "" {
						return usagef("use --image or --git, not both")
					}
					source := image + git
					name := ""
					if len(args) == 1 {
						name = strings.TrimSpace(args[0])
					}
					if name == "" && source != "" {
						name = nameFrom(source)
					}
					if name == "" {
						if !a.interactive {
							return usagef("name the project")
						}
						var err error
						if name, err = a.prompt("Project name: "); err != nil || name == "" {
							return errors.New("a project name is required")
						}
					}
					tid, err := a.teamID()
					if err != nil {
						return err
					}
					srv, err := a.pickServer(server)
					if err != nil {
						return err
					}
					services := []map[string]any{}
					if source != "" {
						svc := serviceName
						if svc == "" {
							svc = nameFrom(source)
						}
						if svc == "" {
							svc = "app"
						}
						src := map[string]any{"image": image}
						if git != "" {
							src = map[string]any{"git": git}
						}
						services = append(services, map[string]any{"name": svc, "source": src, "ports": []any{}})
					}
					body := map[string]any{"team_id": tid, "server_id": srv.ID,
						"manifest": map[string]any{"version": 1, "name": name, "services": services}}
					var p api.Project
					if err := a.post("/projects", body, &p); err != nil {
						return err
					}
					a.cfg.SetTeam(p.TeamID)
					a.cfg.SetProject(p.ID)
					if err := a.save(); err != nil {
						return err
					}
					if a.jsonOut {
						return a.printJSON(p)
					}
					a.info("Created project %s (%s) on %s", p.Name, p.ID, srv.Name)
					if len(p.Services) > 0 {
						a.info("Set its port and variables in the console or with 'zenavo env set', then run: zenavo deploy")
					}
					return nil
				},
			},
			{
				name: "delete", aliases: []string{"rm"}, args: "[name|id]", summary: "Delete a project and remove it from its server",
				flags: func(fs *flag.FlagSet) {
					fs.StringVar(&confirmName, "confirm", "", "the project `name`, to confirm without a prompt")
					fs.BoolVar(&detach, "detach", false, "do not wait for the removal job")
				},
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := maxArgs(args, 1); err != nil {
						return err
					}
					if len(args) == 1 {
						a.project = args[0]
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					if confirmName == "" {
						if !a.interactive {
							return usagef("pass --confirm %q to delete without a prompt", p.Name)
						}
						a.info("This deletes %s (%s), its services and their data on the server.", p.Name, p.ID)
						if confirmName, err = a.prompt("Type the project name to confirm: "); err != nil {
							return err
						}
					}
					if confirmName != p.Name {
						return errors.New("the name does not match; nothing was deleted")
					}
					var out struct {
						ID      string `json:"id"`
						Deleted bool   `json:"deleted"`
					}
					if err := a.post("/projects/"+api.P(p.ID)+"/delete", map[string]string{"confirmation": confirmName}, &out); err != nil {
						return err
					}
					if p.ID == a.cfg.Project {
						a.cfg.SetProject("")
						if err := a.save(); err != nil {
							return err
						}
					}
					if out.ID != "" {
						return a.followJob(out.ID, detach)
					}
					a.info("Deleted %s", p.Name)
					return nil
				},
			},
		},
	}
}

func projectState(p *api.Project) string {
	switch {
	case p.Running:
		return "running"
	case p.ActiveJob != nil || p.RuntimeJob != nil:
		return "stopped"
	case len(p.Services) == 0:
		return "empty"
	}
	return "not deployed"
}

func portsText(ports []api.Port) string {
	var out []string
	for _, p := range ports {
		s := fmt.Sprintf("%d/%s", p.Port, strings.ToLower(p.Type))
		if p.Domain != nil {
			s += " " + *p.Domain
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ", ")
}

var nonName = regexp.MustCompile(`[^a-z0-9-]+`)

// nameFrom derives a service name from an image (ghcr.io/org/app:1 → app) or a repository URL.
func nameFrom(source string) string {
	s := source
	if u, err := url.Parse(source); err == nil && u.Host != "" {
		s = u.Path
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	s = path.Base(s)
	if i := strings.IndexAny(s, ":@"); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(nonName.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func serviceCmd() *command {
	var detach, ack bool
	job := func(kind, done string) *command {
		return &command{
			name: kind, summary: strings.ToUpper(kind[:1]) + kind[1:] + " a service",
			flags: func(fs *flag.FlagSet) { fs.BoolVar(&detach, "detach", false, "do not wait for the job") },
			run: func(a *App, _ *flag.FlagSet, args []string) error {
				if err := maxArgs(args, 1); err != nil {
					return err
				}
				if len(args) == 1 {
					a.service = args[0]
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
				if err := a.post("/projects/"+api.P(p.ID)+"/services/"+api.P(name)+"/"+kind, nil, &q); err != nil {
					return err
				}
				a.info("%s %s…", done, name)
				return a.followJob(q.ID, detach)
			},
		}
	}
	return &command{
		name:    "service",
		aliases: []string{"svc"},
		summary: "List and operate services",
		sub: []*command{
			{
				name: "ls", aliases: []string{"list"}, summary: "List the project's services and their state",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					var rt api.Runtime
					if err := a.get("/projects/"+api.P(p.ID)+"/runtime", &rt); err != nil {
						return err
					}
					state := map[string]api.ServiceState{}
					for _, s := range rt.Services {
						state[s.Service] = s
					}
					rows := [][]string{}
					for _, s := range p.Services {
						st, ok := state[s.Key()]
						status, ready, restarts, usage := "not deployed", "-", "-", "-"
						if ok {
							status, ready, restarts = st.State, fmt.Sprintf("%d/%d", st.Ready, st.Desired), fmt.Sprint(st.Restarts)
							if st.Health != nil {
								status += " (" + *st.Health + ")"
							}
							if st.CPUMilli != nil && st.MemoryBytes != nil {
								usage = fmt.Sprintf("%dm / %dMi", *st.CPUMilli, *st.MemoryBytes>>20)
							}
						}
						rows = append(rows, []string{s.Name, s.Key(), s.Source, status, ready, restarts, usage, portsText(s.Ports)})
					}
					if a.jsonOut {
						return a.printJSON(map[string]any{"services": p.Services, "runtime": rt})
					}
					return a.table(nil, []string{"SERVICE", "ID", "SOURCE", "STATE", "READY", "RESTARTS", "CPU / MEM", "PORTS"}, rows)
				},
			},
			{
				name: "deploy", args: "[service]", summary: "Build and deploy one service; the others stay as deployed",
				flags: func(fs *flag.FlagSet) {
					fs.BoolVar(&detach, "detach", false, "do not wait for the job")
					fs.BoolVar(&ack, "acknowledge-init", false, "allow initialization commands, which can change data")
				},
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := maxArgs(args, 1); err != nil {
						return err
					}
					if len(args) == 1 {
						a.service = args[0]
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					name, err := a.currentService(p)
					if err != nil {
						return err
					}
					return a.startDeploy(p, name, ack, detach)
				},
			},
			job("restart", "Restarting"),
			job("suspend", "Suspending"),
			job("resume", "Resuming"),
			{
				name: "exec", args: "-- <command…>", summary: "Run a command in the service's container",
				help: "The command runs with sh -c, for up to 60 seconds. Example: zenavo service exec -s web -- ls -la /app",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if len(args) == 0 {
						return usagef("give the command after --")
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					name, err := a.currentService(p)
					if err != nil {
						return err
					}
					var out api.ExecResult
					if err := a.post("/projects/"+api.P(p.ID)+"/services/"+api.P(name)+"/exec", map[string]string{"command": strings.Join(args, " ")}, &out); err != nil {
						return err
					}
					if a.jsonOut {
						return a.printJSON(out)
					}
					fmt.Fprint(a.Out, out.Output)
					if out.Output != "" && !strings.HasSuffix(out.Output, "\n") {
						fmt.Fprintln(a.Out)
					}
					if out.ExitCode != 0 {
						return &ExitError{Code: out.ExitCode}
					}
					return nil
				},
			},
		},
	}
}
