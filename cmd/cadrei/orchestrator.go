//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
	"golang.org/x/term"
)

// runPlain is plain cadrei: open this cadrei's orchestrator where this
// machine's setting says (tmux or this terminal), or where a flag says for
// this run.
func runPlain(e *env) int {
	tmuxFlag, noTmux, detach, fresh := false, false, false, false
	for _, a := range e.args {
		switch a {
		case "--tmux":
			tmuxFlag = true
		case "--no-tmux":
			noTmux = true
		case "--detach":
			detach = true
		case "--fresh":
			fresh = true
		default:
			return e.failWith("There's no option "+a+" for cadrei.", "Its options are --tmux, --no-tmux, --detach and --fresh.")
		}
	}
	switch {
	case tmuxFlag && noTmux:
		return e.failWith("Pick one: --tmux or --no-tmux.")
	case noTmux && detach:
		return e.failWith("--detach only works with tmux.", "Drop --no-tmux, or drop --detach.")
	}
	if e.member("start the orchestrator") {
		return 1
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	first := false
	if list, _ := cadreis.List(); len(list) == 0 || !cadreis.DefaultSet() {
		if !e.firstRun(rt) {
			return 1
		}
		first = true
	}
	// The login, asked while the rest gets ready (the first run asked
	// already): logged out, the orchestrator's first message would only
	// get an error back.
	var login chan string
	if !first {
		login = make(chan string, 1)
		go func() { login <- rt.Probe().Login }()
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	for _, name := range e.moved {
		e.note("  ", markDone, name+" now keeps track of where its projects are on this machine.")
	}
	if !first {
		e.openingNotes(r)
	}
	// After a first run, its screen has already checked the tools: the
	// quick check runs, without what the screen said (D3, 3.6).
	if first {
		e.shown = true
		cadreis.SetState(cadreis.CheckedVersion, version)
	}
	if _, fatal := e.health(rt, false); fatal {
		return 1
	}
	if e.eof {
		return e.failWith("The input ended before your orchestrator opened.", "Run "+cmd("cadrei")+" again to open it.")
	}
	if err := runtime.CanOrchestrate(rt); err != nil {
		return e.fail("%s", err)
	}
	mode := e.mode(e.conf(r))
	if err := runtime.Usable(rt, mode); err != nil {
		return e.fail("%s", err)
	}
	t := session.Default()
	// Flags win over the setting for one run; --detach means tmux.
	where, _ := cadreis.OpensIn(r.Cadrei)
	useTmux := where == cadreis.OpensInTmux
	switch {
	case tmuxFlag || detach:
		useTmux = true
	case noTmux:
		useTmux = false
	}
	if useTmux && !haveTmux() {
		e.note("  ", markWarn, "tmux isn't installed, so your orchestrator opens here this time.")
		useTmux = false
	}
	if useTmux && !detach && t.InTmux() == session.OtherServer {
		// Switching there would move another terminal's client.
		e.note("  ", markWarn, "You're in tmux on another server, so your orchestrator opens right here this time.")
		useTmux = false
	}
	if useTmux && !e.tmuxReady() {
		return 1
	}
	build := rt.BuildDir(r.Path)
	// Logged out for sure: log in first, unless it's open already (that
	// one is only switched to). Unsure (offline, an old runtime) opens as
	// before. Asked before the start guard, which a login would hold.
	if login != nil && <-login == runtime.LoggedOut && !e.orchestratorOpen(t, r, build) {
		e.say("")
		e.screen("You're not logged in to " + rt.Title() + ", so log in first.")
		if !e.logInNow(rt, "") {
			return 1
		}
	}
	if err := session.EnsureBuild(build); err != nil {
		return e.fail("%s", err)
	}
	// One cadrei run at a time reads the lock, starts and writes the lock.
	guard, err := orchestrator.Guard(build)
	if err != nil {
		return e.fail("%s", err)
	}
	released := false
	release := func() {
		if !released {
			guard.Release()
			released = true
		}
	}
	defer release()
	lockPath := orchestrator.LockPath(build)
	orchestrator.ClearStale(lockPath)
	if l := openOrchestrator(t, r.Name, r.Path, lockPath, true); l != nil {
		if l.Mode == orchestrator.Tmux {
			release()
			if !useTmux {
				e.show("  ", "Your orchestrator is running in tmux. Opening it there.")
			}
			return e.attachTo(l.Session, detach)
		}
		return e.failWith("Your orchestrator is already open in another terminal (open since "+l.Since.Format("15:04")+").", "Switch to that one, or close it first.")
	}
	// One in tmux that cadrei could not record in the lock still counts.
	if name := orchestrator.SessionName(r.Name); haveTmux() && orchestratorSession(t, name, r.Path, 0) {
		release()
		if !useTmux {
			e.show("  ", "Your orchestrator is running in tmux. Opening it there.")
		}
		return e.attachTo(name, detach)
	}
	text, _ := cadrei.Assets.ReadFile("orchestrator.md")
	prompt := filepath.Join(build, "orchestrator.md")
	in := orchestrator.Terminal
	switch {
	case useTmux && t.InTmux() == session.SameServer:
		in = "tmux-inside"
	case useTmux:
		in = orchestrator.Tmux
	}
	wd, _ := paths.Getwd()
	settings := e.snapshotSettings(rt, build)
	body := orchestrator.Prompt(text, r.Name, display(r.Path))
	if settings == "" {
		// No hook may name this binary: the snapshot goes into the
		// prompt, fresh at startup only.
		body = append(body, "\n"+snapshotOf(rt, r, opening{in: in, from: wd, proj: r.Project}, time.Now())...)
	}
	if err := fsx.WriteFile(prompt, body, 0o644); err != nil {
		return e.fail("%s", err)
	}
	// The orchestrator takes only its own model and effort keys, never the
	// members' MODEL and EFFORT (7.11.5).
	machine, shared := crewLayers(r.Path, true)
	for _, c := range []session.Choice{machine, shared} {
		if err := (choiceCheck{who: "The orchestrator's", crew: r.Path, orchestrator: true}).check(rt, c); err != nil {
			return e.failErr(err)
		}
	}
	own := machine.Over(shared)
	// The orchestrator resumes its last conversation, as members do.
	record := session.RecordPath(build, orchestrator.Name(r.Name))
	launch := func(conv session.Conversation) (runtime.Command, error) {
		if note := freshNote(conv.Note); note != "" {
			e.show("  ", note)
		}
		if conv.SessionID != "" {
			session.WriteRecord(record, session.Record{ID: conv.SessionID, Dir: r.Path, Since: time.Now()})
			cadreis.SetOrchestratorID(r.Cadrei, conv.SessionID)
		}
		// Every open gets the seed, so the orchestrator greets the user,
		// except on top of a seed it hasn't answered yet.
		first := seed
		if conv.Resume != "" {
			ops := rt.Sessions()
			if ops.UnansweredPrompt(ops.TranscriptFile(conv.Resume, r.Path), seed) {
				first = ""
			}
		}
		cmd, err := rt.Launch(runtime.LaunchSpec{Role: runtime.Orchestrator, Name: orchestrator.Name(r.Name), Cadrei: r.Path,
			WorkDir: r.Path, Mode: mode, PromptFile: prompt, Settings: settings, Allowed: readOnly, Prompt: first,
			SessionID: conv.SessionID, Resume: conv.Resume, Model: own.Model.Value, Effort: own.Effort.Value})
		// Pinned to this cadrei, and marked as the orchestrator, so the
		// hook adds nothing more (K.3). What only this launch knows goes
		// to the snapshot hook.
		cmd.Env = append(cmd.Env, "CADREI_HOME="+r.Path, "CADREI_ORCHESTRATOR=1", envOpensIn+"="+in, envOpenedFrom+"="+wd)
		if r.Project != "" {
			cmd.Env = append(cmd.Env, envOpenedProject+"="+r.Project)
		}
		return cmd, err
	}
	conv := session.Plan(rt, record, r.Path, fresh)
	// Only a conversation cadrei itself started for this orchestrator is
	// resumed: a record written by anything else is not.
	if conv.Resume != "" && conv.Resume != cadreis.OrchestratorID(r.Cadrei) {
		conv = session.Conversation{SessionID: rt.Sessions().NewID(), Note: "a new conversation: its record is not one cadrei wrote"}
	}
	cmd, err := launch(conv)
	if err != nil {
		return e.fail("%s", err)
	}
	// When the runtime will not resume the conversation, a new one.
	var renew func() (runtime.Command, error)
	if conv.Resume != "" {
		renew = func() (runtime.Command, error) {
			conv = session.Conversation{SessionID: rt.Sessions().NewID(), Note: "a new conversation: resuming failed"}
			return launch(conv)
		}
	}
	if useTmux {
		// It got going once it writes to its conversation (the seed prompt
		// keeps the footer busy), or shows its prompt.
		id := func() string { return conv.SessionID + conv.Resume }
		mod0, size0, _ := rt.Sessions().Transcript(id(), r.Path)
		// A login, trust or first-run screen (no footer, nothing written
		// yet) is going too: it waits for the user. Only screens the
		// runtime knows count; any other is watched for the whole wait, so
		// a command that prints a line and ends isn't taken for one.
		// A screen that waits counts once it has held for waitHeld, with
		// the pane alive at each look (StartCheck looks first): a command
		// that prints such a line and ends isn't taken for one.
		name := orchestrator.SessionName(r.Name)
		var waiting time.Time
		ready := func() bool {
			ops := rt.Sessions()
			if mod, size, ok := ops.Transcript(id(), r.Path); ok && (!mod.Equal(mod0) || size != size0) {
				return true
			}
			screen := t.Screen(name, "orchestrator")
			if ops.Ready(screen) {
				return true
			}
			if !ops.Waiting(screen) {
				waiting = time.Time{}
				return false
			}
			if waiting.IsZero() {
				waiting = time.Now()
			}
			return time.Since(waiting) >= waitHeld
		}
		return e.startTmux(r, cmd, lockPath, detach, release, renew, ready)
	}
	start := time.Now()
	var mod0 time.Time
	var size0 int64
	if renew != nil {
		mod0, size0, _ = rt.Sessions().Transcript(conv.Resume, r.Path)
	}
	code := e.runTerminal(cmd, lockPath, release)
	// The run keeps the real terminal, so its output is not read: a resume
	// failed when the run ended at once, with an error, and wrote nothing
	// to the conversation. A quick quit after it got going is not that.
	if renew != nil && code != 0 && time.Since(start) < 5*time.Second && untouched(rt, conv.Resume, r.Path, mod0, size0) {
		// Under the start guard again, so no other cadrei run opens a
		// second orchestrator meanwhile.
		g, err := orchestrator.Guard(build)
		if err != nil {
			return e.fail("%s", err)
		}
		orchestrator.ClearStale(lockPath)
		if l := openOrchestrator(t, r.Name, r.Path, lockPath, true); l != nil {
			g.Release()
			return e.failWith("Your orchestrator was just opened somewhere else. Use that one.")
		}
		if cmd, err = renew(); err != nil {
			g.Release()
			return e.fail("%s", err)
		}
		code = e.runTerminal(cmd, lockPath, func() { g.Release() })
	}
	return code
}

// orchestratorOpen reports whether the cadrei's orchestrator looks open,
// in a terminal or in tmux, without the start guard: a look, which the
// open under the guard checks again.
func (e *env) orchestratorOpen(t session.Tmux, r *cadreis.Resolved, build string) bool {
	if openOrchestrator(t, r.Name, r.Path, orchestrator.LockPath(build), false) != nil {
		return true
	}
	return haveTmux() && orchestratorSession(t, orchestrator.SessionName(r.Name), r.Path, 0)
}

func haveTmux() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// orchestratorSession reports whether the tmux session name is this
// cadrei's orchestrator: marked as such for home, and, when pid is set,
// running pid in its pane.
func orchestratorSession(t session.Tmux, name, home string, pid int) bool {
	if !t.Has(name) || t.Option(name, "@cadrei_role") != "orchestrator" || t.Option(name, "@cadrei_home") != home {
		return false
	}
	if pid > 0 {
		p, err := t.PanePID(name, "orchestrator")
		return err == nil && p == pid
	}
	return true
}

// openOrchestrator returns the cadrei's open orchestrator from its lock, or
// nil. A tmux lock counts only when it names this cadrei's orchestrator
// session running the recorded process; otherwise the lock was not
// cadrei's (a member's shell can write it), so cadrei never attaches the
// user to a session that only claims to be the orchestrator. A caller
// holding the start guard sets clear, and such a lock is removed.
func openOrchestrator(t session.Tmux, cadrei, home, lockPath string, clear bool) *orchestrator.Lock {
	l := orchestrator.ReadLock(lockPath)
	if l == nil || l.Mode != orchestrator.Tmux {
		return l
	}
	if l.Session != orchestrator.SessionName(cadrei) || !orchestratorSession(t, l.Session, home, l.PID) {
		if clear {
			orchestrator.RemoveLock(lockPath, l)
		}
		return nil
	}
	return l
}

// openingNotes says, in a line, what is worth saying before Claude Code or
// tmux takes the terminal: which cadrei opens when there are several, and
// a cadre from 0.1.x in this folder. A git repository that is no project
// is for the orchestrator to offer.
func (e *env) openingNotes(r *cadreis.Resolved) {
	if r.From != "default" {
		return
	}
	if list, _ := cadreis.List(); len(list) > 1 {
		e.show("  ", "Opening "+r.Name+", your default crew. To open another, run "+cmd("cadrei")+" in one of its projects.")
	}
	if wd, err := paths.Getwd(); err == nil {
		if look := lookalike(wd); look != "" {
			e.show("  ", "This folder looks like a cadre from 0.1.x. To bring it in, tell your orchestrator: bring in my old cadre from "+unbroken(display(look)))
		}
	}
}

// freshNote is what to say when the orchestrator doesn't pick up its last
// chat, or "".
func freshNote(note string) string {
	if note == "a new conversation, as asked" {
		return "Starting a fresh chat."
	}
	if why := freshWhy(note); why != "" {
		return "Starting a fresh chat. " + why
	}
	return ""
}

// freshWhy says why a chat starts fresh when it could have picked up the
// last one, or "".
func freshWhy(note string) string {
	switch note {
	case "a new conversation: the last one is gone":
		return "The last one couldn't be found."
	case "a new conversation: resuming failed":
		return "The last one wouldn't reopen."
	case "a new conversation: its record is not one cadrei wrote":
		return "The last one wasn't opened by cadrei."
	case "a new conversation: it works in another folder now":
		return "It works in another folder now."
	}
	return ""
}

// lookalike returns the folder at or above dir that looks like a 0.1.x
// cadrei, or "". Nothing in it is read.
func lookalike(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if cadreis.OldCadre(d) {
			return d
		}
		if d == "/" || d == "." {
			return ""
		}
	}
}

// runTerminal runs the orchestrator as a child in this terminal, holding
// the lock while it runs, and returns its exit code. release ends the
// start guard once the lock is written.
func (e *env) runTerminal(c runtime.Command, lockPath string, release func()) int {
	cmd := e.child(c)
	defer e.lendTerminal()()
	if err := cmd.Start(); err != nil {
		return e.failWith("Your orchestrator didn't start: " + strings.TrimSuffix(err.Error(), ".") + ".")
	}
	tty := terminalName()
	lock, lockErr := orchestrator.WriteLock(lockPath, cmd.Process.Pid, orchestrator.Terminal, tty, "")
	release()
	if lockErr == nil {
		// Removed under the start guard, so a run starting now never
		// loses the lock it just wrote.
		defer func() {
			if g, err := orchestrator.Guard(filepath.Dir(lockPath)); err == nil {
				orchestrator.RemoveLock(lockPath, lock)
				g.Release()
			}
		}()
	}
	// A closed terminal or a kill ends the orchestrator; the lock goes with it.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(stop)
	go func() {
		for s := range stop {
			cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	// Said once the orchestrator is gone: while it runs, the terminal is
	// its own, and its output is still being copied.
	if lockErr != nil {
		e.warn("", "Couldn't note that your orchestrator was open: "+strings.TrimSuffix(lockErr.Error(), ".")+".")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return e.fail("%s", err)
	}
	return 0
}

// child builds a runtime command run in this terminal.
func (e *env) child(c runtime.Command) *exec.Cmd {
	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = append(unsetIn(withoutMember(os.Environ()), c.Unset), c.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = e.stdin, e.stdout, e.stderr
	return cmd
}

// lendTerminal hands the terminal to a child until back is called. The
// terminal comes back as it was, even if the child dies raw. Ctrl-C and
// the like are for the child: cadrei catches them and only waits. Caught,
// not ignored: an ignored signal stays ignored in the child across exec.
func (e *env) lendTerminal() (back func()) {
	var saved *term.State
	f, ok := e.stdin.(*os.File)
	if ok && term.IsTerminal(int(f.Fd())) {
		saved, _ = term.GetState(int(f.Fd()))
	}
	quiet := make(chan os.Signal, 1)
	signal.Notify(quiet, syscall.SIGINT, syscall.SIGQUIT)
	go func() {
		for range quiet {
		}
	}()
	return func() {
		signal.Stop(quiet)
		close(quiet)
		if saved != nil {
			term.Restore(int(f.Fd()), saved)
		}
	}
}

// waitHeld is how long a screen that waits on the user must hold before
// the orchestrator counts as started.
const waitHeld = time.Second

// waitForTests, when set, is how long a start check may wait: a test on a
// busy machine waits long, and ends early as a real start does.
var waitForTests time.Duration

// startWait is how long a start check may wait.
func startWait() time.Duration {
	if waitForTests > 0 {
		return waitForTests
	}
	return upWait()
}

// withoutMember drops CADREI_MEMBER (and 0.1.x's CADRE_PERSONA): the
// orchestrator is the user's own session, never a member's.
func withoutMember(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "CADREI_MEMBER=") && !strings.HasPrefix(kv, "CADRE_PERSONA=") {
			out = append(out, kv)
		}
	}
	return out
}

// unsetIn drops the named variables from env.
func unsetIn(env, names []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !contains(names, name) {
			out = append(out, kv)
		}
	}
	return out
}

// startTmux starts the orchestrator in its tmux session (K.1), or finds
// the running one, and attaches unless detach is set.
func (e *env) startTmux(r *cadreis.Resolved, c runtime.Command, lockPath string, detach bool, release func(), renew func() (runtime.Command, error), ready func() bool) int {
	t := session.Default()
	name := orchestrator.SessionName(r.Name)
	if t.Has(name) {
		if t.Option(name, "@cadrei_role") == "orchestrator" && t.Option(name, "@cadrei_home") == r.Path {
			release()
			return e.attachTo(name, detach)
		}
		return e.failWith("There's a tmux session called "+name+" that isn't your orchestrator.", "Rename it with "+cmd("tmux rename-session -t "+name+" <new name>")+", then run "+cmd("cadrei")+" again.")
	}
	if !e.recordServer(r.Cadrei, t) {
		release()
		return 1
	}
	look, windowLook := t.Look(r.Name, "", os.Getenv("NO_COLOR") != "")
	start := func(c runtime.Command) error {
		// The window stays when its command ends, until the start check,
		// so a command that ends at once can say why (as a member's does).
		err := t.Start(session.StartSpec{Session: name, Window: "orchestrator", Dir: c.Dir, Env: c.Env, Unset: c.Unset, Argv: c.Argv,
			SessionOptions: append([]session.Option{{Name: "@cadrei_home", Value: r.Path}, {Name: "@cadrei_role", Value: "orchestrator"}, {Name: "@cadrei_version", Value: version}}, look...),
			WindowOptions:  append([]session.Option{{Name: "remain-on-exit", Value: "on"}}, windowLook...)})
		if err != nil {
			return err
		}
		defer t.UnsetWindowOption(name, "orchestrator", "remain-on-exit")
		if last, ok := t.StartCheck(name, "orchestrator", startWait(), ready); !ok {
			return &session.ExitedError{Name: "the orchestrator", Dir: c.Dir, Line: session.Line(append(c.Env, c.Argv...)), Last: last}
		}
		return nil
	}
	err := start(c)
	if err != nil && renew != nil {
		// The runtime would not resume it: start a new conversation.
		t.KillSession(name)
		if c, err = renew(); err == nil {
			err = start(c)
		}
	}
	if err != nil {
		e.exitedOr(err, "Your orchestrator didn't start", fixOrchestratorModel(r.Path))
		return 1
	}
	// Without the lock, cadrei still finds it by its session (runPlain).
	pid, err := t.PanePID(name, "orchestrator")
	if err == nil {
		_, err = orchestrator.WriteLock(lockPath, pid, orchestrator.Tmux, "", name)
	}
	if err != nil {
		e.warn("", "Couldn't note your orchestrator in "+unbroken(display(lockPath))+" ("+err.Error()+"). cadrei finds it by its tmux session instead.")
	}
	release()
	return e.attachTo(name, detach)
}

// attachTo attaches to the orchestrator's session, or only says how when
// detach is set or there is no terminal.
func (e *env) attachTo(name string, detach bool) int {
	if detach || !e.interactive() {
		e.note("", markDone, "Your orchestrator is running in tmux. Open it anytime with "+cmd("cadrei"))
		return 0
	}
	return e.attachOrchestratorSession(name)
}

// attachOrchestratorSession shows the orchestrator's tmux session. When
// the user leaves it (and it still runs), cadrei says it keeps going.
func (e *env) attachOrchestratorSession(name string) int {
	t := session.Default()
	switched, err := t.Attach(name)
	if err != nil {
		return e.fail("%s", err)
	}
	if !switched && t.Has(name) {
		e.show("  ", "Your orchestrator is still running. Run "+cmd("cadrei")+" to come back.")
	}
	return 0
}

// attachOrchestrator is cadrei attach with no team (K.2).
func (e *env) attachOrchestrator(r *cadreis.Resolved) int {
	t := session.Default()
	name := orchestrator.SessionName(r.Name)
	if !t.Has(name) || t.Option(name, "@cadrei_role") != "orchestrator" || t.Option(name, "@cadrei_home") != r.Path {
		return e.failWith("Your orchestrator isn't running in tmux.", "Start it with "+cmd("cadrei --tmux")+", or attach to a team: "+cmd("cadrei attach dev my-app"))
	}
	if !e.interactive() {
		return e.failWith("Run " + cmd("cadrei attach") + " in a terminal.")
	}
	return e.attachOrchestratorSession(name)
}

// terminalName is this process's controlling terminal as /dev/<name>, or
// "" when it has none (a pipe on stdin is not a terminal).
func terminalName() string {
	out, err := exec.Command("ps", "-o", "tty=", "-p", strconv.Itoa(os.Getpid())).Output()
	name := strings.TrimSpace(string(out))
	if err != nil || strings.Trim(name, "?") == "" {
		return ""
	}
	return orchestrator.CleanTTY("/dev/" + name)
}

// untouched reports whether a conversation's transcript is missing, or
// has the time and size it had before the run: the size too, since on
// filesystems with 1 or 2 second times a write in the same tick keeps
// the time.
func untouched(rt runtime.Runtime, id, dir string, mod0 time.Time, size0 int64) bool {
	mod, size, ok := rt.Sessions().Transcript(id, dir)
	return !ok || (mod.Equal(mod0) && size == size0)
}
