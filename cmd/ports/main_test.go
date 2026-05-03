package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseLsofFSkipsConnectedUDP(t *testing.T) {
	input := `p100
cCodex Helper
Lerdem
f23
n10.58.200.113:53695->172.64.155.209:443
p200
cmDNSResponder
L_mdnsresponder
f10
n*:5353
`

	listeners := parseLsofF(input, "UDP")
	if len(listeners) != 1 {
		t.Fatalf("expected 1 unconnected UDP listener, got %d: %#v", len(listeners), listeners)
	}
	if listeners[0].PID != 200 || listeners[0].Port != 5353 || listeners[0].Host != "0.0.0.0" {
		t.Fatalf("unexpected listener: %#v", listeners[0])
	}
}

func TestResolveProcessTargetsStrictDirSkipsSharedPID(t *testing.T) {
	benzersor := "/Users/erdem/Documents/devirventures/benzersor"
	devredin := "/Users/erdem/Documents/devirventures/devredin/backend"
	listeners := []Listener{
		{PID: 85016, Port: 5432, Display: "docker(benzersor-postgres-1)", Cwd: benzersor},
		{PID: 85016, Port: 3001, Display: "docker(devredin_backend)", Cwd: devredin},
		{PID: 99370, Port: 3030, Command: "node", Cwd: benzersor + "/apps/api"},
	}

	defaultTargets := resolveProcessTargets(targetArgs{dirPrefix: benzersor}, listeners)
	if _, ok := defaultTargets[85016]; !ok {
		t.Fatalf("expected default --dir targeting to include shared pid 85016: %#v", defaultTargets)
	}

	strictTargets := resolveProcessTargets(targetArgs{dirPrefix: benzersor, strictDir: true}, listeners)
	if _, ok := strictTargets[85016]; ok {
		t.Fatalf("expected --strict-dir to skip shared pid 85016: %#v", strictTargets)
	}
	if _, ok := strictTargets[99370]; !ok {
		t.Fatalf("expected --strict-dir to keep exclusive pid 99370: %#v", strictTargets)
	}
}

func TestResolveCaffeinateTargetsKeepsPortResolution(t *testing.T) {
	listeners := []Listener{
		{PID: 85016, Port: 5432, Command: "ssh", Display: "docker(benzersor-postgres-1)", Cwd: "/Users/erdem/Documents/devirventures/benzersor"},
	}

	targets, err := resolveCaffeinateTargets(targetArgs{positional: []string{"5432"}}, listeners)
	if err != nil {
		t.Fatalf("resolveCaffeinateTargets returned error: %v", err)
	}
	if got := targets[85016]; got != "port 5432, ssh" {
		t.Fatalf("expected port 5432 to target pid 85016, got %#v", targets)
	}
}

func TestDirScopeLeaksFindsSharedPID(t *testing.T) {
	benzersor := "/Users/erdem/Documents/devirventures/benzersor"
	devredin := "/Users/erdem/Documents/devirventures/devredin/backend"
	listeners := []Listener{
		{PID: 85016, Port: 5432, Display: "docker(benzersor-postgres-1)", Cwd: benzersor},
		{PID: 85016, Port: 3001, Display: "docker(devredin_backend)", Cwd: devredin},
		{PID: 99370, Port: 3030, Command: "node", Cwd: benzersor + "/apps/api"},
	}

	leaks := dirScopeLeaks(listeners, benzersor)
	if len(leaks) != 1 {
		t.Fatalf("expected one shared pid leak, got %#v", leaks)
	}
	if leaks[0].pid != 85016 || len(leaks[0].inside) != 1 || len(leaks[0].outside) != 1 {
		t.Fatalf("unexpected leak details: %#v", leaks[0])
	}
}

func TestCaffeinateWatchTargetsParsesMacOSWatchForms(t *testing.T) {
	got := caffeinateWatchTargets("/usr/bin/caffeinate -dimsu -w 85016")
	if len(got) != 1 || got[0] != 85016 {
		t.Fatalf("expected -w 85016 target, got %#v", got)
	}

	got = caffeinateWatchTargets("/usr/bin/caffeinate -dimsu -w85016 -t 5")
	if len(got) != 1 || got[0] != 85016 {
		t.Fatalf("expected -w85016 target, got %#v", got)
	}
}

func TestMatchedProcessQueriesFindsCodexApp(t *testing.T) {
	m := ProcessMatch{
		Command: "Codex",
		FullCmd: "/Applications/Codex.app/Contents/MacOS/Codex",
		ExePath: "/Applications/Codex.app/Contents/MacOS/Codex",
		Cwd:     "/",
	}

	got := matchedProcessQueries(m, []string{"codex"})
	if len(got) != 1 || got[0] != "codex" {
		t.Fatalf("expected codex match, got %#v", got)
	}
}

func TestMatchedProcessQueriesIgnoresCodexSystemEnvNoise(t *testing.T) {
	m := ProcessMatch{
		Command: "Cursor Helper",
		FullCmd: "Cursor Helper PATH=/usr/bin:/var/run/com.apple.security.cryptexd/codex.system/bootstrap/usr/bin PWD=/",
		ExePath: "/Applications/Cursor.app/Contents/Frameworks/Cursor Helper.app/Contents/MacOS/Cursor Helper",
		Cwd:     "/",
	}

	got := matchedProcessQueries(m, []string{"codex"})
	if len(got) != 0 {
		t.Fatalf("expected no codex match from codex.system PATH noise, got %#v", got)
	}
}

func TestMatchedProcessQueriesUsesTermBoundaries(t *testing.T) {
	m := ProcessMatch{
		Command: "notcodex",
		FullCmd: "/usr/local/bin/notcodex",
		ExePath: "/usr/local/bin/notcodex",
		Cwd:     "/",
	}

	got := matchedProcessQueries(m, []string{"codex"})
	if len(got) != 0 {
		t.Fatalf("expected no codex match inside a larger token, got %#v", got)
	}
}

func TestDirectProcessMatchedQueriesSkipsShellFindQuery(t *testing.T) {
	row := psProcess{pid: 10, ppid: 1, fullCmd: `/bin/zsh -lc ports --find codex`}

	got := directProcessMatchedQueries(row, []string{"codex"})
	if len(got) != 0 {
		t.Fatalf("expected shell finder invocation to be ignored, got %#v", got)
	}
}

func TestIdentifyAIProcessDoesNotTreatShellArgAsCodexProcess(t *testing.T) {
	got := identifyAIProcess("zsh", `/bin/zsh -lc ports --find codex`, "/bin/zsh", "/tmp", 10)
	if got.provider != "" {
		t.Fatalf("expected shell command with codex argument not to be identified as Codex, got %#v", got)
	}
}

func TestParseTargetArgsPIDForcesExactPID(t *testing.T) {
	targets := parseTargetArgs([]string{"--pid", "93633", "--pid=51927", "--find", "codex", "--find=cursor", "3000"})

	if len(targets.pidTargets) != 2 || targets.pidTargets[0] != 93633 || targets.pidTargets[1] != 51927 {
		t.Fatalf("unexpected pid targets: %#v", targets.pidTargets)
	}
	if len(targets.findQueries) != 2 || targets.findQueries[0] != "codex" || targets.findQueries[1] != "cursor" {
		t.Fatalf("unexpected find queries: %#v", targets.findQueries)
	}
	if len(targets.positional) != 1 || targets.positional[0] != "3000" {
		t.Fatalf("unexpected positional targets: %#v", targets.positional)
	}
}

func TestParseTargetArgsFollowOptions(t *testing.T) {
	targets := parseTargetArgs([]string{"codex", "--follow", "--interval", "2", "--dir", "~/Documents"})

	if !targets.follow {
		t.Fatalf("expected --follow to be enabled")
	}
	if targets.followInterval != 2*time.Second {
		t.Fatalf("expected 2s follow interval, got %s", targets.followInterval)
	}
	if targets.dirPrefix == "" {
		t.Fatalf("expected directory prefix to be parsed")
	}
}

func TestParseTargetArgsFindWithoutQueryDefaults(t *testing.T) {
	targets := parseTargetArgs([]string{"--find"})

	if len(targets.findQueries) != 4 {
		t.Fatalf("expected default AI find queries, got %#v", targets.findQueries)
	}
}

func TestSplitNumericAndQueryTargets(t *testing.T) {
	numeric, queries := splitNumericAndQueryTargets([]string{"3000", "codex", "93633", "claude code"})

	if len(numeric) != 2 || numeric[0] != "3000" || numeric[1] != "93633" {
		t.Fatalf("unexpected numeric targets: %#v", numeric)
	}
	if len(queries) != 2 || queries[0] != "codex" || queries[1] != "claude code" {
		t.Fatalf("unexpected query targets: %#v", queries)
	}
}

func TestAddProcessMatchesToTargetsSkipsSupportOnly(t *testing.T) {
	pids := map[int]string{}
	addProcessMatchesToTargets(pids, []ProcessMatch{
		{PID: 93590, Provider: "Codex", Role: "app root", Identity: "Codex desktop app"},
		{PID: 93624, Provider: "Codex", Role: "support", Identity: "Codex crash reporter"},
		{PID: 93625, Provider: "Codex", Role: "app helper", Identity: "Codex helper"},
	}, targetArgs{}, nil)

	if _, ok := pids[93590]; !ok {
		t.Fatalf("expected app root pid to be targeted: %#v", pids)
	}
	if _, ok := pids[93625]; !ok {
		t.Fatalf("expected app helper pid to be targeted: %#v", pids)
	}
	if _, ok := pids[93624]; ok {
		t.Fatalf("expected support pid to be skipped: %#v", pids)
	}
}

func TestAddProcessMatchesToTargetsFiltersByDir(t *testing.T) {
	benzersor := "/Users/erdem/Documents/devirventures/benzersor"
	devredin := "/Users/erdem/Documents/devirventures/devredin/backend"
	pids := map[int]string{}

	addProcessMatchesToTargets(pids, []ProcessMatch{
		{PID: 10, Provider: "Codex", Role: "workspace child", Workspace: benzersor + "/apps/api"},
		{PID: 20, Provider: "Codex", Role: "workspace child", Workspace: devredin},
	}, targetArgs{dirPrefix: benzersor}, nil)

	if _, ok := pids[10]; !ok {
		t.Fatalf("expected benzersor Codex child to be included: %#v", pids)
	}
	if _, ok := pids[20]; ok {
		t.Fatalf("expected devredin Codex child to be filtered out: %#v", pids)
	}
}

func TestFollowSpecStableAndArgsComplete(t *testing.T) {
	a := targetArgs{
		positional:     []string{"cursor", "codex"},
		dirPrefix:      "/tmp/project",
		pidTargets:     []int{9, 2},
		findQueries:    []string{"gemini", "claude code"},
		strictDir:      true,
		followInterval: 2 * time.Second,
	}
	b := targetArgs{
		positional:     []string{"codex", "cursor"},
		dirPrefix:      "/tmp/project",
		pidTargets:     []int{2, 9},
		findQueries:    []string{"claude code", "gemini"},
		strictDir:      true,
		followInterval: 9 * time.Second,
	}

	if followKeyForTargets(a) != followKeyForTargets(b) {
		t.Fatalf("expected follow key to ignore selector ordering and interval")
	}
	args := followArgsForTargets(a)
	got := strings.Join(args, "\x00")
	for _, want := range []string{"__follow-caffeinate", "--strict-dir", "--dir\x00/tmp/project", "--pid\x002", "--pid\x009", "--find\x00claude code", "--find\x00gemini", "codex", "cursor"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected follow args %q to contain %q", got, want)
		}
	}
}

func TestProcessMatchTargetReasonIncludesUsefulDetails(t *testing.T) {
	reason := processMatchTargetReason(ProcessMatch{
		Provider:  "Codex",
		Role:      "workspace child",
		Identity:  "workspace child process",
		Workspace: "/Users/erdem/Documents/devirventures/benzersor/apps/api",
		Listening: []ProcessListener{{Protocol: "TCP", Port: 3030}},
	})

	for _, want := range []string{"Codex", "workspace child", "~/Documents/devirventures/benzersor/apps/api", "TCP:3030"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("expected reason %q to contain %q", reason, want)
		}
	}
}

func TestAIQueryAliasesIncludeCursor(t *testing.T) {
	aliases := processQueryAliases("ai")
	for _, alias := range aliases {
		if alias == "cursor" {
			return
		}
	}
	t.Fatalf("expected ai aliases to include cursor, got %#v", aliases)
}

func TestAIDescendantFilterDropsTransientUtilityChildren(t *testing.T) {
	m := ProcessMatch{
		Provider:    "Codex",
		Identity:    "workspace child process",
		Role:        "workspace child",
		Workspace:   "/Users/erdem/Documents/personal/ports-cli",
		MatchSource: "child of pid 51927",
		Command:     "sed",
		FullCmd:     "sed -n 1,25p",
	}

	if isRelevantAIDescendant(m) {
		t.Fatalf("expected transient utility child to be filtered: %#v", m)
	}
}

func TestFormatStartedZero(t *testing.T) {
	if got := formatStarted(time.Time{}); got != "-" {
		t.Fatalf("expected zero started time to render as dash, got %q", got)
	}
}

func TestAIDescendantFilterDropsFinderUtilityChildren(t *testing.T) {
	m := ProcessMatch{
		Provider:    "Codex",
		Identity:    "",
		Role:        "",
		Workspace:   "",
		MatchSource: "child of pid 51927",
		Command:     "ps",
		FullCmd:     "/bin/ps axww -o pid=,ppid=,command=",
	}

	if isRelevantAIDescendant(m) {
		t.Fatalf("expected unrelated tool child to be filtered: %#v", m)
	}
}

func TestNearestDirectAncestorStopsAtPortsProcess(t *testing.T) {
	direct := map[int][]string{51927: {"codex"}}
	byPID := map[int]psProcess{
		100:   {pid: 100, ppid: 200, fullCmd: "/bin/ps axww -o pid=,ppid=,command="},
		200:   {pid: 200, ppid: 51927, fullCmd: "/private/tmp/ports-ai-check --find codex"},
		51927: {pid: 51927, ppid: 51926, fullCmd: "/Users/erdem/.local/share/fnm/node-versions/v24.15.0/installation/lib/node_modules/@openai/codex/codex"},
	}

	if got := nearestDirectAncestor(200, direct, byPID); got != 0 {
		t.Fatalf("expected ports process boundary to stop ancestry match, got %d", got)
	}
}
