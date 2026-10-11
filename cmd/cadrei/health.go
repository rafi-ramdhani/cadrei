//go:build !windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	cadrei "github.com/rafi-ramdhani/cadrei"
	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// A finding of the health check: a problem, and, when cadrei can fix it, the
// question it asks first. Nothing is fixed without a yes.
type finding struct {
	runtime.Problem
	ask      string       // the question for the fix, or ""
	yes      bool         // the answer when the user just presses Enter
	fix      func() error // makes the fix
	declined func()       // remembers a no, when it should not be asked again
	failed   bool         // shown with the failed mark, though cadrei goes on
	screen   bool         // the first run's screen already said it
}

// ensureFramework keeps ~/.cadrei/framework exactly as this binary
// carries it, on every run: the skill holds the orchestrator's consent
// rules, and a member's shell could change it. A change made outside cadrei
// is undone and reported.
func (e *env) ensureFramework() []finding {
	restored, err := framework.Sync(cadrei.Assets, version)
	if err != nil {
		return []finding{{Problem: runtime.Problem{What: "Couldn't write cadrei's instructions to " + unbroken(display(framework.Dir())) + ": " + strings.TrimSuffix(err.Error(), ".") + ".",
			Fix: "Check that folder's permissions."}, failed: true}}
	}
	if len(restored) > 0 {
		return []finding{{Problem: runtime.Problem{What: "Something changed cadrei's instructions outside cadrei (" + strings.Join(restored, ", ") + "). They're put back now.",
			Fix: "If a team member did it, check what it was working on."}}}
	}
	return nil
}

// findings runs the health check: fast checks only look at files and PATH;
// full ones also run programs.
func (e *env) findings(rt runtime.Runtime, full bool) []finding {
	var out []finding
	var dirs []string
	if list, err := cadreis.List(); err == nil {
		for _, c := range list {
			dirs = append(dirs, c.Path)
		}
	}
	for _, p := range rt.Health(full, dirs) {
		out = append(out, finding{Problem: p})
	}
	// The same checks as the first run's screen (tools.go); tmux's version
	// only in the full check.
	if c := gitCheck(); c.mark == markFail {
		out = append(out, finding{Problem: runtime.Problem{What: c.what, Fatal: true, Fix: c.fix}, screen: true})
	}
	if _, err := exec.LookPath("tmux"); err != nil || full {
		if c := tmuxCheck(); c.mark != markDone {
			out = append(out, finding{Problem: runtime.Problem{What: c.what, Fix: c.fix}, failed: c.mark == markFail, screen: true})
		}
	}
	if err := e.hookPlaced(framework.Binary()); err != nil {
		out = append(out, finding{Problem: runtime.Problem{
			What: "Team members pick up where they left off only from their start, not after /clear or /compact.",
			Fix:  "Install cadrei with Homebrew or install.sh to fix this."}, screen: true})
	}
	// The full identity row is for cadrei --check only (D3).
	if e.checking && !hasIdentity(wdOrHome()) {
		out = append(out, finding{Problem: runtime.Problem{What: "Git has no name or email set, so cadrei can't save its history.",
			Fix: "Set them with:\n" + cmd(`git config --global user.name "Your Name"`) + "\n" + cmd(`git config --global user.email "you@example.com"`)}})
	}
	out = append(out, e.guardFindings()...)
	out = append(out, skillFindings(rt)...)
	out = append(out, oldLinkFindings(rt)...)
	out = append(out, e.hookFindings(rt)...)
	if f, ok := pathFinding(); ok {
		out = append(out, f)
	}
	return out
}

// guardFindings keeps cadrei's pre-push guard in every cadrei repository,
// which is cadrei's own setup, so it needs no question. A pre-push hook of
// the user's own is left alone and reported.
// It names this binary only when it is safely placed, as the orchestrator
// hook does; otherwise a hook already there is left as it is, and a cadrei
// with none is reported.
func (e *env) guardFindings() []finding {
	var out []finding
	list, _ := cadreis.List()
	placed := e.hookPlaced(framework.Binary())
	for _, c := range list {
		if placed != nil {
			if !backup.Installed(c.Path) {
				out = append(out, finding{Problem: runtime.Problem{
					What: c.Name + " has no check for passwords and keys before a backup.",
					Fix:  "Install cadrei with Homebrew or install.sh to add it."}, screen: true})
			}
			continue
		}
		if _, err := backup.Install(c.Path, framework.Binary()); errors.Is(err, backup.ErrForeign) {
			out = append(out, finding{Problem: runtime.Problem{
				What: c.Name + " has its own git pre-push hook, so cadrei's check for passwords and keys before a backup doesn't run.",
				Fix:  "Add this line to " + unbroken(display(c.Path)+"/.git/hooks/pre-push") + ": " + cmd(display(framework.Binary())+` hook pre-push "$@" || exit 1`)}})
		}
	}
	return out
}

// skillFindings checks the link that gives the runtime cadrei's skill.
func skillFindings(rt runtime.Runtime) []finding {
	ops := rt.Instructions()
	want := framework.SkillDir()
	target, err := ops.Target()
	link := func() error { return ops.Link(want) }
	kept := func(state string) func() { return func() { cadreis.SetState(cadreis.SkillKept, state) } }
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if cadreis.GetState(cadreis.SkillKept) == "missing" {
			return nil
		}
		return []finding{{Problem: runtime.Problem{What: "cadrei's instructions aren't linked into " + rt.Title() + ".",
			Fix: "Run " + cmd("cadrei") + " in a terminal and answer yes."}, ask: "Link them now?", yes: true, fix: link, declined: kept("missing")}}
	case errors.Is(err, runtime.ErrNotLink):
		return []finding{{Problem: runtime.Problem{What: unbroken(display(ops.Path())) + " is a folder, not cadrei's link.",
			Fix: "Move it somewhere else, then run " + cmd("cadrei") + " again."}}}
	case err != nil:
		return []finding{{Problem: runtime.Problem{What: "Couldn't read " + unbroken(display(ops.Path())) + ": " + strings.TrimSuffix(err.Error(), ".") + ".", Fix: "Check that folder's permissions."}}}
	case paths.Real(target) != paths.Real(want):
		if cadreis.GetState(cadreis.SkillKept) == target {
			return nil
		}
		return []finding{{Problem: runtime.Problem{What: "cadrei's instructions link points at another cadrei program (" + unbroken(display(target)) + ").",
			Fix: "Run " + cmd("cadrei") + " in a terminal and answer yes."}, ask: "Point it at this one?", yes: true, fix: link, declined: kept(target)}}
	}
	return nil
}

// oldLinkFindings offers to remove the links a 0.1.x install made into its
// clone: the cadre command in ~/.local/bin and the cadre skill beside
// cadrei's. After a pull they lead only to the bridge files. The question
// defaults to no, since a link is something the user may have made, and a
// no is remembered for that link.
func oldLinkFindings(rt runtime.Runtime) []finding {
	var out []finding
	for _, l := range []struct{ link, inClone string }{
		{filepath.Join(paths.Home(), ".local", "bin", "cadre"), "bin/cadre"},
		{filepath.Join(filepath.Dir(rt.Instructions().Path()), "cadre"), "skills/cadre"},
	} {
		clone := oldClone(rt, l.link, l.inClone)
		if clone == "" || cadreis.GetState(cadreis.OldLinkKept(l.link)) != "" {
			continue
		}
		link := l.link
		out = append(out, finding{Problem: runtime.Problem{What: unbroken(cadreis.Tilde(link)) + " is a leftover link from cadre 0.1.x.",
			Fix: "Remove it with " + cmd("rm "+cadreis.Tilde(link)) + ". The old folder (" + unbroken(display(clone)) + ") stays."},
			ask:      "Remove it? The old folder stays.",
			fix:      func() error { return os.Remove(link) },
			declined: func() { cadreis.SetState(cadreis.OldLinkKept(link), "yes") }})
	}
	return out
}

// oldClone returns the 0.1.x clone link points into, as <clone>/inClone,
// or "" when link is not such a link (see runtime.InstructionOps.OldClone).
func oldClone(rt runtime.Runtime, link, inClone string) string {
	if st, err := os.Lstat(link); err != nil || st.Mode()&fs.ModeSymlink == 0 {
		return ""
	}
	to, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(to) {
		to = filepath.Join(filepath.Dir(link), to)
	}
	clone, ok := strings.CutSuffix(filepath.Clean(to), "/"+inClone)
	if !ok || !rt.Instructions().OldClone(clone) {
		return ""
	}
	return clone
}

// hookPlaced says why the hook must not name bin, or nil (see
// framework.Placed).
func (e *env) hookPlaced(bin string) error {
	if hookAnywhere() {
		return nil
	}
	return framework.Placed(bin)
}

// hookFindings checks that an orchestrator hook, when there is one, runs
// this binary. A no is remembered for that pair of programs. The hook is
// pointed here only when this binary is safely placed.
func (e *env) hookFindings(rt runtime.Runtime) []finding {
	hooks := rt.Hooks()
	progs, err := hooks.Find()
	if err != nil {
		return []finding{{Problem: runtime.Problem{What: "Couldn't read the every-chat hook in " + unbroken(display(hooks.File())) + ": " + strings.TrimSuffix(err.Error(), ".") + ".",
			Fix: "Fix that file, or remove cadrei's hook from it."}}}
	}
	bin := framework.Binary()
	placed := e.hookPlaced(bin)
	var out []finding
	if old, err := hooks.Outdated(bin); err == nil && old && placed == nil {
		out = append(out, finding{Problem: runtime.Problem{What: "The every-chat hook is written in an older form, which can show an error in a chat after cadrei is removed or an older one replaces it.",
			Fix: "Run " + cmd("cadrei") + " in a terminal and answer yes."}, ask: "Update it?", yes: true, fix: func() error { _, err := hooks.Set(bin); return err }})
	}
	for _, p := range progs {
		if paths.Real(p) == paths.Real(bin) {
			continue
		}
		pair := p + " -> " + bin
		if cadreis.GetState(cadreis.HookKept) == pair {
			continue
		}
		f := finding{Problem: runtime.Problem{What: "The every-chat hook runs another cadrei program (" + unbroken(display(p)) + ").",
			Fix: "Run " + cmd("cadrei") + " in a terminal and answer yes."}}
		if placed != nil {
			f.Fix = "Install cadrei with Homebrew or install.sh, then run it from there."
		} else {
			f.ask = "Point it at this one?"
			f.fix = func() error { _, err := hooks.Set(bin); return err }
			f.declined = func() { cadreis.SetState(cadreis.HookKept, pair) }
		}
		out = append(out, f)
	}
	return out
}

// pathFinding checks that the cadrei first on PATH is this one. A binary not
// named cadrei (a test binary) is not the command users type.
func pathFinding() (finding, bool) {
	exe, err := os.Executable()
	if err != nil || filepath.Base(exe) != "cadrei" {
		return finding{}, false
	}
	first, err := exec.LookPath("cadrei")
	if err != nil || paths.Real(first) == paths.Real(exe) {
		return finding{}, false
	}
	return finding{Problem: runtime.Problem{What: "Another cadrei comes first on your PATH (" + unbroken(display(first)) + ").",
		Fix: "Remove it, or put " + unbroken(display(filepath.Dir(exe))) + " earlier in your PATH."}, screen: true}, true
}

// health runs the health check, silent unless something is wrong: fast
// checks every time, full ones when forced, after a version change, or
// when a fast check finds something new (a lasting finding does not slow
// every start). It offers each fix it can make, and reports how many
// problems it found and whether cadrei must stop.
func (e *env) health(rt runtime.Runtime, force bool) (found int, fatal bool) {
	list := e.ensureFramework()
	fast := e.findings(rt, false)
	key := findingsKey(fast)
	full := force || cadreis.GetState(cadreis.CheckedVersion) != version || (len(fast) > 0 && cadreis.GetState(cadreis.LastFindings) != key)
	// Right after a first run, its screen ran the full checks: the quick
	// ones are all that's left (D3), so the runtime isn't asked again.
	if e.shown && !force {
		full = false
	}
	if full {
		list = append(list, e.findings(rt, true)...)
	} else {
		list = append(list, fast...)
	}
	// On open, findings are part of the screen; --check is command output.
	indent := "  "
	if force {
		indent = ""
	}
	styled := streamStyled(e.stderr)
	if e.shown {
		kept := list[:0]
		for _, f := range list {
			if !f.screen {
				kept = append(kept, f)
			}
		}
		list = kept
	}
	for _, f := range list {
		m := markWarn
		if f.Fatal || f.failed {
			m = markFail
		}
		write(e.stderr, f.What, indent+m.text(styled)+" ", indent+"  ")
		// Once the input has ended, nothing more is asked, and the end is
		// not remembered as a no: the fix is printed instead.
		if f.ask != "" && e.interactive() && !e.eof {
			if e.yes(indent+"  "+f.ask, f.yes) {
				if err := f.fix(); err != nil {
					write(e.stderr, "Couldn't fix it: "+strings.TrimSuffix(err.Error(), ".")+".", indent+markFail.text(styled)+" ", indent+"  ")
				} else {
					write(e.stderr, "Fixed", indent+markDone.text(styled)+" ", indent+"  ")
				}
				continue
			}
			if !e.eof {
				if f.declined != nil {
					f.declined()
				}
				continue
			}
		}
		for _, l := range strings.Split(f.Fix, "\n") {
			if l != "" {
				write(e.stderr, l, indent+"  ", indent+"  ")
			}
		}
		fatal = fatal || f.Fatal
	}
	cadreis.SetState(cadreis.LastFindings, key)
	if full && !fatal {
		cadreis.SetState(cadreis.CheckedVersion, version)
	}
	return len(list), fatal
}

// findingsKey identifies a set of findings, to tell a new one from those
// already reported.
func findingsKey(list []finding) string {
	if len(list) == 0 {
		return ""
	}
	var whats []string
	for _, f := range list {
		whats = append(whats, f.What)
	}
	sort.Strings(whats)
	sum := sha256.Sum256([]byte(strings.Join(whats, "\n")))
	return hex.EncodeToString(sum[:8])
}

// yes asks a yes-or-no question; Enter gives def.
func (e *env) yes(question string, def bool) bool {
	hint := " [y/N] "
	if def {
		hint = " [Y/n] "
	}
	answer, ok := e.answer(question + hint)
	if !ok {
		return false // the end of the input is never a yes
	}
	switch strings.ToLower(answer) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}

// runCheck is cadrei --check: the full health check, with its fixes.
func runCheck(e *env) int {
	if len(e.args) > 0 {
		return e.failWith("Use " + cmd("cadrei --check") + ", with nothing after it.")
	}
	e.checking = true
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return e.fail("%s", err)
	}
	found, fatal := e.health(rt, true)
	if r, err := cadreis.Resolve(wdOrHome(), nil); err == nil {
		if keys := unknownConf(r.Path); len(keys) > 0 {
			e.warn("", "cadrei.conf has keys this crew doesn't use: "+strings.Join(keys, ", ")+". They're kept as they are.")
			found++
		}
		styled := streamStyled(e.stderr)
		// A warning, not a failure: cadrei goes on, and up refuses them.
		for _, p := range choiceProblems(rt, r.Path) {
			write(e.stderr, p.what, markWarn.text(styled)+" ", "  ")
			for _, l := range p.fix {
				write(e.stderr, l, "  ", "  ")
			}
			found++
		}
	}
	if found == 0 {
		e.note("", markDone, "Everything looks good. (cadrei "+version+")")
	}
	if fatal {
		return 1
	}
	return 0
}

// wdOrHome is the current folder, or the home folder when it's gone.
func wdOrHome() string {
	if wd, err := paths.Getwd(); err == nil {
		return wd
	}
	return paths.Home()
}
