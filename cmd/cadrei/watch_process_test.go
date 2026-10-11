//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperProc is cadrei running as a process of its own.
type helperProc struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// startHelper runs cadrei as a process of its own, its stdout on a pipe
// that ends when it does. One goroutine waits for it.
func startHelper(t *testing.T, fast bool, env []string, args ...string) (*helperProc, *bufio.Reader, io.ReadCloser, *lockedBuffer) {
	t.Helper()
	cmd := helperCmd(fast, args...)
	cmd.Env = append(cmd.Env, env...)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = w
	errOut := &lockedBuffer{}
	cmd.Stderr = errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	p := &helperProc{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-p.done
		r.Close()
	})
	return p, bufio.NewReader(r), r, errOut
}

// waitExit waits for a helper to end within d, and gives its exit code.
func waitExit(t *testing.T, p *helperProc, d time.Duration) int {
	t.Helper()
	select {
	case <-p.done:
		if p.err == nil {
			return 0
		}
		if ee, ok := p.err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		t.Fatal(p.err)
	case <-time.After(d):
		t.Fatalf("still running after %s", d)
	}
	return -1
}

// Acceptance 122: SIGINT, SIGTERM and SIGHUP end the stream with 0 and a
// whole last line.
func TestTheStreamEndsOnASignal(t *testing.T) {
	c := teamOf(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		cmd, r, _, errOut := startHelper(t, true, []string{"CADREI_HOME=" + c}, "ls", "--json", "--watch")
		if _, err := r.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		cmd.cmd.Process.Signal(sig)
		rest, _ := io.ReadAll(r)
		if code := waitExit(t, cmd, 5*time.Second); code != 0 || errOut.Len() > 0 {
			t.Errorf("%v: exit %d, %q", sig, code, errOut.String())
		}
		if len(rest) > 0 && !bytes.HasSuffix(rest, []byte("\n")) {
			t.Errorf("%v: a half line: %q", sig, rest)
		}
	}
}

// Acceptance 122 and T-W2, T-W3: a reader that leaves (cadrei ls --json
// --watch | head -1) ends the stream with 0, nothing on stderr, within a
// heartbeat, at the real timings.
func TestTheStreamEndsWhenItsReaderLeaves(t *testing.T) {
	if raceOn {
		t.Skip("real timings: the race detector slows the program down")
	}
	c := teamOf(t)
	cmd, r, out, errOut := startHelper(t, false, []string{"CADREI_HOME=" + c}, "ls", "--json", "--watch")
	if _, err := r.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	out.Close()
	if code := waitExit(t, cmd, 16*time.Second); code != 0 || errOut.Len() > 0 {
		t.Errorf("exit %d, %q", code, errOut.String())
	}
}

// failingWriter fails every write, as a full disk does.
type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// Acceptance 122: any other write error ends it with 1 and the message;
// --watch without --json is refused.
func TestTheStreamsOtherEnds(t *testing.T) {
	c := teamOf(t)
	t.Setenv("CADREI_HOME", c)
	var errOut bytes.Buffer
	code := run([]string{"ls", "--json", "--watch"}, strings.NewReader(""), failingWriter{syscall.ENOSPC}, &errOut)
	if code != 1 || !strings.Contains(flat(errOut.String()), "couldn't write the stream: no space left on device.") {
		t.Errorf("a full disk: %d %q", code, errOut.String())
	}
	if _, err := os.Stat("/dev/full"); err == nil {
		cmd := helperCmd(true, "ls", "--json", "--watch")
		full, _ := os.OpenFile("/dev/full", os.O_WRONLY, 0)
		defer full.Close()
		cmd.Stdout = full
		var e bytes.Buffer
		cmd.Stderr = &e
		if err := cmd.Run(); err == nil || !strings.Contains(e.String(), "write the stream") {
			t.Errorf("/dev/full: %v %q", err, e.String())
		}
	}
	code, out, stderr := call("ls", "--watch")
	if code != 1 || out != "" || !strings.Contains(flat(stderr), "--watch only works with --json. It's for programs that read cadrei's state.") ||
		!strings.Contains(stderr, "To look now, run cadrei ls.") {
		t.Errorf("--watch alone: %d %q %q", code, out, stderr)
	}
}

// loggingShims puts a tmux and a claude in front of PATH that log each
// call made with the tag, then run the real tmux (claude stays the stub).
func loggingShims(t *testing.T, home string) (tmuxLog, claudeLog string) {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	dir := filepath.Join(home, "shims")
	os.MkdirAll(dir, 0o755)
	tmuxLog, claudeLog = filepath.Join(home, "tmux.log"), filepath.Join(home, "claude.log")
	writeStub(t, filepath.Join(dir, "tmux"), "#!/bin/sh\n[ -n \"$WATCH_TAG\" ] && echo \"$*\" >> '"+tmuxLog+"'\nexec '"+real+"' \"$@\"\n")
	stub, _ := os.ReadFile(filepath.Join(home, "bin", "claude"))
	writeStub(t, filepath.Join(dir, "claude"), "#!/bin/sh\n[ -n \"$WATCH_TAG\" ] && echo \"$*\" >> '"+claudeLog+"'\n"+string(bytes.TrimPrefix(stub, []byte("#!/bin/sh\n"))))
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return tmuxLog, claudeLog
}

// files lists every file under dir with its time and size, but those in
// skip folders.
func files(dir string, skip ...string) map[string]string {
	out := map[string]string{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		for _, s := range skip {
			if strings.HasPrefix(p, s) {
				return nil
			}
		}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			out[p] = fmt.Sprint(info.ModTime().UnixNano(), info.Size())
		}
		return nil
	})
	return out
}

// Acceptance 123: a stream with members changing state runs no claude,
// calls tmux only for list-windows after its first snapshot, never reads
// a pane or sends keys, and writes nothing outside the state folder.
func TestTheStreamOnlyLooks(t *testing.T) {
	c := teamOf(t)
	home := filepath.Dir(filepath.Dir(c))
	tmuxLog, claudeLog := loggingShims(t, home)
	build := c + "/.claude/build"
	before := files(home, build+"/state", tmuxLog, claudeLog, home+"/.claude.json", home+"/args-")
	cmd, r, _, _ := startHelper(t, true, []string{"CADREI_HOME=" + c, "WATCH_TAG=1"}, "ls", "--json", "--watch")
	if _, err := r.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	logged, _ := os.ReadFile(tmuxLog)
	atStart := strings.Count(string(logged), "\n")
	go io.Copy(io.Discard, r)
	for i := 0; i < 4; i++ {
		for _, p := range []string{`{"hook_event_name":"UserPromptSubmit"}`, `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`, `{"hook_event_name":"Stop"}`} {
			feed(t, c, "work-dev-engineer", p)
			time.Sleep(250 * time.Millisecond)
		}
	}
	cmd.cmd.Process.Signal(syscall.SIGINT)
	waitExit(t, cmd, 5*time.Second)
	logged, _ = os.ReadFile(tmuxLog)
	var calls []string
	for _, l := range strings.Split(strings.TrimSpace(string(logged)), "\n") {
		calls = append(calls, subcommand(l))
	}
	for _, call := range calls {
		if call == "capture-pane" || call == "send-keys" || strings.HasPrefix(call, "new-") || strings.HasPrefix(call, "kill-") || strings.HasPrefix(call, "set-") {
			t.Errorf("the stream ran tmux %s", call)
		}
	}
	for _, call := range calls[atStart:] {
		if call != "list-windows" {
			t.Errorf("after its first snapshot, tmux %s", call)
		}
	}
	if len(calls)-atStart < 10 {
		t.Errorf("only %d tmux calls after the snapshot", len(calls)-atStart)
	}
	if raw, _ := os.ReadFile(claudeLog); len(raw) > 0 {
		t.Errorf("the stream ran claude: %s", raw)
	}
	after := files(home, build+"/state", tmuxLog, claudeLog, home+"/.claude.json", home+"/args-")
	for p, v := range after {
		if before[p] != v {
			t.Errorf("written: %s", p)
		}
	}
}

// Acceptance 124: with 10 members running, all idle, the stream stays
// under 0.5% of one core and 30 MB over 20 seconds, at the real timings.
// T-W1's 10 minutes on macOS and Linux are measured by hand.
func TestTheStreamIdlesCheaply(t *testing.T) {
	if testing.Short() || raceOn {
		t.Skip("takes 20 seconds, and the race detector's program isn't the real one")
	}
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	for i := 0; i < 10; i++ {
		os.WriteFile(fmt.Sprintf("%s/members/dev/m%d.md", c, i), []byte("# Member\n"), 0o644)
	}
	os.Remove(c + "/members/dev/engineer.md")
	os.Remove(c + "/members/dev/reviewer.md")
	must(t, "up", "dev")
	// As Claude Code's hooks do, each names its transcript: a member with
	// none is looked for every round.
	for i := 0; i < 10; i++ {
		transcript := fmt.Sprintf("%s/t%d.jsonl", t.TempDir(), i)
		os.WriteFile(transcript, []byte("{}\n"), 0o600)
		feed(t, c, fmt.Sprintf("work-dev-m%d", i), `{"hook_event_name":"Stop","transcript_path":"`+transcript+`"}`)
	}
	cmd, r, _, _ := startHelper(t, false, []string{"CADREI_HOME=" + c}, "ls", "--json", "--watch")
	r.ReadString('\n')
	go io.Copy(io.Discard, r)
	// The idle time alone: from after the start (the process and its
	// first snapshot), by the process's own CPU time.
	time.Sleep(2 * time.Second)
	start := cpuOf(t, cmd.cmd.Process.Pid)
	time.Sleep(20 * time.Second)
	idle := cpuOf(t, cmd.cmd.Process.Pid) - start
	cmd.cmd.Process.Signal(syscall.SIGINT)
	waitExit(t, cmd, 5*time.Second)
	ru := cmd.cmd.ProcessState.SysUsage().(*syscall.Rusage)
	rss := ru.Maxrss
	if runtime.GOOS == "linux" {
		rss *= 1024
	}
	t.Logf("idle CPU %s over 20 s (%.2f%%), max RSS %.1f MB, %d voluntary and %d involuntary context switches in all", idle, 100*idle.Seconds()/20, float64(rss)/(1<<20), ru.Nvcsw, ru.Nivcsw)
	if idle > 100*time.Millisecond {
		t.Errorf("idle CPU %s, over 0.5%% of 20 s", idle)
	}
	// Acceptance 124's wakeups, loosely: over the stream's whole life (its
	// start, then about 22 s at 1 round a second and a tmux listing every
	// 2), it switches about 1,200 to 1,900 times, under load too, which
	// counts the runtime's timers (macOS counts them as involuntary). A
	// timer of its own 20 or 50 times a second takes it to 4,000 to 6,000.
	if n := ru.Nvcsw + ru.Nivcsw; n > 3000 {
		t.Errorf("%d context switches, over 3,000: something wakes the stream far more than twice a second", n)
	}
	if rss > 30<<20 {
		t.Errorf("max RSS %d bytes", rss)
	}
}

// Acceptance 124: no more than 2 wakeups a second, at the real timings.
func TestTheStreamWakesTwiceASecondAtMost(t *testing.T) {
	c := teamOf(t)
	t.Setenv("CADREI_HOME", c)
	// The real rounds; a short heartbeat only so the stream ends soon.
	old := watchTiming
	watchTiming.heartbeat = time.Second
	t.Cleanup(func() { watchTiming = old })
	before := watchWakeups.Load()
	s := startWatch(t)
	s.next(t, 5*time.Second)
	time.Sleep(3 * time.Second)
	if n := watchWakeups.Load() - before; n > 7 {
		t.Errorf("%d wakeups in 3 s", n)
	}
}

// Acceptance 125: no listening socket or port, no --watch for the
// orchestrator or in the skill.
func TestTheStreamOpensNoPort(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("no lsof")
	}
	c := teamOf(t)
	cmd, r, _, _ := startHelper(t, true, []string{"CADREI_HOME=" + c}, "ls", "--json", "--watch")
	r.ReadString('\n')
	go io.Copy(io.Discard, r)
	out, _ := exec.Command("lsof", "-a", "-p", fmt.Sprint(cmd.cmd.Process.Pid), "-i").Output()
	if len(bytes.TrimSpace(out)) > 0 {
		t.Errorf("open network sockets:\n%s", out)
	}
	unix, _ := exec.Command("lsof", "-a", "-p", fmt.Sprint(cmd.cmd.Process.Pid), "-U").Output()
	if bytes.Contains(unix, []byte("LISTEN")) {
		t.Errorf("a listening socket:\n%s", unix)
	}
	cmd.cmd.Process.Signal(syscall.SIGINT)
	waitExit(t, cmd, 5*time.Second)
	for _, r := range readOnly {
		if strings.Contains(r, "--watch") {
			t.Errorf("the orchestrator may run %s", r)
		}
	}
	if len(readOnly) != 8 {
		t.Errorf("%d pre-allowed commands", len(readOnly))
	}
	skill, err := os.ReadFile(filepath.Join(repoRoot, "skills", "cadrei", "SKILL.md"))
	if err != nil || bytes.Contains(skill, []byte("--watch")) {
		t.Error("the skill mentions --watch")
	}
}

// cadreiShim puts a cadrei on PATH that is this test binary, for the docs'
// examples.
func cadreiShim(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "cadrei-bin")
	os.MkdirAll(dir, 0o755)
	writeStub(t, filepath.Join(dir, "cadrei"), "#!/bin/sh\nCADREI_HELPER_ARGS=shim CADREI_HELPER_FAST=1 exec '"+os.Args[0]+"' -test.run='^TestHelperCadrei$' -- \"$@\"\n")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// Acceptance 126: the jq line and the Python script in docs/tools.md run
// as shown.
func TestTheToolsPageExamplesRun(t *testing.T) {
	c := teamOf(t)
	home := filepath.Dir(filepath.Dir(c))
	cadreiShim(t, home)
	t.Setenv("CADREI_HOME", c)
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	t.Setenv("CADREI_HOME", c)
	page, err := os.ReadFile(filepath.Join(repoRoot, "docs", "tools.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("jq"); err == nil {
		start := strings.Index(string(page), "cadrei ls --json | jq")
		jq := string(page)[start:]
		jq = jq[:strings.Index(jq, "\n")]
		out, err := exec.Command("sh", "-c", jq).Output()
		if err != nil || !strings.Contains(string(out), "engineer: working\n") || !strings.Contains(string(out), "reviewer: starting\n") {
			t.Errorf("jq: %v %q", err, out)
		}
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return
	}
	start := strings.Index(string(page), "```python\n")
	script := string(page)[start+len("```python\n"):]
	script = script[:strings.Index(script, "```")]
	file := filepath.Join(home, "needs.py")
	os.WriteFile(file, []byte(script), 0o644)
	// One member waits before the script starts, one after.
	feed(t, c, "work-dev-engineer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
	py := exec.Command("python3", "-I", "-u", file) // -u: its print reaches the pipe at once
	out, _ := py.StdoutPipe()
	py.Start()
	t.Cleanup(func() { py.Process.Kill(); py.Wait() })
	lines := make(chan string, 2)
	go func() {
		r := bufio.NewReader(out)
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			lines <- l
		}
	}()
	for i, want := range []string{"engineer in dev needs you\n", "reviewer in dev needs you\n"} {
		if i == 1 {
			feed(t, c, "work-dev-reviewer", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`)
		}
		select {
		case l := <-lines:
			if l != want {
				t.Errorf("the script printed %q, want %q", l, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the script printed nothing, want %q", want)
		}
	}
}

// subcommand is the tmux command in a logged call, after tmux's own flags.
func subcommand(line string) string {
	f := strings.Fields(line)
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case "-u", "-2":
		case "-L", "-S", "-f":
			i++
		default:
			return f[i]
		}
	}
	return ""
}

// repoRoot is the repository's top folder, found when the tests start
// (a sandbox changes the working folder).
var repoRoot = func() string {
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..")
}()

// cpuOf is a process's CPU time so far, as ps has it, its own and that of
// the children it waited for (the tmux calls).
func cpuOf(t *testing.T, pid int) time.Duration {
	t.Helper()
	out, err := exec.Command("ps", "-o", "time=", "-p", fmt.Sprint(pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	// [[dd-]hh:]mm:ss[.ss]
	f := strings.TrimSpace(string(out))
	var total time.Duration
	parts := strings.Split(f, ":")
	for i, p := range parts {
		unit := time.Second
		switch len(parts) - 1 - i {
		case 1:
			unit = time.Minute
		case 2:
			unit = time.Hour
		}
		if d, err := time.ParseDuration(p + "s"); err == nil {
			total += time.Duration(float64(d) * float64(unit) / float64(time.Second))
		}
	}
	return total
}

// T-W6: a big crew's snapshot (30 members running, 20 projects, some
// found in the projects folder): how big and how long to build.
func TestABigCrewsSnapshot(t *testing.T) {
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	c := home + "/.cadrei/work"
	for _, team := range []string{"a", "b", "c"} {
		os.MkdirAll(c+"/members/"+team, 0o755)
		for i := 0; i < 10; i++ {
			os.WriteFile(fmt.Sprintf("%s/members/%s/m%d.md", c, team, i), []byte("# Member\n"), 0o644)
		}
	}
	var reg strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&reg, "p%d:\n  repo: https://example.com/me/p%d.git\n", i, i)
		if i%2 == 0 {
			dir := fmt.Sprintf("%s/Developer/p%d", home, i)
			os.MkdirAll(dir, 0o755)
			exec.Command("git", "-C", dir, "init", "-q").Run()
			exec.Command("git", "-C", dir, "remote", "add", "origin", fmt.Sprintf("https://example.com/me/p%d.git", i)).Run()
		}
	}
	register(t, home, "work", reg.String())
	must(t, "project", "dir", "~/Developer")
	for _, team := range []string{"a", "b", "c"} {
		must(t, "up", team)
	}
	t.Setenv("CADREI_HOME", c)
	start := time.Now()
	out := must(t, "ls", "--json")
	took := time.Since(start)
	t.Logf("a snapshot of 30 members and 20 projects: %d bytes as ls --json prints it, built in %s", len(out), took)
	if took > 5*time.Second {
		t.Errorf("took %s", took)
	}
}

// watchDefaults are the stream's timings as built, before a test changes
// them.
var watchDefaults = watchTiming

// Audit qual-tests-10, acceptance 118 and 124: the stream's timings as
// built are 7.13.3's and 7.13.4's.
func TestTheStreamsTimingsAreTheSpecs(t *testing.T) {
	want := watchTimes{round: time.Second, tmux: 2 * time.Second, projects: 10 * time.Second,
		heartbeat: 15 * time.Second, periodic: 5 * time.Minute, late: 10 * time.Second}
	if watchDefaults != want {
		t.Errorf("timings %+v, want %+v", watchDefaults, want)
	}
}

// Audit qual-tests-10, acceptance 118: at the real timings, a member's new
// state is a line within about a second, and a team that starts within
// about two.
func TestTheStreamKeepsItsTimesForReal(t *testing.T) {
	if testing.Short() || raceOn {
		t.Skip("runs at the real timings, and the race detector slows it")
	}
	c := teamOf(t)
	_, r, _, _ := startHelper(t, false, []string{"CADREI_HOME=" + c}, "ls", "--json", "--watch")
	r.ReadString('\n') // the snapshot
	lines := make(chan string, 64)
	go func() {
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				close(lines)
				return
			}
			lines <- l
		}
	}()
	within := func(what, typ string, limit time.Duration, act func()) {
		t.Helper()
		start := time.Now()
		act()
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("%s: the stream ended", what)
				}
				if strings.Contains(l, `"type":"`+typ+`"`) {
					if took := time.Since(start); took > limit {
						t.Errorf("%s took %s", what, took)
					}
					return
				}
			case <-time.After(limit + 2*time.Second):
				t.Fatalf("%s: no %s line", what, typ)
			}
		}
	}
	within("a new state", lineMemberState, 1500*time.Millisecond, func() {
		feed(t, c, "work-dev-engineer", `{"hook_event_name":"UserPromptSubmit"}`)
	})
	os.MkdirAll(c+"/members/ops", 0o755)
	os.WriteFile(c+"/members/ops/sre.md", []byte("# Member\n"), 0o644)
	// The new team's member file is a crew change: let its snapshot pass.
	time.Sleep(2500 * time.Millisecond)
	for len(lines) > 0 {
		<-lines
	}
	t.Setenv("CADREI_HOME", c)
	within("a team's start", lineTeamStarted, 2500*time.Millisecond, func() { must(t, "up", "ops") })
}
