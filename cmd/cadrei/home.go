//go:build !windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/project"
	"github.com/rafi-ramdhani/cadrei/internal/registry"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// interactive reports whether cadrei may ask the user: stdin is a terminal,
// or, in a test build, answers come from stdin (see tty*.go).
func (e *env) interactive() bool {
	if testTTY() {
		return true
	}
	f, ok := e.stdin.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// ask prints question on stderr and reads one line of answer.
func (e *env) ask(question string) string {
	answer, _ := e.answer(question)
	return answer
}

// answer asks like ask, and reports false when the input has ended (Ctrl-D
// on a terminal): never an answer, so it never accepts a default.
func (e *env) answer(question string) (string, bool) {
	// Once the input has ended, nothing more is asked: a terminal would
	// read again after Ctrl-D.
	if e.eof {
		return "", false
	}
	fmt.Fprint(e.stderr, question)
	if e.lines == nil {
		e.lines = bufio.NewReader(e.stdin)
	}
	line, err := e.lines.ReadString('\n')
	if err != nil && line == "" {
		e.eof = true
		fmt.Fprintln(e.stderr)
		return "", false
	}
	return strings.TrimSpace(line), true
}

// resolve finds the cadrei this command acts on (N.3), asking which one
// when several cadreis link the project the user is in.
func (e *env) resolve() (*cadreis.Resolved, bool) {
	var ask cadreis.Asker
	if e.interactive() && !e.noAsk {
		ask = func(project string, names []string) (string, error) {
			return e.ask(fmt.Sprintf("%s is in %s crews, %s. Which one? [%s] ", project, countWord(len(names)), andList(names), strings.Join(names, "/"))), nil
		}
	}
	wd, err := paths.Getwd()
	if err != nil {
		wd = paths.Home()
	}
	r, err := cadreis.Resolve(wd, ask)
	if errors.Is(err, cadreis.ErrNoDefault) {
		e.failWith("Pick a default crew first: run " + cmd("cadrei") + ".")
		return nil, false
	}
	if err != nil {
		e.fail("%s", err)
		return nil, false
	}
	if why := cadreis.NewerFormat(r.Path); e.changes && why != "" {
		e.failWith(why, cadreis.UpgradeFix)
		return nil, false
	}
	return r, true
}

// changesCrew are the commands that write to their crew or start its
// members, and the ones that open its orchestrator, whose instructions
// change the crew in this version's layout: a crew a newer cadrei changed
// refuses them (cadreis.Format). ls, attach, stop, --check and uninstall
// still work there.
var changesCrew = map[string]bool{
	"": true, "--tmux": true, "up": true, "allow add": true, "allow remove": true, "project add": true, "project link": true,
	"project unlink": true, "project sync": true, "project trust": true, "tmux": true, "pull": true,
}

// inMember reports whether cadrei runs in a member's session: CADREI_MEMBER,
// which cadrei sets, or CADRE_PERSONA, which sessions started by 0.1.x
// carry. Set counts, even empty: cadrei never sets either one empty, and a
// member that empties it is still a member. CADRE_PERSONA is read only,
// never set, and is a temporary alias: it goes once no 0.1.x sessions are
// around.
func inMember() bool {
	_, member := os.LookupEnv("CADREI_MEMBER")
	_, persona := os.LookupEnv("CADRE_PERSONA")
	return member || persona
}

// member refuses a command in a member's session.
func (e *env) member(why string) bool {
	if inMember() {
		e.failWith("Team members can't "+why+".", "Ask the user to do it from the orchestrator.")
		return true
	}
	return false
}

// memberAnyway is member for the commands that move or delete crews: it
// also refuses in a member's tmux window when the member's variables are
// gone, since a member can drop those but not its window's own marker.
func (e *env) memberAnyway(why string) bool {
	if e.member(why) {
		return true
	}
	list, _ := cadreis.List()
	if name := memberWindow(servers(list), selfPID()); name != "" {
		e.failWith("Team members can't "+why+". This runs in the window of the member "+printable(name)+".", "Ask the user to do it from the orchestrator.")
		return true
	}
	return false
}

// selfPID is this process, which tests set to a member's pane.
var selfPID = os.Getpid

// memberWindow names the member whose tmux window runs pid, or one of its
// parents: the window's @cadrei_member, which cadrei sets when it starts
// the member, on any of the servers. "" when none does.
func memberWindow(servers []session.Tmux, pid int) string {
	panes := map[int]string{}
	for _, t := range servers {
		for _, w := range t.AllWindows() {
			if w.Member != "" && w.Session.Role != "orchestrator" {
				panes[w.PanePID] = w.Member
			}
		}
	}
	if len(panes) == 0 {
		return ""
	}
	parent := parents()
	for n := 0; pid > 1 && n < 256; n++ {
		if name, ok := panes[pid]; ok {
			return name
		}
		pid = parent[pid]
	}
	return ""
}

// parents maps each process to its parent, as ps has them: none when ps
// can't say.
func parents() map[int]int {
	parent := map[int]int{}
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return parent
	}
	for _, line := range strings.Split(string(out), "\n") {
		var p, pp int
		if n, _ := fmt.Sscan(line, &p, &pp); n == 2 {
			parent[p] = pp
		}
	}
	return parent
}

// runsUnder reports whether this process runs under pid: started by it,
// or by a process it started, and so on.
func runsUnder(pid int) bool {
	parent := parents()
	for p, n := parent[selfPID()], 0; p > 1 && n < 256; p, n = parent[p], n+1 {
		if p == pid {
			return true
		}
	}
	return false
}

// runNew is cadrei new <name> (and its old name, cadrei init): make a crew
// from the template, trust its folder and, with no default yet, make it
// the default.
func runNew(e *env) int {
	if len(e.args) != 1 || strings.HasPrefix(e.args[0], "-") {
		return e.failWith("Use " + cmd("cadrei new <name>") + ".")
	}
	if e.member("create or switch crews") {
		return 1
	}
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	// The default as it was before this crew existed: with none set, a
	// crew that is the only one counts as the default.
	def := cadreis.Default()
	// A note about a missing git identity stays silent (D3).
	c, _, err := cadreis.Create(e.args[0], cadrei.Assets)
	if err != nil {
		return e.fail("%s", err)
	}
	e.note("", markDone, "Created your crew "+c.Name+" in "+unbroken(display(c.Path)))
	if !e.trustCrew(rt, c) {
		e.note("", markWarn, rt.Title()+" will ask to trust "+unbroken(display(c.Path))+" when it opens. That's your crew's own folder, so say yes.")
	}
	e.guard(c)
	if d, ok := cadreis.Find(def); def == "" || !ok || !d.Present() {
		if err := cadreis.SetDefault(c.Name); err != nil {
			return e.fail("%s", err)
		}
		e.show("", c.Name+" is your default crew now.")
	} else {
		e.show("", "Your default crew stays "+def+". Switch with "+cmd("cadrei use "+c.Name)+".")
	}
	return 0
}

func runUse(e *env) int {
	if len(e.args) != 1 || strings.HasPrefix(e.args[0], "-") {
		return e.failWith("Use " + cmd("cadrei use <name>") + ".")
	}
	if e.member("create or switch crews") {
		return 1
	}
	c, ok := cadreis.Find(e.args[0])
	if !ok || !c.Present() {
		return e.failWith("There's no crew called "+e.args[0]+" in "+unbroken(cadreis.Tilde(paths.Real(cadreis.Root())))+".",
			"See your crews with "+cmd("cadrei ls --all"))
	}
	if err := cadreis.SetDefault(c.Name); err != nil {
		return e.fail("%s", err)
	}
	e.note("", markDone, c.Name+" is your default crew now.")
	return 0
}

// projectDir finds a registered project's folder on this machine, or
// says why it has none: not here yet, gone, or on a drive that is not
// connected. Cadrei never unlinks a missing project by itself.
func projectDir(r *cadreis.Resolved, name string) (string, error) {
	reg, err := registry.Load(r.Registry())
	if err != nil {
		return "", err
	}
	entry := reg.Get(name)
	if entry == nil {
		return "", errNotRegistered
	}
	d := cadreis.ProjectDir(r.Cadrei, entry)
	if why := placeRefusal(d); d != "" && why != "" {
		return "", fmt.Errorf("%s's folder on this machine, %s, can't hold a project: %s. Link its folder with cadrei project link %s <folder>", name, display(d), why, name)
	}
	switch cadreis.Where(d) {
	case "not here":
		return "", fmt.Errorf("%s isn't on this machine yet. Clone it with cadrei project sync, or link its folder with cadrei project link %s <folder>", name, name)
	case "drive":
		return "", fmt.Errorf("%s is on a drive that isn't connected (%s). Connect the drive", name, display(d))
	case "missing":
		return "", fmt.Errorf("%s is missing: %s is gone. Clone it again with cadrei project sync, link its new folder with cadrei project link %s <folder>, or remove it with cadrei project unlink %s", name, display(d), name, name)
	}
	return d, nil
}

var errNotRegistered = errors.New("not registered")

// placeRefusal says why a project's recorded place can't hold a project,
// or "": the same rule as linking it, so a place from anywhere (an
// archive from elsewhere, a hand edit) never puts members in cadrei's or
// the runtime's own folders.
func placeRefusal(dir string) string {
	rt, err := runtime.Get(runtimeName())
	if err != nil || dir == "" {
		return ""
	}
	return project.DestRefusal(paths.Real(dir), rt.Trust().Protected())
}

func runProjectPath(e *env) int {
	if len(e.args) != 1 {
		return e.failWith("Use " + cmd("cadrei project path <name>") + ".")
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	d, err := projectDir(r, e.args[0])
	switch {
	case errors.Is(err, errNotRegistered):
		// As in 0.1.x, a folder is printed as it is.
		if st, serr := os.Stat(e.args[0]); serr == nil && st.IsDir() {
			abs, _ := filepath.Abs(e.args[0])
			e.say("%s", abs)
			return 0
		}
		return e.failWith("There's no project called " + e.args[0] + " in this crew, and no folder by that name.")
	case err != nil:
		return e.fail("%s", err)
	}
	e.say("%s", d)
	return 0
}

// guard installs cadrei's pre-push guard in a cadrei repository.
func (e *env) guard(c cadreis.Cadrei) {
	if err := e.hookPlaced(framework.Binary()); err != nil {
		if e.shown {
			return // the first run's screen already said the check is off
		}
		e.warn("", c.Name+" has no check for passwords and keys before a backup, since this copy of cadrei isn't installed in a safe place.",
			"Install cadrei with Homebrew or install.sh to add it.")
		return
	}
	if _, err := backup.Install(c.Path, framework.Binary()); errors.Is(err, backup.ErrForeign) {
		e.warn("", c.Name+" has its own git pre-push hook, so cadrei's check for passwords and keys before a backup doesn't run.")
	} else if err != nil {
		e.warn("", c.Name+" has no check for passwords and keys before a backup: "+strings.TrimSuffix(err.Error(), ".")+".")
	}
}

// runTmuxSetting is cadrei tmux [on|off] [--here]: where plain cadrei
// opens this cadrei's orchestrator. It's a setting of the cadrei, kept in
// its cadrei.conf and committed; --here sets it for this machine only.
func runTmuxSetting(e *env) int {
	here := contains(e.args, "--here")
	args := without(e.args, "--here")
	if len(args) > 1 || (len(args) == 1 && args[0] != "on" && args[0] != "off") || (len(args) == 0 && here) {
		return e.failWith("Use " + cmd("cadrei tmux on") + " or " + cmd("cadrei tmux off") + ", with --here for this machine only.")
	}
	r, ok := e.resolve()
	if !ok {
		return 1
	}
	if len(args) == 0 {
		where, local := cadreis.OpensIn(r.Cadrei)
		line := "Your orchestrator opens in this terminal"
		if where == cadreis.OpensInTmux {
			line = "Your orchestrator opens in tmux"
		}
		if local {
			line += " (this machine only)"
		}
		e.say("%s.", line)
		return 0
	}
	if e.member("change where the orchestrator opens") {
		return 1
	}
	where, line := cadreis.OpensInTmux, "Your orchestrator opens in tmux from now on"
	if args[0] == "off" {
		where, line = cadreis.OpensInTerminal, "Your orchestrator opens in this terminal from now on"
	}
	if here {
		line += ", on this machine only"
	}
	if err := cadreis.SetOpensIn(r.Cadrei, where, here); err != nil {
		return e.fail("%s", err)
	}
	e.note("", markDone, line+".")
	return 0
}

// countWord writes a small count as a word.
func countWord(n int) string {
	if n >= 2 && n <= 5 {
		return []string{"two", "three", "four", "five"}[n-2]
	}
	return fmt.Sprint(n)
}
