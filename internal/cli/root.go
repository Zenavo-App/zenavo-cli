package cli

import (
	"flag"
	"fmt"
)

func root() *command {
	return &command{
		name:    "zenavo",
		summary: "Zenavo CLI: deploy and operate projects on your own servers.",
		help: `Context: 'zenavo team switch' and 'zenavo context set project|service' save defaults that later
commands use. --team, --project and --service override them for one command.

Scripts and CI: set ZENAVO_TOKEN (an API key) and pass -i=false so commands never prompt.`,
		sub: []*command{
			authCmd(),
			teamCmd(),
			contextCmd(),
			serverCmd(),
			projectCmd(),
			serviceCmd(),
			deployCmd(),
			logsCmd(),
			jobCmd(),
			rollbackCmd(),
			envCmd(),
			domainCmd(),
			templateCmd(),
			backupCmd(),
			{name: "version", summary: "Print the CLI version", run: func(a *App, _ *flag.FlagSet, _ []string) error {
				fmt.Fprintln(a.Out, "zenavo", Version)
				return nil
			}},
		},
	}
}

// noArgs rejects positional arguments.
func noArgs(args []string) error {
	if len(args) > 0 {
		return usagef("unexpected argument %q", args[0])
	}
	return nil
}

// maxArgs rejects more than n positional arguments.
func maxArgs(args []string, n int) error {
	if len(args) > n {
		return usagef("unexpected argument %q", args[n])
	}
	return nil
}
