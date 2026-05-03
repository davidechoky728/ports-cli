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
project context, working directory, parent process, uptime, and caffeinate
status that `lsof` doesn't give you. It can also find local AI app and agent
sessions that do not bind ports, so you can keep the exact process awake by
PID. Kill, pause, resume, or caffeinate by port number, PID, project
directory, or AI session.

```
$ ports
PORT   PROTO  PID    COMMAND  PARENT          PATH                              CAFFEINATE  HOST       AGE
3000   TCP    15711  node     node            ~/code/web-app                    on:82031    0.0.0.0    27m
3030   TCP    12405  node     node            ~/code/api                        -           0.0.0.0    29m
5432   TCP    23514  ssh      launchd         ~/code/infra                      -           127.0.0.1  10d5h
6379   TCP    23514  ssh      launchd         ~/code/infra                      -           127.0.0.1  10d5h
51606  TCP    91160  workerd  launchd         ~/code/edge-app                   -           127.0.0.1  9d2h

5 listener(s)
```

## Keep Claude Code, Codex, and local agents awake

Long-running agent work often depends on a local port: a Vite preview, a
browser automation bridge, an MCP/tool server, an API, a model proxy, or a
dashboard. `ports` lets you attach macOS `caffeinate` to the process that
owns that port:

```sh
ports caffeinate 3000                 # keep whatever owns :3000 awake
ports --caffeinate 12345              # keep a PID awake directly
ports caffeinate --dir ~/code/agent   # keep project listeners awake
ports --find codex "claude code"      # find AI/app/agent PIDs
ports caffeinate codex                # keep matching Codex app/agent/session PIDs awake
ports caffeinate codex --dir ~/code/agent --follow
                                      # keep new Codex children in that project awake
ports caffeinate --find cursor        # explicit AI selector for caffeinate
ports caffeinate --pid 12345          # keep an exact PID awake
ports uncaffeinate 3000               # stop watcher, keep process running
ports decaffeinate                    # stop all caffeinate watchers found
```

`ports` then shows `CAFFEINATE on:<watcher-pid>` in the normal table, so you
can see whether a Claude Code, Codex, or background-agent session is protected
from idle sleep. For lid-closed runs, keep the Mac on power in a supported
clamshell setup; macOS can still force sleep outside those conditions.

`ports --find codex "claude code" gemini cursor` inspects the live process tree for
AI-related app roots, CLI agents, workspace sessions, MCP/tool helpers, and
workspace child processes. It prints identity, role, workspace/CWD, age,
listener ports, caffeinate status, parent chain, and exact `--pid` commands.
The same names can be used directly with `ports caffeinate <name>` and
`ports uncaffeinate <name>`. Add `--dir PATH` to constrain AI matches to a
project workspace, and add `--follow` when newly spawned agent children should
be caffeinated automatically.

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
- *Can I keep this AI agent or dev server awake while the Mac idles?*
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
- **Caffeinate by port, pid, or project directory** so long-running local
  agents, tunnels, and dev servers can keep running through idle sleep.

## What it isn't

- **Not an always-on ports daemon.** No LaunchAgent, no SQLite, no
  `~/Library/...` data dir. Every normal invocation reads live state. When you
  ask for `ports caffeinate`, it starts a normal macOS `/usr/bin/caffeinate`
  watcher for the target PID and that watcher exits automatically with the
  target. `--follow` is the opt-in exception: it starts a small background
  `ports` watcher that rescans the same selector.
- **Not cross-platform.** macOS only — it shells out to `/usr/sbin/lsof` and
  `/bin/ps` with macOS-specific flags.
- **Not a monitor.** No history, no notifications, no traffic metrics. If you
  want "when did port 3000 first appear two days ago," that needs persistent
  state — out of scope.
- **Not a closed-lid hardware bypass.** `caffeinate` prevents idle sleep while
  macOS allows it. Many Mac laptops still sleep when the lid is closed unless
  they are on power in a supported clamshell setup.
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
ports caffeinate [<port|pid|AI|--dir PATH> ...] Keep Mac awake while process runs
ports --caffeinate <port|pid>                 Shortcut for ports caffeinate
ports uncaffeinate [<port|pid|AI|--dir PATH> ...] Stop awake watchers
ports decaffeinate [<port|pid|AI|--dir PATH> ...] Same as uncaffeinate
ports find [QUERY...]                         Find AI/app/agent process PIDs
ports --find [QUERY...]                       Shortcut for ports find
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
| `--strict-dir`   | For control commands with `--dir`, skip PIDs that also own listeners outside `PATH` |
| `--find QUERY`   | For `caffeinate` / `uncaffeinate`, target matching AI processes |
| `--follow` / `--watch` | For `caffeinate`, keep rescanning and caffeinate newly matching PIDs |
| `--interval DUR` | Rescan interval for `--follow`, such as `1s`, `5s`, or `30s` |
| `--since DUR`    | Started within DUR (e.g. `30m`, `2h`, `today`)             |
| `--today`        | Shortcut for processes started since 00:00                 |
| `--tcp`          | TCP only                                                   |
| `--udp`          | UDP only                                                   |
| `--sort KEY[:DIR]` | Sort by `path` (default), `port`, `pid`, `age`, `command`, or `kind`. Optional `:asc` (default) or `:desc`. The default groups same-project ports together. |
| `--reverse` / `-r` | Flip the current sort direction                          |
| `--json`         | Machine-readable output                                    |

For `ports find`, pass one or more queries such as `codex`, `"claude code"`,
`gemini`, or `cursor`. With no query it searches all four. Add `--verbose` for
full session details or `--json` for automation.

### Keeping dev servers and AI agents awake

`ports caffeinate` wraps macOS's built-in `caffeinate` command around the
process that owns a port. It starts `/usr/bin/caffeinate -dimsu -w <pid>` in
the background, returns immediately, and the watcher exits automatically when
the target process exits.

```sh
ports caffeinate 3000                 # keep whatever owns :3000 awake
ports --caffeinate 12345              # same shortcut, targeting a pid
ports caffeinate --dir ~/code/agent   # keep every listener in a project awake
ports caffeinate                      # keep all current listening PIDs awake
ports uncaffeinate 3000               # stop the awake watcher, keep app running
ports decaffeinate                    # stop every caffeinate watcher found
```

This is useful for long-running local AI agents and coding sessions: Claude
Code, Codex, background tool servers, local dashboards, Vite/Next.js previews,
Docker tunnels, and project-specific API stacks. If the thing you care about
listens on a port, pass the port. If it does not listen on a port, pass the
PID directly.

`ports` shows the current status in the `CAFFEINATE` column:

```sh
PORT  PROTO  PID    COMMAND  PARENT  PATH          CAFFEINATE  HOST       AGE
3000  TCP    15711  node     zsh     ~/code/agent  on:82031    127.0.0.1  4h12m
```

The `on:<pid>` value is the background `caffeinate` watcher PID. JSON output
also includes `caffeinated` and `caffeinate_pids`.

For AI apps and agents that do not listen on a local port, use process discovery:

```sh
ports --find codex "claude code" gemini cursor
ports find codex --verbose
ports caffeinate codex
ports caffeinate codex --dir ~/code/agent --follow
ports caffeinate --find "claude code" cursor
ports caffeinate --pid 93633
ports uncaffeinate --pid 93633
ports uncaffeinate codex
ports decaffeinate codex --follow
```

`--pid` forces PID targeting for control commands. That avoids the normal
`port first, then PID` numeric lookup when you know you want an exact process.
AI names are only accepted by `caffeinate` and `uncaffeinate`; destructive
commands stay explicit, so use `ports find ...` and then `--pid` when you
really mean to signal an AI process.

`--follow` fixes the "new child process appeared later" case. It starts a
background `ports __follow-caffeinate ...` watcher that repeatedly resolves
the same selector and attaches `/usr/bin/caffeinate -w <pid>` to new matches.
For example, `ports caffeinate codex --dir ~/code/agent --strict-dir --follow`
keeps current and future Codex workspace/session PIDs under that project awake,
while skipping shared listener PIDs that also expose ports outside the project.
Stop both the current PID watchers and the follow watcher with
`ports decaffeinate codex --dir ~/code/agent --strict-dir --follow`.

With no target, `ports caffeinate` is a bulk operation: after confirmation it
caffeinates every currently listening PID visible to `ports`. With no target,
`ports decaffeinate` stops all active `/usr/bin/caffeinate -w <pid>` watchers it
can discover, including watchers started manually or by another tool, and also
stops any `ports --follow` watchers.

`ports find` understands common AI process identities:

- Codex desktop app roots, app-server processes, `node_repl` workspace
  sessions, Codex CLI wrappers/native agents, and Computer Use MCP helpers.
- Claude / Claude Code desktop and helper processes.
- Gemini CLI or app processes when present.
- Cursor app, helper, and agent processes.
- Workspace child processes launched below an AI root, including local dev
  servers with listening ports and active caffeinate watchers.

Use `--verbose` when deciding what to keep awake. It includes the full command,
executable path, cwd, workspace, parent chain, root process, listening ports,
and the exact `ports caffeinate --pid <PID>` / `ports uncaffeinate --pid <PID>`
commands for every match.

Important macOS reality: this prevents idle sleep while macOS permits the
assertion. A closed laptop lid can still force sleep unless the Mac is on
power in a supported clamshell setup. Keep lid-closed agent runs on power and
ventilated.

### Killing by directory

`kill`, `force-kill`, `pause`, `resume`, `caffeinate`, and `uncaffeinate` all
accept `--dir PATH` to target every listener whose working directory is at or
under the given path. Useful for "shut down everything in this project" or
"keep this whole agent workspace awake" without listing pids by hand. When
more than one process would be targeted (or when `--dir` is used at all),
you'll be asked to confirm — pass `--yes` / `-y` to skip.

```sh
ports kill --dir ~/code/web-app          # SIGTERM everything in this project
ports force-kill --dir ~/code/web-app -y # SIGKILL, no confirmation
ports pause --dir ~/code/api             # freeze the API stack
ports resume --dir ~/code/api            # unfreeze it
ports caffeinate --dir ~/code/agent -y    # keep project listeners awake
ports caffeinate --dir ~/code/agent --strict-dir -y
                                      # skip shared PIDs that cross projects
ports uncaffeinate --dir ~/code/agent -y  # release their awake watchers
ports caffeinate codex --dir ~/code/agent --follow
                                      # keep new AI children under this project awake
```

Some runtimes expose ports for multiple projects from one shared host PID. The
common macOS case is Docker via Colima/Lima, where one SSH multiplexer can own
port forwards for several Compose projects. Because macOS `caffeinate -w`
watches a PID, `ports caffeinate --dir ...` warns when a selected PID also owns
listeners outside that directory. Use `--strict-dir` when you prefer skipping
those shared PIDs over affecting another project. `--strict-dir` does not make
macOS caffeinate only selected ports inside a shared PID; that is not possible
with PID-scoped `caffeinate`.

When `--dir` is combined with an AI selector such as `codex`, `claude code`,
`gemini`, or `cursor`, AI matches are filtered to processes whose workspace,
cwd, or listening port cwd is under that directory. That keeps
`ports caffeinate codex --dir ~/code/benzersor --follow` from also selecting a
separate Codex session under `~/code/devredin`.

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

# Keep a local AI agent, dev server, or tunnel running through idle sleep
ports caffeinate 3000
ports --caffeinate 12345
ports caffeinate --pid 12345
ports caffeinate codex
ports caffeinate "claude code" gemini cursor
ports caffeinate codex --dir ~/code/agent-workspace --strict-dir --follow
ports caffeinate --dir ~/code/agent-workspace --yes
ports uncaffeinate 3000
ports uncaffeinate codex
ports decaffeinate

# Find AI app/agent/session PIDs that may not listen on ports
ports --find codex "claude code" gemini cursor
ports find codex --verbose

# Full detail + HTTP probe on whatever's there
ports inspect 3000

# Pipe into jq
ports --json --range 3000:9000 | jq '.[] | {port, command, cwd, caffeinated}'
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
| Keep awake by port/project     | ✓       | ✗               | ✗              | ✗                     |
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
- Circumventing macOS lid-close sleep rules. `ports caffeinate` uses the
  supported system assertion mechanism; it does not patch power management.

If any of those would change the tool's character (a ports-owned daemon,
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
project-aware port listing · dev server cleanup · caffeinate port mac ·
keep Mac awake by pid · run Claude Code with lid closed · run Codex in
background · keep AI agents running on Mac · portscli.com
