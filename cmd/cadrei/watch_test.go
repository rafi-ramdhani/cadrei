//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fastWatch shortens the stream's timings for a test.
func fastWatch(t *testing.T) {
	t.Helper()
	old := watchTiming
	watchTiming = watchTimes{round: 20 * time.Millisecond, tmux: 40 * time.Millisecond, projects: 80 * time.Millisecond,
		heartbeat: 600 * time.Millisecond, periodic: time.Hour, late: time.Minute}
	t.Cleanup(func() { watchTiming = old })
}

// liveStream is cadrei ls --json --watch running in this test.
type liveStream struct {
	raw    chan string
	done   chan int
	mu     sync.Mutex
	errOut bytes.Buffer
}

// startWatch starts the stream with args after --watch. It ends when the
// test does, as a reader that goes away ends it.
func startWatch(t *testing.T, args ...string) *liveStream {
	t.Helper()
	pr, pw := io.Pipe()
	s := &liveStream{raw: make(chan string, 10000), done: make(chan int, 1)}
	var errOut lockedBuffer
	go func() {
		code := run(append([]string{"ls", "--json", "--watch"}, args...), strings.NewReader(""), pw, &errOut)
		pw.Close()
		s.mu.Lock()
		s.errOut.WriteString(errOut.String())
		s.mu.Unlock()
		s.done <- code
	}()
	go func() {
		r := bufio.NewReader(pr)
		for {
			line, err := r.ReadString('\n')
			if line != "" {
				s.raw <- line
			}
			if err != nil {
				close(s.raw)
				return
			}
		}
	}()
	t.Cleanup(func() {
		pr.Close()
		select {
		case code := <-s.done:
			if code != 0 {
				t.Errorf("the stream ended with %d: %s", code, s.errOut.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("the stream didn't end when its reader left")
		}
	})
	return s
}

// lockedBuffer is a buffer safe to write from the stream's goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// Len is how much was written.
func (l *lockedBuffer) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Len()
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// streamLine is one parsed line, with its raw text.
type parsedLine struct {
	raw string
	m   map[string]any
}

func (p parsedLine) typ() string { s, _ := p.m["type"].(string); return s }

// next is the stream's next line, heartbeats too.
func (s *liveStream) next(t *testing.T, within time.Duration) parsedLine {
	t.Helper()
	select {
	case raw, ok := <-s.raw:
		if !ok {
			t.Fatal("the stream ended")
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("not one JSON object: %q", raw)
		}
		// One type for a field on every line: project is always a name.
		if pr, ok := m["project"]; ok {
			if _, name := pr.(string); !name {
				t.Fatalf("project isn't a name: %s", raw)
			}
		}
		return parsedLine{raw, m}
	case <-time.After(within):
		t.Fatalf("no line within %s", within)
	}
	return parsedLine{}
}

// expect reads lines, heartbeats aside, until it has as many as types, and
// checks their types in order.
func (s *liveStream) expect(t *testing.T, types ...string) []parsedLine {
	t.Helper()
	var got []parsedLine
	deadline := time.Now().Add(15 * time.Second)
	for len(got) < len(types) {
		l := s.next(t, time.Until(deadline))
		if l.typ() != lineHeartbeat {
			got = append(got, l)
		}
	}
	var have []string
	for _, l := range got {
		have = append(have, l.typ())
	}
	if !reflect.DeepEqual(have, types) {
		t.Fatalf("lines %v, want %v:\n%s", have, types, raws(got))
	}
	return got
}

// settle applies lines of the allowed types until 400 ms pass with none.
func (s *liveStream) settle(t *testing.T, p *picture, allowed ...string) {
	t.Helper()
	for {
		select {
		case raw := <-s.raw:
			var m map[string]any
			json.Unmarshal([]byte(raw), &m)
			l := parsedLine{raw, m}
			if l.typ() != lineHeartbeat && !slicesContains(allowed, l.typ()) {
				t.Fatalf("an unexpected line: %s", raw)
			}
			p.apply(t, l)
		case <-time.After(400 * time.Millisecond):
			return
		}
	}
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// quiet checks that no line but heartbeats comes for d.
func (s *liveStream) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	end := time.After(d)
	for {
		select {
		case raw := <-s.raw:
			if !strings.Contains(raw, `"type":"heartbeat"`) {
				t.Fatalf("an unexpected line: %s", raw)
			}
		case <-end:
			return
		}
	}
}

func raws(lines []parsedLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.raw)
	}
	return b.String()
}

// picture is what a reader keeps: the last snapshot's status with every
// line after it applied (7.13.3).
type picture struct{ status map[string]any }

func (p *picture) apply(t *testing.T, l parsedLine) {
	t.Helper()
	m := l.m
	switch l.typ() {
	case lineSnapshot:
		p.status = m["status"].(map[string]any)
	case lineHeartbeat:
	case lineOrchestrator:
		p.status["orchestrator"] = m["orchestrator"]
	case lineTeamStarted:
		team := map[string]any{"session": m["session"], "team": m["team"], "legacy": m["legacy"] == true, "members": m["members"]}
		if pr, ok := m["project"]; ok {
			team["project"] = pr
		}
		running := append(p.status["running"].([]any), team)
		sort.Slice(running, func(a, b int) bool {
			return running[a].(map[string]any)["session"].(string) < running[b].(map[string]any)["session"].(string)
		})
		p.status["running"] = running
	case lineTeamStopped:
		var kept []any
		for _, r := range p.status["running"].([]any) {
			if r.(map[string]any)["session"] != m["session"] {
				kept = append(kept, r)
			}
		}
		if kept == nil {
			kept = []any{}
		}
		p.status["running"] = kept
	case lineMemberStarted, lineMemberStopped, lineMemberState:
		member := m["member"].(map[string]any)
		for _, r := range p.status["running"].([]any) {
			team := r.(map[string]any)
			if team["session"] != m["session"] {
				continue
			}
			members := team["members"].([]any)
			var out []any
			found := false
			for _, x := range members {
				if x.(map[string]any)["name"] == member["name"] {
					found = true
					if l.typ() == lineMemberState {
						out = append(out, member)
					}
					continue
				}
				out = append(out, x)
			}
			if l.typ() == lineMemberStarted && !found {
				out = append(out, member)
			}
			if out == nil {
				out = []any{}
			}
			team["members"] = out
		}
	case lineProjectState:
		if _, ok := m["project"].(string); !ok {
			t.Fatalf("project isn't a name: %s", l.raw)
		}
		project := m["project_info"].(map[string]any)
		for i, x := range p.status["projects"].([]any) {
			if x.(map[string]any)["name"] == project["name"] {
				p.status["projects"].([]any)[i] = project
			}
		}
	default:
		t.Fatalf("a line type the test doesn't know: %s", l.raw)
	}
}

// matchesLs checks the picture against cadrei ls --json now, leaving out
// what only snapshots bring up to date.
func (p *picture) matchesLs(t *testing.T, args ...string) {
	t.Helper()
	var now map[string]any
	if err := json.Unmarshal([]byte(must(t, append([]string{"ls", "--json"}, args...)...)), &now); err != nil {
		t.Fatal(err)
	}
	a, b := comparable(p.status), comparable(now)
	if !reflect.DeepEqual(a, b) {
		x, _ := json.MarshalIndent(a, "", " ")
		y, _ := json.MarshalIndent(b, "", " ")
		t.Errorf("the picture differs from ls --json:\n%s\nls --json:\n%s", x, y)
	}
}

func comparable(status map[string]any) map[string]any {
	raw, _ := json.Marshal(status)
	var c map[string]any
	json.Unmarshal(raw, &c)
	for _, k := range []string{"hook", "problems", "other_cadreis"} {
		delete(c, k)
	}
	if projects, ok := c["projects"].([]any); ok {
		for _, x := range projects {
			delete(x.(map[string]any), "found")
		}
	}
	return c
}

// Acceptance 117: the first line is a start snapshot equal to ls --json,
// every line is one compact JSON object, and seq goes up by one.
func TestTheStreamStartsWithTheSameSnapshot(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	first := s.next(t, 5*time.Second)
	if first.typ() != lineSnapshot || first.m["reason"] != "start" || first.m["seq"] != 1.0 {
		t.Fatalf("first line: %s", first.raw)
	}
	var ls map[string]any
	json.Unmarshal([]byte(must(t, "ls", "--json")), &ls)
	if !reflect.DeepEqual(first.m["status"], ls) {
		t.Errorf("the snapshot isn't ls --json:\n%s\n%s", first.raw, must(t, "ls", "--json"))
	}
	for i := 2; i <= 3; i++ {
		l := s.next(t, 5*time.Second)
		if l.m["seq"] != float64(i) || strings.Count(l.raw, "\n") != 1 || !strings.HasSuffix(l.raw, "\n") || strings.Contains(l.raw, "\x1b") {
			t.Errorf("line %d: %q", i, l.raw)
		}
	}
}

// Acceptance 118 and the 7.13.3 guarantee: a team's life gives the lines
// in order, and after each, the picture matches ls --json.
func TestTheStreamFollowsATeam(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	os.MkdirAll(home+"/src/app", 0o755)
	exec.Command("git", "-C", home+"/src/app", "init", "-q").Run()
	must(t, "project", "add", "app", "--path", home+"/src/app", "--no-trust")
	fastWatch(t)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	var p picture
	p.apply(t, s.expect(t, lineSnapshot)[0])
	step := func(want ...string) []parsedLine {
		t.Helper()
		lines := s.expect(t, want...)
		for _, l := range lines {
			p.apply(t, l)
		}
		s.quiet(t, 300*time.Millisecond)
		p.matchesLs(t)
		return lines
	}
	must(t, "up", "dev")
	// Members start one after another: the team can show with its first,
	// and the next join it.
	p.apply(t, s.expect(t, lineTeamStarted)[0])
	s.settle(t, &p, lineMemberStarted, lineMemberState)
	p.matchesLs(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	t.Setenv("CADREI_HOME", c)
	if l := step(lineMemberState)[0]; l.m["member"].(map[string]any)["state"] != "working" {
		t.Errorf("working: %s", l.raw)
	}
	// The same state again: no line (acceptance 119).
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"PreToolUse","tool_name":"Read"}`)
	t.Setenv("CADREI_HOME", c)
	s.quiet(t, 300*time.Millisecond)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
	t.Setenv("CADREI_HOME", c)
	if l := step(lineMemberState)[0]; l.m["member"].(map[string]any)["state"] != "waiting" || l.m["member"].(map[string]any)["waiting_for"] != "approval" {
		t.Errorf("waiting: %s", l.raw)
	}
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	t.Setenv("CADREI_HOME", c)
	if l := step(lineMemberState)[0]; l.m["member"].(map[string]any)["state"] != "idle" {
		t.Errorf("idle: %s", l.raw)
	}
	must(t, "stop", "dev/reviewer")
	if l := step(lineMemberStopped)[0]; !reflect.DeepEqual(l.m["member"], map[string]any{"name": "work-dev-reviewer", "role": "reviewer"}) {
		t.Errorf("member_stopped: %s", l.raw)
	}
	os.Rename(home+"/src/app", home+"/src/app-moved")
	if l := step(lineProjectState)[0]; l.m["project"] != "app" || l.m["project_info"].(map[string]any)["state"] != "missing" {
		t.Errorf("project: %s", l.raw)
	}
	must(t, "stop", "dev")
	if l := step(lineTeamStopped)[0]; l.m["crew"] != "work" || l.m["team"] != "dev" {
		t.Errorf("team_stopped: %s", l.raw)
	}
}

// Acceptance 119: a safeguard stop shows for an idle member at once.
func TestTheStreamSeesASafeguardStop(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	// An idle member has the transcript of the turn it ended.
	placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", func([]byte) []byte { return []byte("{}\n") })
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", nil)
	if l := s.expect(t, lineMemberState)[0]; l.m["member"].(map[string]any)["state"] != "stopped_by_safeguards" {
		t.Errorf("safeguard: %s", l.raw)
	}
}

// Audit perf-hooks-4 (7.13.4): a waiting or working member whose files
// don't change isn't read again round after round; a change is read at
// once.
func TestTheStreamReadsAMemberOnlyWhenSomethingChanged(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
	feed(t, c, "work-dev-reviewer", `{"hook_event_name":"UserPromptSubmit"}`)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	time.Sleep(200 * time.Millisecond)
	before := watchReads.Load()
	time.Sleep(500 * time.Millisecond) // about 25 rounds
	if n := watchReads.Load() - before; n != 0 {
		t.Errorf("%d reads with nothing changed", n)
	}
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	t.Setenv("CADREI_HOME", c)
	if l := s.expect(t, lineMemberState)[0]; l.m["member"].(map[string]any)["state"] != "working" {
		t.Errorf("after a change: %s", l.raw)
	}
}

// Acceptance 119, working: the stop counts after the runtime's own wait,
// worked out from time alone, with no new hook event.
func TestTheStreamWaitsOutASafeguardRetry(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	// A stop 57 seconds ago: the runtime's 60-second retry ends soon.
	placeTranscript(t, c, "work-dev-engineer", "safeguard-recent.jsonl", func(b []byte) []byte {
		return []byte(strings.ReplaceAll(string(b), "2026-01-01T10:00:05.100Z", time.Now().Add(-57*time.Second).UTC().Format(time.RFC3339Nano)))
	})
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	if l := s.expect(t, lineSnapshot)[0]; !strings.Contains(l.raw, `"state":"working"`) {
		t.Fatalf("not working at the start: %s", l.raw)
	}
	if l := s.expect(t, lineMemberState)[0]; l.m["member"].(map[string]any)["state"] != "stopped_by_safeguards" {
		t.Errorf("safeguard: %s", l.raw)
	}
}

// Acceptance 120: the crew's folder going and coming back, a new member
// file, and with --all a new crew, each give a snapshot.
func TestTheStreamSnapshotsWhenTheCrewChanges(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	fastWatch(t)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	os.WriteFile(c+"/members/dev/writer.md", []byte("# Member: Writer\n"), 0o644)
	if l := s.expect(t, lineSnapshot)[0]; l.m["reason"] != "crew_changed" {
		t.Errorf("member file: %s", l.raw)
	}
	os.Rename(c, home+"/away")
	l := s.expect(t, lineSnapshot)[0]
	status := l.m["status"].(map[string]any)
	if l.m["reason"] != "crew_gone" || status["cadrei"].(map[string]any)["missing"] != true ||
		len(status["running"].([]any)) != 0 || len(status["projects"].([]any)) != 0 || len(status["teams"].(map[string]any)) != 0 || len(status["problems"].([]any)) != 0 {
		t.Errorf("gone: %s", l.raw)
	}
	s.quiet(t, 300*time.Millisecond)
	os.Rename(home+"/away", c)
	if l := s.expect(t, lineSnapshot)[0]; l.m["reason"] != "crew_back" {
		t.Errorf("back: %s", l.raw)
	}

	t.Setenv("CADREI_HOME", "")
	all := startWatch(t, "--all")
	all.expect(t, lineSnapshot)
	must(t, "new", "life")
	if l := all.expect(t, lineSnapshot)[0]; l.m["reason"] != "crews_changed" {
		t.Errorf("--all: %s", l.raw)
	}
	must(t, "up", "dev")
	if l := all.expect(t, lineTeamStarted)[0]; l.m["crew"] != "work" {
		t.Errorf("--all team line: %s", l.raw)
	}
}

// Review of fix round 5 (S1): a crew moved away between a round's two
// reads of it is gone, never only changed.
func TestTheStreamSeesACrewMovedBetweenItsReadsAsGone(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	fastWatch(t)
	t.Setenv("CADREI_HOME", c)
	// Set before the stream starts, and cleared after it ends.
	var once sync.Once
	crewReadsForTests = func() { once.Do(func() { os.Rename(c, home+"/away") }) }
	t.Cleanup(func() { crewReadsForTests = nil })
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	if l := s.expect(t, lineSnapshot)[0]; l.m["reason"] != "crew_gone" {
		t.Errorf("moved between the reads: %s", l.raw)
	}
}

// Acceptance 121: a heartbeat after the quiet time, and a periodic
// snapshot.
func TestTheStreamBeatsAndRefreshes(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	watchTiming.periodic = 1500 * time.Millisecond
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	if l := s.next(t, 2*time.Second); l.typ() != lineHeartbeat || len(l.m) != 3 {
		t.Errorf("heartbeat: %s", l.raw)
	}
	for {
		l := s.next(t, 3*time.Second)
		if l.typ() == lineSnapshot {
			if l.m["reason"] != "periodic" {
				t.Errorf("periodic: %s", l.raw)
			}
			break
		}
	}
}

// T-W4: a round more than the late time behind gives one woke snapshot,
// and no burst of heartbeats.
func TestTheStreamWakesUp(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	watchTiming.late = 300 * time.Millisecond
	var once sync.Once
	stall := make(chan struct{})
	watchStall = func() {
		select {
		case <-stall:
			once.Do(func() { time.Sleep(500 * time.Millisecond) })
		default:
		}
	}
	t.Cleanup(func() { watchStall = nil })
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	close(stall)
	l := s.next(t, 3*time.Second)
	if l.typ() != lineSnapshot || l.m["reason"] != "woke" {
		t.Errorf("after the stall: %s", l.raw)
	}
}

// T-W5: a state file that doesn't parse keeps the last value, with no
// line.
func TestTheStreamKeepsAStateItCantRead(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"Stop"}`)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	os.WriteFile(c+"/.claude/build/state/work-dev-engineer.json", []byte("{half"), 0o644)
	s.quiet(t, 400*time.Millisecond)
}

// helperArgs runs this test binary as cadrei with args, for what needs a
// process of its own: signals, a closed pipe, its CPU time.
func helperCmd(fast bool, args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperCadrei$")
	cmd.Env = append(os.Environ(), "CADREI_HELPER_ARGS="+strings.Join(args, "\x1f"))
	if fast {
		cmd.Env = append(cmd.Env, "CADREI_HELPER_FAST=1")
	}
	return cmd
}

// TestHelperCadrei is cadrei itself, when a test runs this binary with
// CADREI_HELPER_ARGS.
func TestHelperCadrei(t *testing.T) {
	args := os.Getenv("CADREI_HELPER_ARGS")
	if args == "" {
		t.Skip("only as a helper")
	}
	if os.Getenv("CADREI_HELPER_FAST") != "" {
		watchTiming = watchTimes{round: 20 * time.Millisecond, tmux: 40 * time.Millisecond, projects: 80 * time.Millisecond,
			heartbeat: 600 * time.Millisecond, periodic: time.Hour, late: time.Minute}
	}
	argv := strings.Split(args, "\x1f")
	if args == "shim" {
		// A cadrei on PATH for the docs' examples: the arguments after --.
		argv = flag.Args()
	}
	os.Exit(run(argv, os.Stdin, os.Stdout, os.Stderr))
}

var _ = filepath.Join

// Audit qual-tests-05: a 0.1.x team that starts while the stream runs is a
// team_started line with legacy true, and the picture stays the same as
// ls --json, which counts it as the default crew's.
func TestTheStreamFollowsALegacyTeam(t *testing.T) {
	home := sandbox(t)
	socket := withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	fastWatch(t)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	var p picture
	p.apply(t, s.expect(t, lineSnapshot)[0])
	tmuxIn(socket, "new-session", "-d", "-s", "cadre-dev", "-n", "pm", "sleep", "300")
	l := s.expect(t, lineTeamStarted)[0]
	if l.m["legacy"] != true {
		t.Errorf("team_started: %s", l.raw)
	}
	p.apply(t, l)
	s.settle(t, &p, lineMemberState)
	p.matchesLs(t)
}

// Review of fix round 4 (S1): a member with no state file (unknown) whose
// transcript appears after the stream saw it is still read, so a safeguard
// stop shows at once, not at the next periodic snapshot.
func TestTheStreamSeesAStopOfAMemberWithNoState(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	os.Remove(c + "/.claude/build/state/work-dev-engineer.json")
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	time.Sleep(200 * time.Millisecond)
	placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", nil)
	t.Setenv("CADREI_HOME", c)
	if l := s.expect(t, lineMemberState)[0]; l.m["member"].(map[string]any)["state"] != "stopped_by_safeguards" {
		t.Errorf("member_state: %s", l.raw)
	}
}

// Review of fix round 5 (S1): a member whose state file names no
// transcript, whatever its state, still finds the one its record leads to.
func TestTheStreamFindsATranscriptTheStateDoesntName(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	if l := s.expect(t, lineSnapshot)[0]; !strings.Contains(l.raw, `"working"`) {
		t.Fatalf("snapshot: %s", l.raw)
	}
	time.Sleep(200 * time.Millisecond)
	placeTranscript(t, c, "work-dev-engineer", "safeguard-stuck.jsonl", nil)
	t.Setenv("CADREI_HOME", c)
	if l := s.expect(t, lineMemberState)[0]; l.m["member"].(map[string]any)["state"] != "stopped_by_safeguards" {
		t.Errorf("member_state: %s", l.raw)
	}
}

// Review of fix round 4 (perfbugs N6): a working member whose transcript
// stays as it is gets its no-activity note when its 10 minutes are up,
// with nothing else changing: the stream keeps that deadline.
func TestTheStreamKeepsTheQuietDeadline(t *testing.T) {
	c := teamOf(t)
	fastWatch(t)
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(transcript, []byte("{}\n"), 0o600)
	almost := time.Now().Add(-quietAfter + 700*time.Millisecond)
	os.Chtimes(transcript, almost, almost)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit","transcript_path":"`+transcript+`"}`)
	t.Setenv("CADREI_HOME", c)
	s := startWatch(t)
	if l := s.expect(t, lineSnapshot)[0]; strings.Contains(l.raw, "no_activity_since") {
		t.Fatalf("quiet already: %s", l.raw)
	}
	if l := s.expect(t, lineMemberState)[0]; l.m["member"].(map[string]any)["no_activity_since"] == nil {
		t.Errorf("member_state: %s", l.raw)
	}
}

// PR #40's CI: a member stopped between the stream's tmux listing and its
// read of the member's state file is member_stopped, never unknown first:
// a gone state file waits for a listing taken after it.
func TestAMemberStoppedMidRoundIsOnlyStopped(t *testing.T) {
	c := teamOf(t)
	socket := os.Getenv("CADREI_TMUX_SOCKET")
	fastWatch(t)
	watchTiming.tmux = watchTiming.round // every round lists tmux first
	t.Setenv("CADREI_HOME", c)
	var armed atomic.Bool
	var once sync.Once
	// Set before the stream starts, and cleared after it ends.
	crewReadsForTests = func() {
		if armed.Load() {
			once.Do(func() {
				tmuxIn(socket, "kill-window", "-t", "=cadrei-work-dev:reviewer")
				os.Remove(c + "/.claude/build/state/work-dev-reviewer.json")
			})
		}
	}
	t.Cleanup(func() { crewReadsForTests = nil })
	s := startWatch(t)
	s.expect(t, lineSnapshot)
	armed.Store(true)
	for {
		l := s.next(t, 5*time.Second)
		if l.typ() == lineHeartbeat {
			continue
		}
		if l.typ() != lineMemberStopped {
			t.Fatalf("before member_stopped: %s", l.raw)
		}
		break
	}
}
