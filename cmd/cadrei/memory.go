//go:build !windows

package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/memory"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// envMemoryNotice tells the orchestrator's hooks that this open told the
// user the crew keeps its memory: its snapshot says so, and nothing is
// committed until a later session.
const envMemoryNotice = "CADREI_MEMORY_NOTICE"

// The crew's memory commits.
const (
	memoryCommit = "Update the orchestrator's memory"
	memoryLeft   = "Keep the orchestrator's memory on this machine"
)

// crewMemory gets the orchestrator's memory ready for an open: with
// MEMORY=crew, it moves the notes on this machine into the crew and
// commits them (once the user was told), and returns the crew's memory
// folder; with MEMORY=machine, it moves them back out once and returns
// "". notice reports that this open tells the user, once per crew.
func (e *env) crewMemory(rt runtime.Runtime, c cadreis.Cadrei) (dir string, notice bool) {
	crew := paths.Real(c.Path)
	folders := rt.Memory().Folders(crew)
	if cadreis.MemoryIn(c) == cadreis.MemoryOnMachine {
		e.keepOnMachine(c, crew, folders)
		return "", false
	}
	res, err := memory.MoveIn(crew, folders, memory.Machine())
	if err != nil {
		e.warn("", "Your orchestrator's memory couldn't move into your crew: "+strings.TrimSuffix(err.Error(), ".")+".",
			"It stays where it was, and your orchestrator keeps using it there.")
		return "", false
	}
	if len(res.Copies) > 0 {
		e.note("  ", markWarn, fmt.Sprintf("%d of your orchestrator's notes differed from the crew's, so both are kept: %s.", len(res.Copies), strings.Join(res.Copies, ", ")))
	}
	if _, err := os.Stat(memory.Dir(crew)); err != nil {
		return memory.Dir(crew), false
	}
	// Committed with the memory: a crew with none gets no stray file.
	memory.Attributes(crew)
	if cadreis.MemoryNotice(c) == "" {
		if cadreis.SetMemoryNotice(c, time.Now().Format(time.RFC3339)) == nil {
			e.note("", markDone, "Your orchestrator's memory now lives in your crew (.memory/), so your backup keeps it")
		}
		return memory.Dir(crew), true
	}
	memory.Commit(crew, memoryCommit)
	return memory.Dir(crew), false
}

// keepOnMachine moves the crew's notes back into the runtime's own folder
// for it, once, and commits their removal from the crew.
func (e *env) keepOnMachine(c cadreis.Cadrei, crew string, folders []string) {
	for _, d := range folders[1:] {
		memory.RemovePointer(d)
	}
	if _, err := os.Stat(memory.Dir(crew)); err != nil {
		memory.RemovePointer(folders[0])
		// A removal that wasn't committed yet (git was busy).
		memory.CommitRemoval(crew, memoryLeft)
		return
	}
	if _, err := memory.MoveOut(crew, folders[0], memory.Machine()); err != nil {
		e.warn("", "Your orchestrator's memory couldn't move back to this machine: "+strings.TrimSuffix(err.Error(), ".")+".",
			"It stays in your crew (.memory/) for now, and your orchestrator uses its own folder.")
		return
	}
	memory.CommitRemoval(crew, memoryLeft)
	cadreis.SetMemoryNotice(c, "")
	e.note("", markDone, "Your orchestrator's memory is back on this machine only. Earlier backups still have it in their history.")
}

// commitMemory is the session start hook's commit: once the user was told
// and outside the session that told them.
func commitMemory(c cadreis.Cadrei) {
	if os.Getenv(envMemoryNotice) != "" || cadreis.MemoryIn(c) != cadreis.MemoryInCrew || cadreis.MemoryNotice(c) == "" {
		return
	}
	memory.Commit(paths.Real(c.Path), memoryCommit)
}

// memoryLine is the snapshot's word on the orchestrator's memory.
func memoryLine(c cadreis.Cadrei) string {
	if cadreis.MemoryIn(c) == cadreis.MemoryOnMachine {
		return "kept on this machine only (MEMORY=machine)"
	}
	crew := paths.Real(c.Path)
	if _, err := os.Stat(memory.Dir(crew)); err != nil {
		return "kept in the crew (.memory/), nothing saved yet"
	}
	line := "kept in the crew (.memory/, " + count(memory.Count(crew), "note") + ")"
	if os.Getenv(envMemoryNotice) != "" {
		line += ", notice not given yet"
	}
	return line
}

// backupLine is the snapshot's word on the crew's backup, as git last saw
// it (no network).
func backupLine(c cadreis.Cadrei) string {
	crew := paths.Real(c.Path)
	if !memory.HasRemote(crew) {
		return "none set up"
	}
	switch n := memory.Unpushed(crew); {
	case n < 0:
		return "a remote, but this branch tracks no upstream"
	case n == 0:
		return "up to date"
	default:
		return count(n, "commit") + " not pushed"
	}
}
