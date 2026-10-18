package cli

import (
	"flag"
	"fmt"
)

func teamCmd() *command {
	return &command{
		name:    "team",
		aliases: []string{"workspace"},
		summary: "List and switch teams",
		sub: []*command{
			{
				name: "ls", aliases: []string{"list"}, summary: "List your teams",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					me, err := a.me()
					if err != nil {
						return err
					}
					current, _ := a.teamID()
					rows := [][]string{}
					for _, t := range me.Teams {
						mark := " "
						if t.ID == current {
							mark = "*"
						}
						name := t.Name
						if t.Personal {
							name += " (personal)"
						}
						rows = append(rows, []string{mark, name, t.ID, t.Role})
					}
					return a.table(me.Teams, []string{"", "NAME", "ID", "ROLE"}, rows)
				},
			},
			{
				name: "current", summary: "Show the current team",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					tid, err := a.teamID()
					if err != nil {
						return err
					}
					me, err := a.me()
					if err != nil {
						return err
					}
					t, err := findTeam(me.Teams, tid)
					if err != nil {
						return err
					}
					if a.jsonOut {
						return a.printJSON(t)
					}
					fmt.Fprintf(a.Out, "%s (%s, %s)\n", t.Name, t.ID, t.Role)
					return nil
				},
			},
			{
				name: "switch", args: "[name|id]", summary: "Save the team that later commands use",
				help: "Switching teams clears the saved project and service.",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := maxArgs(args, 1); err != nil {
						return err
					}
					me, err := a.me()
					if err != nil {
						return err
					}
					var ref string
					if len(args) == 1 {
						ref = args[0]
					} else if a.interactive {
						names := make([]string, len(me.Teams))
						for i, t := range me.Teams {
							names[i] = fmt.Sprintf("%s (%s)", t.Name, t.Role)
						}
						i, err := a.choose("Team:", names)
						if err != nil {
							return err
						}
						ref = me.Teams[i].ID
					} else {
						return usagef("name the team to switch to")
					}
					t, err := findTeam(me.Teams, ref)
					if err != nil {
						return err
					}
					a.cfg.SetTeam(t.ID)
					if err := a.save(); err != nil {
						return err
					}
					a.info("Switched to team %s", t.Name)
					return nil
				},
			},
			{
				name: "clear", summary: "Go back to your personal team",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					a.cfg.SetTeam("")
					if err := a.save(); err != nil {
						return err
					}
					a.info("Using your personal team")
					return nil
				},
			},
		},
	}
}

func contextCmd() *command {
	return &command{
		name:    "context",
		summary: "Show or set the saved team, project and service",
		sub: []*command{
			{
				name: "show", aliases: []string{"get"}, summary: "Show the saved context",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					out := map[string]string{"api": a.client.Base, "team": a.cfg.Team, "project": a.cfg.Project, "service": a.cfg.Service}
					if a.jsonOut {
						return a.printJSON(out)
					}
					for _, k := range []string{"api", "team", "project", "service"} {
						v := out[k]
						if v == "" {
							v = "-"
							if k == "team" {
								v = "(personal)"
							}
						}
						fmt.Fprintf(a.Out, "%-8s %s\n", k, v)
					}
					return nil
				},
			},
			{
				name:    "set",
				summary: "Save a project or service",
				sub: []*command{
					{
						name: "project", args: "[name|id]", summary: "Save the project that later commands use",
						run: func(a *App, _ *flag.FlagSet, args []string) error {
							if err := maxArgs(args, 1); err != nil {
								return err
							}
							if len(args) == 1 {
								a.project = args[0]
							} else if a.project == "" {
								a.cfg.Project = "" // pick afresh
							}
							p, err := a.currentProject()
							if err != nil {
								return err
							}
							if a.team != "" {
								a.cfg.SetTeam(p.TeamID)
							}
							a.cfg.SetProject(p.ID)
							if err := a.save(); err != nil {
								return err
							}
							a.info("Project: %s (%s)", p.Name, p.ID)
							return nil
						},
					},
					{
						name: "service", args: "[name]", summary: "Save the service that later commands use",
						run: func(a *App, _ *flag.FlagSet, args []string) error {
							if err := maxArgs(args, 1); err != nil {
								return err
							}
							p, err := a.currentProject()
							if err != nil {
								return err
							}
							if len(args) == 1 {
								a.service = args[0]
							} else if a.service == "" {
								a.cfg.Service = ""
							}
							name, err := a.currentService(p)
							if err != nil {
								return err
							}
							a.cfg.SetProject(p.ID)
							a.cfg.Service = name
							if err := a.save(); err != nil {
								return err
							}
							a.info("Service: %s in %s", name, p.Name)
							return nil
						},
					},
				},
			},
			{
				name: "clear", summary: "Forget the saved project and service",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					a.cfg.SetProject("")
					return a.save()
				},
			},
		},
	}
}
