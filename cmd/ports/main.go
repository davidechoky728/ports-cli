package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"
)

const version = "0.6.0"

const (
	defaultFollowInterval = 5 * time.Second
	minFollowInterval     = time.Second
)

type Listener struct {
	Command   string    `json:"command"` // raw process name from lsof (e.g. "ssh")
	Display   string    `json:"display"` // human-friendly (e.g. "docker(colima)")
	PID       int       `json:"pid"`
	User      string    `json:"user"`
	Protocol  string    `json:"protocol"`
	Address   string    `json:"address"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
	Age       string    `json:"age"`
	FullCmd   string    `json:"full_command"`
	ExePath   string    `json:"exe_path"`
	Cwd       string    `json:"cwd"`
	ParentPID int       `json:"parent_pid"`
	ParentCmd string    `json:"parent_command"`
	Kind      string    `json:"kind"` // "dev", "app", "system"

	Caffeinated    bool  `json:"caffeinated"`
	CaffeinatePIDs []int `json:"caffeinate_pids,omitempty"`
}

type filter struct {
	portMin   int
	portMax   int
	pid       int
	cmdSubstr string
	dirPrefix string
	since     time.Duration
	asJSON    bool
	tcpOnly   bool
	udpOnly   bool
	showAll   bool
	appsOnly  bool
	sortKey   string
	sortDesc  bool
}

func main() {
	if len(os.Args) < 2 {
		listCmd(os.Args[1:])
		return
	}
	switch os.Args[1] {
	case "list", "ls":
		listCmd(os.Args[2:])
	case "kill":
		signalCmd(os.Args[2:], syscall.SIGTERM, "kill")
	case "force-kill":
		signalCmd(os.Args[2:], syscall.SIGKILL, "force-kill")
	case "pause":
		signalCmd(os.Args[2:], syscall.SIGSTOP, "pause")
	case "resume":
		signalCmd(os.Args[2:], syscall.SIGCONT, "resume")
	case "caffeinate", "keep-awake", "awake":
		caffeinateCmd(os.Args[2:], true)
	case "uncaffeinate", "decaffeinate", "sleep-ok":
		caffeinateCmd(os.Args[2:], false)
	case "find", "ai":
		processFindCmd(os.Args[2:])
	case "__follow-caffeinate":
		followCaffeinateCmd(os.Args[2:])
	case "inspect":
		inspectCmd(os.Args[2:])
	case "self-destroy":
		selfDestroyCmd()
	case "version", "--version", "-v":
		fmt.Println("ports", version)
	case "help", "--help", "-h":
		printHelp()
	default:
		if os.Args[1] == "--caffeinate" {
			caffeinateCmd(os.Args[2:], true)
			return
		}
		if strings.HasPrefix(os.Args[1], "--caffeinate=") {
			caffeinateCmd(append([]string{strings.TrimPrefix(os.Args[1], "--caffeinate=")}, os.Args[2:]...), true)
			return
		}
		if os.Args[1] == "--uncaffeinate" || os.Args[1] == "--decaffeinate" {
			caffeinateCmd(os.Args[2:], false)
			return
		}
		if strings.HasPrefix(os.Args[1], "--uncaffeinate=") {
			caffeinateCmd(append([]string{strings.TrimPrefix(os.Args[1], "--uncaffeinate=")}, os.Args[2:]...), false)
			return
		}
		if strings.HasPrefix(os.Args[1], "--decaffeinate=") {
			caffeinateCmd(append([]string{strings.TrimPrefix(os.Args[1], "--decaffeinate=")}, os.Args[2:]...), false)
			return
		}
		if os.Args[1] == "--find" {
			processFindCmd(os.Args[2:])
			return
		}
		if strings.HasPrefix(os.Args[1], "--find=") {
			processFindCmd(append([]string{strings.TrimPrefix(os.Args[1], "--find=")}, os.Args[2:]...))
			return
		}
		// Treat unknown first arg as a flag for list
		listCmd(os.Args[1:])
	}
}

func printHelp() {
	fmt.Print(`ports — list and control processes bound to local ports

USAGE
  ports [list] [flags]                          Show listening ports (default)
  ports kill <port|pid|--dir PATH> [...]        Send SIGTERM (graceful)
  ports force-kill <port|pid|--dir PATH> [...]  Send SIGKILL (immediate)
  ports pause <port|pid|--dir PATH> [...]       Freeze process (SIGSTOP)
  ports resume <port|pid|--dir PATH> [...]      Unfreeze process (SIGCONT)
  ports caffeinate [<port|pid|AI|--dir PATH> ...] Keep Mac awake while process runs
  ports --caffeinate <port|pid>                 Shortcut for ports caffeinate
  ports uncaffeinate [<port|pid|AI|--dir PATH> ...] Stop awake watchers for target
  ports decaffeinate [<port|pid|AI|--dir PATH> ...] Same as uncaffeinate
  ports find [QUERY...]                         Find AI/app/agent process PIDs
  ports --find [QUERY...]                       Shortcut for ports find
  ports inspect <port>                          HTTP probe + process detail
  ports self-destroy                            Uninstall ports from this machine
  ports version                                 Print version

FLAGS (list)
  --all                Include GUI apps and system services (hidden by default)
  --apps               Show ONLY GUI apps and system services
  --range A:B          Only ports in range, e.g. 3000:6000
  --pid N              Only this PID
  --cmd SUBSTR         Filter by command name (case-insensitive)
  --dir PATH           Only processes whose cwd is at or under PATH
                       (accepts ~, relative, or absolute paths)
  --since DUR          Only processes started within DUR (e.g. 30m, 2h, today)
  --today              Shortcut for processes started since 00:00
  --tcp                TCP only
  --udp                UDP only
  --sort KEY[:DIR]     Sort by KEY (path, port, pid, age, command, kind);
                       optional :asc (default) or :desc. Default: path
                       (groups same-project ports together).
  --reverse / -r       Flip the current sort direction
  --json               Machine-readable output

FLAGS (kill / force-kill / pause / resume / caffeinate / uncaffeinate / decaffeinate)
  --dir PATH           Target every listener whose cwd is at or under PATH
  --pid N              Target this exact PID, even if N is also a port number
  --find QUERY         For caffeinate/decaffeinate, target matching AI processes
  --strict-dir         With --dir, skip PIDs that also own listeners outside PATH
  --follow / --watch   For caffeinate, keep rescanning and caffeinate new matches
                       For decaffeinate, also stop the matching follow watcher
  --interval DUR       Rescan interval for --follow (default 5s)
  --yes / -y           Skip the safety confirmation prompt

FLAGS (find)
  --json               Machine-readable output
  --verbose            Full command, executable path, and suggested command
  QUERY                AI/app/agent search term. Defaults to codex, claude code,
                       gemini, and cursor when omitted.

CAFFEINATE
  ports caffeinate 3000 starts /usr/bin/caffeinate in the background with
  -dimsu -w <pid>. It prevents idle sleep while that listener is alive and
  stops automatically when the process exits. ports decaffeinate 3000 stops
  caffeinate watchers for that target without killing the target process.
  AI names such as codex, "claude code", gemini, cursor, or ai resolve through
  ports find and target the matched app/agent/session PIDs.
  Add --follow to keep watching the same selector and caffeinate newly spawned
  matching PIDs, such as fresh Codex workspace children in the same project.
  Run ports decaffeinate with no target to stop every active caffeinate watcher
  discovered on the machine, including watchers not started by ports.

  macOS may still force sleep when a laptop lid is closed unless the machine
  is in a supported powered/clamshell setup. ports shows the current watcher
  status in the CAFFEINATE column.

By default only "dev" listeners are shown (anything not launched from a .app
bundle or /System/Library path). Use --all to see Spotify, Chrome, system
daemons, etc.

When more than one process would be signaled (or when --dir is used), you
are asked for confirmation. Pass --yes to skip the prompt.

EXAMPLES
  ports                                # default sort: by working directory
  ports --sort age:desc                # oldest-running first (zombie hunt)
  ports --sort port                    # original numeric port ordering
  ports --all
  ports --range 3000:9000 --tcp
  ports --cmd node --since 1h
  ports --dir ~/Documents              # only stuff running under ~/Documents
  ports --dir .                        # only stuff in the current directory
  ports kill 3000
  ports kill 12345 4000
  ports kill --dir ~/code/web-app      # everything under that project
  ports force-kill --dir ~/code -y     # skip the confirmation
  ports caffeinate 3000                # keep Mac awake while :3000 runs
  ports --caffeinate 12345             # same, targeting a pid
  ports caffeinate --pid 12345          # force PID targeting
  ports caffeinate codex                # keep matching Codex processes awake
  ports caffeinate codex --dir ~/code/agent --follow
                                       # keep new Codex children in that project awake
  ports caffeinate --find "claude code" # same using an explicit find selector
  ports caffeinate --dir ~/code/agent  # keep a project stack awake
  ports caffeinate --dir ~/code/agent --strict-dir
                                       # skip shared PIDs that cross projects
  ports uncaffeinate 3000              # stop the awake watcher
  ports decaffeinate                   # stop all active caffeinate watchers
  ports --find codex "claude code" gemini cursor
                                       # find AI-related PIDs to caffeinate
`)
}

func listCmd(args []string) {
	f := parseListFlags(args)
	listeners, err := fetchListeners()
	if err != nil {
		exitErr(err)
	}
	listeners = applyFilters(listeners, f)
	sortListeners(listeners, f.sortKey, f.sortDesc)
	if f.asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(listeners)
		return
	}
	renderTable(listeners, f.showAll || f.appsOnly)
}

func parseListFlags(args []string) filter {
	f := filter{portMin: -1, portMax: -1, pid: -1}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			f.asJSON = true
		case a == "--tcp":
			f.tcpOnly = true
		case a == "--udp":
			f.udpOnly = true
		case a == "--all", a == "-a":
			f.showAll = true
		case a == "--apps":
			f.appsOnly = true
		case a == "--today":
			f.since = sinceMidnight()
		case a == "--range" && i+1 < len(args):
			parseRange(args[i+1], &f)
			i++
		case strings.HasPrefix(a, "--range="):
			parseRange(strings.TrimPrefix(a, "--range="), &f)
		case a == "--pid" && i+1 < len(args):
			f.pid, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--pid="):
			f.pid, _ = strconv.Atoi(strings.TrimPrefix(a, "--pid="))
		case a == "--cmd" && i+1 < len(args):
			f.cmdSubstr = strings.ToLower(args[i+1])
			i++
		case strings.HasPrefix(a, "--cmd="):
			f.cmdSubstr = strings.ToLower(strings.TrimPrefix(a, "--cmd="))
		case a == "--dir" && i+1 < len(args):
			f.dirPrefix = resolveDir(args[i+1])
			i++
		case strings.HasPrefix(a, "--dir="):
			f.dirPrefix = resolveDir(strings.TrimPrefix(a, "--dir="))
		case a == "--sort" && i+1 < len(args):
			f.sortKey, f.sortDesc = parseSort(args[i+1])
			i++
		case strings.HasPrefix(a, "--sort="):
			f.sortKey, f.sortDesc = parseSort(strings.TrimPrefix(a, "--sort="))
		case a == "--reverse", a == "-r":
			f.sortDesc = !f.sortDesc
		case a == "--since" && i+1 < len(args):
			f.since = parseDur(args[i+1])
			i++
		case strings.HasPrefix(a, "--since="):
			f.since = parseDur(strings.TrimPrefix(a, "--since="))
		}
	}
	return f
}

func parseRange(s string, f *filter) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return
	}
	f.portMin, _ = strconv.Atoi(parts[0])
	f.portMax, _ = strconv.Atoi(parts[1])
}

func parseDur(s string) time.Duration {
	if s == "today" {
		return sinceMidnight()
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func parseSort(s string) (string, bool) {
	parts := strings.SplitN(s, ":", 2)
	key := strings.ToLower(strings.TrimSpace(parts[0]))
	desc := false
	if len(parts) == 2 {
		d := strings.ToLower(strings.TrimSpace(parts[1]))
		desc = d == "desc" || d == "d" || d == "down"
	}
	return key, desc
}

func sortListeners(ls []Listener, key string, desc bool) {
	if key == "" {
		key = "path"
	}
	sort.SliceStable(ls, func(i, j int) bool {
		a, b := i, j
		if desc {
			a, b = j, i
		}
		return primaryLess(ls, a, b, key)
	})
}

func primaryLess(ls []Listener, i, j int, key string) bool {
	switch key {
	case "path", "cwd", "dir":
		ai, aj := ls[i].Cwd, ls[j].Cwd
		if ai == "" && aj != "" {
			return false
		}
		if ai != "" && aj == "" {
			return true
		}
		if ai != aj {
			return ai < aj
		}
		return ls[i].Port < ls[j].Port
	case "port":
		if ls[i].Port != ls[j].Port {
			return ls[i].Port < ls[j].Port
		}
		return ls[i].PID < ls[j].PID
	case "pid":
		if ls[i].PID != ls[j].PID {
			return ls[i].PID < ls[j].PID
		}
		return ls[i].Port < ls[j].Port
	case "age", "started", "time":
		// "age asc" = smallest age first = most recently started.
		if !ls[i].StartedAt.Equal(ls[j].StartedAt) {
			return ls[i].StartedAt.After(ls[j].StartedAt)
		}
		return ls[i].Port < ls[j].Port
	case "command", "cmd":
		ci, cj := strings.ToLower(ls[i].Command), strings.ToLower(ls[j].Command)
		if ci != cj {
			return ci < cj
		}
		return ls[i].Port < ls[j].Port
	case "kind":
		if ls[i].Kind != ls[j].Kind {
			return ls[i].Kind < ls[j].Kind
		}
		return ls[i].Port < ls[j].Port
	default:
		return ls[i].Port < ls[j].Port
	}
}

func sinceMidnight() time.Duration {
	now := time.Now()
	mid := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return now.Sub(mid)
}

func resolveDir(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		p = os.Getenv("HOME")
	} else if strings.HasPrefix(p, "~/") {
		p = filepath.Join(os.Getenv("HOME"), p[2:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

func pathHasPrefix(path, prefix string) bool {
	if path == "" || prefix == "" {
		return false
	}
	cleaned := filepath.Clean(path)
	if cleaned == prefix {
		return true
	}
	prefixWithSep := prefix
	if !strings.HasSuffix(prefixWithSep, string(filepath.Separator)) {
		prefixWithSep += string(filepath.Separator)
	}
	return strings.HasPrefix(cleaned+string(filepath.Separator), prefixWithSep)
}

func applyFilters(in []Listener, f filter) []Listener {
	out := in[:0]
	cutoff := time.Time{}
	if f.since > 0 {
		cutoff = time.Now().Add(-f.since)
	}
	for _, l := range in {
		if f.tcpOnly && l.Protocol != "TCP" {
			continue
		}
		if f.udpOnly && l.Protocol != "UDP" {
			continue
		}
		if f.portMin >= 0 && (l.Port < f.portMin || l.Port > f.portMax) {
			continue
		}
		if f.pid >= 0 && l.PID != f.pid {
			continue
		}
		if f.cmdSubstr != "" &&
			!strings.Contains(strings.ToLower(l.Command), f.cmdSubstr) &&
			!strings.Contains(strings.ToLower(l.Display), f.cmdSubstr) &&
			!strings.Contains(strings.ToLower(l.FullCmd), f.cmdSubstr) {
			continue
		}
		if f.dirPrefix != "" && !pathHasPrefix(l.Cwd, f.dirPrefix) {
			continue
		}
		if !cutoff.IsZero() && l.StartedAt.Before(cutoff) {
			continue
		}
		if f.appsOnly && l.Kind == "dev" {
			continue
		}
		if !f.showAll && !f.appsOnly && l.Kind != "dev" {
			continue
		}
		out = append(out, l)
	}
	return out
}

func renderTable(ls []Listener, showKind bool) {
	if len(ls) == 0 {
		fmt.Println("No listening ports match. (Run with --all to include GUI apps and system services.)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if showKind {
		fmt.Fprintln(w, "PORT\tPROTO\tPID\tCOMMAND\tPARENT\tPATH\tKIND\tCAFFEINATE\tHOST\tAGE")
	} else {
		fmt.Fprintln(w, "PORT\tPROTO\tPID\tCOMMAND\tPARENT\tPATH\tCAFFEINATE\tHOST\tAGE")
	}
	for _, l := range ls {
		path := prettyPath(l.Cwd)
		parent := l.ParentCmd
		if parent == "" {
			parent = "?"
		}
		display := l.Display
		if display == "" {
			display = l.Command
		}
		if showKind {
			fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				l.Port, l.Protocol, l.PID, truncate(display, 30),
				truncate(parent, 14), truncateLeft(path, 42),
				l.Kind, caffeineSummary(l.CaffeinatePIDs), l.Host, l.Age)
		} else {
			fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
				l.Port, l.Protocol, l.PID, truncate(display, 30),
				truncate(parent, 14), truncateLeft(path, 42),
				caffeineSummary(l.CaffeinatePIDs), l.Host, l.Age)
		}
	}
	w.Flush()
	fmt.Printf("\n%d listener(s)\n", len(ls))
}

type processFindOptions struct {
	queries []string
	asJSON  bool
	verbose bool
}

type ProcessMatch struct {
	Queries             []string          `json:"queries"`
	Provider            string            `json:"provider"`
	Identity            string            `json:"identity"`
	Role                string            `json:"role"`
	Session             string            `json:"session"`
	PID                 int               `json:"pid"`
	Command             string            `json:"command"`
	FullCmd             string            `json:"full_command"`
	ExePath             string            `json:"exe_path"`
	Cwd                 string            `json:"cwd"`
	Workspace           string            `json:"workspace"`
	ParentPID           int               `json:"parent_pid"`
	ParentCmd           string            `json:"parent_command"`
	ParentChain         []ProcessAncestor `json:"parent_chain,omitempty"`
	RootPID             int               `json:"root_pid"`
	RootCommand         string            `json:"root_command"`
	RootIdentity        string            `json:"root_identity"`
	MatchSource         string            `json:"match_source"`
	StartedAt           time.Time         `json:"started_at"`
	Age                 string            `json:"age"`
	Kind                string            `json:"kind"`
	Listening           []ProcessListener `json:"listening,omitempty"`
	Caffeinated         bool              `json:"caffeinated"`
	CaffeinatePIDs      []int             `json:"caffeinate_pids,omitempty"`
	CaffeinateCommand   string            `json:"caffeinate_command"`
	UncaffeinateCommand string            `json:"uncaffeinate_command"`
}

type ProcessAncestor struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	FullCmd string `json:"full_command"`
}

type ProcessListener struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Display  string `json:"display"`
	Cwd      string `json:"cwd"`
	Address  string `json:"address"`
}

func processFindCmd(args []string) {
	opts := parseProcessFindArgs(args)
	matches, err := findProcessMatches(opts.queries)
	if err != nil {
		exitErr(err)
	}
	if opts.asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(matches)
		return
	}
	if opts.verbose {
		renderProcessFindDetails(matches)
		return
	}
	renderProcessFindTable(matches)
}

func parseProcessFindArgs(args []string) processFindOptions {
	var opts processFindOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			opts.asJSON = true
		case a == "--verbose":
			opts.verbose = true
		case a == "--find" && i+1 < len(args):
			opts.queries = append(opts.queries, args[i+1])
			i++
		case strings.HasPrefix(a, "--find="):
			opts.queries = append(opts.queries, strings.TrimPrefix(a, "--find="))
		default:
			opts.queries = append(opts.queries, a)
		}
	}
	if len(opts.queries) == 0 {
		opts.queries = defaultProcessFindQueries()
	}
	return opts
}

func defaultProcessFindQueries() []string {
	return []string{"codex", "claude code", "gemini", "cursor"}
}

func findProcessMatches(queries []string) ([]ProcessMatch, error) {
	rows, err := fetchProcessRows()
	if err != nil {
		return nil, err
	}
	byPID := map[int]psProcess{}
	for _, row := range rows {
		byPID[row.pid] = row
	}

	direct := map[int][]string{}
	for _, row := range rows {
		if row.pid == os.Getpid() || isSelfPortsProcess(row.fullCmd) {
			continue
		}
		matched := directProcessMatchedQueries(row, queries)
		if len(matched) == 0 {
			continue
		}
		direct[row.pid] = matched
	}

	candidates := map[int]processCandidate{}
	for _, row := range rows {
		if row.pid == os.Getpid() || isSelfPortsProcess(row.fullCmd) {
			continue
		}
		if matched, ok := direct[row.pid]; ok {
			candidates[row.pid] = processCandidate{
				row:         row,
				queries:     matched,
				matchSource: "direct command",
				rootPID:     row.pid,
			}
			continue
		}
		rootPID := nearestDirectAncestor(row.ppid, direct, byPID)
		if rootPID == 0 {
			continue
		}
		candidates[row.pid] = processCandidate{
			row:         row,
			queries:     append([]string(nil), direct[rootPID]...),
			matchSource: fmt.Sprintf("child of pid %d", rootPID),
			rootPID:     rootPID,
		}
	}

	pids := make([]int, 0, len(candidates))
	for pid := range candidates {
		pids = append(pids, pid)
	}
	sort.Ints(pids)

	starts := fetchProcStarts(pids)
	exes := fetchProcExePaths(pids)
	cwds := fetchProcCwds(pids)
	listeners, _ := fetchListeners()
	listenersByPID := processListenersByPID(listeners)
	watchers := findCaffeinateWatchers()

	matches := []ProcessMatch{}
	for _, pid := range pids {
		candidate := candidates[pid]
		row := candidate.row
		exePath := exes[pid]
		cwd := cwds[pid]
		started := starts[pid]
		command := processCommandNameFromParts(exePath, row.fullCmd)
		parentCmd := parentCommandFor(row.ppid, byPID)
		chain := processParentChain(row.ppid, byPID)
		identity := identifyAIProcess(command, row.fullCmd, exePath, cwd, candidate.rootPID)
		workspace := deriveWorkspace(cwd, row.fullCmd)
		rootCommand := ""
		rootIdentity := ""
		rootProvider := ""
		if root, ok := byPID[candidate.rootPID]; ok {
			rootCommand = processCommandNameFromParts(exes[root.pid], root.fullCmd)
			rootInfo := identifyAIProcess(rootCommand, root.fullCmd, exes[root.pid], cwds[root.pid], root.pid)
			rootProvider = rootInfo.provider
			rootIdentity = rootInfo.identity
			if rootIdentity == "" {
				rootIdentity = rootCommand
			}
		}
		provider := identity.provider
		if provider == "" {
			provider = rootProvider
		}

		m := ProcessMatch{
			Queries:             dedupeStrings(candidate.queries),
			Provider:            provider,
			Identity:            identity.identity,
			Role:                identity.role,
			Session:             deriveSession(identity, workspace, candidate.rootPID, row.pid),
			PID:                 pid,
			Command:             command,
			FullCmd:             row.fullCmd,
			ExePath:             exePath,
			Cwd:                 cwd,
			Workspace:           workspace,
			ParentPID:           row.ppid,
			ParentCmd:           parentCmd,
			ParentChain:         chain,
			RootPID:             candidate.rootPID,
			RootCommand:         rootCommand,
			RootIdentity:        rootIdentity,
			MatchSource:         candidate.matchSource,
			StartedAt:           started,
			Age:                 humanAge(started),
			Kind:                classify(command, exePath, ""),
			Listening:           listenersByPID[pid],
			CaffeinatePIDs:      append([]int(nil), watchers[pid]...),
			CaffeinateCommand:   fmt.Sprintf("ports caffeinate --pid %d", pid),
			UncaffeinateCommand: fmt.Sprintf("ports uncaffeinate --pid %d", pid),
		}
		m.Caffeinated = len(m.CaffeinatePIDs) > 0
		if candidate.matchSource != "direct command" && !isRelevantAIDescendant(m) {
			continue
		}
		sort.Ints(m.CaffeinatePIDs)
		matches = append(matches, m)
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Provider != matches[j].Provider {
			return matches[i].Provider < matches[j].Provider
		}
		if roleRank(matches[i].Role) != roleRank(matches[j].Role) {
			return roleRank(matches[i].Role) < roleRank(matches[j].Role)
		}
		if matches[i].RootPID != matches[j].RootPID {
			return matches[i].RootPID < matches[j].RootPID
		}
		if matches[i].StartedAt.IsZero() != matches[j].StartedAt.IsZero() {
			return !matches[i].StartedAt.IsZero()
		}
		if !matches[i].StartedAt.Equal(matches[j].StartedAt) {
			return matches[i].StartedAt.Before(matches[j].StartedAt)
		}
		return matches[i].PID < matches[j].PID
	})
	return matches, nil
}

type psProcess struct {
	pid     int
	ppid    int
	fullCmd string
}

type processCandidate struct {
	row         psProcess
	queries     []string
	matchSource string
	rootPID     int
}

type aiIdentity struct {
	provider string
	identity string
	role     string
}

func fetchProcessRows() ([]psProcess, error) {
	out, err := exec.Command("/bin/ps", "axww", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, err
	}
	rows := []psProcess{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[1]))
		rows = append(rows, psProcess{pid: pid, ppid: ppid, fullCmd: strings.TrimSpace(rest)})
	}
	return rows, nil
}

func fetchProcStarts(pids []int) map[int]time.Time {
	out := map[int]time.Time{}
	if len(pids) == 0 {
		return out
	}
	cmdOut, err := exec.Command("/bin/ps", "-p", commaInts(pids), "-o", "pid=,lstart=").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(cmdOut), "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		startText := strings.Join(fields[1:6], " ")
		started, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", startText, time.Local)
		if err == nil {
			out[pid] = started
		}
	}
	return out
}

func fetchProcExePaths(pids []int) map[int]string {
	out := map[int]string{}
	if len(pids) == 0 {
		return out
	}
	cmdOut, err := exec.Command("/bin/ps", "-p", commaInts(pids), "-o", "pid=,comm=").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(cmdOut), "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		exe := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		out[pid] = exe
	}
	return out
}

func fetchProcCwds(pids []int) map[int]string {
	out := map[int]string{}
	if len(pids) == 0 {
		return out
	}
	cmdOut, err := exec.Command("/usr/sbin/lsof", "-a", "-d", "cwd", "-Fn", "-p", commaInts(pids)).Output()
	if err != nil && len(cmdOut) == 0 {
		return out
	}
	curPID := 0
	for _, line := range strings.Split(string(cmdOut), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			curPID, _ = strconv.Atoi(line[1:])
		case 'n':
			if curPID > 0 {
				out[curPID] = line[1:]
			}
		}
	}
	return out
}

func commaInts(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

func processCommandNameFromParts(exePath, fullCmd string) string {
	if exePath != "" {
		base := filepath.Base(exePath)
		if base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	fields := strings.Fields(fullCmd)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func parentCommandFor(ppid int, byPID map[int]psProcess) string {
	if ppid == 0 {
		return ""
	}
	if ppid == 1 {
		return "launchd"
	}
	if row, ok := byPID[ppid]; ok {
		return processCommandNameFromParts("", row.fullCmd)
	}
	return ""
}

func processParentChain(ppid int, byPID map[int]psProcess) []ProcessAncestor {
	chain := []ProcessAncestor{}
	seen := map[int]bool{}
	for ppid > 1 && len(chain) < 8 {
		if seen[ppid] {
			break
		}
		seen[ppid] = true
		row, ok := byPID[ppid]
		if !ok {
			break
		}
		chain = append(chain, ProcessAncestor{
			PID:     row.pid,
			Command: processCommandNameFromParts("", row.fullCmd),
			FullCmd: row.fullCmd,
		})
		ppid = row.ppid
	}
	return chain
}

func nearestDirectAncestor(ppid int, direct map[int][]string, byPID map[int]psProcess) int {
	seen := map[int]bool{}
	for ppid > 1 {
		if seen[ppid] {
			return 0
		}
		seen[ppid] = true
		if _, ok := direct[ppid]; ok {
			return ppid
		}
		row, ok := byPID[ppid]
		if !ok {
			return 0
		}
		if isSelfPortsProcess(row.fullCmd) {
			return 0
		}
		ppid = row.ppid
	}
	return 0
}

func processListenersByPID(listeners []Listener) map[int][]ProcessListener {
	out := map[int][]ProcessListener{}
	for _, l := range listeners {
		display := l.Display
		if display == "" {
			display = l.Command
		}
		out[l.PID] = append(out[l.PID], ProcessListener{
			Protocol: l.Protocol,
			Host:     l.Host,
			Port:     l.Port,
			Display:  display,
			Cwd:      l.Cwd,
			Address:  l.Address,
		})
	}
	for pid := range out {
		sort.SliceStable(out[pid], func(i, j int) bool {
			if out[pid][i].Protocol != out[pid][j].Protocol {
				return out[pid][i].Protocol < out[pid][j].Protocol
			}
			return out[pid][i].Port < out[pid][j].Port
		})
	}
	return out
}

func matchedProcessQueriesFromText(text string, queries []string) []string {
	normalized := " " + normalizeSearchText(stripSearchEnvNoise(text)) + " "
	out := []string{}
	seen := map[string]bool{}
	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		for _, term := range processQueryAliases(query) {
			term = normalizeSearchText(term)
			if term == "" {
				continue
			}
			if strings.Contains(normalized, " "+term+" ") {
				if !seen[query] {
					out = append(out, query)
					seen[query] = true
				}
				break
			}
		}
	}
	return out
}

func directProcessMatchedQueries(row psProcess, queries []string) []string {
	command := processCommandNameFromParts("", row.fullCmd)
	if isTransientUtilityCommand(command) {
		return nil
	}
	return matchedProcessQueriesFromText(row.fullCmd, queries)
}

func matchedProcessQueries(m ProcessMatch, queries []string) []string {
	return matchedProcessQueriesFromText(processSearchText(m), queries)
}

func processSearchText(m ProcessMatch) string {
	parts := []string{
		m.Provider,
		m.Identity,
		m.Role,
		m.Command,
		stripSearchEnvNoise(m.FullCmd),
		m.ExePath,
		m.Cwd,
		m.ParentCmd,
	}
	return normalizeSearchText(strings.Join(parts, " "))
}

func identifyAIProcess(command, fullCmd, exePath, cwd string, rootPID int) aiIdentity {
	text := normalizeSearchText(strings.Join([]string{command, stripSearchEnvNoise(fullCmd), exePath}, " "))
	switch {
	case strings.Contains(text, "applications codex app contents macos codex"):
		return aiIdentity{provider: "Codex", identity: "Codex desktop app", role: "app root"}
	case strings.Contains(text, "contents resources codex app server"):
		return aiIdentity{provider: "Codex", identity: "Codex app server", role: "agent server"}
	case strings.Contains(text, "codex helper renderer"):
		return aiIdentity{provider: "Codex", identity: "Codex renderer", role: "app helper"}
	case strings.Contains(text, "codex helper"):
		return aiIdentity{provider: "Codex", identity: "Codex helper", role: "app helper"}
	case strings.Contains(text, "chrome crashpad handler") && strings.Contains(text, "codex"):
		return aiIdentity{provider: "Codex", identity: "Codex crash reporter", role: "support"}
	case strings.Contains(text, "sparkle") && strings.Contains(text, "codex"):
		return aiIdentity{provider: "Codex", identity: "Codex updater", role: "support"}
	case strings.Contains(text, "node repl") && strings.Contains(text, "codex app"):
		return aiIdentity{provider: "Codex", identity: "Codex workspace REPL", role: "workspace session"}
	case strings.Contains(text, "skycomputeruseclient") || strings.Contains(text, "codex computer use"):
		return aiIdentity{provider: "Codex", identity: "Codex Computer Use MCP", role: "mcp tool"}
	case strings.Contains(text, "openai codex") || strings.Contains(text, "node modules openai codex"):
		return aiIdentity{provider: "Codex", identity: "Codex CLI native agent", role: "cli agent"}
	case strings.Contains(text, "bin codex") && (command == "node" || strings.Contains(text, "node")):
		return aiIdentity{provider: "Codex", identity: "Codex CLI wrapper", role: "cli wrapper"}
	case command == "codex":
		return aiIdentity{provider: "Codex", identity: "Codex process", role: "agent"}
	case strings.Contains(text, "claude code"):
		return aiIdentity{provider: "Claude Code", identity: "Claude Code", role: "agent"}
	case strings.Contains(text, "claude"):
		return aiIdentity{provider: "Claude", identity: "Claude process", role: "agent"}
	case strings.Contains(text, "anthropic"):
		return aiIdentity{provider: "Claude", identity: "Anthropic/Claude process", role: "agent"}
	case strings.Contains(text, "gemini"):
		return aiIdentity{provider: "Gemini", identity: "Gemini process", role: "agent"}
	case strings.Contains(text, "applications cursor app contents macos cursor"):
		return aiIdentity{provider: "Cursor", identity: "Cursor desktop app", role: "app root"}
	case strings.Contains(text, "cursor helper renderer"):
		return aiIdentity{provider: "Cursor", identity: "Cursor renderer", role: "app helper"}
	case strings.Contains(text, "cursor helper"):
		return aiIdentity{provider: "Cursor", identity: "Cursor helper", role: "app helper"}
	case strings.Contains(text, "cursor"):
		return aiIdentity{provider: "Cursor", identity: "Cursor process", role: "agent"}
	default:
		if rootPID > 0 && hasUsefulWorkspace(cwd) {
			return aiIdentity{identity: "workspace child process", role: "workspace child"}
		}
		return aiIdentity{}
	}
}

func deriveWorkspace(cwd, fullCmd string) string {
	if hasUsefulWorkspace(cwd) {
		return cwd
	}
	return ""
}

func deriveSession(identity aiIdentity, workspace string, rootPID, pid int) string {
	parts := []string{}
	if identity.provider != "" {
		parts = append(parts, identity.provider)
	}
	if identity.role != "" {
		parts = append(parts, identity.role)
	}
	if workspace != "" {
		parts = append(parts, "workspace "+prettyPath(workspace))
	}
	if rootPID > 0 && rootPID != pid {
		parts = append(parts, fmt.Sprintf("root pid %d", rootPID))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " / ")
}

func isRelevantAIDescendant(m ProcessMatch) bool {
	if strings.HasPrefix(m.MatchSource, "child of ") && len(m.Listening) == 0 && !m.Caffeinated && isTransientUtilityCommand(m.Command) {
		return false
	}
	if strings.HasPrefix(m.MatchSource, "child of ") && m.Identity == "" && len(m.Listening) == 0 && !hasUsefulWorkspace(m.Workspace) {
		return false
	}
	if m.Provider != "" {
		return true
	}
	if len(m.Listening) > 0 {
		return true
	}
	if hasUsefulWorkspace(m.Workspace) {
		return true
	}
	return false
}

func hasUsefulWorkspace(path string) bool {
	if path == "" || path == "/" {
		return false
	}
	home := os.Getenv("HOME")
	if home != "" {
		if path == home {
			return false
		}
		if strings.HasPrefix(path, home+"/Library/") {
			return false
		}
		if strings.HasPrefix(path, home+"/.local/") {
			return false
		}
	}
	if strings.HasPrefix(path, "/Applications/") || strings.HasPrefix(path, "/System/") {
		return false
	}
	return true
}

func isSelfPortsProcess(fullCmd string) bool {
	fields := strings.Fields(fullCmd)
	if len(fields) == 0 {
		return false
	}
	base := filepath.Base(fields[0])
	if base == "ports" || strings.HasPrefix(base, "ports-") {
		return true
	}
	return strings.Contains(fullCmd, "/ports-cli/ports")
}

func isTransientUtilityCommand(command string) bool {
	switch filepath.Base(command) {
	case "sh", "bash", "zsh", "fish",
		"cat", "head", "tail", "sed", "awk", "grep", "rg",
		"sort", "uniq", "wc", "tee", "xargs",
		"ps", "lsof", "pgrep", "ruby", "python", "python3",
		"perl", "go", "make":
		return true
	default:
		return false
	}
}

func dedupeStrings(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func roleRank(role string) int {
	switch role {
	case "app root", "cli wrapper", "cli agent", "agent":
		return 0
	case "agent server", "workspace session":
		return 1
	case "workspace child":
		return 2
	case "mcp tool":
		return 3
	case "app helper":
		return 4
	case "support":
		return 5
	default:
		return 9
	}
}

func stripSearchEnvNoise(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return s
	}
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.Contains(field, "=") && !strings.HasPrefix(field, "/") {
			continue
		}
		if strings.Contains(field, "/var/run/com.apple.security.cryptexd/codex.system/") {
			continue
		}
		out = append(out, field)
	}
	return strings.Join(out, " ")
}

func processQueryAliases(query string) []string {
	norm := normalizeSearchText(query)
	switch norm {
	case "ai", "agent", "agents", "ai agent", "ai agents":
		return []string{"codex", "openai codex", "claude", "claude code", "gemini", "cursor"}
	case "codex", "openai codex":
		return []string{"codex", "openai codex", "com openai codex", "codex app", "codex computer use", "skycomputeruseclient"}
	case "claude", "claude code", "claude cli":
		return []string{"claude", "claude code", "claude-code", "anthropic"}
	case "gemini", "gemini cli", "google gemini":
		return []string{"gemini", "gemini cli", "google gemini"}
	case "cursor", "cursor ai", "cursor ide":
		return []string{"cursor", "cursor ai", "cursor helper"}
	default:
		return []string{query}
	}
}

func normalizeSearchText(s string) string {
	s = strings.ToLower(s)
	replacer := strings.NewReplacer(
		"_", " ",
		"-", " ",
		"/", " ",
		".", " ",
		"@", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		":", " ",
		"=", " ",
		",", " ",
		"\"", " ",
		"'", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(s)), " ")
}

func renderProcessFindTable(matches []ProcessMatch) {
	if len(matches) == 0 {
		fmt.Println("No matching AI/app/agent processes found.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "MATCH\tPROVIDER\tROLE\tPID\tIDENTITY\tWORKSPACE\tLISTEN\tCAFFEINATE\tAGE")
	for _, m := range matches {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			truncate(strings.Join(m.Queries, ","), 18),
			truncate(emptyDash(m.Provider), 14),
			truncate(emptyDash(m.Role), 17),
			m.PID,
			truncate(processDisplayName(m), 30),
			truncateLeft(prettyPath(firstNonEmpty(m.Workspace, m.Cwd)), 42),
			truncate(processListenSummary(m.Listening), 26),
			caffeineSummary(m.CaffeinatePIDs),
			m.Age,
		)
	}
	w.Flush()
	fmt.Printf("\n%d process(es). Use `ports caffeinate --pid <PID>` to keep an exact process awake.\n", len(matches))
}

func renderProcessFindDetails(matches []ProcessMatch) {
	if len(matches) == 0 {
		fmt.Println("No matching AI/app/agent processes found.")
		return
	}
	for _, m := range matches {
		fmt.Printf("Match      %s\n", strings.Join(m.Queries, ", "))
		fmt.Printf("Provider   %s\n", emptyDash(m.Provider))
		fmt.Printf("Identity   %s\n", processDisplayName(m))
		fmt.Printf("Role       %s\n", emptyDash(m.Role))
		fmt.Printf("Session    %s\n", emptyDash(m.Session))
		fmt.Printf("PID        %d\n", m.PID)
		fmt.Printf("Command    %s\n", m.Command)
		fmt.Printf("Full cmd   %s\n", m.FullCmd)
		fmt.Printf("Exe path   %s\n", m.ExePath)
		fmt.Printf("Cwd        %s\n", prettyPath(m.Cwd))
		fmt.Printf("Workspace  %s\n", prettyPath(m.Workspace))
		fmt.Printf("Parent     pid %d (%s)\n", m.ParentPID, m.ParentCmd)
		if m.RootPID != 0 {
			fmt.Printf("Root       pid %d (%s)\n", m.RootPID, emptyDash(m.RootIdentity))
		}
		fmt.Printf("Matched by %s\n", emptyDash(m.MatchSource))
		fmt.Printf("Chain      %s\n", processParentChainSummary(m.ParentChain))
		fmt.Printf("Started    %s (%s ago)\n", formatStarted(m.StartedAt), m.Age)
		fmt.Printf("Kind       %s\n", m.Kind)
		fmt.Printf("Listening  %s\n", processListenDetails(m.Listening))
		if m.Caffeinated {
			fmt.Printf("Caffeinate active (watcher pid(s): %s)\n", intList(m.CaffeinatePIDs))
		} else {
			fmt.Printf("Caffeinate inactive\n")
		}
		fmt.Printf("Keep awake %s\n", m.CaffeinateCommand)
		fmt.Printf("Release    %s\n", m.UncaffeinateCommand)
		fmt.Println()
	}
}

func processDisplayName(m ProcessMatch) string {
	if m.Identity != "" {
		return m.Identity
	}
	if m.Command == "codex" && strings.Contains(m.FullCmd, " app-server") {
		return "codex app-server"
	}
	if m.Command == "node" && strings.Contains(m.FullCmd, "/bin/codex") {
		return "node codex wrapper"
	}
	return m.Command
}

func processParentChainSummary(chain []ProcessAncestor) string {
	if len(chain) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(chain))
	for _, a := range chain {
		parts = append(parts, fmt.Sprintf("%d:%s", a.PID, a.Command))
	}
	return strings.Join(parts, " <- ")
}

func processListenSummary(listeners []ProcessListener) string {
	if len(listeners) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(listeners))
	for _, l := range listeners {
		parts = append(parts, fmt.Sprintf("%s:%d", l.Protocol, l.Port))
	}
	return strings.Join(parts, ",")
}

func processListenDetails(listeners []ProcessListener) string {
	if len(listeners) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(listeners))
	for _, l := range listeners {
		parts = append(parts, fmt.Sprintf("%s %s :%d %s %s",
			l.Protocol, l.Host, l.Port, l.Display, prettyPath(l.Cwd)))
	}
	return strings.Join(parts, "; ")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatStarted(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC1123)
}

func caffeineSummary(pids []int) string {
	switch len(pids) {
	case 0:
		return "-"
	case 1:
		return fmt.Sprintf("on:%d", pids[0])
	default:
		return fmt.Sprintf("on:%d+", len(pids))
	}
}

func prettyPath(p string) string {
	if p == "" {
		return "-"
	}
	home := os.Getenv("HOME")
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func truncateLeft(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n+1:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func fetchListeners() ([]Listener, error) {
	tcp, err := runLsof([]string{"-iTCP", "-sTCP:LISTEN", "-nP", "-FpcLnT"}, "TCP")
	if err != nil {
		return nil, err
	}
	udp, err := runLsof([]string{"-iUDP", "-nP", "-FpcLnT"}, "UDP")
	if err != nil {
		return nil, err
	}
	all := append(tcp, udp...)
	deduped := dedupe(all)
	enrichDocker(deduped)
	enrichCaffeinateStatus(deduped)
	return deduped, nil
}

type dockerContainerInfo struct {
	name  string
	cwd   string
	image string
}

var (
	dockerOnce sync.Once
	dockerMap  map[int]dockerContainerInfo
)

// loadDockerInfo runs `docker ps` once per invocation and builds a map from
// host port to container metadata. Compose-managed containers expose their
// source directory via the com.docker.compose.project.working_dir label —
// that's the *real* project for a tunneled port. Falls back silently if
// docker isn't installed, isn't running, or times out.
func loadDockerInfo() {
	dockerOnce.Do(func() {
		dockerMap = map[int]dockerContainerInfo{}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "docker", "ps", "--format", "{{json .}}").Output()
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(out), "\n") {
			if line == "" {
				continue
			}
			var c struct {
				Names  string `json:"Names"`
				Ports  string `json:"Ports"`
				Labels string `json:"Labels"`
				Image  string `json:"Image"`
			}
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				continue
			}
			workDir := ""
			for _, lab := range strings.Split(c.Labels, ",") {
				if strings.HasPrefix(lab, "com.docker.compose.project.working_dir=") {
					workDir = strings.TrimPrefix(lab, "com.docker.compose.project.working_dir=")
					break
				}
			}
			info := dockerContainerInfo{
				name:  strings.TrimPrefix(c.Names, "/"),
				cwd:   workDir,
				image: c.Image,
			}
			for _, m := range strings.Split(c.Ports, ", ") {
				i := strings.Index(m, "->")
				if i < 0 {
					continue
				}
				left := m[:i]
				j := strings.LastIndex(left, ":")
				if j < 0 {
					continue
				}
				port, err := strconv.Atoi(left[j+1:])
				if err != nil {
					continue
				}
				if _, exists := dockerMap[port]; !exists {
					dockerMap[port] = info
				}
			}
		}
	})
}

func enrichDocker(listeners []Listener) {
	needs := false
	for _, l := range listeners {
		if strings.HasPrefix(l.Display, "docker(") {
			needs = true
			break
		}
	}
	if !needs {
		return
	}
	loadDockerInfo()
	for i := range listeners {
		if !strings.HasPrefix(listeners[i].Display, "docker(") {
			continue
		}
		info, ok := dockerMap[listeners[i].Port]
		if !ok {
			continue
		}
		if info.cwd != "" {
			listeners[i].Cwd = info.cwd
		}
		if info.name != "" {
			listeners[i].Display = "docker(" + info.name + ")"
		}
	}
}

func enrichCaffeinateStatus(listeners []Listener) {
	watchers := findCaffeinateWatchers()
	for i := range listeners {
		pids := append([]int(nil), watchers[listeners[i].PID]...)
		if len(pids) == 0 {
			continue
		}
		sort.Ints(pids)
		listeners[i].Caffeinated = true
		listeners[i].CaffeinatePIDs = pids
	}
}

func findCaffeinateWatchers() map[int][]int {
	out := map[int][]int{}
	ps, err := exec.Command("/bin/ps", "axo", "pid=,command=").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(ps), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		firstSpace := strings.IndexAny(line, " \t")
		if firstSpace < 0 {
			continue
		}
		watcherPID, err := strconv.Atoi(strings.TrimSpace(line[:firstSpace]))
		if err != nil || watcherPID == os.Getpid() {
			continue
		}
		command := strings.TrimSpace(line[firstSpace:])
		if !isCaffeinateCommand(command) {
			continue
		}
		for _, targetPID := range caffeinateWatchTargets(command) {
			out[targetPID] = append(out[targetPID], watcherPID)
		}
	}
	for pid := range out {
		sort.Ints(out[pid])
	}
	return out
}

func isCaffeinateCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return filepath.Base(fields[0]) == "caffeinate"
}

func caffeinateWatchTargets(command string) []int {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return nil
	}
	var targets []int
	for i := 1; i < len(fields); i++ {
		tok := fields[i]
		if tok == "--" {
			break
		}
		if !strings.HasPrefix(tok, "-") {
			break
		}
		switch {
		case tok == "-w" && i+1 < len(fields):
			if pid, err := strconv.Atoi(fields[i+1]); err == nil {
				targets = append(targets, pid)
			}
			i++
		case strings.HasPrefix(tok, "-w") && len(tok) > 2:
			if pid, err := strconv.Atoi(tok[2:]); err == nil {
				targets = append(targets, pid)
			}
		case tok == "-t" && i+1 < len(fields):
			i++
		}
	}
	return targets
}

func runLsof(args []string, proto string) ([]Listener, error) {
	cmd := exec.Command("/usr/sbin/lsof", args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return parseLsofF(out.String(), proto), nil
}

// parseLsofF parses `lsof -F` field output. Each record begins with `p<pid>`,
// then process-level fields (c=command, L=user), then per-fd lines starting
// with `f<fd>` followed by n=name, T=tcp state, etc.
func parseLsofF(s, proto string) []Listener {
	scanner := bufio.NewScanner(strings.NewReader(s))
	var (
		out                 []Listener
		curPID              int
		curCmd, curUser     string
		curName, curTcpInfo string
		inFD                bool
	)
	flush := func() {
		if curPID == 0 || curName == "" {
			return
		}
		if proto == "UDP" && strings.Contains(curName, "->") {
			curName, curTcpInfo = "", ""
			inFD = false
			return
		}
		host, port := splitHostPort(curName)
		if port <= 0 {
			return
		}
		info := getProc(curPID)
		out = append(out, Listener{
			Command:   curCmd,
			Display:   humanizeCommand(curCmd, info.fullCmd, info.exePath),
			PID:       curPID,
			User:      curUser,
			Protocol:  proto,
			Address:   curName,
			Host:      host,
			Port:      port,
			StartedAt: info.started,
			Age:       humanAge(info.started),
			FullCmd:   info.fullCmd,
			ExePath:   info.exePath,
			Cwd:       info.cwd,
			ParentPID: info.parentPID,
			ParentCmd: info.parentCmd,
			Kind:      classify(curCmd, info.exePath, curUser),
		})
		curName, curTcpInfo = "", ""
		inFD = false
	}
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		tag, val := line[0], line[1:]
		switch tag {
		case 'p':
			flush()
			curPID, _ = strconv.Atoi(val)
			curCmd, curUser = "", ""
			curName, curTcpInfo = "", ""
			inFD = false
		case 'c':
			curCmd = val
		case 'L':
			curUser = val
		case 'f':
			flush()
			inFD = true
		case 'n':
			if inFD {
				curName = val
			}
		case 'T':
			if inFD {
				curTcpInfo = val
			}
		}
	}
	flush()
	_ = curTcpInfo
	// For UDP we accept all; for TCP -sTCP:LISTEN already filtered.
	return out
}

func splitHostPort(name string) (string, int) {
	// Possible shapes: "*:3000", "127.0.0.1:3000", "[::1]:3000", "192.168.1.5:5353->1.2.3.4:53"
	if i := strings.Index(name, "->"); i >= 0 {
		name = name[:i]
	}
	idx := strings.LastIndex(name, ":")
	if idx < 0 {
		return name, 0
	}
	host := name[:idx]
	portStr := name[idx+1:]
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "*" {
		host = "0.0.0.0"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return host, 0
	}
	return host, port
}

func dedupe(in []Listener) []Listener {
	seen := make(map[string]struct{}, len(in))
	out := make([]Listener, 0, len(in))
	for _, l := range in {
		key := fmt.Sprintf("%d-%s-%s-%d", l.PID, l.Protocol, l.Host, l.Port)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, l)
	}
	return out
}

func procStart(pid int) time.Time {
	out, err := exec.Command("/bin/ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return time.Time{}
	}
	s := strings.TrimSpace(string(out))
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func procFullCmd(pid int) string {
	out, err := exec.Command("/bin/ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func procExePath(pid int) string {
	out, err := exec.Command("/bin/ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type procInfo struct {
	started   time.Time
	fullCmd   string
	exePath   string
	cwd       string
	parentPID int
	parentCmd string
}

var (
	procCacheMu sync.Mutex
	procCache   = map[int]*procInfo{}
)

func getProc(pid int) *procInfo {
	procCacheMu.Lock()
	if p, ok := procCache[pid]; ok {
		procCacheMu.Unlock()
		return p
	}
	procCacheMu.Unlock()

	p := &procInfo{
		started: procStart(pid),
		fullCmd: procFullCmd(pid),
		exePath: procExePath(pid),
		cwd:     procCwd(pid),
	}
	p.parentPID, p.parentCmd = procParent(pid)

	procCacheMu.Lock()
	procCache[pid] = p
	procCacheMu.Unlock()
	return p
}

func procParent(pid int) (int, string) {
	out, err := exec.Command("/bin/ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, ""
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, ""
	}
	if ppid == 1 {
		return 1, "launchd"
	}
	parentCmd, err := exec.Command("/bin/ps", "-o", "comm=", "-p", strconv.Itoa(ppid)).Output()
	if err != nil {
		return ppid, ""
	}
	name := strings.TrimSpace(string(parentCmd))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return ppid, name
}

func procCwd(pid int) string {
	cmd := exec.Command("/usr/sbin/lsof", "-a", "-d", "cwd", "-p", strconv.Itoa(pid), "-Fn")
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}

// humanizeCommand turns truthful-but-confusing process names into something a
// reader recognizes. The classic case: Colima/Lima forward Docker container
// ports out of the VM via an SSH multiplexer, so the host-side listener is
// really named "ssh" — but the user thinks of it as docker.
func humanizeCommand(command, fullCmd, exePath string) string {
	if command != "ssh" {
		return command
	}
	if strings.Contains(fullCmd, "/.colima/") || strings.Contains(fullCmd, "colima/ssh.sock") {
		return "docker(colima)"
	}
	if strings.Contains(fullCmd, "/.lima/") || (strings.Contains(fullCmd, "/lima/") && strings.Contains(fullCmd, "ssh.sock")) {
		return "docker(lima)"
	}
	if strings.Contains(fullCmd, "orbstack") || strings.Contains(exePath, "orbstack") {
		return "docker(orbstack)"
	}
	if strings.Contains(fullCmd, " -L ") || strings.Contains(fullCmd, " -R ") {
		return "ssh→tunnel"
	}
	return command
}

// classify returns "app" for GUI app-bundle processes, "system" for OS daemons,
// and "dev" for everything else (the things you actually care about).
func classify(command, exePath, user string) string {
	if strings.Contains(exePath, ".app/Contents/") {
		return "app"
	}
	systemPrefixes := []string{
		"/System/",
		"/usr/libexec/",
		"/Library/Apple/",
		"/Library/PrivilegedHelperTools/",
		"/Library/Application Support/com.apple.",
	}
	for _, p := range systemPrefixes {
		if strings.HasPrefix(exePath, p) {
			return "system"
		}
	}
	if strings.HasPrefix(user, "_") || user == "root" && strings.HasPrefix(exePath, "/Library/") {
		return "system"
	}
	// Known noisy GUI helpers / agents that sometimes live outside .app bundles
	noisy := map[string]bool{
		"rapportd":          true,
		"sharingd":          true,
		"mDNSResponder":     true,
		"identityservicesd": true,
		"cloudd":            true,
		"bluetoothd":        true,
		"airportd":          true,
		"ControlCenter":     true,
		"replicatord":       true,
		"remoted":           true,
		"nehelper":          true,
		"trustd":            true,
		"netbiosd":          true,
	}
	if noisy[command] {
		return "system"
	}
	return "dev"
}

func humanAge(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

type targetArgs struct {
	positional     []string
	dirPrefix      string
	pidTargets     []int
	findQueries    []string
	yes            bool
	strictDir      bool
	follow         bool
	followInterval time.Duration
	followKey      string
}

func parseTargetArgs(args []string) targetArgs {
	out := targetArgs{followInterval: defaultFollowInterval}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dir" && i+1 < len(args):
			out.dirPrefix = resolveDir(args[i+1])
			i++
		case strings.HasPrefix(a, "--dir="):
			out.dirPrefix = resolveDir(strings.TrimPrefix(a, "--dir="))
		case a == "--find":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				out.findQueries = append(out.findQueries, args[i+1])
				i++
			} else {
				out.findQueries = append(out.findQueries, defaultProcessFindQueries()...)
			}
		case strings.HasPrefix(a, "--find="):
			out.findQueries = append(out.findQueries, strings.TrimPrefix(a, "--find="))
		case a == "--pid" && i+1 < len(args):
			if pid, err := strconv.Atoi(args[i+1]); err == nil {
				out.pidTargets = append(out.pidTargets, pid)
			} else {
				fmt.Fprintf(os.Stderr, "skip %q: not a pid\n", args[i+1])
			}
			i++
		case strings.HasPrefix(a, "--pid="):
			raw := strings.TrimPrefix(a, "--pid=")
			if pid, err := strconv.Atoi(raw); err == nil {
				out.pidTargets = append(out.pidTargets, pid)
			} else {
				fmt.Fprintf(os.Stderr, "skip %q: not a pid\n", raw)
			}
		case a == "--yes", a == "-y":
			out.yes = true
		case a == "--strict-dir", a == "--exclusive-dir":
			out.strictDir = true
		case a == "--follow", a == "--watch":
			out.follow = true
		case (a == "--interval" || a == "--every") && i+1 < len(args):
			out.followInterval = parseFollowInterval(args[i+1])
			i++
		case strings.HasPrefix(a, "--interval="):
			out.followInterval = parseFollowInterval(strings.TrimPrefix(a, "--interval="))
		case strings.HasPrefix(a, "--every="):
			out.followInterval = parseFollowInterval(strings.TrimPrefix(a, "--every="))
		case strings.HasPrefix(a, "--follow-key="):
			out.followKey = strings.TrimSpace(strings.TrimPrefix(a, "--follow-key="))
		default:
			out.positional = append(out.positional, a)
		}
	}
	return out
}

func parseFollowInterval(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultFollowInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if seconds, atoiErr := strconv.Atoi(raw); atoiErr == nil {
			d = time.Duration(seconds) * time.Second
		} else {
			return defaultFollowInterval
		}
	}
	if d < minFollowInterval {
		return minFollowInterval
	}
	return d
}

func hasTargetSelectors(targets targetArgs) bool {
	return targets.dirPrefix != "" ||
		len(targets.pidTargets) > 0 ||
		len(targets.findQueries) > 0 ||
		len(targets.positional) > 0
}

func signalCmd(args []string, sig syscall.Signal, label string) {
	targets := parseTargetArgs(args)
	if len(targets.findQueries) > 0 {
		exitErr(fmt.Errorf("%s does not support --find/AI selectors; use `ports find ...` then target an exact --pid", label))
	}
	if targets.dirPrefix == "" && len(targets.pidTargets) == 0 && len(targets.positional) == 0 {
		exitErr(fmt.Errorf("%s requires at least one port, pid, or --dir PATH", label))
	}
	listeners, err := fetchListeners()
	if err != nil {
		exitErr(err)
	}

	pids := resolveProcessTargets(targets, listeners)
	if len(pids) == 0 {
		if targets.dirPrefix != "" {
			if targets.strictDir && hasListenersUnderDir(listeners, targets.dirPrefix) {
				emitDirScopeWarnings(label, targets, listeners)
				exitErr(fmt.Errorf("no exclusive listening processes found under %s; shared PIDs were skipped by --strict-dir", targets.dirPrefix))
			}
			exitErr(fmt.Errorf("no listening processes found under %s", targets.dirPrefix))
		}
		exitErr(fmt.Errorf("no matching processes"))
	}
	emitDirScopeWarnings(label, targets, listeners)

	needsConfirm := !targets.yes && (targets.dirPrefix != "" || len(pids) > 1)
	if needsConfirm {
		fmt.Printf("About to %s %d process(es):\n", label, len(pids))
		for _, pid := range sortedTargetPIDs(pids) {
			why := pids[pid]
			fmt.Printf("  pid %d — %s\n", pid, why)
		}
		fmt.Print("Continue? [y/N] ")
		r := bufio.NewReader(os.Stdin)
		ans, _ := r.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(ans)) != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	for _, pid := range sortedTargetPIDs(pids) {
		why := pids[pid]
		err := syscall.Kill(pid, sig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s pid %d (%s): %v\n", label, pid, why, err)
			if err == syscall.EPERM {
				fmt.Fprintf(os.Stderr, "    → owned by another user; try: sudo ports %s %s\n", label, strings.Join(args, " "))
			}
			continue
		}
		fmt.Printf("  %s pid %d (%s) ✓\n", label, pid, why)
	}
}

func resolveProcessTargets(targets targetArgs, listeners []Listener) map[int]string {
	pids := map[int]string{}
	if targets.dirPrefix != "" {
		shared := pidsWithListenersOutsideDir(listeners, targets.dirPrefix)
		for _, l := range listeners {
			if pathHasPrefix(l.Cwd, targets.dirPrefix) {
				if targets.strictDir && shared[l.PID] {
					continue
				}
				display := l.Display
				if display == "" {
					display = l.Command
				}
				pids[l.PID] = fmt.Sprintf("%s on :%d (%s)", display, l.Port, prettyPath(l.Cwd))
			}
		}
	}
	if len(targets.positional) > 0 {
		for k, v := range resolveTargets(targets.positional, listeners) {
			pids[k] = v
		}
	}
	for _, pid := range targets.pidTargets {
		if procExists(pid) {
			pids[pid] = fmt.Sprintf("pid %d", pid)
		} else {
			fmt.Fprintf(os.Stderr, "no pid %d\n", pid)
		}
	}
	return pids
}

func resolveAllListeningTargets(listeners []Listener) map[int]string {
	byPID := map[int][]Listener{}
	for _, l := range listeners {
		if l.PID <= 1 {
			continue
		}
		byPID[l.PID] = append(byPID[l.PID], l)
	}
	out := map[int]string{}
	for pid, pidListeners := range byPID {
		out[pid] = "all listening processes: " + listenerSummary(pidListeners)
	}
	return out
}

func resolveCaffeinateTargets(targets targetArgs, listeners []Listener) (map[int]string, error) {
	numericArgs, queryArgs := splitNumericAndQueryTargets(targets.positional)
	queryArgs = append(queryArgs, targets.findQueries...)

	baseTargets := targets
	baseTargets.positional = numericArgs
	baseTargets.findQueries = nil
	pids := resolveProcessTargets(baseTargets, listeners)

	if len(queryArgs) == 0 {
		return pids, nil
	}
	matches, err := findProcessMatches(queryArgs)
	if err != nil {
		return pids, err
	}
	shared := map[int]bool{}
	if targets.dirPrefix != "" && targets.strictDir {
		shared = pidsWithListenersOutsideDir(listeners, targets.dirPrefix)
	}
	addProcessMatchesToTargets(pids, matches, targets, shared)
	return pids, nil
}

func splitNumericAndQueryTargets(args []string) (numeric []string, queries []string) {
	for _, arg := range args {
		if _, err := strconv.Atoi(arg); err == nil {
			numeric = append(numeric, arg)
			continue
		}
		queries = append(queries, arg)
	}
	return numeric, queries
}

func addProcessMatchesToTargets(pids map[int]string, matches []ProcessMatch, targets targetArgs, shared map[int]bool) {
	for _, m := range matches {
		if !shouldCaffeinateProcessMatch(m) {
			continue
		}
		if targets.dirPrefix != "" && !processMatchInDir(m, targets.dirPrefix) {
			continue
		}
		if targets.strictDir && shared[m.PID] {
			continue
		}
		pids[m.PID] = processMatchTargetReason(m)
	}
}

func processMatchInDir(m ProcessMatch, prefix string) bool {
	if pathHasPrefix(m.Workspace, prefix) || pathHasPrefix(m.Cwd, prefix) {
		return true
	}
	for _, l := range m.Listening {
		if pathHasPrefix(l.Cwd, prefix) {
			return true
		}
	}
	return false
}

func shouldCaffeinateProcessMatch(m ProcessMatch) bool {
	if m.PID <= 1 {
		return false
	}
	switch m.Role {
	case "support":
		return false
	default:
		return true
	}
}

func processMatchTargetReason(m ProcessMatch) string {
	parts := []string{}
	if m.Provider != "" {
		parts = append(parts, m.Provider)
	}
	if m.Role != "" {
		parts = append(parts, m.Role)
	}
	display := processDisplayName(m)
	if display != "" {
		parts = append(parts, display)
	}
	if m.Workspace != "" {
		parts = append(parts, prettyPath(m.Workspace))
	}
	if len(m.Listening) > 0 {
		parts = append(parts, processListenSummary(m.Listening))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("pid %d", m.PID)
	}
	return strings.Join(parts, ", ")
}

func resolveTargets(args []string, listeners []Listener) map[int]string {
	out := map[int]string{}
	for _, a := range args {
		n, err := strconv.Atoi(a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %q: not a number\n", a)
			continue
		}
		matched := false
		// Try as port first
		for _, l := range listeners {
			if l.Port == n {
				out[l.PID] = fmt.Sprintf("port %d, %s", l.Port, l.Command)
				matched = true
			}
		}
		if matched {
			continue
		}
		// Then as pid
		for _, l := range listeners {
			if l.PID == n {
				out[l.PID] = fmt.Sprintf("pid %d, %s on :%d", l.PID, l.Command, l.Port)
				matched = true
				break
			}
		}
		if !matched {
			// Allow killing any pid even if not bound to a port
			if procExists(n) {
				out[n] = fmt.Sprintf("pid %d", n)
			} else {
				fmt.Fprintf(os.Stderr, "no listener on port %d and no pid %d\n", n, n)
			}
		}
	}
	return out
}

func pidsWithListenersOutsideDir(listeners []Listener, prefix string) map[int]bool {
	hasInside := map[int]bool{}
	hasOutside := map[int]bool{}
	for _, l := range listeners {
		if pathHasPrefix(l.Cwd, prefix) {
			hasInside[l.PID] = true
			continue
		}
		hasOutside[l.PID] = true
	}
	shared := map[int]bool{}
	for pid := range hasInside {
		if hasOutside[pid] {
			shared[pid] = true
		}
	}
	return shared
}

func hasListenersUnderDir(listeners []Listener, prefix string) bool {
	for _, l := range listeners {
		if pathHasPrefix(l.Cwd, prefix) {
			return true
		}
	}
	return false
}

type dirScopeLeak struct {
	pid     int
	inside  []Listener
	outside []Listener
}

func dirScopeLeaks(listeners []Listener, prefix string) []dirScopeLeak {
	byPID := map[int]*dirScopeLeak{}
	for _, l := range listeners {
		leak := byPID[l.PID]
		if leak == nil {
			leak = &dirScopeLeak{pid: l.PID}
			byPID[l.PID] = leak
		}
		if pathHasPrefix(l.Cwd, prefix) {
			leak.inside = append(leak.inside, l)
		} else {
			leak.outside = append(leak.outside, l)
		}
	}

	out := []dirScopeLeak{}
	for _, leak := range byPID {
		if len(leak.inside) > 0 && len(leak.outside) > 0 {
			out = append(out, *leak)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].pid < out[j].pid
	})
	return out
}

func emitDirScopeWarnings(label string, targets targetArgs, listeners []Listener) {
	if targets.dirPrefix == "" {
		return
	}
	leaks := dirScopeLeaks(listeners, targets.dirPrefix)
	if len(leaks) == 0 {
		return
	}
	if targets.strictDir {
		fmt.Fprintf(os.Stderr, "warning: --strict-dir skipped %d shared PID(s); %s would affect listeners outside %s.\n",
			len(leaks), label, prettyPath(targets.dirPrefix))
	} else {
		fmt.Fprintf(os.Stderr, "warning: --dir %s matches shared PID(s); %s affects whole processes, including listeners outside that directory.\n",
			prettyPath(targets.dirPrefix), label)
		fmt.Fprintln(os.Stderr, "         use --strict-dir to skip shared PIDs.")
	}
	for _, leak := range leaks {
		fmt.Fprintf(os.Stderr, "  pid %d inside: %s\n", leak.pid, listenerSummary(leak.inside))
		fmt.Fprintf(os.Stderr, "  pid %d outside: %s\n", leak.pid, listenerSummary(leak.outside))
	}
}

func listenerSummary(listeners []Listener) string {
	if len(listeners) == 0 {
		return "-"
	}
	sort.SliceStable(listeners, func(i, j int) bool {
		if listeners[i].Cwd != listeners[j].Cwd {
			return listeners[i].Cwd < listeners[j].Cwd
		}
		return listeners[i].Port < listeners[j].Port
	})
	seen := map[string]bool{}
	parts := []string{}
	for _, l := range listeners {
		display := l.Display
		if display == "" {
			display = l.Command
		}
		part := fmt.Sprintf(":%d %s (%s)", l.Port, display, prettyPath(l.Cwd))
		if seen[part] {
			continue
		}
		seen[part] = true
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func canonicalFollowSpec(targets targetArgs) string {
	parts := []string{"v1"}
	if !hasTargetSelectors(targets) {
		parts = append(parts, "all-listeners")
	}
	if targets.dirPrefix != "" {
		parts = append(parts, "dir="+filepath.Clean(targets.dirPrefix))
	}
	if targets.strictDir {
		parts = append(parts, "strict-dir")
	}
	for _, pid := range sortedUniqueInts(targets.pidTargets) {
		parts = append(parts, fmt.Sprintf("pid=%d", pid))
	}
	for _, query := range sortedUniqueStrings(targets.findQueries) {
		parts = append(parts, "find="+query)
	}
	for _, arg := range sortedUniqueStrings(targets.positional) {
		parts = append(parts, "arg="+arg)
	}
	return strings.Join(parts, "\n")
}

func followKeyForTargets(targets targetArgs) string {
	if targets.followKey != "" {
		return targets.followKey
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(canonicalFollowSpec(targets)))
	return fmt.Sprintf("%016x", h.Sum64())
}

func followArgsForTargets(targets targetArgs) []string {
	args := []string{
		"__follow-caffeinate",
		"--follow-key=" + followKeyForTargets(targets),
		"--interval=" + targets.followInterval.String(),
	}
	if targets.strictDir {
		args = append(args, "--strict-dir")
	}
	if targets.dirPrefix != "" {
		args = append(args, "--dir", targets.dirPrefix)
	}
	for _, pid := range sortedUniqueInts(targets.pidTargets) {
		args = append(args, "--pid", strconv.Itoa(pid))
	}
	for _, query := range sortedUniqueStrings(targets.findQueries) {
		args = append(args, "--find", query)
	}
	args = append(args, sortedUniqueStrings(targets.positional)...)
	return args
}

func sortedUniqueStrings(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortedUniqueInts(in []int) []int {
	out := []int{}
	seen := map[int]bool{}
	for _, n := range in {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

func findFollowCaffeinateWatchers(key string) []int {
	out := []int{}
	ps, err := exec.Command("/bin/ps", "axo", "pid=,command=").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(ps), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		firstSpace := strings.IndexAny(line, " \t")
		if firstSpace < 0 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line[:firstSpace]))
		if err != nil || pid == os.Getpid() {
			continue
		}
		command := strings.TrimSpace(line[firstSpace:])
		if !strings.Contains(command, "__follow-caffeinate") || !isSelfPortsProcess(command) {
			continue
		}
		if key != "" && !strings.Contains(command, "--follow-key="+key) {
			continue
		}
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
}

func startFollowCaffeinateWatcher(targets targetArgs) (int, bool, error) {
	key := followKeyForTargets(targets)
	if existing := findFollowCaffeinateWatchers(key); len(existing) > 0 {
		return existing[0], true, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, false, err
	}
	cmd := exec.Command(exe, followArgsForTargets(targets)...)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err == nil {
		defer devNull.Close()
		cmd.Stdin = devNull
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}
	if err := cmd.Start(); err != nil {
		return 0, false, err
	}
	watcherPID := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return watcherPID, false, err
	}
	return watcherPID, false, nil
}

func followCaffeinateCmd(args []string) {
	targets := parseTargetArgs(args)
	for {
		listeners, err := fetchListeners()
		if err == nil {
			pids := map[int]string{}
			if hasTargetSelectors(targets) {
				pids, err = resolveCaffeinateTargets(targets, listeners)
			} else {
				pids = resolveAllListeningTargets(listeners)
			}
			if err == nil {
				ensureCaffeinateWatchers(pids)
			}
		}
		time.Sleep(targets.followInterval)
	}
}

func ensureCaffeinateWatchers(pids map[int]string) {
	watchers := findCaffeinateWatchers()
	for _, pid := range sortedTargetPIDs(pids) {
		if len(watchers[pid]) > 0 {
			continue
		}
		_, _ = startCaffeinateWatcher(pid)
	}
}

type watcherTarget struct {
	watcherPID int
	targetPID  int
}

func caffeinateWatcherTargets(watchers map[int][]int) []watcherTarget {
	pairs := []watcherTarget{}
	for targetPID, watcherPIDs := range watchers {
		for _, watcherPID := range watcherPIDs {
			pairs = append(pairs, watcherTarget{watcherPID: watcherPID, targetPID: targetPID})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].watcherPID != pairs[j].watcherPID {
			return pairs[i].watcherPID < pairs[j].watcherPID
		}
		return pairs[i].targetPID < pairs[j].targetPID
	})
	return pairs
}

func decaffeinateAllCmd(targets targetArgs) {
	watcherPairs := caffeinateWatcherTargets(findCaffeinateWatchers())
	followWatchers := findFollowCaffeinateWatchers("")
	if len(watcherPairs) == 0 && len(followWatchers) == 0 {
		fmt.Println("No active caffeinate watchers found.")
		return
	}

	if !targets.yes {
		fmt.Printf("About to stop ALL caffeinate watchers discovered on this machine:\n")
		for _, pair := range watcherPairs {
			fmt.Printf("  watcher %d -> pid %d\n", pair.watcherPID, pair.targetPID)
		}
		for _, pid := range followWatchers {
			fmt.Printf("  ports follow watcher %d\n", pid)
		}
		fmt.Print("Continue? [y/N] ")
		r := bufio.NewReader(os.Stdin)
		ans, _ := r.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(ans)) != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	stopped := map[int]bool{}
	for _, pair := range watcherPairs {
		if stopped[pair.watcherPID] {
			continue
		}
		stopped[pair.watcherPID] = true
		if err := syscall.Kill(pair.watcherPID, syscall.SIGTERM); err != nil {
			fmt.Fprintf(os.Stderr, "  decaffeinate watcher %d for pid %d: %v\n", pair.watcherPID, pair.targetPID, err)
			continue
		}
		fmt.Printf("  decaffeinate watcher %d ✓\n", pair.watcherPID)
	}
	for _, pid := range followWatchers {
		if stopped[pid] {
			continue
		}
		stopped[pid] = true
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			fmt.Fprintf(os.Stderr, "  decaffeinate ports follow watcher %d: %v\n", pid, err)
			continue
		}
		fmt.Printf("  decaffeinate ports follow watcher %d ✓\n", pid)
	}
}

func needsCaffeinateConfirmation(targets targetArgs, pids map[int]string, start bool, selectorless bool, followWatchers []int) bool {
	if targets.yes {
		return false
	}
	return selectorless || targets.follow || targets.dirPrefix != "" || len(pids) > 1 || (!start && len(followWatchers) > 0)
}

func confirmCaffeinatePlan(targets targetArgs, pids map[int]string, start bool, selectorless bool, followWatchers []int) bool {
	if start {
		if selectorless {
			fmt.Printf("About to start caffeinate watchers for ALL %d current listening process(es):\n", len(pids))
		} else if len(pids) == 0 && targets.follow {
			fmt.Printf("About to start a follow caffeinate watcher for future matching process(es):\n")
		} else {
			fmt.Printf("About to start caffeinate watchers for %d process(es):\n", len(pids))
		}
	} else {
		fmt.Printf("About to stop caffeinate watchers for %d process(es):\n", len(pids))
	}
	for _, pid := range sortedTargetPIDs(pids) {
		why := pids[pid]
		fmt.Printf("  pid %d — %s\n", pid, why)
	}
	if start && targets.follow {
		fmt.Printf("  follow watcher — rescan every %s for new matches\n", targets.followInterval)
	}
	if !start && targets.follow {
		for _, pid := range followWatchers {
			fmt.Printf("  ports follow watcher %d\n", pid)
		}
	}
	fmt.Print("Continue? [y/N] ")
	r := bufio.NewReader(os.Stdin)
	ans, _ := r.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(ans)) != "y" {
		fmt.Println("Aborted.")
		return false
	}
	return true
}

func stopFollowWatchers(followWatchers []int) {
	for _, pid := range sortedUniqueInts(followWatchers) {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			fmt.Fprintf(os.Stderr, "  decaffeinate ports follow watcher %d: %v\n", pid, err)
			continue
		}
		fmt.Printf("  decaffeinate ports follow watcher %d ✓\n", pid)
	}
}

func caffeinateCmd(args []string, start bool) {
	label := "caffeinate"
	if !start {
		label = "decaffeinate"
	}
	targets := parseTargetArgs(args)
	selectorless := !hasTargetSelectors(targets)
	if selectorless && !start {
		decaffeinateAllCmd(targets)
		return
	}
	listeners, err := fetchListeners()
	if err != nil {
		exitErr(err)
	}
	pids := map[int]string{}
	if selectorless {
		pids = resolveAllListeningTargets(listeners)
	} else {
		pids, err = resolveCaffeinateTargets(targets, listeners)
		if err != nil {
			exitErr(err)
		}
	}
	followWatchers := []int{}
	if targets.follow {
		followWatchers = findFollowCaffeinateWatchers(followKeyForTargets(targets))
	}
	if len(pids) == 0 {
		if start && targets.follow {
			emitDirScopeWarnings(label, targets, listeners)
			if needsCaffeinateConfirmation(targets, pids, start, selectorless, followWatchers) &&
				!confirmCaffeinatePlan(targets, pids, start, selectorless, followWatchers) {
				return
			}
			watcherPID, already, err := startFollowCaffeinateWatcher(targets)
			if err != nil {
				exitErr(err)
			}
			if already {
				fmt.Printf("  follow caffeinate already active via watcher %d\n", watcherPID)
			} else {
				fmt.Printf("  follow caffeinate ✓ watcher %d (interval %s)\n", watcherPID, targets.followInterval)
			}
			return
		}
		if !start && targets.follow && len(followWatchers) > 0 {
			if needsCaffeinateConfirmation(targets, pids, start, selectorless, followWatchers) &&
				!confirmCaffeinatePlan(targets, pids, start, selectorless, followWatchers) {
				return
			}
			stopFollowWatchers(followWatchers)
			return
		}
		if selectorless {
			exitErr(fmt.Errorf("no listening processes found"))
		}
		if targets.dirPrefix != "" {
			if targets.strictDir && hasListenersUnderDir(listeners, targets.dirPrefix) {
				emitDirScopeWarnings(label, targets, listeners)
				exitErr(fmt.Errorf("no exclusive listening processes found under %s; shared PIDs were skipped by --strict-dir", targets.dirPrefix))
			}
			exitErr(fmt.Errorf("no listening processes found under %s", targets.dirPrefix))
		}
		if len(targets.findQueries) > 0 || hasProcessQueryTargets(targets.positional) {
			exitErr(fmt.Errorf("no matching AI/app/agent processes"))
		}
		exitErr(fmt.Errorf("no matching processes"))
	}
	emitDirScopeWarnings(label, targets, listeners)

	if needsCaffeinateConfirmation(targets, pids, start, selectorless, followWatchers) &&
		!confirmCaffeinatePlan(targets, pids, start, selectorless, followWatchers) {
		return
	}

	watchers := findCaffeinateWatchers()
	for _, pid := range sortedTargetPIDs(pids) {
		why := pids[pid]
		if start {
			if active := watchers[pid]; len(active) > 0 {
				fmt.Printf("  caffeinate pid %d (%s) already active via watcher(s) %s\n", pid, why, intList(active))
				continue
			}
			watcherPID, err := startCaffeinateWatcher(pid)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  caffeinate pid %d (%s): %v\n", pid, why, err)
				continue
			}
			fmt.Printf("  caffeinate pid %d (%s) ✓ watcher %d\n", pid, why, watcherPID)
			continue
		}

		active := watchers[pid]
		if len(active) == 0 {
			fmt.Printf("  %s pid %d (%s): no active watcher\n", label, pid, why)
			continue
		}
		for _, watcherPID := range active {
			if err := syscall.Kill(watcherPID, syscall.SIGTERM); err != nil {
				fmt.Fprintf(os.Stderr, "  %s watcher %d for pid %d (%s): %v\n", label, watcherPID, pid, why, err)
				continue
			}
			fmt.Printf("  %s pid %d (%s) ✓ stopped watcher %d\n", label, pid, why, watcherPID)
		}
	}
	if start && targets.follow {
		watcherPID, already, err := startFollowCaffeinateWatcher(targets)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  follow caffeinate: %v\n", err)
		} else if already {
			fmt.Printf("  follow caffeinate already active via watcher %d\n", watcherPID)
		} else {
			fmt.Printf("  follow caffeinate ✓ watcher %d (interval %s)\n", watcherPID, targets.followInterval)
		}
	}
	if !start && targets.follow {
		stopFollowWatchers(followWatchers)
	}
}

func sortedTargetPIDs(targets map[int]string) []int {
	pids := make([]int, 0, len(targets))
	for pid := range targets {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}

func hasProcessQueryTargets(args []string) bool {
	_, queries := splitNumericAndQueryTargets(args)
	return len(queries) > 0
}

func startCaffeinateWatcher(pid int) (int, error) {
	if !procExists(pid) {
		return 0, fmt.Errorf("pid %d does not exist", pid)
	}
	cmd := exec.Command("/usr/bin/caffeinate", "-dimsu", "-w", strconv.Itoa(pid))
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err == nil {
		defer devNull.Close()
		cmd.Stdin = devNull
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	watcherPID := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return watcherPID, err
	}
	return watcherPID, nil
}

func intList(nums []int) string {
	if len(nums) == 0 {
		return "-"
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

func procExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func inspectCmd(args []string) {
	if len(args) == 0 {
		exitErr(fmt.Errorf("inspect requires a port"))
	}
	port, err := strconv.Atoi(args[0])
	if err != nil {
		exitErr(err)
	}
	listeners, err := fetchListeners()
	if err != nil {
		exitErr(err)
	}
	matches := []Listener{}
	for _, l := range listeners {
		if l.Port == port {
			matches = append(matches, l)
		}
	}
	if len(matches) == 0 {
		fmt.Printf("Nothing listening on :%d\n", port)
		return
	}
	for _, l := range matches {
		fmt.Printf("Port       %d (%s)\n", l.Port, l.Protocol)
		fmt.Printf("PID        %d\n", l.PID)
		fmt.Printf("Command    %s", l.Command)
		if l.Display != "" && l.Display != l.Command {
			fmt.Printf("  →  %s", l.Display)
		}
		fmt.Println()
		fmt.Printf("Full cmd   %s\n", l.FullCmd)
		fmt.Printf("Exe path   %s\n", l.ExePath)
		fmt.Printf("Cwd        %s\n", prettyPath(l.Cwd))
		fmt.Printf("Parent     pid %d (%s)\n", l.ParentPID, l.ParentCmd)
		fmt.Printf("User       %s\n", l.User)
		fmt.Printf("Bind       %s\n", l.Address)
		fmt.Printf("Started    %s (%s ago)\n", l.StartedAt.Format(time.RFC1123), l.Age)
		fmt.Printf("Kind       %s\n", l.Kind)
		if l.Caffeinated {
			fmt.Printf("Caffeinate active (watcher pid(s): %s)\n", intList(l.CaffeinatePIDs))
		} else {
			fmt.Printf("Caffeinate inactive\n")
		}
		probeHTTP(l.Host, l.Port)
		fmt.Println()
	}
}

func probeHTTP(host string, port int) {
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:%d/", host, port)
	cmd := exec.Command("/usr/bin/curl", "-sS", "-o", "/dev/null", "-w",
		"HTTP/1.1 %{http_code} %{content_type} (%{time_total}s)",
		"--max-time", "2", "-I", url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("HTTP      no response (not http or refused)\n")
		return
	}
	fmt.Printf("HTTP      %s %s\n", url, strings.TrimSpace(string(out)))
}

func selfDestroyCmd() {
	fmt.Println("This will remove the `ports` binary from /usr/local/bin and ~/.local/bin.")
	fmt.Print("Continue? [y/N] ")
	r := bufio.NewReader(os.Stdin)
	ans, _ := r.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(ans)) != "y" {
		fmt.Println("Aborted.")
		return
	}
	paths := []string{"/usr/local/bin/ports", os.ExpandEnv("$HOME/.local/bin/ports")}
	for _, p := range paths {
		if err := os.Remove(p); err == nil {
			fmt.Printf("removed %s\n", p)
		}
	}
	fmt.Println("Source tree at ~/Documents/personal/ports-cli kept — delete manually if you want.")
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
