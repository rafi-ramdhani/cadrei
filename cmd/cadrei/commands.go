//go:build !windows

package main

import (
	"fmt"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// A command as the user types it. The table below is the single source for
// dispatch, `cadrei help` and `cadrei help advanced`.
type command struct {
	name    string // the words that select it: "ls", "project add", "--version"; "" is plain cadrei
	usage   string // its arguments, for help
	summary string
	group   string // "" for a visible command, else its group in help advanced
	hidden  bool   // in neither help screen (the hook subcommands)
	shown   string // how help advanced writes it, when not as name and usage
	run     func(e *env) int
}

// An old 0.1.x name: not an alias, only a one-line pointer to the new way.
type oldName struct {
	name string // as typed
	use  string // what to do now
}

var groups = []string{"Crews", "Projects", "Sessions", "Permissions", "Setup", "Health", "Data"}

var commands []command

var oldNames = []oldName{
	{"down", "Use " + cmd("cadrei stop")},
	{"projects", "Use " + cmd("cadrei ls")},
	{"path", "Use " + cmd("cadrei project path")},
	{"sync", "Use " + cmd("cadrei project sync")},
	{"add project", "Ask your orchestrator, or use " + cmd("cadrei project add")},
	{"add team", "Ask your orchestrator to add the team"},
	{"add persona", "Ask your orchestrator to add the team member"},
	{"version", "Use " + cmd("cadrei --version")},
	{"trust", "Use " + cmd("cadrei project trust")},
	{"update", "Update cadrei with " + cmd("brew upgrade cadrei") + ", or by running install.sh again"},
}

// lookupError is a command that isn't there: what happened, then the fix.
type lookupError struct {
	what string
	fix  []string
}

func (l *lookupError) Error() string {
	return render(strings.Join(append([]string{l.what}, l.fix...), " "), false)
}

func init() {
	commands = []command{
		// Visible.
		{name: "", usage: "[--tmux | --no-tmux] [--detach] [--fresh]", summary: "open your orchestrator", run: runPlain},
		{name: "ls", usage: "[--all]", summary: "what's running, and your projects", run: runLs},
		{name: "attach", usage: "[team] [project]", summary: "jump to your orchestrator, or a team: cadrei attach dev", run: runAttach},
		{name: "stop", usage: "[team[/role]] [project] [--all] [--yes] [--fresh]", summary: "stop your teams (asks first)", run: runStop},
		{name: "help", usage: "[advanced]", summary: "this screen. Every command: cadrei help advanced", run: runHelp},
		{name: "--version", summary: "the version, and how cadrei was installed", run: runVersion},
		{name: "uninstall", usage: "[--yes | --dry-run] [--keep-program]", summary: "remove cadrei, Homebrew's program too (keeps your crews)", run: runUninstall},
		// Plain cadrei's options, listed on one line of help.
		{name: "--tmux", usage: "[--detach]", hidden: true, run: runTmux},

		// Advanced, grouped.
		{name: "new", usage: "<name>", summary: "create a crew in ~/.cadrei/<name>", group: "Crews", run: runNew},
		{name: "use", usage: "<name>", summary: "make a crew your default", group: "Crews", run: runUse},
		{name: "pull", summary: "bring in what your crew's other machines backed up, keeping both versions of a note both changed", group: "Crews", run: runPull},
		// The old name of cadrei new: it works, and no help lists it.
		{name: "init", usage: "<name>", hidden: true, run: runNew},

		{name: "project add", usage: "<name> <repo> [team] [about] | <name> --path <dir> [--repo <repo>] [team] [about] [--no-trust]", summary: "clone a project into your projects folder, or link a folder", group: "Projects", run: runProjectAdd},
		{name: "project link", usage: "<name> <dir> [--no-trust]", summary: "set where a project's folder is on this machine", group: "Projects", run: runProjectLink},
		{name: "project unlink", usage: "<name> [--untrust]", summary: "remove a project from this crew (its folder is kept)", group: "Projects", run: runProjectUnlink},
		{name: "project sync", usage: "[--no-trust]", summary: "clone this crew's projects that aren't on this machine yet", group: "Projects", run: runProjectSync},
		{name: "project trust", usage: "<name> | --all", summary: "mark project folders as trusted, so members start there without asking", group: "Projects", run: runProjectTrust},
		{name: "project path", usage: "<name>", summary: "print a project's folder", group: "Projects", run: runProjectPath},
		{name: "project dir", usage: "[<folder>]", summary: "where new clones go", group: "Projects", run: runProjectDir},

		{name: "up", usage: "<team|team/role> [project|dir] [--fresh] [--model <model>] [--effort <level>]", summary: "start a team or one member (--model and --effort for this start only)", group: "Sessions", run: runUp},
		{name: "answer", usage: "[<code> yes|no|<n>]", summary: "see who's waiting on you, and answer a member's prompt or question", group: "Sessions", run: runAnswer},
		{name: "compact", usage: "<team>/<role> [project]", summary: "compact an idle member's conversation", group: "Sessions", run: runCompact},

		{name: "allow", usage: "[list]", summary: "the grants every member gets", group: "Permissions", run: runAllowList},
		{name: "allow add", usage: "[--once] <rule> | [--once] --auto \"<text>\"", summary: "grant a rule or a plain-English allowance to members", group: "Permissions", run: runAllowAdd},
		{name: "allow remove", usage: "<rule|number|--once>", summary: "remove a grant, or every one-time grant", group: "Permissions", run: runAllowRemove},

		{name: "hook", usage: "[on|off|skip]", summary: "the every-chat hook: every new chat opens as your orchestrator", group: "Setup", run: runHook},
		{name: "tmux", usage: "[on|off] [--here]", summary: "where your orchestrator opens: in tmux, or in this terminal", group: "Setup", run: runTmuxSetting},
		{name: "uninstall --archive", usage: "[--dry-run | --yes] [--keep-program]", summary: "pack your crews and settings away in ~/.cadrei-archive, to start over", group: "Setup", run: runUninstallArchive},
		{name: "uninstall --all", usage: "[--dry-run | --confirm \"delete everything\"] [--keep-program]", summary: "delete every crew, archive and setting of cadrei's (your projects stay)", group: "Setup", run: runUninstallAll},
		{name: "restore", usage: "[<archive>]", summary: "bring back a setup you packed away", group: "Setup", run: runRestore},

		{name: "--check", summary: "the full health check", group: "Health", run: runCheck},

		{name: "ls --json", shown: "ls [--all] [--json [--watch] [--json-version 1]]", summary: "the status screen's data, for your orchestrator and other programs (--watch keeps it coming)", group: "Data", run: runLsJSON},

		// Hidden: run by Claude Code and git as hooks. These names are
		// written into files cadrei doesn't own, or into copies a running
		// member keeps across an upgrade, so none is ever renamed or
		// dropped; new ones are only added.
		{name: "hook orchestrator", hidden: true, run: runHookOrchestrator},
		{name: "hook session", hidden: true, run: runHookSession},
		{name: "hook snapshot", hidden: true, run: runHookSnapshot},
		{name: "hook state", hidden: true, run: runHookState},
		{name: "hook team-status", hidden: true, run: runHookTeamStatus},
		{name: "hook pre-push", hidden: true, run: runHookPrePush},
		{name: "hook guard", hidden: true, run: runHookGuard},
		{name: "hook notify", hidden: true, run: runHookNotify},
	}
}

// runTmux is plain cadrei with the flag its entry takes.
func runTmux(e *env) int {
	e.args = append([]string{"--tmux"}, e.args...)
	return runPlain(e)
}

// runLsJSON is cadrei ls --json: the table's entry takes the flag.
func runLsJSON(e *env) int {
	e.args = append([]string{"--json"}, e.args...)
	return runLs(e)
}

// lookup finds the command for args, the longest name first, and returns
// the arguments after it. An old 0.1.x name is refused with the new way;
// -h, --help and -v are the usual flags for help and the version.
func lookup(args []string) (command, []string, error) {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help":
			args = append([]string{"help"}, args[1:]...)
		case "-v":
			args = append([]string{"--version"}, args[1:]...)
		case "--no-tmux", "--detach", "--fresh":
			// Plain cadrei's options, in any order.
			return commands[0], args, nil
		}
	}
	for _, o := range oldNames {
		if matches(o.name, args) > 0 {
			return command{}, nil, &lookupError{what: cmd("cadrei "+o.name) + " is gone since 0.2.0. " + o.use}
		}
	}
	best, bestLen := -1, -1
	for i, c := range commands {
		if n := matches(c.name, args); n > bestLen {
			best, bestLen = i, n
		}
	}
	if best < 0 || (bestLen == 0 && len(args) > 0) {
		// A group word alone, such as cadrei project: that group's help.
		if len(args) == 1 && isGroupWord(args[0]) {
			return command{name: groupHelpName, run: groupHelp(args[0])}, nil, nil
		}
		typed := args[0]
		if len(args) > 1 && isGroupWord(args[0]) {
			typed = args[0] + " " + args[1]
		}
		// Two words typed may be a two-word command misspelled.
		if len(args) > 1 && typed == args[0] {
			if near := nearest(args[0] + " " + args[1]); near != "" {
				return command{}, nil, &lookupError{what: "There's no command " + args[0] + " " + args[1] + ". Did you mean " + cmd("cadrei "+near) + "?"}
			}
		}
		if near := nearest(typed); near != "" {
			return command{}, nil, &lookupError{what: "There's no command " + typed + ". Did you mean " + cmd("cadrei "+near) + "?"}
		}
		return command{}, nil, &lookupError{what: "There's no command " + typed + ".", fix: []string{"See what there is with " + cmd("cadrei help")}}
	}
	return commands[best], args[bestLen:], nil
}

// nearest is the command name closest to typed: every name in help, and
// the old 0.1.x names, with the same number of words. It's within an edit
// distance of 2, and of half the typed length, so a short word isn't
// matched to anything at all. A hidden command is never suggested.
func nearest(typed string) string {
	words := len(strings.Fields(typed))
	var names []string
	for _, c := range commands {
		if !c.hidden && c.name != "" {
			names = append(names, c.name)
		}
	}
	for _, o := range oldNames {
		names = append(names, o.name)
	}
	best, bestD := "", min(2, len(typed)/2)+1
	for _, n := range names {
		if len(strings.Fields(n)) != words {
			continue
		}
		if d := distance(typed, n); d < bestD {
			best, bestD = n, d
		}
	}
	return best
}

// distance is the edit distance between a and b, with two swapped
// neighbours (sl for ls) as one edit.
func distance(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}

// groupHelp prints the help advanced group of a group word's commands.
func groupHelp(word string) func(e *env) int {
	return func(e *env) int {
		group := ""
		for _, c := range commands {
			if w := strings.Fields(c.name); len(w) > 1 && w[0] == word && c.group != "" {
				group = c.group
				break
			}
		}
		e.say("%s", group)
		for _, c := range commands {
			if c.group == group && !c.hidden {
				line(e, c)
			}
		}
		return 0
	}
}

// isGroupWord reports whether word starts multi-word commands only, such as
// "project" or "allow".
// groupHelpName names a group word typed alone (cadrei project): it only
// shows that group's help. No command is called this.
const groupHelpName = "group help"

func isGroupWord(word string) bool {
	for _, c := range commands {
		if w := strings.Fields(c.name); len(w) > 1 && w[0] == word {
			return true
		}
	}
	return false
}

// matches returns how many words of args a command name takes, or -1 when
// it does not match. The empty name (plain cadrei) matches only no args.
func matches(name string, args []string) int {
	if name == "" {
		if len(args) == 0 {
			return 0
		}
		return -1
	}
	words := strings.Fields(name)
	if len(args) < len(words) {
		return -1
	}
	for i, w := range words {
		if args[i] != w {
			return -1
		}
	}
	return len(words)
}

func runVersion(e *env) int {
	if len(e.args) > 0 {
		return e.failWith("Use " + cmd("cadrei --version") + ", with nothing after it.")
	}
	kind := framework.Kind(version)
	// Through a pipe or in a script, the one line, for scripts and the
	// Homebrew test; in a terminal, the compact header (7.12.2).
	if !e.terminal() && !testTTY() {
		e.say("%s", versionLine(kind, version))
		return 0
	}
	first := productLine()
	if kind == "homebrew" {
		first += ", installed with Homebrew"
	}
	wd, err := paths.Getwd()
	if err != nil {
		wd = paths.Home()
	}
	r, err := cadreis.Resolve(wd, nil)
	if err != nil {
		if c, ok := cadreis.Find(cadreis.Default()); ok {
			r = &cadreis.Resolved{Cadrei: c}
		}
	}
	if r == nil {
		e.header(first, "no crew yet · run cadrei to set one up")
		return 0
	}
	n := 0
	for _, s := range runningOf(r.Cadrei, cadreis.Default(), session.Default()) {
		n += len(s.Members)
	}
	running := "nothing running"
	switch {
	case n == 1:
		running = "1 member running"
	case n > 1:
		running = fmt.Sprintf("%d members running", n)
	}
	e.header(first, "crew "+r.Name+" · "+running, display(r.Path))
	return 0
}

// versionLine is cadrei --version's line for an install kind.
func versionLine(kind, v string) string {
	switch {
	case kind == "homebrew":
		return "cadrei " + v + ", installed with Homebrew"
	case kind == "dev" && v == "dev":
		return "cadrei, a development build"
	case kind == "dev":
		return "cadrei " + v + ", a development build"
	}
	return "cadrei " + v
}

func runHelp(e *env) int {
	switch {
	case len(e.args) == 0:
		e.say("cadrei: a team of Claude Code helpers you lead from one chat\n")
		e.say("%s", plainWords)
		e.say("  \"review my-app and tell me what to fix\"")
		e.say("  \"add CSV export to my-app\"")
		e.say("  \"make me a research team\"")
		e.say("\nCommands")
		for _, c := range commands {
			if c.group == "" && !c.hidden {
				e.say("  %-19s %s", strings.TrimSpace("cadrei "+c.name), c.summary)
			}
		}
		e.say("\nOptions for cadrei: --tmux, --no-tmux, --detach, --fresh")
		return 0
	case len(e.args) == 1 && e.args[0] == "advanced":
		e.say("Advanced commands (the orchestrator runs these for you when you ask):")
		// The everyday ones too, with every option they take.
		e.say("\nEveryday")
		for _, c := range commands {
			if c.group == "" && !c.hidden {
				line(e, c)
			}
		}
		for _, g := range groups {
			e.say("\n%s", g)
			for _, c := range commands {
				if c.group == g {
					line(e, c)
				}
			}
		}
		e.say("")
		e.show("", "Commands act on the crew of the folder or project you're in, else your default crew.")
		e.show("", strings.TrimSuffix(plainWords, ":")+".")
		return 0
	}
	return e.failWith("Use " + cmd("cadrei help") + ", or " + cmd("cadrei help advanced") + ".")
}

// plainWords is help's line on talking to the orchestrator.
const plainWords = "Run cadrei and tell your orchestrator what you need, in plain words:"

// line is a command's usage and summary, the summary wrapped at W under
// its own column (2.4).
func line(e *env, c command) {
	words := strings.Join(strings.Fields("cadrei "+c.name+" "+c.usage), " ")
	if c.shown != "" {
		words = "cadrei " + c.shown
	}
	column := spaces(2 + 44 + 1)
	first := fmt.Sprintf("  %-44s ", words)
	if len(words) > 44 {
		e.say("  %s", words)
		first = column
	}
	write(e.stdout, c.summary, first, column)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func without(list []string, s string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
