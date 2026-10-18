package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

// pollInterval is how often a followed job is read; tests shorten it.
var pollInterval = 2 * time.Second

func deployCmd() *command {
	var detach, ack bool
	return &command{
		name:    "deploy",
		args:    "[service]",
		summary: "Deploy the project, or one service, and follow the build",
		help: `Without a service, every service is built and deployed with the saved configuration.
With a service (or --service), only that service changes; the others stay as deployed.

Zenavo builds from Git or pulls images; it does not upload a local directory. Push your code first,
or create a service with: zenavo project create --git <url>`,
		flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&detach, "detach", false, "print the job ID and return without waiting")
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
			service := ""
			if a.service != "" {
				if service, err = a.currentService(p); err != nil {
					return err
				}
			}
			return a.startDeploy(p, service, ack, detach)
		},
	}
}

// startDeploy queues a project deploy (service == "") or a service deploy and follows it.
func (a *App) startDeploy(p *api.Project, service string, ack, detach bool) error {
	path := "/projects/" + api.P(p.ID) + "/deploy"
	what := p.Name
	if service != "" {
		path = "/projects/" + api.P(p.ID) + "/services/" + api.P(service) + "/deploy"
		what = service + " in " + p.Name
	}
	var q api.Queued
	err := a.post(path, map[string]bool{"acknowledge_init": ack}, &q)
	var ae *api.Error
	if !ack && errors.As(err, &ae) && ae.Status == 409 && strings.Contains(ae.Detail, "Initialization commands") {
		if cerr := a.confirm("This deployment runs initialization commands, which can change data. Continue?"); cerr != nil {
			return fmt.Errorf("%s (or pass --acknowledge-init)", cerr)
		}
		err = a.post(path, map[string]bool{"acknowledge_init": true}, &q)
	}
	if err != nil {
		return err
	}
	a.info("Deploying %s (job %s)", what, q.ID)
	return a.followJob(q.ID, detach)
}

// followJob prints a job's log as it grows and its per-service progress until it finishes.
// A failed job is an error, so scripts can rely on the exit status.
func (a *App) followJob(id string, detach bool) error {
	if detach {
		if a.jsonOut {
			return a.printJSON(api.Queued{ID: id})
		}
		fmt.Fprintln(a.Out, id)
		return nil
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	defer signal.Stop(stop)
	printed := 0
	progress := map[string]string{}
	for {
		var j api.Job
		if err := a.get("/jobs/"+api.P(id), &j); err != nil {
			return err
		}
		if !a.jsonOut {
			if j.Log != nil {
				log := *j.Log
				if len(log) < printed {
					printed = 0 // the log was replaced; print it again
				}
				fmt.Fprint(a.Err, log[printed:])
				printed = len(log)
			}
			names := make([]string, 0, len(j.Progress))
			for n := range j.Progress {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				if progress[n] != j.Progress[n] {
					progress[n] = j.Progress[n]
					a.info("» %s: %s", n, j.Progress[n])
				}
			}
		}
		if j.Done() {
			if a.jsonOut {
				j.Log = nil
				if err := a.printJSON(j); err != nil {
					return err
				}
			} else if printed > 0 {
				a.info("")
			}
			if j.Status != "succeeded" {
				return fmt.Errorf("job %s %s", j.ID, j.Status)
			}
			if !a.jsonOut {
				a.info("✔ %s %s", j.Kind, j.Status)
			}
			return nil
		}
		select {
		case <-a.ctx.Done():
			return a.ctx.Err()
		case <-stop:
			a.info("\nStopped following. The job keeps running: zenavo job get %s", id)
			return &ExitError{Code: 130}
		case <-time.After(pollInterval):
		}
	}
}

func logsCmd() *command {
	var follow, build bool
	return &command{
		name:    "logs",
		aliases: []string{"log"},
		args:    "[service]",
		summary: "Show a service's runtime logs, or the latest build log",
		help: `Runtime logs are the last 200 lines of the newest instance; --follow streams new lines.
--build prints the log of the project's latest deploy job instead (zenavo job log <id> for others).`,
		flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&follow, "follow", false, "stream new lines until interrupted (-f)")
			fs.BoolVar(&follow, "f", false, "same as --follow")
			fs.BoolVar(&build, "build", false, "print the latest build and deploy log")
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
			if build {
				j, err := a.latestJob(p.ID, "deploy", "rollback")
				if err != nil {
					return err
				}
				if follow && !j.Done() {
					return a.followJob(j.ID, false)
				}
				return a.printJobLog(j.ID)
			}
			name, err := a.currentService(p)
			if err != nil {
				return err
			}
			base := "/projects/" + api.P(p.ID) + "/logs/" + api.P(name)
			if !follow {
				var out struct {
					Text string `json:"text"`
				}
				if err := a.get(base, &out); err != nil {
					return err
				}
				if a.jsonOut {
					return a.printJSON(out)
				}
				fmt.Fprint(a.Out, out.Text)
				return nil
			}
			if err := a.authed(); err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(a.ctx, os.Interrupt)
			defer cancel()
			return a.client.Stream(ctx, base+"/stream", func(data string) error {
				var line string
				if json.Unmarshal([]byte(data), &line) != nil {
					line = data
				}
				if a.jsonOut {
					return json.NewEncoder(a.Out).Encode(map[string]string{"line": line})
				}
				_, err := fmt.Fprintln(a.Out, line)
				return err
			})
		},
	}
}

// latestJob is the project's newest job of one of these kinds.
func (a *App) latestJob(pid string, kinds ...string) (*api.Job, error) {
	var list []api.Job
	if err := a.get("/jobs"+api.Q("project_id", pid), &list); err != nil {
		return nil, err
	}
	for i := range list {
		if len(kinds) == 0 || contains(kinds, list[i].Kind) {
			return &list[i], nil
		}
	}
	return nil, errors.New("this project has not been deployed yet")
}

func (a *App) printJobLog(id string) error {
	var j api.Job
	if err := a.get("/jobs/"+api.P(id), &j); err != nil {
		return err
	}
	if a.jsonOut {
		return a.printJSON(j)
	}
	if j.Log != nil {
		fmt.Fprint(a.Out, *j.Log)
	}
	return nil
}

func jobCmd() *command {
	return &command{
		name:    "job",
		aliases: []string{"deployment"},
		summary: "List, inspect and wait for jobs (deploys, restarts, backups…)",
		sub: []*command{
			{
				name: "ls", aliases: []string{"list"}, summary: "List the project's recent jobs",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					p, err := a.currentProject()
					if err != nil {
						return err
					}
					var list []api.Job
					if err := a.get("/jobs"+api.Q("project_id", p.ID), &list); err != nil {
						return err
					}
					rows := [][]string{}
					for _, j := range list {
						mark := " "
						if p.ActiveJob != nil && *p.ActiveJob == j.ID {
							mark = "*"
						}
						rows = append(rows, []string{mark, j.ID, j.Kind, j.Status, ago(j.Created), duration(j)})
					}
					return a.table(list, []string{"", "ID", "KIND", "STATUS", "CREATED", "DURATION"}, rows)
				},
			},
			{
				name: "get", args: "[id]", summary: "Show a job's status and progress (default: the latest)",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					j, err := a.jobArg(args)
					if err != nil {
						return err
					}
					var full api.Job
					if err := a.get("/jobs/"+api.P(j), &full); err != nil {
						return err
					}
					full.Log = nil
					if a.jsonOut {
						return a.printJSON(full)
					}
					fmt.Fprintf(a.Out, "%s  %s  %s  %s  %s\n", full.ID, full.Kind, full.Status, ago(full.Created), duration(full))
					for n, st := range full.Progress {
						fmt.Fprintf(a.Out, "  %s: %s\n", n, st)
					}
					return nil
				},
			},
			{
				name: "log", aliases: []string{"logs"}, args: "[id]", summary: "Print a job's log (default: the latest)",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					j, err := a.jobArg(args)
					if err != nil {
						return err
					}
					return a.printJobLog(j)
				},
			},
			{
				name: "wait", aliases: []string{"watch"}, args: "[id]", summary: "Follow a job until it finishes; fails if the job fails",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					j, err := a.jobArg(args)
					if err != nil {
						return err
					}
					return a.followJob(j, false)
				},
			},
		},
	}
}

func (a *App) jobArg(args []string) (string, error) {
	if err := maxArgs(args, 1); err != nil {
		return "", err
	}
	if len(args) == 1 {
		return args[0], nil
	}
	p, err := a.currentProject()
	if err != nil {
		return "", err
	}
	j, err := a.latestJob(p.ID)
	if err != nil {
		return "", err
	}
	return j.ID, nil
}

func duration(j api.Job) string {
	if j.Finished == nil {
		return "-"
	}
	return (time.Duration(*j.Finished-j.Created) * time.Second).String()
}

func rollbackCmd() *command {
	var detach bool
	return &command{
		name:    "rollback",
		args:    "[job-id]",
		summary: "Roll the project back to an earlier successful deployment",
		flags:   func(fs *flag.FlagSet) { fs.BoolVar(&detach, "detach", false, "do not wait for the job") },
		run: func(a *App, _ *flag.FlagSet, args []string) error {
			if err := maxArgs(args, 1); err != nil {
				return err
			}
			p, err := a.currentProject()
			if err != nil {
				return err
			}
			target := ""
			if len(args) == 1 {
				target = args[0]
			} else {
				if !a.interactive {
					return usagef("name the deployment job to roll back to (see zenavo job ls)")
				}
				var list []api.Job
				if err := a.get("/jobs"+api.Q("project_id", p.ID), &list); err != nil {
					return err
				}
				var ids, labels []string
				for _, j := range list {
					if (j.Kind == "deploy" || j.Kind == "rollback") && j.Status == "succeeded" && (p.ActiveJob == nil || *p.ActiveJob != j.ID) {
						ids = append(ids, j.ID)
						labels = append(labels, fmt.Sprintf("%s  %s  %s", j.ID, j.Kind, ago(j.Created)))
					}
				}
				if len(ids) == 0 {
					return errors.New("no earlier successful deployment to roll back to")
				}
				i, err := a.choose("Roll back to:", labels)
				if err != nil {
					return err
				}
				target = ids[i]
			}
			if err := a.confirm(fmt.Sprintf("Roll %s back to %s?", p.Name, target)); err != nil {
				return err
			}
			var q api.Queued
			if err := a.post("/projects/"+api.P(p.ID)+"/rollback", map[string]string{"target_job": target}, &q); err != nil {
				return err
			}
			a.info("Rolling back %s (job %s)", p.Name, q.ID)
			return a.followJob(q.ID, detach)
		},
	}
}
