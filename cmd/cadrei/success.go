//go:build !windows

package main

import (
	"io"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	rtpkg "github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// The success screen (3.1): the work lines a setup prints as it goes,
// then the line that says what opens. The same ending serves a new crew, a
// restore and a plug-in.

// work prints a line of a setup's work.
func (e *env) work(m mark, text string) { e.note("  ", m, text) }

// lastLine is what the success screen ends with: what opens, and where.
func lastLine(tmux, resumes bool) string {
	line := "Opening your orchestrator here."
	switch {
	case tmux && session.Default().InTmux() == session.SameServer:
		line = "Switching to your orchestrator."
	case tmux && session.Default().InTmux() == session.NotInTmux:
		line = "Opening your orchestrator in tmux."
	}
	if resumes {
		line += " It picks up your last chat."
	}
	return line
}

// resumes reports whether a crew's orchestrator will pick its last chat
// up: a record cadrei wrote, whose transcript is still there.
func resumes(rt rtpkg.Runtime, c cadreis.Cadrei) bool {
	record := session.RecordPath(rt.BuildDir(c.Path), orchestrator.Name(c.Name))
	conv := session.Plan(rt, record, c.Path, false)
	return conv.Resume != "" && conv.Resume == cadreis.OrchestratorID(c)
}

// finish ends a setup: notice (a restore's trust paragraph) when there is
// one, the last line, and, with wait, a key before tmux takes the screen,
// so a warning gets read. It reports whether the orchestrator opens.
func (e *env) finish(tmux bool, last, notice string, wait bool) bool {
	if notice != "" {
		e.say("")
		e.screen(notice)
	}
	e.say("")
	para := last
	if strings.HasPrefix(last, "Opening your orchestrator here.") {
		para += " " + stillRuns
	}
	e.screen(para)
	if wait && tmux && session.Default().InTmux() != session.OtherServer {
		e.say("")
		key, ok := e.readKey(e.wrapScreen("Press Enter to open your orchestrator."), "q")
		return ok && key == keyEnter
	}
	return true
}

// stillRuns is the plain terminal's word on the team.
var stillRuns = "Your team still runs in tmux, and you can watch any of them with " + cmd("cadrei attach") + "."

// countWriter counts the lines written through it, so a block can be
// drawn again in place.
type countWriter struct {
	w     io.Writer
	lines int
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.lines += strings.Count(string(p), "\n")
	return c.w.Write(p)
}

// unwrapWriter is the stream under a countWriter, for the style checks.
func unwrapWriter(w io.Writer) io.Writer {
	if c, ok := w.(*countWriter); ok {
		return c.w
	}
	return w
}
