// Package project adds, clones, links and trusts a cadrei's projects.
package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
	"github.com/rafi-ramdhani/cadrei/internal/runtime"
)

// TrustRefusal says why folder dir (physical) must not be trusted, or ""
// when it may be. Only the top folder of a project's git repository is
// trusted: never a 0.1.x cadre's top folder, /, the home folder, ~/.cadrei
// or a folder containing it, a cadrei's folder or anything in it (team
// folders included), a folder containing a cadrei, or anything inside a
// folder the runtime loads code from (protected).
func TrustRefusal(dir string, protected []string) string {
	if why := oldCadrei(dir); why != "" {
		return why
	}
	home := paths.Home()
	root := paths.Real(cadreis.Root())
	switch {
	case dir == "/":
		return "it's the root folder"
	case same(dir, home):
		return "it's your home folder"
	case paths.Holds(dir, root):
		return "it's ~/.cadrei, or holds it"
	case paths.Inside(dir, root):
		return "it's inside ~/.cadrei, where cadrei keeps its own files"
	}
	for _, p := range protected {
		if paths.Inside(dir, p) {
			return "it's inside " + cadreis.Tilde(p)
		}
	}
	list, _ := cadreis.List()
	for _, c := range list {
		if paths.Holds(dir, c.Path) {
			return "it's the folder of the crew " + c.Name + ", or holds it"
		}
		if paths.Inside(dir, filepath.Join(c.Path, "teams")) {
			return "it's a team folder"
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil || paths.Real(strings.TrimSpace(string(out))) != dir {
		return "it is not the top folder of a git repository"
	}
	return ""
}

// Trust marks folders as trusted in the runtime, refusing the ones
// TrustRefusal names, and every one in a team member's session: only the
// user trusts folders. It returns a result per folder and, when the
// runtime's config was left alone, a note for the user.
func Trust(rt runtime.Runtime, folders []runtime.Folder) ([]runtime.TrustResult, string) {
	var results []runtime.TrustResult
	var ok []runtime.Folder
	protected := rt.Trust().Protected()
	_, member := os.LookupEnv("CADREI_MEMBER")
	if _, persona := os.LookupEnv("CADRE_PERSONA"); persona {
		member = true
	}
	for _, f := range folders {
		if member {
			results = append(results, runtime.TrustResult{Name: f.Name, State: "refused", Reason: "team members can't trust folders"})
			continue
		}
		if why := TrustRefusal(paths.Real(f.Dir), protected); why != "" {
			results = append(results, runtime.TrustResult{Name: f.Name, State: "refused", Reason: why})
			continue
		}
		ok = append(ok, f)
	}
	marked, note := rt.Trust().Mark(ok)
	return append(results, marked...), note
}

// Line is how cadrei reports one trust result in runtime title's
// settings, and whether it went well.
func Line(r runtime.TrustResult, title string) (string, bool) {
	switch r.State {
	case "trusted":
		return r.Name + " is trusted in " + title, true
	case "already":
		return r.Name + " was already trusted in " + title, true
	case "refused":
		return r.Name + " isn't trusted in " + title + ": " + r.Reason + ".", false
	}
	return title + " will ask to trust " + r.Name + " the first time it opens there.", false
}

// same reports whether a and b are the same folder, however each is
// spelled: on a case-insensitive disk, ~/.CLAUDE is ~/.claude.
func same(a, b string) bool { return paths.Inside(a, b) && paths.Inside(b, a) }
