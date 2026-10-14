package cadreis

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/conf"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
)

// Where plain cadrei opens the orchestrator.
const (
	OpensInTmux     = "tmux"
	OpensInTerminal = "terminal"
)

// OpensIn is where plain cadrei opens a cadrei's orchestrator, a setting
// of the cadrei: ORCHESTRATOR in the local cadrei.conf (this machine
// only), else in its cadrei.conf, else an old ORCHESTRATOR_TMUX=1. Without
// any (a cadrei from 0.2.0) it opens in the terminal, as before. here says
// the value is this machine's own.
func OpensIn(c Cadrei) (where string, here bool) {
	read := func(file string) map[string]string {
		raw, _ := os.ReadFile(file)
		values, _ := conf.Parse(string(raw))
		return values
	}
	local, shared := read(filepath.Join(LocalDir(c.Path), "cadrei.conf")), read(filepath.Join(c.Path, "cadrei.conf"))
	switch {
	case valid(local["ORCHESTRATOR"]):
		return local["ORCHESTRATOR"], true
	case valid(shared["ORCHESTRATOR"]):
		return shared["ORCHESTRATOR"], false
	}
	switch strings.ToLower(shared["ORCHESTRATOR_TMUX"]) {
	case "1", "yes", "true", "on":
		return OpensInTmux, false
	}
	return OpensInTerminal, false
}

func valid(where string) bool { return where == OpensInTmux || where == OpensInTerminal }

// Where the orchestrator's memory lives (MEMORY in cadrei.conf).
const (
	MemoryInCrew    = "crew"
	MemoryOnMachine = "machine"
)

// MemoryIn is where a cadrei's orchestrator keeps its memory: MEMORY in
// the local cadrei.conf (this machine only), else in its cadrei.conf, else
// the crew. A value it doesn't know counts as the crew.
func MemoryIn(c Cadrei) string {
	for _, file := range []string{filepath.Join(LocalDir(c.Path), "cadrei.conf"), filepath.Join(c.Path, "cadrei.conf")} {
		raw, _ := os.ReadFile(file)
		values, _ := conf.Parse(string(raw))
		switch strings.ToLower(strings.TrimSpace(values["MEMORY"])) {
		case MemoryOnMachine:
			return MemoryOnMachine
		case MemoryInCrew:
			return MemoryInCrew
		}
	}
	return MemoryInCrew
}

// SetOpensIn records where a cadrei's orchestrator opens: in its
// cadrei.conf, committed, or with here in its local cadrei.conf, for this
// machine only.
func SetOpensIn(c Cadrei, where string, here bool) error {
	file := filepath.Join(c.Path, "cadrei.conf")
	if here {
		file = filepath.Join(LocalDir(c.Path), "cadrei.conf")
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := fsx.WriteFile(file, []byte(conf.Set(string(raw), "ORCHESTRATOR", where)), 0o644); err != nil {
		return err
	}
	if !here {
		// The crew's setting is meant to hold: this machine's own override
		// would hide it, so it goes.
		local := filepath.Join(LocalDir(c.Path), "cadrei.conf")
		if raw, err := os.ReadFile(local); err == nil {
			if left := conf.Unset(string(raw), "ORCHESTRATOR"); left != string(raw) {
				if err := fsx.WriteFile(local, []byte(left), 0o644); err != nil {
					return err
				}
			}
		}
		msg := "Open the orchestrator in tmux"
		if where == OpensInTerminal {
			msg = "Open the orchestrator in this terminal"
		}
		Commit(c.Path, msg, "cadrei.conf")
	}
	return nil
}
