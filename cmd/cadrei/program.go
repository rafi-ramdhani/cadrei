//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/framework"
)

// keepFlag keeps the program on a Homebrew install, which every
// uninstall otherwise removes as its plan's last step.
const keepFlag = "--keep-program"

// brewRemove is the command that removes a Homebrew install.
const brewRemove = "brew uninstall cadrei"

// kindForTests, when set, is the install kind the uninstalls see: a test
// can't run a binary from a Homebrew keg.
var kindForTests string

// installKind is how this binary was installed (framework.Kind).
func installKind() string {
	if kindForTests != "" {
		return kindForTests
	}
	return framework.Kind(version)
}

// program is an uninstall's last step: removing the program itself, part
// of the plan the one yes covers. Only a Homebrew install is removed by
// cadrei, with brew, which knows what it installed and what else needs
// it. Any other install is a file cadrei only names for the user to
// delete: install.sh may have put it in a folder other programs share, or
// one that needs sudo, and a plain rm can't be undone.
type program struct {
	remove bool // a Homebrew install, and no --keep-program
}

// planProgram is the program step for this run.
func planProgram(keep bool) program {
	return program{remove: installKind() == "homebrew" && !keep}
}

// step is the plan's last line for it, or "".
func (p program) step() string {
	if p.remove {
		return "remove the cadrei program (" + cmd(brewRemove) + ")"
	}
	return ""
}

// then is the dry run's line on what's left to do after, or "".
func (p program) then(bin string) string {
	if p.remove {
		return ""
	}
	return "Then: " + finishText(bin)
}

// finishProgram removes the program when the plan says so, or says how
// to. It reports whether the program is gone, and code 2 when brew failed.
func (e *env) finishProgram(p program, bin string) (removed bool, code int) {
	if !p.remove {
		e.show("  ", "To finish: "+finishText(bin))
		return false, 0
	}
	return e.removeProgram()
}

// removeProgram runs brew uninstall: after all of cadrei's own cleanup,
// so a brew that fails leaves nothing half done. code is 2 when it
// failed, with the command to run.
func (e *env) removeProgram() (removed bool, code int) {
	c := exec.Command(brewProgram(), "uninstall", "cadrei")
	c.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1")
	out, err := c.CombinedOutput()
	if err != nil {
		why := firstLine(strings.TrimPrefix(strings.TrimSpace(string(out)), "Error: "))
		if why == "" {
			why = err.Error()
		}
		e.warn("", "Couldn't remove the cadrei program: brew says "+strings.TrimSuffix(why, ".")+". Everything else is done.")
		e.show("  ", "Run "+cmd(brewRemove)+" yourself.")
		return false, 2
	}
	e.note("", markDone, "Removed the cadrei program ("+brewRemove+")")
	return true, 0
}

// brewProgram is the brew of the prefix this binary came from, or the one
// on PATH.
func brewProgram() string {
	if prefix := framework.BrewPrefix(); prefix != "" {
		if b := filepath.Join(prefix, "bin", "brew"); exists(b) {
			return b
		}
	}
	return "brew"
}
