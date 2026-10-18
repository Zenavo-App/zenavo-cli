// Package cli implements the zenavo command tree.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Zenavo-App/zenavo-cli/internal/api"
	"github.com/Zenavo-App/zenavo-cli/internal/config"
	"golang.org/x/term"
)

// Version is set at build time with -ldflags "-X github.com/Zenavo-App/zenavo-cli/internal/cli.Version=…".
var Version = "dev"

// App holds the global flags, the saved config and the I/O of one invocation.
type App struct {
	In       io.Reader
	Out, Err io.Writer
	Env      func(string) string
	// TTY reports whether stdin is a terminal; prompts are shown only then.
	TTY bool

	ctx     context.Context
	cfgPath string
	cfg     *config.Config
	client  *api.Client
	stdin   *bufio.Reader

	// Global flags.
	apiURL, team, project, service string
	jsonOut, yes                   bool
	interactive                    bool
}

// ExitError ends the program with Code without printing anything more.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// usageError is a mistake in the command line; the command's help follows it.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return &usageError{fmt.Sprintf(format, args...)} }

type command struct {
	name    string
	aliases []string
	args    string // positional arguments in the usage line
	summary string
	help    string // extra text after the flags
	sub     []*command
	flags   func(fs *flag.FlagSet) // command-specific flags
	run     func(a *App, fs *flag.FlagSet, args []string) error
}

// Main runs the CLI and returns the process exit code.
func Main(ctx context.Context, args []string) int {
	a := &App{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Env: os.Getenv, TTY: term.IsTerminal(int(os.Stdin.Fd()))}
	return a.Run(ctx, args)
}

// Run executes one command line.
func (a *App) Run(ctx context.Context, args []string) int {
	a.ctx = ctx
	a.stdin = bufio.NewReader(a.In)
	a.interactive = a.TTY
	err := a.dispatch(root(), args, "zenavo")
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(a.Err, "error:", err)
	if api.IsStatus(err, 401) {
		fmt.Fprintln(a.Err, "Sign in again with: zenavo auth login")
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}

func (a *App) dispatch(c *command, args []string, path string) error {
	if len(c.sub) > 0 {
		if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
			a.printGroupHelp(c, path)
			if len(args) == 0 && c.run == nil {
				return &ExitError{Code: 2}
			}
			return nil
		}
		if strings.HasPrefix(args[0], "-") && c.run == nil {
			// Global flags before the command name: zenavo --team acme project ls
			fs := a.newFlags(path)
			rest, err := parseInterspersed(fs, args, true)
			if err != nil {
				return err
			}
			return a.dispatch(c, rest, path)
		}
		for _, s := range c.sub {
			if s.name == args[0] || contains(s.aliases, args[0]) {
				return a.dispatch(s, args[1:], path+" "+s.name)
			}
		}
		if c.run == nil {
			return usagef("unknown command %q for %s; see %s --help", args[0], path, path)
		}
	}
	fs := a.newFlags(path)
	if c.flags != nil {
		c.flags(fs)
	}
	fs.Usage = func() { a.printCommandHelp(c, fs, path) }
	rest, err := parseInterspersed(fs, args, false)
	if err != nil {
		return err
	}
	if err := a.load(); err != nil {
		return err
	}
	err = c.run(a, fs, rest)
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintln(a.Err, "error:", ue.msg)
		fmt.Fprintf(a.Err, "Run '%s --help' for usage.\n", path)
		return &ExitError{Code: 2}
	}
	return err
}

// newFlags returns a flag set with the global flags. Values already set (before the command name) stay.
func (a *App) newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.StringVar(&a.apiURL, "api", a.apiURL, "control plane `URL` (default $ZENAVO_API_URL or "+config.DefaultAPI+")")
	fs.StringVar(&a.team, "team", a.team, "team `name or ID` for this command (alias --workspace)")
	fs.StringVar(&a.team, "workspace", a.team, "same as --team")
	fs.StringVar(&a.project, "project", a.project, "project `name or ID` for this command (-p)")
	fs.StringVar(&a.project, "p", a.project, "same as --project")
	fs.StringVar(&a.service, "service", a.service, "service `name` for this command (-s)")
	fs.StringVar(&a.service, "s", a.service, "same as --service")
	fs.BoolVar(&a.jsonOut, "json", a.jsonOut, "print JSON")
	fs.BoolVar(&a.yes, "yes", a.yes, "answer yes to confirmations (-y)")
	fs.BoolVar(&a.yes, "y", a.yes, "same as --yes")
	fs.BoolVar(&a.interactive, "i", a.interactive, "interactive prompts (default when stdin is a terminal; -i=false for scripts)")
	return fs
}

// parseInterspersed parses flags anywhere in args; everything after "--" is positional.
// With stopAtArg it stops at the first positional (used before a command name).
func parseInterspersed(fs *flag.FlagSet, args []string, stopAtArg bool) ([]string, error) {
	var tail []string
	if !stopAtArg {
		if i := indexOf(args, "--"); i >= 0 {
			args, tail = args[:i], args[i+1:]
		}
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 || stopAtArg {
			return append(append(pos, args...), tail...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
