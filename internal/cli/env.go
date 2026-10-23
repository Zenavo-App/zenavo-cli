package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

var reference = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)

// onlyReferences reports whether v just points at other variables, such as ${POSTGRES_HOST}; that is not secret.
func onlyReferences(v string) bool {
	return reference.MatchString(v) && strings.TrimSpace(reference.ReplaceAllString(v, "")) == ""
}

func envCmd() *command {
	var reveal, deploy bool
	var file string
	return &command{
		name:    "env",
		aliases: []string{"variable", "var"},
		summary: "List and change a service's environment variables",
		help: `Changes are saved to the project's configuration. Variables are read when the service starts, so
'zenavo service restart' puts changed ones into effect without building it again. A variable its build reads
(a build argument references it), and changes besides variables, take a deploy (pass --deploy to deploy the
service right away).`,
		sub: []*command{
			{
				name: "ls", aliases: []string{"list"}, summary: "List variables (values hidden unless --reveal)",
				flags: func(fs *flag.FlagSet) { fs.BoolVar(&reveal, "reveal", false, "print values") },
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
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
					env, err := m.env(name)
					if err != nil {
						return err
					}
					keys := make([]string, 0, len(env))
					for k := range env {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					out := map[string]string{}
					rows := [][]string{}
					for _, k := range keys {
						v := fmt.Sprint(env[k])
						if !reveal && v != "" && !onlyReferences(v) {
							v = "••••••"
						}
						out[k] = v
						rows = append(rows, []string{k, v})
					}
					return a.table(out, []string{"KEY", "VALUE"}, rows)
				},
			},
			{
				name: "set", args: "KEY=value… | KEY", summary: "Set variables",
				help: `KEY=value sets a value. A bare KEY asks for the value without echoing it (or reads one line
of stdin), which keeps secrets out of shell history:

  zenavo env set DATABASE_PASSWORD
  zenavo env set --file .env`,
				flags: func(fs *flag.FlagSet) {
					fs.StringVar(&file, "file", "", "read KEY=value lines from a dotenv `file`")
					fs.BoolVar(&deploy, "deploy", false, "deploy the service after saving")
				},
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if len(args) == 0 && file == "" {
						return usagef("give KEY=value pairs, a KEY, or --file")
					}
					set := map[string]string{}
					var order []string
					add := func(k, v string) error {
						if !envKey.MatchString(k) {
							return fmt.Errorf("invalid variable name %q", k)
						}
						if _, dup := set[k]; !dup {
							order = append(order, k)
						}
						set[k] = v
						return nil
					}
					if file != "" {
						pairs, err := readDotenv(file)
						if err != nil {
							return err
						}
						for _, kv := range pairs {
							if err := add(kv[0], kv[1]); err != nil {
								return err
							}
						}
					}
					for _, arg := range args {
						k, v, ok := strings.Cut(arg, "=")
						if !ok {
							if !envKey.MatchString(k) {
								return fmt.Errorf("invalid variable name %q", k)
							}
							var err error
							if v, err = a.secret(k + ": "); err != nil {
								return err
							}
						}
						if err := add(k, v); err != nil {
							return err
						}
					}
					return a.editEnv(deploy, func(env map[string]any) string {
						for _, k := range order {
							env[k] = set[k]
						}
						return "Set " + strings.Join(order, ", ")
					})
				},
			},
			{
				name: "unset", aliases: []string{"rm"}, args: "KEY…", summary: "Remove variables",
				flags: func(fs *flag.FlagSet) { fs.BoolVar(&deploy, "deploy", false, "deploy the service after saving") },
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if len(args) == 0 {
						return usagef("name the variables to remove")
					}
					var missing error
					err := a.editEnv(deploy, func(env map[string]any) string {
						for _, k := range args {
							if _, ok := env[k]; !ok {
								missing = fmt.Errorf("no variable %q; nothing was changed", k)
								return ""
							}
							delete(env, k)
						}
						return "Removed " + strings.Join(args, ", ")
					})
					if missing != nil {
						return missing
					}
					return err
				},
			},
		},
	}
}

// editEnv loads the configuration, applies change to the service's variables and saves it.
// change returns a message, or "" to abort without saving.
func (a *App) editEnv(deploy bool, change func(env map[string]any) string) error {
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
	env, err := m.env(name)
	if err != nil {
		return err
	}
	msg := change(env)
	if msg == "" {
		return nil
	}
	if err := a.saveManifest(m); err != nil {
		return err
	}
	a.info("%s on %s", msg, name)
	if deploy {
		return a.startDeploy(p, name, false, false)
	}
	// What applying it takes: a restart when only variables changed, otherwise a deploy (and why).
	var plan struct{ Needs, Reason string }
	if err := a.get("/projects/"+api.P(p.ID)+"/services/"+api.P(name)+"/apply", &plan); err == nil {
		switch plan.Needs {
		case "nothing":
			return nil
		case "restart":
			a.info("Apply with: zenavo service restart %s (no rebuild)", name)
			return nil
		}
		if plan.Reason != "" {
			a.info("%s", plan.Reason)
		}
	}
	a.info("Apply with: zenavo deploy %s", name)
	return nil
}

// readDotenv parses KEY=value lines; # comments, "export " prefixes and surrounding quotes are allowed.
func readDotenv(path string) ([][2]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out [][2]string
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=value", path, n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out = append(out, [2]string{k, v})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New(path + " has no variables")
	}
	return out, nil
}
