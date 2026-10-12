package session

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/testguard"
)

// private gives a test its own tmux server, stopped at the end.
func private(t *testing.T) Tmux {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	testguard.Check(t)
	tm := Tmux{Socket: fmt.Sprintf("cadrei-gotest-%d-%d", os.Getpid(), time.Now().UnixNano())}
	t.Cleanup(func() { tm.command("kill-server").Run() })
	return tm
}

// cadreiDir makes a cadrei folder with a team dev (pm, engineer) and returns
// it, and a stub claude that records its arguments and environment.
func cadreiDir(t *testing.T, name string) (string, string) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	c := filepath.Join(root, name)
	for _, r := range []string{"pm", "engineer"} {
		os.MkdirAll(filepath.Join(c, "members", "dev"), 0o755)
		os.WriteFile(filepath.Join(c, "members", "dev", r+".md"), []byte("# "+r+"\n"), 0o644)
	}
	stub := filepath.Join(root, "claude")
	os.WriteFile(stub, []byte("#!/bin/sh\n{ printf '%s\\n' \"$@\"; echo \"HOME=$CADREI_HOME\"; echo \"MEMBER=$CADREI_MEMBER\"; } > \"$0.$CADREI_MEMBER\"\nexec sleep 300\n"), 0o755)
	return c, stub
}

// stubRuntime runs a stub program in place of an agent CLI.
type stubRuntime struct{ bin string }

func (s stubRuntime) Name() string                            { return "stub" }
func (s stubRuntime) Title() string                           { return "Stub" }
func (s stubRuntime) Probe(context.Context) runtime.Probe     { return runtime.Probe{} }
func (s stubRuntime) Help() runtime.InstallHelp               { return runtime.InstallHelp{} }
func (s stubRuntime) Login() (runtime.Command, error)         { return runtime.Command{}, nil }
func (s stubRuntime) Model() string                           { return "Stub" }
func (s stubRuntime) ModelHint() string                       { return "a stub model" }
func (s stubRuntime) SessionVars() []string                   { return nil }
func (s stubRuntime) SetupVars() []string                     { return nil }
func (s stubRuntime) Caps() runtime.Capabilities              { return runtime.Capabilities{} }
func (s stubRuntime) Detect() (runtime.Install, error)        { return runtime.Install{Path: s.bin}, nil }
func (s stubRuntime) BuildDir(cadrei string) string           { return filepath.Join(cadrei, ".build") }
func (s stubRuntime) Permissions() runtime.PermissionOps      { return nil }
func (s stubRuntime) Trust() runtime.TrustOps                 { return nil }
func (s stubRuntime) Health(bool, []string) []runtime.Problem { return nil }
func (s stubRuntime) Instructions() runtime.InstructionOps    { return nil }
func (s stubRuntime) Hooks() runtime.HookOps                  { return nil }
func (s stubRuntime) Sessions() runtime.SessionOps            { return nil }
func (s stubRuntime) Launch(l runtime.LaunchSpec) (runtime.Command, error) {
	return runtime.Command{Argv: []string{s.bin, "--name", l.Name, "--mode", l.Mode, "--prompt", l.PromptFile}}, nil
}

func pick(bin string) func(string) (runtime.Runtime, string, error) {
	return func(string) (runtime.Runtime, string, error) { return stubRuntime{bin}, "", nil }
}

func up(tm Tmux, cadrei, stub, team, role, project string) (string, bool) {
	u := Up{Scope: Scope{Name: filepath.Base(cadrei), Path: cadrei, T: tm}, Team: team, Role: role, Project: project,
		Dir: DefaultDir(cadrei, team), Protocol: []byte("PROTOCOL\n"), Mode: "default", Pick: pick(stub), Wait: 300 * time.Millisecond}
	var out bytes.Buffer
	failed, _ := u.Start(&out)
	return out.String(), failed
}

func TestNames(t *testing.T) {
	if SessionName("work", Key("dev", "app")) != "cadrei-work-dev-app" || MemberName("work", Key("dev", ""), "pm") != "work-dev-pm" || LegacyName("dev-app") != "cadre-dev-app" {
		t.Error("names")
	}
	if Quote("it's a dir") != `'it'\''s a dir'` || Quote("/x/y.md") != "/x/y.md" || Quote("") != "''" {
		t.Error("Quote")
	}
}

func TestUpStartsMembersWithTheirCadrei(t *testing.T) {
	tm := private(t)
	c, stub := cadreiDir(t, "work")
	out, failed := up(tm, c, stub, "dev", "", "")
	if failed || !strings.Contains(out, "work-dev-engineer started in "+c+"/teams/dev") || !strings.Contains(out, "work-dev-pm started") {
		t.Fatalf("up: %v %q", failed, out)
	}
	s := "cadrei-work-dev"
	if tm.Option(s, "@cadrei_home") != c || tm.Option(s, "@cadrei_team") != "dev" || tm.Option(s, "@cadrei_project") != "" {
		t.Errorf("options: %q %q", tm.Option(s, "@cadrei_home"), tm.Option(s, "@cadrei_team"))
	}
	args := waitFile(t, stub+".work-dev-pm")
	for _, want := range []string{"--name\nwork-dev-pm\n", "--mode\ndefault\n", "HOME=" + c + "\n", "MEMBER=work-dev-pm\n"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("the member was started without %q:\n%s", want, args)
		}
	}
	prompt, _ := os.ReadFile(filepath.Join(c, ".build", "dev-pm.md"))
	if string(prompt) != "PROTOCOL\n\n# pm\n" {
		t.Errorf("prompt %q", prompt)
	}
	if out, _ := up(tm, c, stub, "dev", "pm", ""); !strings.Contains(out, "work-dev-pm already running") {
		t.Errorf("second up: %q", out)
	}
	scope := Scope{Name: "work", Path: c, T: tm}
	if r := scope.Running(); len(r) != 1 || r[0].Name != s {
		t.Errorf("Running: %+v", r)
	}
	if names := tm.MemberNames(scope.Running()[0]); strings.Join(names, ",") != "work-dev-engineer,work-dev-pm" {
		t.Errorf("MemberNames %v", names)
	}
	if res := scope.StopRole("dev", "", "pm"); !res.Stopped {
		t.Errorf("StopRole: %+v", res)
	}
	if res := scope.StopTeam("dev", ""); !res.Stopped {
		t.Errorf("StopTeam: %+v", res)
	}
	if res := scope.StopTeam("dev", ""); res.Stopped || res.Unmarked != "" || res.Instead != "" {
		t.Errorf("StopTeam again: %+v", res)
	}
}

// The wait is long so a loaded machine can't outlast it before tmux shows
// the pane dead (it failed once under four suites at once); a dead pane
// ends the check early, so it costs nothing here.
func TestAFailedStartIsReported(t *testing.T) {
	tm := private(t)
	c, _ := cadreiDir(t, "work")
	bad := filepath.Join(filepath.Dir(c), "bad")
	os.WriteFile(bad, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	tm.command("new-session", "-d", "-s", "keepalive", "sleep", "60").Run()
	u := Up{Scope: Scope{Name: "work", Path: c, T: tm}, Team: "dev", Role: "pm", Dir: c, Mode: "default", Pick: pick(bad), Wait: 20 * time.Second}
	var buf bytes.Buffer
	start := time.Now()
	failed, _ := u.Start(&buf)
	out := buf.String()
	if !failed || !strings.Contains(out, "work-dev-pm failed to start") || !strings.Contains(out, "CADREI_MEMBER=work-dev-pm") {
		t.Errorf("failed start: %v %q", failed, out)
	}
	// The dead pane ends the check, well before its 20 seconds (review
	// of fix round 6).
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("a dead pane was watched for %s", took)
	}
}

func TestSessionCollisions(t *testing.T) {
	tm := private(t)
	c, stub := cadreiDir(t, "b")
	other := filepath.Join(filepath.Dir(c), "a")
	scope := Scope{Name: "b", Path: c, T: tm}
	// Another cadrei holds the name.
	tm.command("new-session", "-d", "-s", "cadrei-b-dev", "sleep", "60").Run()
	tm.SetOption("cadrei-b-dev", "@cadrei_home", other)
	if err := scope.CheckSession("dev", ""); err == nil || !strings.Contains(err.Error(), "belongs to the crew a ("+other+"), not the crew b") {
		t.Errorf("another cadrei: %v", err)
	}
	// A session without a home holds it.
	tm.SetOption("cadrei-b-dev", "@cadrei_home", "")
	tm.command("set-option", "-u", "-t", "=cadrei-b-dev:", "@cadrei_home").Run()
	if err := scope.CheckSession("dev", ""); err == nil || !strings.Contains(err.Error(), "without cadrei's markers") {
		t.Errorf("no home: %v", err)
	}
	tm.KillSession("cadrei-b-dev")
	// Team dev with project x-y and team dev-x with project y join the same way.
	up(tm, c, stub, "dev", "pm", "x-y")
	if err := scope.CheckSession("dev-x", "y"); err == nil || !strings.Contains(err.Error(), "which gives the same session name") {
		t.Errorf("joined names: %v", err)
	}
	if err := scope.CheckSession("dev", "x-y"); err != nil {
		t.Errorf("its own session: %v", err)
	}
}

// The reviewer's exp32: names that join the same way in different tmux
// sessions. The tmux session check cannot see these; the Claude name check
// does.
func TestClaudeNamesCannotCollide(t *testing.T) {
	tm := private(t)
	c, stub := cadreiDir(t, "c")
	for _, r := range []string{"x-y"} {
		os.WriteFile(filepath.Join(c, "members", "dev", r+".md"), []byte("# "+r+"\n"), 0o644)
	}
	os.MkdirAll(filepath.Join(c, "members", "dev-x"), 0o755)
	os.WriteFile(filepath.Join(c, "members", "dev-x", "y.md"), []byte("# y\n"), 0o644)
	if out, failed := up(tm, c, stub, "dev", "x-y", ""); failed {
		t.Fatalf("first: %q", out)
	}
	out, failed := up(tm, c, stub, "dev-x", "y", "")
	if !failed || !strings.Contains(out, "the session name c-dev-x-y is already used by window x-y of tmux session cadrei-c-dev") {
		t.Errorf("one cadrei, two teams: %v %q", failed, out)
	}
	// Across cadreis: a with team x and role y-z, a-x with team y and role z.
	a, stubA := cadreiDir(t, "a")
	os.MkdirAll(filepath.Join(a, "members", "x"), 0o755)
	os.WriteFile(filepath.Join(a, "members", "x", "y-z.md"), []byte("# y-z\n"), 0o644)
	ax, stubAX := cadreiDir(t, "a-x")
	os.MkdirAll(filepath.Join(ax, "members", "y"), 0o755)
	os.WriteFile(filepath.Join(ax, "members", "y", "z.md"), []byte("# z\n"), 0o644)
	if out, failed := up(tm, a, stubA, "x", "y-z", ""); failed {
		t.Fatalf("a: %q", out)
	}
	out, failed = up(tm, ax, stubAX, "y", "z", "")
	if !failed || !strings.Contains(out, "a-x-y-z is already used") {
		t.Errorf("two cadreis: %v %q", failed, out)
	}
}

func TestLegacySessions(t *testing.T) {
	tm := private(t)
	c, stub := cadreiDir(t, "work")
	tm.command("new-session", "-d", "-s", "cadre-dev", "-n", "pm", "sleep", "60").Run()
	def := Scope{Name: "work", Path: c, T: tm, Default: true}
	other := Scope{Name: "work", Path: c, T: tm}
	if def.Live("dev", "") != "cadre-dev" || other.Live("dev", "") != "" {
		t.Errorf("Live: default %q, other %q", def.Live("dev", ""), other.Live("dev", ""))
	}
	if len(def.Running()) != 1 || len(other.Running()) != 0 {
		t.Errorf("Running: default %d, other %d", len(def.Running()), len(other.Running()))
	}
	u := Up{Scope: def, Team: "dev", Role: "pm", Dir: c, Protocol: nil, Mode: "default", Pick: pick(stub), Wait: 300 * time.Millisecond}
	var out bytes.Buffer
	u.Start(&out)
	if !strings.Contains(out.String(), "dev-pm already running (legacy session cadre-dev") {
		t.Errorf("up with a legacy session: %q", out.String())
	}
	if line := def.StopTeam("dev", ""); !line.Stopped || tm.Has("cadre-dev") {
		t.Errorf("stop a legacy team: %+v", line)
	}
	// tmux 3.5 and later kept a dot in a 0.1.x session's name; 3.4 made
	// it "_". Either is found.
	tm.command("new-session", "-d", "-s", "cadre-dev-my.app", "-n", "pm", "sleep", "60").Run()
	if line := def.StopTeam("dev", "my.app"); !line.Stopped {
		t.Errorf("stop a dotted legacy team: %+v", line)
	}
}

func TestAnOrchestratorSessionIsNotAMemberSession(t *testing.T) {
	tm := private(t)
	c, _ := cadreiDir(t, "work")
	tm.command("new-session", "-d", "-s", "cadrei-work", "sleep", "60").Run()
	tm.SetOption("cadrei-work", "@cadrei_home", c)
	tm.SetOption("cadrei-work", "@cadrei_role", "orchestrator")
	if r := (Scope{Name: "work", Path: c, T: tm}).Running(); len(r) != 0 {
		t.Errorf("Running lists the orchestrator: %+v", r)
	}
	if len(All(tm, nil)) != 0 {
		t.Error("All lists the orchestrator")
	}
}

func TestVersion(t *testing.T) {
	tm := private(t)
	major, _, err := tm.Version()
	if err != nil || major < 3 {
		t.Errorf("tmux version %d: %v", major, err)
	}
}

// waitFile waits for a stub to write its file, under load too.
func waitFile(t *testing.T, path string) string {
	t.Helper()
	// Up to 10 s: a heavily loaded machine can take seconds to run a stub.
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 && bytes.Contains(b, []byte("MEMBER=")) {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s was not written", path)
	return ""
}

func TestStartPassesEveryArgumentAsItIs(t *testing.T) {
	tm := private(t)
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe")
	os.WriteFile(probe, []byte("#!/bin/sh\nout=$1; shift\nprintf '%s|' \"$@\" > \"$out\"\nexec sleep 30\n"), 0o755)
	out := filepath.Join(dir, "out")
	// tmux splits at an argument that is or ends with ";".
	err := tm.Start(StartSpec{Session: "cadrei-x", Window: "w", Dir: dir, Argv: []string{probe, out, "a;", ";", "x;y", "$HOME", "it's"},
		SessionOptions: []Option{{"@cadrei_home", "/path;"}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if b, _ := os.ReadFile(out); len(b) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if b, _ := os.ReadFile(out); string(b) != "a;|;|x;y|$HOME|it's|" {
		t.Errorf("the command got %q", b)
	}
	if tm.Option("cadrei-x", "@cadrei_home") != "/path;" {
		t.Errorf("option %q", tm.Option("cadrei-x", "@cadrei_home"))
	}
	if err := tm.Start(StartSpec{Session: "cadrei-y", Window: "w", Dir: dir, Argv: []string{probe, `a\;`}}); err == nil {
		t.Error("an argument ending in a backslash and a semicolon was passed")
	}
	if err := tm.Start(StartSpec{Session: "cadrei-z", Window: "w", Dir: dir, Argv: []string{"sleep"}}); err == nil {
		t.Error("a one-word command, which tmux runs through a shell, was started")
	}
}

// Acceptance 22 and 23: a cadrei session gets its look at session and
// window scope only, history-limit reaches its real window (on tmux 3.2
// too, through the placeholder), and the bar shows the user's prefix.
func TestTeamSessionsHaveTheirLook(t *testing.T) {
	tm := private(t)
	c, stub := cadreiDir(t, "work")
	global := func() string {
		var b strings.Builder
		for _, flag := range []string{"-g", "-s", "-gw"} {
			out, _ := tm.run("show-options", flag)
			b.WriteString(out + "\n")
		}
		return b.String()
	}
	// The server needs a session for show-options; one of the user's own.
	// Its new-session starts the server: a start-server alone ends at
	// once, with no session, and a new-session meanwhile can meet it
	// ending.
	if out, err := tm.run("new-session", "-d", "-s", "mine", "--", "sleep", "300"); err != nil {
		t.Fatalf("new-session: %v %s", err, out)
	}
	before := global()
	if strings.Contains(before, "no server running") || strings.Contains(before, "error connecting") {
		t.Fatalf("no server to read options from:\n%s", before)
	}
	up(tm, c, stub, "dev", "pm", "app")
	for name, want := range map[string]string{"mouse": "on", "status": "on", "status-position": "bottom", "status-left-length": "40", "status-right-length": "60",
		"status-left": " #[bold,bg=#A9502F,fg=#FAF8F4] work #[default] dev on app ", "status-style": "bg=#3D9F60,fg=#1F1C19", "status-right": "#{?#{e|<:#{client_width},60},C-b s · C-b d,Ctrl-b s switch · Ctrl-b d leave} "} {
		if got := tm.Option("cadrei-work-dev-app", name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	window := "=cadrei-work-dev-app:=pm"
	if got, _ := tm.run("display", "-p", "-t", window, "#{history_limit}"); got != "50000" {
		t.Errorf("the real window's history-limit is %q", got)
	}
	for name, want := range map[string]string{"monitor-bell": "on", "aggressive-resize": "on", "window-status-format": " #W "} {
		if got, _ := tm.run("show-options", "-wqv", "-t", window, name); got != want {
			t.Errorf("window %s = %q, want %q", name, got, want)
		}
	}
	if all := tm.allWindows("cadrei-work-dev-app"); strings.Join(all, " ") != "pm" {
		t.Errorf("windows %v: the placeholder is still there", all)
	}
	if out, _ := tm.run("show-hooks", "-t", "=cadrei-work-dev-app:"); strings.Contains(out, "client-attached") {
		t.Errorf("hooks %q", out)
	}
	if after := global(); after != before {
		t.Errorf("server or global options changed:\n%s\n---\n%s", before, after)
	}
	tm.run("set-option", "-g", "prefix", "C-a")
	up(tm, c, stub, "dev", "pm", "")
	if got := tm.Option("cadrei-work-dev", "status-right"); !strings.Contains(got, "Ctrl-a s switch · Ctrl-a d leave") || !strings.Contains(got, ",C-a s · C-a d,") {
		t.Errorf("another prefix: %q", got)
	}
	t.Setenv("NO_COLOR", "1")
	// Acceptance 102 and 107: under NO_COLOR, the terminal's own colors
	// and a bold name; the orchestrator's label is the crew's name alone.
	if look, _ := tm.Look("work", "", true); look[4].Value != " #[bold]work#[nobold] " || look[3].Value != "bg=default,fg=default" {
		t.Errorf("plain: %q %q", look[4].Value, look[3].Value)
	}
	if look, _ := tm.Look("work", "", false); look[4].Value != " #[bold,bg=#A9502F,fg=#FAF8F4] work #[default] " {
		t.Errorf("orchestrator label %q", look[4].Value)
	}
}

// Acceptance 34: every tmux command names cadrei's server, and from tmux
// on another server cadrei never switches a client.
func TestOneServerOnly(t *testing.T) {
	tm := private(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	real, _ := exec.LookPath("tmux")
	os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+log+"\nexec "+real+" \"$@\"\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	c, stub := cadreiDir(t, "work")
	up(tm, c, stub, "dev", "pm", "")
	tm.Has("cadrei-work-dev")
	t.Setenv("TMUX", "/tmp/tmux-other/default,1,0")
	if tm.InTmux() != OtherServer {
		t.Error("another server's $TMUX was taken for cadrei's")
	}
	if _, err := tm.Attach("cadrei-work-dev"); err != ErrOtherServer {
		t.Errorf("attach from another server: %v", err)
	}
	t.Setenv("TMUX", tm.Path()+",1,0")
	if tm.InTmux() != SameServer {
		t.Errorf("cadrei's own server was not recognised from %s", tm.Path())
	}
	raw, _ := os.ReadFile(log)
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !strings.Contains(l, "-S "+tm.Path()+" ") {
			t.Errorf("a tmux command without -S: %s", l)
		}
		if strings.Contains(l, "switch-client") {
			t.Errorf("switch-client ran: %s", l)
		}
	}
}

func TestStartFolderIsNotAFormat(t *testing.T) {
	tm := private(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	// tmux expands -c as a format; this folder must stay itself, for a
	// new session and for a new window in it.
	dir := filepath.Join(base, "a#{session_name}#(echo hi)##b")
	os.MkdirAll(dir, 0o755)
	for _, w := range []string{"one", "two"} {
		if err := tm.Start(StartSpec{Session: "cadrei-hash", Window: w, Dir: dir, Argv: []string{"sleep", "30"}}); err != nil {
			t.Fatal(err)
		}
		got, _ := tm.run("display-message", "-p", "-t", "=cadrei-hash:="+w, "#{pane_current_path}")
		if got != dir {
			t.Errorf("window %s started in %q, not %q", w, got, dir)
		}
	}
}

func TestRecordedValuesComeBackAsTheyAre(t *testing.T) {
	tm := private(t)
	dir := t.TempDir()
	for _, v := range []string{"/a\nb", "/a\rb", "/a\tb", "/a\x1fb", "/a\x7fb", "/a\xffb", `/a\$HOME`, `/a\${x}`, `/a\$é`} {
		err := tm.Start(StartSpec{Session: "cadrei-bad", Window: "w", Dir: dir, Argv: []string{"sleep", "30"},
			SessionOptions: []Option{{"@cadrei_home", v}}})
		if err == nil || tm.Has("cadrei-bad") {
			t.Errorf("%q was recorded", v)
		}
		if err := tm.Start(StartSpec{Session: "cadrei-bad", Window: "w", Dir: dir, Argv: []string{"sleep", "30"},
			WindowOptions: []Option{{"@cadrei_member", v}}}); err == nil {
			t.Errorf("%q was recorded on a window", v)
		}
		tm.KillSession("cadrei-bad")
	}
	// What may be recorded is read back exactly, even in a C locale, where
	// tmux would turn non-ASCII characters into "_" for a client it does
	// not know to be UTF-8.
	for _, v := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		t.Setenv(v, "C")
	}
	home := `/Users/josé/my cadrei;\ "x" 'y' $HOME #{z} 日本`
	err := tm.Start(StartSpec{Session: "cadrei-ok", Window: "w-1", Dir: dir, Argv: []string{"sleep", "30"},
		SessionOptions: []Option{{"@cadrei_home", home}, {"@cadrei_team", "dev"}, {"@cadrei_target", "ü"}},
		WindowOptions:  []Option{{"@cadrei_member", "ok-dev-w-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	list := tm.Sessions()
	if len(list) != 1 || list[0].Home != home || list[0].Team != "dev" || list[0].Target != "ü" {
		t.Errorf("Sessions read back %+v", list)
	}
	if w := tm.AllWindows(); len(w) != 1 || w[0].Session.Name != "cadrei-ok" || w[0].Name != "w-1" || w[0].Member != "ok-dev-w-1" || w[0].Session.Home != home {
		t.Errorf("AllWindows read back %+v", w)
	}
	if tm.Option("cadrei-ok", "@cadrei_home") != home {
		t.Errorf("Option read back %q", tm.Option("cadrei-ok", "@cadrei_home"))
	}
	if tm.SetOption("cadrei-ok", "@cadrei_target", "x\ny") == nil || tm.SetWindowOption("cadrei-ok", "w-1", "@cadrei_member", "x\ty") == nil {
		t.Error("a control character was set")
	}
}

// tmux 3.4 writes command output through vis(3) with VIS_OCTAL|VIS_CSTYLE,
// which escapes every control character but tab and newline: a separator
// of any other control character (the old \x1f) never reaches cadrei.
func TestTheSeparatorSurvivesTmuxOutput(t *testing.T) {
	for _, b := range []byte(sep) {
		if b != '\t' && (b < 0x20 || b >= 0x7f) {
			t.Errorf("separator byte %#x is escaped by tmux 3.4", b)
		}
	}
	if recordable("x", "a"+sep+"b") == nil {
		t.Error("a value holding the separator can be recorded")
	}
}

// tmux 3.4 writes "$" before a letter, "_" or "{" as "\$" (utf8_strvis);
// later versions write it as it is. Both read back as the value.
func TestDollarsReadBackFromEitherTmux(t *testing.T) {
	for _, v := range []string{`$HOME/x`, `a$_b`, `${x}`, `$1 $ $`, `a\b`, `\$1`, `x$`, `\`, `/a/$é`, `$日本`, `€$€`} {
		if err := recordable("v", v); err != nil {
			t.Fatalf("%q refused: %v", v, err)
		}
		var escaped strings.Builder
		for i := 0; i < len(v); i++ {
			if v[i] == '$' && i+1 < len(v) && dollarEscaped(v[i+1]) {
				escaped.WriteByte('\\')
			}
			escaped.WriteByte(v[i])
		}
		if got := unescape(escaped.String()); got != v {
			t.Errorf("from tmux 3.4: %q read back as %q", v, got)
		}
		if got := unescape(v); got != v {
			t.Errorf("from a later tmux: %q read back as %q", v, got)
		}
	}
}

// resumable is a runtime that can resume; gone names a lost conversation.
type resumable struct {
	stubRuntime
	gone string
}

func (r resumable) Caps() runtime.Capabilities {
	return runtime.Capabilities{Resume: true, AssignSessionID: true}
}
func (r resumable) Sessions() runtime.SessionOps { return resumableOps{r.gone} }

type resumableOps struct{ gone string }

func (o resumableOps) NewID() string              { return "new-id" }
func (o resumableOps) Exists(id, dir string) bool { return id != o.gone }
func (o resumableOps) Transcript(string, string) (time.Time, int64, bool) {
	return time.Time{}, 0, false
}
func (o resumableOps) FromHook(io.Reader) string { return "" }
func (o resumableOps) StateEvent(io.Reader) runtime.StateEvent {
	return runtime.StateEvent{}
}
func (o resumableOps) TranscriptFile(string, string) string { return "" }
func (o resumableOps) UnansweredPrompt(string, string) bool { return false }
func (o resumableOps) Ready(string) bool                    { return true }
func (o resumableOps) Waiting(string) bool                  { return false }
func (o resumableOps) ShellCall(io.Reader) (runtime.ShellCall, error) {
	return runtime.ShellCall{}, nil
}
func (o resumableOps) Ask(string) []byte                     { return nil }
func (o resumableOps) SessionSettings(string) (string, bool) { return "", false }
func (o resumableOps) Sent(io.Reader) (runtime.Sent, bool)   { return runtime.Sent{}, false }
func (o resumableOps) SentContext(string) []byte             { return nil }

func (o resumableOps) SafeguardStop(string) (time.Time, bool) {
	return time.Time{}, false
}

func TestPlan(t *testing.T) {
	dir := t.TempDir()
	record := RecordPath(dir, "work-dev-engineer")
	rt := resumable{}
	if c := Plan(rt, record, dir, false); c.SessionID != "new-id" || c.Note != "a new conversation" {
		t.Errorf("no record: %+v", c)
	}
	WriteRecord(record, Record{ID: "old-id", Dir: dir, Since: time.Now()})
	if c := Plan(rt, record, dir, false); c.Resume != "old-id" || c.SessionID != "" || c.Note != "resumed its conversation" {
		t.Errorf("a record: %+v", c)
	}
	if c := Plan(rt, record, dir, true); c.Resume != "" || c.Note != "a new conversation, as asked" {
		t.Errorf("fresh: %+v", c)
	}
	if c := Plan(rt, record, t.TempDir(), false); c.Resume != "" || !strings.Contains(c.Note, "another folder") {
		t.Errorf("another folder: %+v", c)
	}
	if c := Plan(resumable{gone: "old-id"}, record, dir, false); c.Resume != "" || !strings.Contains(c.Note, "the last one is gone") {
		t.Errorf("gone: %+v", c)
	}
	if c := Plan(stubRuntime{}, record, dir, false); c != (Conversation{}) {
		t.Errorf("a runtime that cannot resume: %+v", c)
	}
	if RecordPath(dir, "../x") != "" || RecordPath(dir, "a/b") != "" {
		t.Error("a name cadrei does not make got a record")
	}
	Forget(record)
	if ReadRecord(record) != nil {
		t.Error("Forget")
	}
}

// tmux reads a dot in a target as the start of a pane, so a project such
// as my.app is named with the colon that ends the session part: it can be
// found, listed, attached to and stopped.
func TestADottedProjectIsFoundStoppedAndAttached(t *testing.T) {
	tm := private(t)
	c, stub := cadreiDir(t, "work")
	if out, failed := up(tm, c, stub, "dev", "pm", "my.app"); failed {
		t.Fatalf("up: %s", out)
	}
	// tmux 3.4 turns the dot into "_"; cadrei names it so on every version.
	name := "cadrei-work-dev-my_app"
	if !tm.Has(name) || tm.Has("cadrei-work-dev-my") || SessionName("work", "dev-my.app") != name {
		t.Fatalf("Has: %v", tm.Has(name))
	}
	if w := tm.Windows(name); len(w) != 1 || w[0] != "pm" {
		t.Errorf("Windows: %q", w)
	}
	// Without a terminal, tmux finds the session and then cannot open the
	// terminal; a target it cannot read fails before that.
	out, _ := tm.command(attachArgs(name, false)...).CombinedOutput()
	if !strings.Contains(string(out), "not a terminal") {
		t.Errorf("attach: %s", out)
	}
	if args := attachArgs(name, true); args[0] != "switch-client" || args[2] != "="+name+":" {
		t.Errorf("switch-client: %q", args)
	}
	s := Scope{Name: "work", Path: c, T: tm}
	if line := s.StopTeam("dev", "my.app"); !line.Stopped || tm.Has(name) {
		t.Errorf("stop: %+v", line)
	}
}

// A build folder, or the .claude folder holding it, that is a link (as a
// cloned cadrei can carry) is refused: nothing is written through it.
func TestEnsureBuildRefusesALink(t *testing.T) {
	for _, linked := range []string{".claude/build", ".claude"} {
		c, scratch := t.TempDir(), t.TempDir()
		os.MkdirAll(filepath.Join(c, filepath.Dir(linked)), 0o755)
		os.Symlink(scratch, filepath.Join(c, linked))
		if err := EnsureBuild(filepath.Join(c, ".claude", "build")); err == nil || !strings.Contains(err.Error(), "is a link or a file, not a folder") {
			t.Errorf("%s: %v", linked, err)
		}
		if entries, _ := os.ReadDir(scratch); len(entries) != 0 {
			t.Errorf("%s: wrote through the link: %v", linked, entries)
		}
	}
	c := t.TempDir()
	if err := EnsureBuild(filepath.Join(c, ".claude", "build")); err != nil {
		t.Errorf("a new cadrei: %v", err)
	}
}

// A session with this cadrei's home but no team or project markers is not
// a team session: up refuses to start members in it, and stop leaves it,
// each saying how to stop it.
func TestUpRefusesASessionWithoutMarkers(t *testing.T) {
	tm := private(t)
	c, _ := cadreiDir(t, "work")
	tm.command("new-session", "-d", "-s", "cadrei-work-dev", "-n", "x", "sleep", "60").Run()
	tm.command("set-option", "-q", "-t", "=cadrei-work-dev:", "@cadrei_home", c).Run()
	s := Scope{Name: "work", Path: c, T: tm}
	err := s.CheckSession("dev", "")
	if err == nil || !strings.Contains(err.Error(), "a session named cadrei-work-dev exists without cadrei's markers; stop it with tmux kill-session -t cadrei-work-dev, or cadrei stop --yes") {
		t.Errorf("CheckSession: %v", err)
	}
	want := "a session named cadrei-work-dev exists without cadrei's markers; stop it with tmux kill-session -t cadrei-work-dev, or cadrei stop --yes"
	if res := s.StopTeam("dev", ""); res.Unmarked != want {
		t.Errorf("stop: %+v", res)
	}
	if res := s.StopRole("dev", "", "x"); res.Unmarked != want {
		t.Errorf("stop a role: %+v", res)
	}
	if !tm.Has("cadrei-work-dev") || !tm.HasWindow("cadrei-work-dev", "x") {
		t.Error("stop stopped it")
	}
}

// Audit sec-exposure-1: a tmux server started by cadrei from inside a chat
// keeps none of the variables kept out of tmux in its global environment.
func TestAServerStartsWithoutKeptVariables(t *testing.T) {
	tm := private(t)
	KeepOut("SOME_RUNTIME_SESSION_MARK")
	t.Setenv("SOME_RUNTIME_SESSION_MARK", "placeholder")
	t.Setenv("CADREI_ORCHESTRATOR", "1")
	t.Setenv("CADREI_KEPT_TEST", "kept")
	tm.run("new-session", "-d", "-s", "plain", "sleep", "30")
	out, _ := tm.run("show-environment", "-g")
	if strings.Contains(out, "SOME_RUNTIME_SESSION_MARK") || strings.Contains(out, "CADREI_ORCHESTRATOR") || !strings.Contains(out, "CADREI_KEPT_TEST=kept") {
		t.Errorf("global environment:\n%s", out)
	}
}

// Review of fix round 5 (bugs, Has): a session that ended counts as
// stopped, but one tmux can't be asked about (here its socket can't be
// opened) doesn't, whatever has-session says.
func TestAStopTmuxCantMakeIsntCounted(t *testing.T) {
	tm := private(t)
	tm.command("new-session", "-d", "-s", "cadrei-work-dev", "sleep", "60").Run()
	st := &Stopping{T: tm}
	st.Stop(io.Discard, []string{"cadrei-work-gone"})
	if len(st.Failed) != 0 {
		t.Errorf("a session that isn't there: %v", st.Failed)
	}
	sock := tm.Path()
	if err := os.Chmod(sock, 0); err != nil {
		t.Skip("can't change the socket's mode")
	}
	t.Cleanup(func() { os.Chmod(sock, 0o700) })
	if out, _ := tm.run("has-session", "-t", "=cadrei-work-dev:"); !strings.Contains(out, "Permission denied") {
		t.Skipf("the socket is still open to this user: %q", out)
	}
	st.Stop(io.Discard, []string{"cadrei-work-dev"})
	if len(st.Failed) != 1 {
		t.Errorf("a session tmux couldn't stop counted as stopped: %v", st.Failed)
	}
	if list, err := tm.List(); err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("a listing tmux couldn't make: %v, %v", list, err)
	}
	if list, err := (Tmux{Socket: tm.Socket + "-none"}).List(); err != nil || list != nil {
		t.Errorf("no server: %v, %v", list, err)
	}
	for out, want := range map[string]bool{"can't find session: =x:": true, "no server running on /tmp/tmux-501/x": true,
		"error connecting to /tmp/tmux-501/x (No such file or directory)": true, "error connecting to /tmp/x (Permission denied)": false,
		"server exited unexpectedly": false, "protocol version mismatch (client 8, server 7)": false} {
		if goneText(out) != want {
			t.Errorf("goneText(%q) != %v", out, want)
		}
	}
}

// Review of fix round 5 (bugs 1, still open): the command's own session
// keeps only the command's window until Last, so its other members stop
// before anything moves.
func TestTheOwnSessionKeepsOnlyTheCommandsWindow(t *testing.T) {
	tm := private(t)
	tm.command("new-session", "-d", "-s", "cadrei-work-dev", "-n", "engineer", "sleep", "60").Run()
	tm.command("new-window", "-d", "-t", "=cadrei-work-dev:", "-n", "reviewer", "sleep", "60").Run()
	tm.command("new-window", "-d", "-t", "=cadrei-work-dev:", "-n", "pm", "sleep", "60").Run()
	pane, _ := tm.run("display", "-p", "-t", "=cadrei-work-dev:reviewer", "#{pane_id}")
	t.Setenv("TMUX", tm.Path()+",1,0")
	t.Setenv("TMUX_PANE", pane)
	st := &Stopping{T: tm}
	st.Stop(io.Discard, []string{"cadrei-work-dev"})
	if left := tm.Windows("cadrei-work-dev"); strings.Join(left, ",") != "reviewer" {
		t.Errorf("windows before Last: %v", left)
	}
	st.Last(io.Discard)
	if tm.Has("cadrei-work-dev") {
		t.Error("Last left the session")
	}

	// A window that won't stop (review of fix round 6): the session counts
	// as one that won't, so nothing moves while that member runs.
	tm.command("new-session", "-d", "-s", "cadrei-work-dev", "-n", "engineer", "sleep", "60").Run()
	tm.command("new-window", "-d", "-t", "=cadrei-work-dev:", "-n", "reviewer", "sleep", "60").Run()
	pane, _ = tm.run("display", "-p", "-t", "=cadrei-work-dev:reviewer", "#{pane_id}")
	t.Setenv("TMUX_PANE", pane)
	real, _ := exec.LookPath("tmux")
	bin := t.TempDir()
	stub := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = kill-window ] && exit 1; done\nexec '" + real + "' \"$@\"\n"
	if err := exec.Command("/bin/sh", "-c", `printf '%s' "$2" > "$1/tmux" && chmod 755 "$1/tmux"`, "sh", bin, stub).Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	st = &Stopping{T: tm}
	st.Stop(io.Discard, []string{"cadrei-work-dev"})
	if strings.Join(st.Failed, ",") != "cadrei-work-dev" {
		t.Errorf("a window that won't stop: failed %v", st.Failed)
	}
}
