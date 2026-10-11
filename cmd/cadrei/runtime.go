//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/backup"
	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/conf"
	"github.com/rafi-ramdhani/cadrei/internal/framework"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
	"github.com/rafi-ramdhani/cadrei/internal/session"
	// The Claude Code adapter, the only runtime.
	_ "github.com/rafi-ramdhani/cadrei/internal/runtime/claude"
)

// cutKeys are cadrei.conf keys that 0.2.0 no longer reads, and what to do
// instead.
var cutKeys = []struct{ key, why string }{
	{"RUNTIME", "There's one runtime, so there's nothing to choose."},
	{"ORCHESTRATOR_RUNTIME", "There's one runtime, so there's nothing to choose."},
	{"ORCHESTRATOR_PERMISSION_MODE", "PERMISSION_MODE sets your orchestrator's mode too."},
}

// conf reads the cadrei's cadrei.conf, warning about lines it ignores and
// keys it no longer reads.
func (e *env) conf(r *cadreis.Resolved) map[string]string {
	raw, _ := os.ReadFile(filepath.Join(r.Path, "cadrei.conf"))
	// Lines and keys cadrei doesn't use are kept, and only --check names
	// them (unknownConf).
	values, _ := conf.Parse(string(raw))
	for _, k := range cutKeys {
		if _, set := values[k.key]; set {
			e.warn("", "cadrei.conf sets "+k.key+", which cadrei doesn't read anymore.", k.why)
		}
	}
	return values
}

// cadreiRuntime is the runtime every session runs: Claude Code (a test
// build may name another, see runtime_cadreitest.go).
func (e *env) cadreiRuntime(r *cadreis.Resolved) (runtime.Runtime, bool) {
	rt, err := runtime.Get(runtimeName())
	if err != nil {
		e.fail("%s", err)
		return nil, false
	}
	return rt, true
}

// places are the folders a runtime's fixed denies must name: ~/.cadrei as
// resolved, the resolved cadrei (registered or not, as with CADREI_HOME), and
// every known cadrei.
func places(r *cadreis.Resolved) runtime.Places {
	p := runtime.Places{Root: paths.Real(cadreis.Root()), Archive: paths.Real(cadreis.ArchiveRoot()), Cadreis: []string{r.Path}, Binary: framework.Binary()}
	if list, err := cadreis.List(); err == nil {
		for _, c := range list {
			if c.Path != r.Path {
				p.Cadreis = append(p.Cadreis, c.Path)
			}
		}
	}
	return p
}

// A cadrei's local folder sits beside the runtime's build folder, so the
// fixed denies that keep members off the build folder cover it too.
func init() {
	if rt, err := runtime.Get(runtime.Default()); err == nil {
		backup.Local = filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(rt.BuildDir("/c"))), "local"))
	}
	// Pinned to the default runtime's folder (.claude/local), whatever
	// runtime starts the members: a crew's notes of this machine never
	// move with a second runtime.
	cadreis.LocalDir = func(cadrei string) string {
		rt, err := runtime.Get(runtime.Default())
		if err != nil {
			return filepath.Join(cadrei, ".local")
		}
		return filepath.Join(filepath.Dir(rt.BuildDir(cadrei)), "local")
	}
	cadreis.ChatTime = func(id, dir string) time.Time {
		rt, err := runtime.Get(runtimeName())
		if err != nil {
			return time.Time{}
		}
		if fi, err := os.Stat(rt.Sessions().TranscriptFile(id, dir)); err == nil {
			return fi.ModTime()
		}
		return time.Time{}
	}
}

// knownKeys are the cadrei.conf keys cadrei reads, besides cutKeys.
var knownKeys = []string{"FORMAT", "PERMISSION_MODE", "ORCHESTRATOR", "ORCHESTRATOR_TMUX", keyModel, keyEffort, keyOrchestratorModel, keyOrchestratorEffort}

// unknownConf lists what a cadrei.conf holds that cadrei doesn't use: keys
// it doesn't know, and lines that aren't KEY=VALUE. They're kept as they
// are, and only cadrei --check says so.
func unknownConf(path string) []string {
	raw, _ := os.ReadFile(filepath.Join(path, "cadrei.conf"))
	values, warnings := conf.Parse(string(raw))
	var out []string
	for k := range values {
		cut := false
		for _, c := range cutKeys {
			cut = cut || c.key == k
		}
		if !cut && !contains(knownKeys, k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	for range warnings {
		out = append(out, "a line that isn't KEY=VALUE")
	}
	return out
}

// moveIn moves what 0.2.0 kept about each cadrei in ~/.cadrei/config into
// the cadrei itself (once; later runs find nothing to move).
func (e *env) moveIn() {
	list, _ := cadreis.List()
	// A crew reached through CADREI_HOME, outside ~/.cadrei, too, when it is
	// one and 0.2.0 kept notes about it: CADREI_HOME can name any folder,
	// which is never written to otherwise.
	if r, err := cadreis.Resolve(wdOrHome(), nil); err == nil && !cadreis.Inside(r.Path) && r.Cadrei.Present() && cadreis.HasOldNotes(r.Cadrei) {
		list = append(list, r.Cadrei)
	}
	for _, c := range list {
		moved, lost, err := cadreis.MoveIn(c)
		switch {
		case err != nil:
			e.warn("", sentence(err.Error()))
		case moved:
			e.moved = append(e.moved, c.Name)
		}
		if len(lost) > 0 {
			kept := unbroken(display(filepath.Join(cadreis.LocalDir(c.Path), "places.0.2.0.json")))
			e.warn("", "cadrei 0.2.0 had other folders for "+andList(lost)+" in "+c.Name+". This machine keeps the current ones, and 0.2.0's are in "+kept+".")
		}
	}
}

// keepSessionsOut keeps every runtime's session variables out of tmux, so
// a tmux server started from inside a session never holds them.
func keepSessionsOut() {
	for _, n := range runtime.Supported() {
		if r, err := runtime.Get(n); err == nil {
			session.KeepOut(r.SessionVars()...)
		}
	}
}
