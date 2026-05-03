# ports

A small macOS CLI that tells you what's actually listening on your laptop, and
why — with the context `lsof` doesn't give you.

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

## Install

You need a Go toolchain (`brew install go`).

```sh
git clone https://github.com/erdemylmaz/ports-cli.git
cd ports-cli
go build -o ports ./cmd/ports

# Pick a location on your $PATH
mkdir -p ~/.local/bin && cp ports ~/.local/bin/ports
# or
sudo cp ports /usr/local/bin/ports
```

Verify:

```sh
ports version
ports --help
```

## Usage

```
ports [list] [flags]              Show listening ports (default)
ports kill <port|pid> [...]       Send SIGTERM (graceful)
ports force-kill <port|pid> [...] Send SIGKILL (immediate)
ports pause <port|pid> [...]      Freeze process (SIGSTOP)
ports resume <port|pid> [...]     Unfreeze process (SIGCONT)
ports inspect <port>              Full process detail + HTTP probe
ports self-destroy                Uninstall the binary
ports version                     Print version
```

### Flags

| Flag             | Effect                                                     |
| ---------------- | ---------------------------------------------------------- |
| `--all` / `-a`   | Include GUI apps and system services                       |
| `--apps`         | Show **only** GUI apps and system services                 |
| `--range A:B`    | Only ports in range, e.g. `--range 3000:9000`              |
| `--pid N`        | Only this PID                                              |
| `--cmd SUBSTR`   | Filter by command name (case-insensitive)                  |
| `--since DUR`    | Started within DUR (e.g. `30m`, `2h`, `today`)             |
| `--today`        | Shortcut for processes started since 00:00                 |
| `--tcp`          | TCP only                                                   |
| `--udp`          | UDP only                                                   |
| `--json`         | Machine-readable output                                    |

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

# Free up port 3000 (graceful)
ports kill 3000

# It didn't shut down? Force it.
ports force-kill 3000

# Multiple at once (mix port numbers and pids)
ports kill 3000 4000 12345

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

## License

MIT — see [LICENSE](./LICENSE).
