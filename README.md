# zenavo-cli

The Zenavo command line: deploy and operate projects on your own servers from a terminal or CI. It talks to the [REST API](https://docs.zenavo.app/api/overview) with an [API key](https://docs.zenavo.app/account/api-keys), and its command layout follows Zeabur's CLI (`auth`, `workspace`, `context`, `project`, `service`, `deployment`).

Full reference: [docs.zenavo.app/cli](https://docs.zenavo.app/cli).

## Install

macOS and Linux:

```sh
curl -fsSL https://zenavo.app/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://zenavo.app/install.ps1 | iex
```

The scripts ([install.sh](install.sh), [install.ps1](install.ps1)) download the binary for your system from the [latest release](https://github.com/Zenavo-App/zenavo-cli/releases/latest), check it against the release's `SHA256SUMS` and put it on your `PATH`, without root or administrator rights. `ZENAVO_VERSION=v0.2.0` picks a release, `ZENAVO_INSTALL_DIR` the directory and `ZENAVO_DOWNLOAD_URL` a mirror.

Or with a package manager:

```sh
brew install zenavo-app/tap/zenavo                    # Homebrew, macOS and Linux
npm install -g zenavo                                 # npm; or run once with npx zenavo
go install github.com/Zenavo-App/zenavo-cli/cmd/zenavo@latest   # Go 1.26+, from source
```

The npm package installs only the binary for your system, from `@zenavo-app/cli-<platform>-<arch>`, and runs nothing at install time. Every release also lists each binary on its own (`zenavo_<os>_<arch>` for `darwin`, `linux` and `windows`, on `amd64` and `arm64`) with `SHA256SUMS`. To build from source:

```sh
make build            # ./zenavo for this machine
make dist             # dist/zenavo_<os>_<arch> for macOS, Linux and Windows, plus SHA256SUMS
```

Requires Go 1.26+. The only dependency outside the standard library is `golang.org/x/term` (hidden input).

## Sign in

```sh
zenavo auth login                         # shows a code and opens <console>/device
zenavo auth login --scope deploy          # ask for less than full access: read, deploy or full (default)
zenavo auth login --api https://console.example.com   # a self-hosted control plane
zenavo auth login --with-token < key.txt  # an API key created under Account settings → API Keys
```

Browser sign-in uses the OAuth 2.0 device flow: approve the request in the console after typing the code from your terminal (over SSH, open the address on any device). The CLI then receives its own key, named "CLI on <hostname>" and valid for 90 days.

`--scope` sets the [permission](https://docs.zenavo.app/account/api-keys#permissions) the CLI asks for: `read` (look only), `deploy` (deploy and configure existing projects) or `full`. The approver may lower it or limit the key to some projects; the CLI saves what was granted and `zenavo auth status` shows it. A `403` caused by the key's scope prints how to sign in with a wider one, or says to use the console for what no API key may do.

In CI, set `ZENAVO_TOKEN` (and `ZENAVO_API_URL` if not `https://zenavo.app`) instead of signing in, and pass `-i=false` so nothing prompts.

## Quick tour

```sh
zenavo team ls                            # your teams; * marks the current one
zenavo team switch acme                   # alias: zenavo workspace switch
zenavo project create blog --git https://github.com/acme/blog
zenavo env set PORT=3000 DATABASE_PASSWORD   # a bare KEY asks for the value
zenavo deploy                             # every service; follows the build log
zenavo deploy web                         # one service; the others stay as deployed
zenavo service ls                         # state, readiness, restarts, CPU and memory
zenavo logs web -f                        # live runtime logs
zenavo logs --build                       # latest build and deploy log
zenavo domain add blog                    # blog.<platform domain>, or a full domain of your own
zenavo rollback                           # pick an earlier successful deployment
zenavo template deploy -f zeabur.yaml --var DOMAIN=blog
```

## Commands

| Command | |
|---|---|
| `auth login \| logout \| status` | Sign in through the browser (`--scope read\|deploy\|full`) or with an API key; show who you are and what the key may do |
| `team ls \| current \| switch \| clear` | Teams (alias `workspace`); switching clears the saved project and service |
| `context show \| set project \| set service \| clear` | Defaults that later commands use |
| `server ls \| get` | Servers of the team (add servers in the console) |
| `project ls \| get \| create \| delete` | Projects; `create --image/--git` adds a first service |
| `service ls \| deploy \| restart \| suspend \| resume \| exec` | Services; `exec -- <command>` runs in the container |
| `deploy [service]` | Deploy the project or one service and follow the job |
| `logs [service] [-f] [--build]` | Runtime logs, or the latest build log |
| `job ls \| get \| log \| wait` | Jobs (alias `deployment`) |
| `rollback [job-id]` | Roll back to an earlier successful deployment |
| `env ls \| set \| unset` | Service variables; values hidden unless `--reveal`; `--file .env`; `--deploy` |
| `domain ls \| add \| rm` | Generated or custom domains on HTTP ports |
| `template deploy -f <file>` | Create a project from a Zeabur template or a Zenavo export |
| `backup ls \| create` | Database and volume backups |

Every command takes `--help`. Global flags: `--team` (`--workspace`), `--project`/`-p`, `--service`/`-s`, `--json`, `--yes`/`-y`, `-i=false`, `--api`.

Without a saved or given project or service, the CLI uses the only one, asks when stdin is a terminal, and otherwise fails with what to pass. Progress and hints go to stderr; `--json` output on stdout is meant for `jq`. A followed job that fails exits 1; `service exec` exits with the command's status.

## Security

- The key is stored in `~/.config/zenavo/config.json` (or `$ZENAVO_CONFIG`) with mode `0600` in a `0700` directory. The CLI refuses to read the file if other users can.
- The key is bound to the API origin it was saved for: `--api` with another origin does not send it. Plain `http` is allowed only for `localhost`, `127.0.0.1` and `::1`.
- Redirects are never followed, so the key reaches only the configured origin.
- `auth login` never takes the key as an argument, and `env set KEY` (without `=value`) reads the value without echo or from stdin, so secrets can stay out of shell history and `ps`.
- `env ls` hides values unless `--reveal`; references such as `${POSTGRES_HOST}` are shown.
- Destructive commands ask first: `project delete` needs the project name typed (or `--confirm <name>`); rollbacks and init commands need `--yes` without a terminal.
- `auth login --scope read` or `--scope deploy` asks for a key that can do less; the API enforces the scope, the CLI only records it. Sign-in never sends a previously saved key.
- `auth logout` only forgets the key locally; delete it in the console to revoke it.
- Browser sign-in never puts the code in the link: the approver must type the code shown in their terminal, and the console shows the requesting device and network before the Approve button, so a phishing link cannot get a one-click approval. The CLI opens only `<--api>/device`, never an address the server sends.

## Not yet (needs API work)

- **Deploying a local directory** (Zeabur's `deploy` uploads code): Zenavo builds from Git or images only; an upload endpoint and isolated build of uploaded archives are needed first.
- Backup download and restore, server setup, team management and GitHub App installs stay in the console.

## Layout

- `cmd/zenavo` — entry point
- `internal/cli` — command tree, flags, prompts, context resolution
- `internal/api` — HTTP client (no redirects, SSE for live logs) and response types
- `internal/config` — config file, permissions and API origin checks

## Releasing

Push a tag from `main`: `git tag v0.2.0 && git push origin v0.2.0`. The Release workflow tests, runs `make dist` with the tag as the version and publishes the binaries, `SHA256SUMS`, `install.sh` and `install.ps1` as the tag's GitHub release. From that release it then updates `Formula/zenavo.rb` in [Zenavo-App/homebrew-tap](https://github.com/Zenavo-App/homebrew-tap) and publishes the npm packages with provenance. A tag with a hyphen (`v0.3.0-rc.1`) becomes a prerelease: npm gets it under `next`, Homebrew does not get it. `https://zenavo.app/install.sh` and `install.ps1` redirect to the latest release's copies.

Before a release, `make dist VERSION=v0.0.0-test` and then `scripts/test-install.sh`, `scripts/test-npm.sh` and `scripts/test-brew.sh` (macOS, Homebrew) try each way of installing locally; CI runs them on Linux, macOS and Windows.

## License

[Apache License 2.0](LICENSE)
