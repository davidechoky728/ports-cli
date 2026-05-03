# ports — project-aware lsof for macOS

> The macOS CLI that finally tells you **which project** owns port 3000.
>
> **Website:** [portscli.com](https://portscli.com)

[![Website](https://img.shields.io/badge/website-portscli.com-7ee787)](https://portscli.com)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![macOS](https://img.shields.io/badge/macOS-12%2B-black?logo=apple)](https://portscli.com)
[![Built with Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
[![Release](https://img.shields.io/github/v/release/erdemylmaz/ports-cli)](https://github.com/erdemylmaz/ports-cli/releases)

`ports` is a small, single-binary, zero-dependency Go CLI for macOS that
shows what's actually listening on your laptop and **why** — with the
project context, working directory, parent process, and uptime that `lsof`
doesn't give you. Kill, pause, or resume processes by port number.

```
$ ports
PORT   PROTO  PID    COMMAND  PARENT          PATH                              HOST       AGE
3000   TCP    15711  node     node            ~/code/web-app                    0.0.0.0    27m
3030   TCP    12405  node     node            ~/code/api                        0.0.0.0    29m
5432   TCP    23514  ssh      launchd         ~/code/infra                      127.0.0.1  10d5h
6379   TCP    23514  ssh      launchd         ~/code/infra                      127.0.0.1  10d5h
51606  TCP    91160  workerd  launchd         ~/code/edge-app                   127.0.0.1  9d2h

5 listener(s)
```

## Install

### Homebrew (recommended)

```sh
brew install erdemylmaz/ports-cli/ports
# or, equivalently:
brew tap erdemylmaz/ports-cli && brew install ports
```

### npm

```sh
npm install -g @erdemyilmaz/ports-cli
# or
pnpm add -g @erdemyilmaz/ports-cli
# or
yarn global add @erdemyilmaz/ports-cli
```

The npm package is a thin wrapper that downloads the right prebuilt binary
from GitHub Releases on `postinstall`. macOS only.

### Go

```sh
go install github.com/erdemylmaz/ports-cli/cmd/ports@latest
```

### Prebuilt binary (no toolchain needed)

```sh
# Apple Silicon
curl -L -o ports https://github.com/erdemylmaz/ports-cli/releases/latest/download/ports-darwin-arm64
# Intel
curl -L -o ports https://github.com/erdemylmaz/ports-cli/releases/latest/download/ports-darwin-amd64

chmod +x ports && mv ports ~/.local/bin/   # or /usr/local/bin/
```

### Build from source

```sh
git clone https://github.com/erdemylmaz/ports-cli.git
cd ports-cli
go build -o ports ./cmd/ports
mv ports ~/.local/bin/
```

Verify:

```sh
ports version
ports --help
```

## Why this exists

`lsof -iTCP -sTCP:LISTEN -nP` answers "what's on port 3000," but in practice
you're trying to answer different questions:

- *Which project is this `node` from?* (you have five running)
- *How long has it been there?* (probably a leaked dev server)
- *Did I start it, or did launchd?* (auto-restart at login, or a real session?)
- *Can I just kill the thing on :3000 without looking up the pid first?*
- *Show me only my dev servers, not Spotify/Chrome/Figma.*

`ports` answers those directly:

- **Working directory** for each process, so you instantly recognize which
  project a `node` belongs to.
- **Parent process** so you can tell `launchd`-started leftovers from things
  spawned by your current shell.
- **Age** computed from `ps -o lstart=`.
- **Filtering by purpose** — by default GUI apps and system daemons are
  hidden. `ports --all` shows everything.
- **Kill / pause / resume by port number**, no `lsof | awk` ritual.

## What it isn't

- **Not a daemon.** No background process, no LaunchAgent, no SQLite, no
  `~/Library/...` data dir. Every invocation reads live state.
- **Not cross-platform.** macOS only — it shells out to `/usr/sbin/lsof` and
  `/bin/ps` with macOS-specific flags.
- **Not a monitor.** No history, no notifications, no traffic metrics. If you
  want "when did port 3000 first appear two days ago," that needs persistent
  state — out of scope.
- **Not a privileged tool.** No setuid, no helper. Killing root-owned ports
  needs `sudo ports kill ...`.

The whole binary is one Go file, no third-party dependencies.

## Usage

```
ports [list] [flags]                          Show listening ports (default)
ports kill <port|pid|--dir PATH> [...]        Send SIGTERM (graceful)
ports force-kill <port|pid|--dir PATH> [...]  Send SIGKILL (immediate)
ports pause <port|pid|--dir PATH> [...]       Freeze process (SIGSTOP)
ports resume <port|pid|--dir PATH> [...]      Unfreeze process (SIGCONT)
ports inspect <port>                          Full process detail + HTTP probe
ports self-destroy                            Uninstall the binary
ports version                                 Print version
```

### Flags

| Flag             | Effect                                                     |
| ---------------- | ---------------------------------------------------------- |
| `--all` / `-a`   | Include GUI apps and system services                       |
| _(automatic)_    | Docker via Colima, Lima, or OrbStack appears as `docker(...)` instead of `ssh`. The truthful raw command is preserved in JSON output and `ports inspect`. |
| `--apps`         | Show **only** GUI apps and system services                 |
| `--range A:B`    | Only ports in range, e.g. `--range 3000:9000`              |
| `--pid N`        | Only this PID                                              |
| `--cmd SUBSTR`   | Filter by command name (case-insensitive)                  |
| `--dir PATH`     | Only processes whose cwd is at or under `PATH` (accepts `~`, relative, or absolute paths) |
| `--since DUR`    | Started within DUR (e.g. `30m`, `2h`, `today`)             |
| `--today`        | Shortcut for processes started since 00:00                 |
| `--tcp`          | TCP only                                                   |
| `--udp`          | UDP only                                                   |
| `--sort KEY[:DIR]` | Sort by `path` (default), `port`, `pid`, `age`, `command`, or `kind`. Optional `:asc` (default) or `:desc`. The default groups same-project ports together. |
| `--reverse` / `-r` | Flip the current sort direction                          |
| `--json`         | Machine-readable output                                    |

### Killing by directory

`kill`, `force-kill`, `pause`, and `resume` all accept `--dir PATH` to target
every listener whose working directory is at or under the given path. Useful
for "shut down everything in this project" without listing pids by hand. When
more than one process would be signaled (or when `--dir` is used at all),
you'll be asked to confirm — pass `--yes` / `-y` to skip.

```sh
ports kill --dir ~/code/web-app          # SIGTERM everything in this project
ports force-kill --dir ~/code/web-app -y # SIGKILL, no confirmation
ports pause --dir ~/code/api             # freeze the API stack
ports resume --dir ~/code/api            # unfreeze it
```

### Examples

```sh
# Just my dev servers (default behavior)
ports

# Everything, including Spotify/Chrome/system daemons, with kind column
ports --all

# Only the noise, in case you want to know what your "background" is doing
ports --apps

# Dev-port range, TCP only
ports --range 3000:9000 --tcp

# Node servers started in the last hour
ports --cmd node --since 1h

# Only listeners running under a specific directory tree
ports --dir ~/Documents
ports --dir ~/code/web-app
ports --dir .                          # current directory

# Sort by something other than path (the new default)
ports --sort port                      # back to numeric port order
ports --sort age:desc                  # oldest-running first — zombie hunt
ports --sort command                   # group by command name
ports --sort path -r                   # path order, descending

# Free up port 3000 (graceful)
ports kill 3000

# It didn't shut down? Force it.
ports force-kill 3000

# Multiple at once (mix port numbers and pids)
ports kill 3000 4000 12345

# Kill everything running under a project tree
ports kill --dir ~/code/web-app          # asks for confirmation
ports force-kill --dir ~/code/web-app -y # immediate, no confirmation

# Freeze a process without killing it (e.g. to see if a request is hanging on it)
ports pause 3000
ports resume 3000

# Full detail + HTTP probe on whatever's there
ports inspect 3000

# Pipe into jq
ports --json --range 3000:9000 | jq '.[] | {port, command, cwd}'
```

### How "dev vs. app" is decided

A listener is classified as `app` if its executable lives inside a
`.app/Contents/` bundle, and `system` if it lives under `/System/`,
`/usr/libexec/`, `/Library/Apple/`, or matches a known noisy daemon name
(`mDNSResponder`, `rapportd`, `sharingd`, etc.). Everything else is `dev` and
shown by default.

Heuristics aren't perfect. Edit the `classify` function in
`cmd/ports/main.go` to teach it about anything in your environment that
sneaks through (e.g. apps installed outside `/Applications/`).

## Comparison

|                                | `ports` | `lsof -i -P -n` | `netstat -anv` | `lsof-ng` / TUI tools |
| ------------------------------ | ------- | --------------- | -------------- | --------------------- |
| Project / cwd shown            | ✓       | ✗               | ✗              | rare                  |
| Parent process shown           | ✓       | ✗               | ✗              | rare                  |
| Process age / uptime           | ✓       | ✗               | ✗              | rare                  |
| Filter by working directory    | ✓       | manual `\| grep`  | ✗              | ✗                     |
| Kill by port number            | ✓       | ✗               | ✗              | some                  |
| Bulk kill by project           | ✓       | ✗               | ✗              | ✗                     |
| Hides GUI apps by default      | ✓       | ✗               | ✗              | varies                |
| Single-binary, no dependencies | ✓       | ✓               | ✓              | varies                |
| macOS only                     | ✓       | cross           | cross          | varies                |

## Scope, on purpose

This is a small tool with a deliberately small surface. Things explicitly
**not** planned:

- Persistent history / event log of port bindings (would need a daemon).
- TUI or menu-bar app.
- Per-port traffic measurement (requires `pktap`/root).
- Linux/Windows support.

If any of those would change the tool's character (background process,
elevated privileges, GUI), they belong in a different project.

## Contributing

PRs welcome for: better classification heuristics, additional `inspect`
output (parent-chain walk, open files, env vars), JSON schema improvements,
and bug fixes for edge-case `lsof` output.

Things less likely to be merged: anything that adds a daemon, a database,
external dependencies, or a config file.

## Links

- **Website:** [portscli.com](https://portscli.com) — install instructions, examples, FAQ
- **Source:** [github.com/erdemylmaz/ports-cli](https://github.com/erdemylmaz/ports-cli)
- **Releases:** [github.com/erdemylmaz/ports-cli/releases](https://github.com/erdemylmaz/ports-cli/releases)
- **Homebrew tap:** [github.com/erdemylmaz/homebrew-ports-cli](https://github.com/erdemylmaz/homebrew-ports-cli)
- **npm:** [@erdemyilmaz/ports-cli](https://www.npmjs.com/package/@erdemyilmaz/ports-cli)
- **Author:** [erdm.io](https://erdm.io)

## License

MIT — see [LICENSE](./LICENSE).

---

**Keywords:** macOS port monitor · which process is using port 3000 ·
kill port 3000 mac · lsof alternative · find process using port macOS ·
free up port mac · check listening ports macOS · `EADDRINUSE` fix mac ·
project-aware port listing · dev server cleanup · portscli.com
