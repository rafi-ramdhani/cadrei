//go:build !windows

package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// runHookGuard is cadrei hook guard: a member's runtime runs it before
// each shell command, and a command it refuses doesn't run (exit 2, the
// reason on stderr). It refuses cadrei's uninstall and restore in any form
// it can see, erring on the side of refusing, and anything it can't read.
// It's one of three layers: the members' deny rules come first, and the
// commands themselves refuse a member's window (memberAnyway). A command
// it can't see into, such as a script file the member wrote, is left to
// those, and to the member's instructions.
func runHookGuard(e *env) int {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		return guardRefuses(e, "cadrei can't check this command ("+err.Error()+"), so it's blocked.")
	}
	command, shell, err := rt.Sessions().ShellCommand(e.stdin)
	switch {
	case err != nil:
		return guardRefuses(e, "cadrei couldn't read this command ("+err.Error()+"), so it's blocked.")
	case !shell:
		return 0
	case removesCrews(command):
		return guardRefuses(e, "Team members can't run cadrei uninstall or cadrei restore, in any form. Ask the user to do it from the orchestrator.")
	}
	return 0
}

// guardRefuses blocks the command, saying why where the runtime shows it.
func guardRefuses(e *env, why string) int {
	fmt.Fprintln(e.stderr, why)
	return 2
}

// expansion is a shell expansion the guard can't know the value of.
var expansion = regexp.MustCompile(`\$\{[^}]*\}|\$\([^)]*\)|\$[a-z_][a-z0-9_]*|\$[0-9@*#?$!-]|` + "`[^`]*`")

// guardWord is a word of a command line, as far as the guard reads one.
var guardWord = regexp.MustCompile(`[a-z0-9_.\-/]+`)

// removesCrews reports whether a command line may run cadrei's uninstall
// or restore: one of those words, with cadrei's program by any path (or
// cadre 0.1.x's), or with something that can hide the program's name: an
// expansion ($, a backtick), or another shell, eval, source or xargs.
// Quotes and backslashes are read through (un"inst"all is uninstall), and
// case doesn't count, as a case-insensitive disk doesn't count it.
func removesCrews(command string) bool {
	text := strings.ToLower(command)
	hides := strings.ContainsAny(text, "$`")
	text = strings.NewReplacer(`"`, "", `'`, "", `\`, "").Replace(text)
	// Read as written, and with each expansion taken out, as an empty one
	// expands: unin${x}stall runs uninstall.
	text += "\n" + expansion.ReplaceAllString(text, "")
	program, sub := false, false
	for _, w := range guardWord.FindAllString(text, -1) {
		switch w[strings.LastIndex(w, "/")+1:] {
		case "cadrei", "cadre":
			program = true
		case "uninstall", "restore":
			sub = true
		case "sh", "bash", "zsh", "dash", "ksh", "fish", "eval", "source", "xargs", "exec", "env":
			hides = true
		}
	}
	return sub && (program || hides)
}
