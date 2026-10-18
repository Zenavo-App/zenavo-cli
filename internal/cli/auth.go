package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
)

func authCmd() *command {
	var withToken bool
	return &command{
		name:    "auth",
		summary: "Sign in and out",
		sub: []*command{
			{
				name:    "login",
				summary: "Sign in through the browser, or with an API key",
				help: `Without flags, the CLI shows a code and opens <api>/device; sign in there, type the code and approve.
The CLI then receives its own API key (named "CLI on <this computer>", valid 90 days), saved in
~/.config/zenavo/config.json (mode 0600) and sent only to the --api origin.

Over SSH, open the printed address on any device and type the code there.

With --with-token the key is read from stdin instead (never from an argument, which would leave it in
shell history): zenavo auth login --with-token < key.txt. In CI, set ZENAVO_TOKEN instead of signing in.`,
				flags: func(fs *flag.FlagSet) {
					fs.BoolVar(&withToken, "with-token", false, "read an API key from stdin instead of signing in through the browser")
				},
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					var token string
					var err error
					if withToken {
						if a.TTY {
							a.info("Create an API key at %s/account/api-keys", a.client.Base)
						}
						if token, err = a.secret("Paste your API key: "); err != nil {
							return err
						}
						token = strings.TrimSpace(token)
						if !strings.HasPrefix(token, "zv_pat_") {
							return errors.New("that is not a Zenavo API key (they start with zv_pat_)")
						}
					} else if token, err = a.deviceLogin(); err != nil {
						return err
					}
					c := api.New(a.client.Base, token, a.client.UserAgent)
					var me api.Me
					if err := c.Get(a.ctx, "/auth/me", &me); err != nil {
						if api.IsStatus(err, 401) {
							return errors.New("the key was rejected: it may be expired or deleted")
						}
						return err
					}
					if a.cfg.API != c.Base {
						// A different control plane: context from the old one means nothing here.
						a.cfg.SetTeam("")
					}
					a.cfg.API, a.cfg.Token = c.Base, token
					if err := a.save(); err != nil {
						return err
					}
					a.info("Signed in to %s as %s", c.Base, me.User.Email)
					return nil
				},
			},
			{
				name:    "logout",
				summary: "Forget the saved API key",
				help:    "This removes the key from this computer only. Delete it in the console to revoke it.",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					if a.cfg.Token == "" {
						a.info("Not signed in")
						return nil
					}
					a.cfg.Token = ""
					if err := a.save(); err != nil {
						return err
					}
					a.info("Signed out of %s. The key still works until you delete it at %s/account/api-keys", a.cfg.API, a.cfg.API)
					return nil
				},
			},
			{
				name:    "status",
				aliases: []string{"whoami"},
				summary: "Show who you are signed in as",
				run: func(a *App, _ *flag.FlagSet, args []string) error {
					if err := noArgs(args); err != nil {
						return err
					}
					me, err := a.me()
					if err != nil {
						return err
					}
					if a.jsonOut {
						return a.printJSON(map[string]any{"api": a.client.Base, "user": me.User, "teams": me.Teams})
					}
					source := "saved key"
					if a.Env("ZENAVO_TOKEN") != "" {
						source = "ZENAVO_TOKEN"
					}
					fmt.Fprintf(a.Out, "%s as %s (%s)\n", a.client.Base, me.User.Email, source)
					return nil
				},
			},
		},
	}
}
