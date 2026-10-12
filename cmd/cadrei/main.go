//go:build !windows

// Command cadrei starts, stops and lists member sessions: Claude Code
// sessions in tmux that an orchestrator session leads by name.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/rafi-ramdhani/cadrei/internal/framework"
	rtpkg "github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// go install sets no stamp: Go's record of the module says which tag.
	// A build in a checkout records its commit too, and stays dev.
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" {
		checkout := false
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				checkout = true
			}
		}
		version = framework.ModuleVersion(bi.Main.Version, checkout)
	}
	if !devGuard() {
		os.Exit(1)
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// env is what a command reads and writes; tests pass their own.
type env struct {
	args   []string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	lines  *bufio.Reader // stdin, read by ask, one reader for every question
	eof    bool          // the input ended while cadrei asked
	moved  []string      // cadreis whose notes this run moved in from 0.2.0
	// shown says the first run's screen already showed the tool checks,
	// so the health check after it leaves them out.
	shown bool
	// checking is cadrei --check: the full findings, the identity one too.
	checking bool
	// noAsk keeps a command from asking which crew, even in a terminal:
	// output for programs (ls --json) never waits on a question.
	noAsk bool
	// changes is set for a command that writes to its crew or starts its
	// members (changesCrew): resolve refuses a crew of a newer format.
	changes bool
	// logIn is the runtime the first run's checks found logged out: the
	// setup logs the user in before the orchestrator opens.
	logIn rtpkg.Runtime
	// loggedIn is set once this run logged the user in: the orchestrator
	// then reads the runtime's config where the login wrote it.
	loggedIn bool
}

// say prints a line on stdout as it is, styled for the stream but never
// wrapped: paths and data that scripts read go out whole.
func (e *env) say(format string, a ...any) {
	fmt.Fprintln(e.stdout, render(fmt.Sprintf(format, a...), streamStyled(e.stdout)))
}

// failErr prints an error: one that knows its fix (a lookupError) gets it
// on its own line.
func (e *env) failErr(err error) int {
	if le, ok := err.(*lookupError); ok {
		return e.failWith(le.what, le.fix...)
	}
	return e.fail("%s", err)
}

// fail prints an error on stderr ("cadrei: <message>" when plain) and
// returns exit code 1.
func (e *env) fail(format string, a ...any) int { return e.failWith(fmt.Sprintf(format, a...)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr}
	keepSessionsOut()
	c, rest, err := lookup(args)
	if err != nil {
		return e.failErr(err)
	}
	e.args = rest
	e.changes = changesCrew[c.name]
	// --tmux and init move 0.2.0's notes in too, though help doesn't list
	// them (movesIn).
	if movesIn(c.name) && !inMember() {
		e.moveIn()
	}
	return c.run(e)
}
