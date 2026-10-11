//go:build !windows && cadreitest

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/runtime/fake"
)

// fakeCadrei makes a cadrei whose sessions run on the fake runtime (a test
// build's CADREI_TEST_RUNTIME), and a stub for the fake to launch that
// records its arguments and environment.
func fakeCadrei(t *testing.T) string {
	t.Helper()
	home := sandbox(t)
	withTmux(t, home)
	must(t, "init", "work")
	t.Setenv("CADREI_TEST_RUNTIME", "fake")
	stub := filepath.Join(home, "fake-agent")
	writeStub(t, stub, "#!/bin/sh\n{ printf '%s\\n' \"$@\"; env; } > \""+home+"/fake-ran\"\nexec sleep 300\n")
	t.Setenv("CADREI_FAKE_BIN", stub)
	t.Cleanup(func() { fake.Off = map[string]bool{} })
	return home
}

// What runs in tmux is what the runtime's Launch built.
func TestUpRunsWhatLaunchBuilt(t *testing.T) {
	home := fakeCadrei(t)
	out := must(t, "up", "dev/engineer")
	if !strings.Contains(out, "+ Started the engineer") {
		t.Fatalf("up: %q", out)
	}
	var ran string
	for i := 0; i < 100 && !strings.Contains(ran, "CADREI_FAKE_LAUNCHED"); i++ {
		time.Sleep(50 * time.Millisecond)
		b, _ := os.ReadFile(home + "/fake-ran")
		ran = string(b)
	}
	for _, want := range []string{"--fake-name\nwork-dev-engineer\n", "--fake-mode\ndefault\n",
		"--fake-prompt\n" + home + "/.cadrei/work/.fake/build/dev-engineer.md\n", "--fake-grants\nfake-grants\n",
		"CADREI_FAKE_LAUNCHED=work-dev-engineer", "CADREI_HOME=" + home + "/.cadrei/work", "CADREI_MEMBER=work-dev-engineer"} {
		if !strings.Contains(ran, want) {
			t.Errorf("the fake ran without %q:\n%s", want, ran)
		}
	}
	if _, err := os.Stat(home + "/.cadrei/work/.claude/member-settings.json"); err == nil {
		t.Error("a member on the fake runtime made Claude Code's settings file")
	}
}

// A runtime that cannot enforce the fixed denies, has no messaging, or
// lacks the permission mode is refused, with a message.
func TestUpRefusesAnUnfitRuntime(t *testing.T) {
	fakeCadrei(t)
	for _, tc := range []struct{ off, want string }{
		{"FixedDenies", "cannot enforce cadrei's fixed denies"},
		{"Messaging", "has no way to message the orchestrator"},
	} {
		fake.Off = map[string]bool{tc.off: true}
		code, _, errOut := call("up", "dev/engineer")
		if code == 0 || !strings.Contains(flat(errOut), "the engineer didn't start: runtime fake "+tc.want) {
			t.Errorf("%s off: %d %q", tc.off, code, errOut)
		}
	}
	fake.Off = map[string]bool{}
	t.Setenv("CADREI_PERMISSION_MODE", "auto")
	if code, _, errOut := call("up", "dev/engineer"); code == 0 || !strings.Contains(flat(errOut), "runtime fake has no permission mode auto") {
		t.Errorf("an unknown mode: %d %q", code, errOut)
	}
	// Capabilities can also come from the environment, for the smoke test.
	t.Setenv("CADREI_PERMISSION_MODE", "")
	t.Setenv("CADREI_FAKE_OFF", "Messaging")
	if code, _, errOut := call("up", "dev/engineer"); code == 0 || !strings.Contains(flat(errOut), "has no way to message") {
		t.Errorf("CADREI_FAKE_OFF: %d %q", code, errOut)
	}
}

// ls says what keeps every member from starting.
func TestLsListsWhatKeepsMembersFromStarting(t *testing.T) {
	fakeCadrei(t)
	var st cadreiStatus
	if err := json.Unmarshal([]byte(must(t, "ls", "--json")), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Problems) != 0 {
		t.Errorf("problems with a fit runtime: %v", st.Problems)
	}
	fake.Off = map[string]bool{"Messaging": true}
	if out := must(t, "ls"); !strings.Contains(out, "! Runtime fake has no way to message the orchestrator") {
		t.Errorf("ls with messaging off:\n%s", out)
	}
}

// A member resumes its last conversation on the next start, and starts a
// new one, saying why, when asked or when it cannot resume.
func TestUpResumesConversations(t *testing.T) {
	home := fakeCadrei(t)
	// Up to 20 seconds on a busy machine; most runs take a moment.
	ran := func() string {
		for i := 0; i < 400; i++ {
			if b, _ := os.ReadFile(home + "/fake-ran"); len(b) > 0 {
				return string(b)
			}
			time.Sleep(50 * time.Millisecond)
		}
		return ""
	}
	restart := func(args ...string) string {
		call("stop", "dev/engineer")
		os.Remove(home + "/fake-ran")
		return must(t, append([]string{"up", "dev/engineer"}, args...)...)
	}
	out := must(t, "up", "dev/engineer")
	if out != "+ Started the engineer (fresh chat)\n" || !strings.Contains(ran(), "--fake-session\nfake-session\n") {
		t.Errorf("first start: %q", out)
	}
	record := home + "/.cadrei/work/.fake/build/sessions/work-dev-engineer.json"
	if !strings.Contains(readFile(t, record), `"id":"fake-session"`) {
		t.Errorf("record %q", readFile(t, record))
	}
	if out := restart(); !strings.Contains(out, "(picked up where it left off)") || !strings.Contains(ran(), "--fake-resume\nfake-session\n") {
		t.Errorf("second start: %q", out)
	}
	t.Setenv("CADREI_FAKE_GONE", "fake-session")
	if out := restart(); out != "+ Started the engineer (fresh chat)\n  The last one couldn't be found.\n" {
		t.Errorf("a lost conversation: %q", out)
	}
	t.Setenv("CADREI_FAKE_GONE", "")
	if out := restart("--fresh"); out != "+ Started the engineer (fresh chat)\n" {
		t.Errorf("--fresh: %q", out)
	}
	call("stop", "dev/engineer", "--fresh")
	if _, err := os.Stat(record); err == nil {
		t.Error("stop --fresh kept the record")
	}
	os.Remove(home + "/fake-ran")
	if out := must(t, "up", "dev/engineer"); out != "+ Started the engineer (fresh chat)\n" {
		t.Errorf("after stop --fresh: %q", out)
	}
}

// The orchestrator resumes too; when the runtime will not resume, a new
// conversation starts at once.
func TestTheOrchestratorResumes(t *testing.T) {
	home := fakeCadrei(t)
	// It exits at once: with an error when asked to resume, as a runtime
	// that lost the conversation does.
	writeStub(t, home+"/fake-agent", "#!/bin/sh\necho \"$@\" >> \""+home+"/orch-runs\"\ncase \"$*\" in *'--fake-resume fake-session'*) exit 1 ;; esac\nexit 0\n")
	// A first chat and a resumed one need no note; a fresh chat says why.
	if out := must(t); out != "" {
		t.Errorf("first: %q", out)
	}
	if out := must(t); out != "  Starting a fresh chat. The last one wouldn't reopen.\n" {
		t.Errorf("second: %q", out)
	}
	if runs := readFile(t, home+"/orch-runs"); strings.Count(runs, "--fake-name work-orchestrator") != 3 {
		t.Errorf("runs:\n%s", runs)
	}
	// A resumed orchestrator that got going (it wrote to its conversation)
	// and then quit at once ends there, with its status: no new
	// conversation opens.
	transcripts := home + "/.cadrei/work/.fake-transcripts"
	writeStub(t, home+"/fake-agent", "#!/bin/sh\necho \"$@\" >> \""+home+"/orch-runs\"\nmkdir -p '"+transcripts+"' && touch '"+transcripts+"/fake-session'\nexit 3\n")
	code, out, _ := call()
	if code != 3 || strings.Contains(out, "resuming failed") {
		t.Errorf("a quick quit: %d %q", code, out)
	}
	if runs := readFile(t, home+"/orch-runs"); strings.Count(runs, "--fake-name work-orchestrator") != 4 {
		t.Errorf("a quick quit ran again:\n%s", runs)
	}
}

// The orchestrator resumes only a conversation cadrei itself started: a
// record that names another id is not resumed.
func TestTheOrchestratorResumesOnlyWhatCadreiIssued(t *testing.T) {
	home := fakeCadrei(t)
	writeStub(t, home+"/fake-agent", "#!/bin/sh\necho \"$@\" >> \""+home+"/orch-runs\"\nexit 0\n")
	must(t)
	c := home + "/.cadrei/work"
	record := c + "/.fake/build/sessions/work-orchestrator.json"
	if _, err := os.Stat(record); err != nil {
		matches, _ := filepath.Glob(c + "/*/build/sessions/work-orchestrator.json")
		if len(matches) != 1 {
			t.Fatalf("no orchestrator record: %v", matches)
		}
		record = matches[0]
	}
	os.WriteFile(record, []byte(`{"id":"planted","dir":"`+c+`"}`), 0o600)
	if out := must(t); out != "  Starting a fresh chat. The last one wasn't opened by cadrei.\n" {
		t.Errorf("planted record: %q", out)
	}
	if strings.Contains(readFile(t, home+"/orch-runs"), "--fake-resume planted") {
		t.Error("the planted conversation was resumed")
	}
	// The one cadrei issued is resumed.
	if out := must(t); out != "" || !strings.Contains(readFile(t, home+"/orch-runs"), "--fake-resume") {
		t.Errorf("issued: %q", out)
	}
}

// A transcript counts as written when its size changed, even when its time
// did not (filesystems with 1 or 2 second times).
func TestUntouchedReadsTheSizeToo(t *testing.T) {
	fakeCadrei(t)
	rt, err := runtime.Get("fake")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, ".fake-transcripts", "id")
	if !untouched(rt, "id", dir, time.Time{}, 0) {
		t.Error("a missing transcript was written")
	}
	os.MkdirAll(filepath.Dir(f), 0o755)
	os.WriteFile(f, []byte("{}\n"), 0o600)
	when := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	os.Chtimes(f, when, when)
	mod, size, _ := rt.Sessions().Transcript("id", dir)
	if !untouched(rt, "id", dir, mod, size) {
		t.Error("an unchanged transcript was written")
	}
	os.WriteFile(f, []byte("{}\n{}\n"), 0o600)
	os.Chtimes(f, when, when)
	if untouched(rt, "id", dir, mod, size) {
		t.Error("a transcript that grew in the same tick reads as untouched")
	}
}
