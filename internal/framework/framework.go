// Package framework writes out the files the cadrei binary carries to
// ~/.cadrei/framework, where tools that read files find them (the
// orchestrator skill), and says which binary cadrei names in hooks.
package framework

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// Dir is ~/.cadrei/framework.
func Dir() string { return filepath.Join(cadreis.Root(), "framework") }

// SkillDir is the written-out orchestrator skill.
func SkillDir() string { return filepath.Join(Dir(), "skills", "cadrei") }

// Version is the version the framework folder was written for, or "".
func Version() string {
	raw, err := os.ReadFile(filepath.Join(Dir(), "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// Sync makes the framework folder hold exactly the embedded skill and this
// version: a file that differs from the embedded one is rewritten, a file
// that is not cadrei's is removed, and a link where a folder or file belongs
// is replaced. It runs on every plain cadrei, since the skill carries the
// orchestrator's consent rules and members can write files with their
// shell. It returns what it restored when VERSION already named this
// version (a change made outside cadrei), and nothing for an upgrade.
func Sync(assets fs.FS, version string) (restored []string, err error) {
	current := Version() == version
	want := map[string][]byte{}
	err = fs.WalkDir(assets, "skills/cadrei", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(assets, p)
		want[p] = data
		return err
	})
	if err != nil {
		return nil, err
	}
	// The folders themselves are cadrei's: a link in their place goes.
	for _, d := range []string{Dir(), filepath.Join(Dir(), "skills"), SkillDir()} {
		if st, err := os.Lstat(d); err == nil && !st.IsDir() {
			os.Remove(d)
			if current {
				restored = append(restored, d+" (not a folder)")
			}
		}
	}
	if err := os.MkdirAll(SkillDir(), 0o755); err != nil {
		return nil, err
	}
	// Anything in the skill folder that is not cadrei's goes.
	filepath.WalkDir(SkillDir(), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(Dir(), p)
		if _, ok := want[filepath.ToSlash(rel)]; !ok {
			os.Remove(p)
			if current {
				restored = append(restored, filepath.ToSlash(rel)+" (not cadrei's)")
			}
		}
		return nil
	})
	for rel, data := range want {
		to := filepath.Join(Dir(), rel)
		if st, err := os.Lstat(to); err == nil && st.Mode().IsRegular() {
			if have, err := os.ReadFile(to); err == nil && string(have) == string(data) {
				continue
			}
		}
		if current {
			restored = append(restored, rel)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return restored, err
		}
		// A new file renamed over the old one, which replaces a link
		// instead of writing through it.
		if err := fsx.WriteFile(to, data, 0o644); err != nil {
			return restored, err
		}
	}
	if !current {
		if err := fsx.WriteFile(filepath.Join(Dir(), "VERSION"), []byte(version+"\n"), 0o644); err != nil {
			return restored, err
		}
	}
	sort.Strings(restored)
	return restored, nil
}

// cellar matches a Homebrew keg's binary: <prefix>/Cellar/cadrei/<version>/bin/cadrei.
var cellar = regexp.MustCompile(`^(.*)/Cellar/cadrei/[^/]+/bin/cadrei$`)

// BrewPrefix is the Homebrew prefix this binary was installed under, from
// its real path (links resolved) in a keg, or "" for any other install.
func BrewPrefix() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if m := cellar.FindStringSubmatch(paths.Real(exe)); m != nil {
		return m[1]
	}
	return ""
}

// Binary is the path cadrei persists wherever it names itself (hooks,
// restart commands): for a Homebrew install, <prefix>/opt/cadrei/bin/cadrei,
// which survives brew upgrade, never the Cellar path; otherwise the
// running binary, symlinks resolved.
func Binary() string {
	exe, err := os.Executable()
	if err != nil {
		return "cadrei"
	}
	real := paths.Real(exe)
	if m := cellar.FindStringSubmatch(real); m != nil {
		return m[1] + "/opt/cadrei/bin/cadrei"
	}
	return real
}

// Placed says why binary must not be named in a hook that every session
// of the runtime runs, or nil: it must not be in a temporary folder, and
// it and every folder above it must belong to the user or root and be
// writable by no one else, so no other user can put a program there.
func Placed(binary string) error {
	p := paths.Real(binary)
	for _, tmp := range []string{os.TempDir(), "/tmp", "/var/tmp", "/var/folders"} {
		if paths.Within(p, paths.Real(tmp)) {
			return fmt.Errorf("%s is in a temporary folder", binary)
		}
	}
	return owned(p)
}

// owned says why the file at p (physical) could be replaced by another
// user, or nil.
func owned(p string) error {
	uid := uint32(os.Getuid())
	for d := p; ; d = filepath.Dir(d) {
		st, err := os.Stat(d)
		if err != nil {
			return err
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot tell who owns %s", d)
		}
		if why := placeOf(d, st.Mode().Perm(), sys.Uid, sys.Gid, uid); why != "" {
			return errors.New(why)
		}
		if d == "/" || d == "." {
			return nil
		}
	}
}

// devVersion matches a version built from source, in any case: dev, a
// snapshot (0.2.2-snapshot, 0.2.1-SNAPSHOT-abc), a -dev or -next one, a
// git describe one (0.2.0-3-gabc, 0.2.1-gabc1234, or a bare commit
// abc1234 from a clone with no tags), Go's pseudo-version for a commit that
// isn't a tag (0.2.1-0.20261010120000-abcdef123456), or a dirty tree. A
// prerelease tag (0.3.0-rc.1) is a release.
var devVersion = regexp.MustCompile(`(?i)^dev$|-snapshot|dirty|-(dev|next)([.-]|$)|-[0-9]+-g[0-9a-f]+|-g[0-9a-f]{7,}|^[0-9a-f]{7,40}$|[0-9]{14}-[0-9a-f]{12}`)

// Kind says how this binary was installed: "homebrew" (in a Homebrew
// keg), "dev" (built from source, devVersion), or "release" (a release
// binary, from install.sh, go install of a tag, or by hand).
func Kind(version string) string {
	exe, _ := os.Executable()
	switch {
	case cellar.MatchString(paths.Real(exe)):
		return "homebrew"
	case devVersion.MatchString(version):
		return "dev"
	}
	return "release"
}

// ModuleVersion is the version for a build with none stamped: Go's own
// record of the module's version when it is a tag (go install ...@v0.2.1),
// or dev. A build from a checkout (checkout: Go recorded its commit) is
// dev, at a tag too, as a pseudo-version, often dirty, always was: only a
// release's own stamp, or go install's, makes a release.
func ModuleVersion(main string, checkout bool) string {
	v := strings.TrimPrefix(main, "v")
	if checkout || v == "" || v == "(devel)" || devVersion.MatchString(v) {
		return "dev"
	}
	return v
}

// placeOf says why a file or folder with this mode, owner and group lets
// another user replace what it holds, or "". It must belong to the user or
// root, and be writable by no one else; a group may write it only when its
// members could become root anyway, or when it is the user's own group.
// Homebrew leaves its prefix group-writable for the admin group (gid 80 on
// macOS), which is why that counts.
func placeOf(d string, mode os.FileMode, owner, group, uid uint32) string {
	switch {
	case owner != uid && owner != 0:
		return d + " belongs to another user"
	case mode&0o002 != 0:
		return "other users can change " + d
	case mode&0o020 != 0 && !groupSafe(group, uid):
		return "other users can change " + d
	}
	return ""
}

// The system's account files, variables for tests.
var (
	groupFile  = "/etc/group"
	passwdFile = "/etc/passwd"
)

// groupSafe reports whether letting a group write a folder gives no one
// more than they have: on macOS, admin (80) and wheel (0), whose members
// can become root; elsewhere, the user's own primary group when no one
// else is in it.
func groupSafe(gid, uid uint32) bool {
	if goruntime.GOOS == "darwin" {
		return gid == 80 || gid == 0
	}
	if gid != uint32(os.Getgid()) {
		return false
	}
	g := strconv.FormatUint(uint64(gid), 10)
	me := strconv.FormatUint(uint64(uid), 10)
	// No user but this one has the group as its primary group...
	raw, err := os.ReadFile(passwdFile)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Split(line, ":")
		if len(f) > 3 && f[3] == g && f[2] != me {
			return false
		}
	}
	// ...and it lists no members.
	raw, err = os.ReadFile(groupFile)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Split(line, ":")
		if len(f) > 3 && f[2] == g {
			return strings.TrimSpace(f[3]) == ""
		}
	}
	return false
}
