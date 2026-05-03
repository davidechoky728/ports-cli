package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

const version = "0.4.2"

type Listener struct {
	Command   string    `json:"command"`     // raw process name from lsof (e.g. "ssh")
	Display   string    `json:"display"`     // human-friendly (e.g. "docker(colima)")
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
	case "inspect":
		inspectCmd(os.Args[2:])
	case "self-destroy":
		selfDestroyCmd()
	case "version", "--version", "-v":
		fmt.Println("ports", version)
	case "help", "--help", "-h":
		printHelp()
	default:
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

FLAGS (kill / force-kill / pause / resume)
  --dir PATH           Target every listener whose cwd is at or under PATH
  --yes / -y           Skip the safety confirmation prompt

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
		fmt.Fprintln(w, "PORT\tPROTO\tPID\tCOMMAND\tPARENT\tPATH\tKIND\tHOST\tAGE")
	} else {
		fmt.Fprintln(w, "PORT\tPROTO\tPID\tCOMMAND\tPARENT\tPATH\tHOST\tAGE")
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
			fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
				l.Port, l.Protocol, l.PID, truncate(display, 30),
				truncate(parent, 14), truncateLeft(path, 42),
				l.Kind, l.Host, l.Age)
		} else {
			fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
				l.Port, l.Protocol, l.PID, truncate(display, 30),
				truncate(parent, 14), truncateLeft(path, 42),
				l.Host, l.Age)
		}
	}
	w.Flush()
	fmt.Printf("\n%d listener(s)\n", len(ls))
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
		"rapportd":         true,
		"sharingd":         true,
		"mDNSResponder":    true,
		"identityservicesd": true,
		"cloudd":           true,
		"bluetoothd":       true,
		"airportd":         true,
		"ControlCenter":    true,
		"replicatord":      true,
		"remoted":          true,
		"nehelper":         true,
		"trustd":           true,
		"netbiosd":         true,
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

func signalCmd(args []string, sig syscall.Signal, label string) {
	var (
		positional []string
		dirPrefix  string
		yes        bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dir" && i+1 < len(args):
			dirPrefix = resolveDir(args[i+1])
			i++
		case strings.HasPrefix(a, "--dir="):
			dirPrefix = resolveDir(strings.TrimPrefix(a, "--dir="))
		case a == "--yes", a == "-y":
			yes = true
		default:
			positional = append(positional, a)
		}
	}
	if dirPrefix == "" && len(positional) == 0 {
		exitErr(fmt.Errorf("%s requires at least one port, pid, or --dir PATH", label))
	}
	listeners, err := fetchListeners()
	if err != nil {
		exitErr(err)
	}

	pids := map[int]string{}
	if dirPrefix != "" {
		for _, l := range listeners {
			if pathHasPrefix(l.Cwd, dirPrefix) {
				pids[l.PID] = fmt.Sprintf("%s on :%d (%s)", l.Command, l.Port, prettyPath(l.Cwd))
			}
		}
	}
	if len(positional) > 0 {
		for k, v := range resolveTargets(positional, listeners) {
			pids[k] = v
		}
	}
	if len(pids) == 0 {
		if dirPrefix != "" {
			exitErr(fmt.Errorf("no listening processes found under %s", dirPrefix))
		}
		exitErr(fmt.Errorf("no matching processes"))
	}

	needsConfirm := !yes && (dirPrefix != "" || len(pids) > 1)
	if needsConfirm {
		fmt.Printf("About to %s %d process(es):\n", label, len(pids))
		for pid, why := range pids {
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

	for pid, why := range pids {
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
