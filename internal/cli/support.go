package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
	"github.com/Zenavo-App/zenavo-cli/internal/config"
	"golang.org/x/term"
)

// load reads the config and builds the API client. The saved token is used only for the saved API origin;
// $ZENAVO_TOKEN overrides it (for CI) and is never written to disk.
func (a *App) load() error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	if p := a.Env("ZENAVO_CONFIG"); p != "" {
		path = p
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	a.cfgPath, a.cfg = path, cfg
	base := a.apiURL
	if base == "" {
		base = a.Env("ZENAVO_API_URL")
	}
	if base == "" {
		base = cfg.API
	}
	if base == "" {
		base = config.DefaultAPI
	}
	if base, err = config.NormalizeAPI(base); err != nil {
		return err
	}
	token := a.Env("ZENAVO_TOKEN")
	if token == "" && cfg.API == base {
		token = cfg.Token
	}
	a.client = api.New(base, token, "zenavo-cli/"+Version)
	return nil
}

func (a *App) save() error { return a.cfg.Save(a.cfgPath) }

// authed fails early with a hint when there is no token.
func (a *App) authed() error {
	if a.client.Token == "" {
		if a.cfg.Token != "" && a.cfg.API != a.client.Base {
			return fmt.Errorf("not signed in to %s (the saved key is for %s); run: zenavo auth login --api %s", a.client.Base, a.cfg.API, a.client.Base)
		}
		return errors.New("not signed in; run: zenavo auth login (or set ZENAVO_TOKEN)")
	}
	return nil
}

func (a *App) get(path string, out any) error {
	if err := a.authed(); err != nil {
		return err
	}
	return a.client.Get(a.ctx, path, out)
}

func (a *App) post(path string, body, out any) error {
	if err := a.authed(); err != nil {
		return err
	}
	return a.client.Post(a.ctx, path, body, out)
}

func (a *App) do(method, path string, body, out any) error {
	if err := a.authed(); err != nil {
		return err
	}
	return a.client.Do(a.ctx, method, path, body, out)
}

// ---- Output ----

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table prints rows under a header, or v as JSON with --json.
func (a *App) table(v any, header []string, rows [][]string) error {
	if a.jsonOut {
		return a.printJSON(v)
	}
	if len(rows) == 0 {
		fmt.Fprintln(a.Err, "(none)")
		return nil
	}
	w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	return w.Flush()
}

// info prints progress and hints to stderr, so stdout stays clean for --json and pipes.
func (a *App) info(format string, args ...any) { fmt.Fprintf(a.Err, format+"\n", args...) }

func ago(unix float64) string {
	if unix == 0 {
		return "-"
	}
	d := time.Since(time.Unix(int64(unix), 0)).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return time.Unix(int64(unix), 0).Format("2006-01-02")
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// ---- Prompts ----

// confirm asks a yes/no question. Without a terminal it needs --yes.
func (a *App) confirm(question string) error {
	if a.yes {
		return nil
	}
	if !a.interactive {
		return errors.New(question + " Pass --yes to confirm without a prompt")
	}
	fmt.Fprintf(a.Err, "%s [y/N] ", question)
	line, _ := a.stdin.ReadString('\n')
	if s := strings.ToLower(strings.TrimSpace(line)); s == "y" || s == "yes" {
		return nil
	}
	return errors.New("cancelled")
}

func (a *App) prompt(label string) (string, error) {
	fmt.Fprint(a.Err, label)
	line, err := a.stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no input")
	}
	return strings.TrimSpace(line), nil
}

// secret reads a value without echo when stdin is a terminal, else one line of stdin.
func (a *App) secret(label string) (string, error) {
	if f, ok := a.In.(*os.File); ok && a.TTY {
		fmt.Fprint(a.Err, label)
		raw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.Err)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	line, err := a.stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no input on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// choose shows a numbered list and returns the chosen index.
func (a *App) choose(title string, items []string) (int, error) {
	fmt.Fprintln(a.Err, title)
	for i, it := range items {
		fmt.Fprintf(a.Err, "  %d) %s\n", i+1, it)
	}
	for {
		s, err := a.prompt(fmt.Sprintf("Choose 1-%d: ", len(items)))
		if err != nil {
			return 0, err
		}
		var n int
		if _, err := fmt.Sscan(s, &n); err == nil && n >= 1 && n <= len(items) {
			return n - 1, nil
		}
	}
}

// ---- Context resolution ----

func (a *App) me() (*api.Me, error) {
	var me api.Me
	return &me, a.get("/auth/me", &me)
}

// findTeam matches a team by ID or name (case-insensitive).
func findTeam(teams []api.Team, ref string) (*api.Team, error) {
	var hits []*api.Team
	for i := range teams {
		t := &teams[i]
		if t.ID == ref {
			return t, nil
		}
		if strings.EqualFold(t.Name, ref) || (t.Personal && strings.EqualFold(ref, "personal")) {
			hits = append(hits, t)
		}
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("no team %q; see zenavo team ls", ref)
	case 1:
		return hits[0], nil
	}
	return nil, fmt.Errorf("several teams are named %q; use the ID", ref)
}

// teamID is --team, the saved team, or the personal team.
func (a *App) teamID() (string, error) {
	ref := a.team
	if ref == "" {
		ref = a.cfg.Team
	}
	if strings.HasPrefix(ref, "team_") {
		return ref, nil
	}
	me, err := a.me()
	if err != nil {
		return "", err
	}
	if ref == "" {
		for _, t := range me.Teams {
			if t.Personal {
				return t.ID, nil
			}
		}
		if len(me.Teams) > 0 {
			return me.Teams[0].ID, nil
		}
		return "", errors.New("you are not in any team")
	}
	t, err := findTeam(me.Teams, ref)
	if err != nil {
		return "", err
	}
	return t.ID, nil
}

func (a *App) projects() ([]api.Project, error) {
	tid, err := a.teamID()
	if err != nil {
		return nil, err
	}
	var list []api.Project
	return list, a.get("/projects"+api.Q("team_id", tid), &list)
}

func findProject(list []api.Project, ref string) (*api.Project, error) {
	var hits []*api.Project
	for i := range list {
		p := &list[i]
		if p.ID == ref {
			return p, nil
		}
		if strings.EqualFold(p.Name, ref) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("no project %q in this team; see zenavo project ls", ref)
	case 1:
		return hits[0], nil
	}
	return nil, fmt.Errorf("several projects are named %q; use the ID", ref)
}

// currentProject is --project, the saved project, or (interactively) one the user picks.
func (a *App) currentProject() (*api.Project, error) {
	ref := a.project
	if ref == "" && a.team == "" {
		ref = a.cfg.Project
	}
	if strings.HasPrefix(ref, "proj_") {
		var p api.Project
		if err := a.get("/projects/"+api.P(ref), &p); err != nil {
			if api.IsStatus(err, 404) && ref == a.cfg.Project && a.project == "" {
				return nil, errors.New("the saved project no longer exists; run: zenavo context set project")
			}
			return nil, err
		}
		return &p, nil
	}
	list, err := a.projects()
	if err != nil {
		return nil, err
	}
	if ref != "" {
		return findProject(list, ref)
	}
	if len(list) == 0 {
		return nil, errors.New("this team has no projects; create one with: zenavo project create")
	}
	if !a.interactive {
		return nil, errors.New("no project selected: pass --project, or run: zenavo context set project")
	}
	names := make([]string, len(list))
	for i, p := range list {
		names[i] = fmt.Sprintf("%s (%s)", p.Name, p.ID)
	}
	i, err := a.choose("Project:", names)
	if err != nil {
		return nil, err
	}
	return &list[i], nil
}

// currentService is --service, the saved service of this project, the only service, or one the user picks.
func (a *App) currentService(p *api.Project) (string, error) {
	ref := a.service
	if ref == "" && p.ID == a.cfg.Project {
		ref = a.cfg.Service
	}
	if ref != "" {
		for _, s := range p.Services {
			if s.Name == ref {
				return s.Name, nil
			}
		}
		return "", fmt.Errorf("project %s has no service %q; see zenavo service ls", p.Name, ref)
	}
	switch {
	case len(p.Services) == 0:
		return "", fmt.Errorf("project %s has no services", p.Name)
	case len(p.Services) == 1:
		return p.Services[0].Name, nil
	case !a.interactive:
		return "", errors.New("no service selected: pass --service, or run: zenavo context set service")
	}
	names := make([]string, len(p.Services))
	for i, s := range p.Services {
		names[i] = s.Name
	}
	i, err := a.choose("Service:", names)
	if err != nil {
		return "", err
	}
	return names[i], nil
}

func (a *App) servers() ([]api.Server, error) {
	tid, err := a.teamID()
	if err != nil {
		return nil, err
	}
	var list []api.Server
	return list, a.get("/servers"+api.Q("team_id", tid), &list)
}

// pickServer resolves a server by ID or name; with none given it uses the only server or asks.
func (a *App) pickServer(ref string) (*api.Server, error) {
	list, err := a.servers()
	if err != nil {
		return nil, err
	}
	if ref != "" {
		var hits []*api.Server
		for i := range list {
			if list[i].ID == ref {
				return &list[i], nil
			}
			if strings.EqualFold(list[i].Name, ref) || list[i].Host == ref {
				hits = append(hits, &list[i])
			}
		}
		switch len(hits) {
		case 0:
			return nil, fmt.Errorf("no server %q in this team; see zenavo server ls", ref)
		case 1:
			return hits[0], nil
		}
		return nil, fmt.Errorf("several servers match %q; use the ID", ref)
	}
	switch {
	case len(list) == 0:
		return nil, errors.New("this team has no servers; add one in the console first")
	case len(list) == 1:
		return &list[0], nil
	case !a.interactive:
		return nil, errors.New("several servers: pass --server")
	}
	names := make([]string, len(list))
	for i, s := range list {
		names[i] = fmt.Sprintf("%s (%s, %s)", s.Name, s.Host, s.Status)
	}
	i, err := a.choose("Server:", names)
	if err != nil {
		return nil, err
	}
	return &list[i], nil
}

// ---- Help ----

func (a *App) printGroupHelp(c *command, path string) {
	w := a.Err
	if c.summary != "" {
		fmt.Fprintln(w, c.summary)
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Usage:\n  %s <command> [flags]\n\nCommands:\n", path)
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	for _, s := range c.sub {
		name := s.name
		if len(s.aliases) > 0 {
			name += " (" + strings.Join(s.aliases, ", ") + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", name, s.summary)
	}
	tw.Flush()
	if c.help != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimSpace(c.help))
	}
	fmt.Fprintf(w, "\nRun '%s <command> --help' for details.\n", path)
}

var globalFlags = map[string]bool{"api": true, "team": true, "workspace": true, "project": true, "p": true,
	"service": true, "s": true, "json": true, "yes": true, "y": true, "i": true}

func (a *App) printCommandHelp(c *command, fs *flag.FlagSet, path string) {
	w := a.Err
	fmt.Fprintln(w, c.summary)
	fmt.Fprintf(w, "\nUsage:\n  %s", path)
	if c.args != "" {
		fmt.Fprint(w, " "+c.args)
	}
	fmt.Fprintln(w, " [flags]")
	var own, global []*flag.Flag
	fs.VisitAll(func(f *flag.Flag) {
		if strings.HasPrefix(f.Usage, "same as ") {
			return // an alias; the main flag's usage names it
		}
		if globalFlags[f.Name] {
			global = append(global, f)
		} else {
			own = append(own, f)
		}
	})
	printFlags := func(title string, list []*flag.Flag) {
		if len(list) == 0 {
			return
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		fmt.Fprintf(w, "\n%s:\n", title)
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		for _, f := range list {
			name, usage := flag.UnquoteUsage(f)
			dash := "--"
			if len(f.Name) == 1 {
				dash = "-"
			}
			fmt.Fprintf(tw, "  %s%s %s\t%s\n", dash, f.Name, name, usage)
		}
		tw.Flush()
	}
	printFlags("Flags", own)
	if c.help != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimSpace(c.help))
	}
	printFlags("Global flags", global)
}

func contains(list []string, s string) bool { return indexOf(list, s) >= 0 }

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
