// Package paths resolves physical paths and the folders cadrei uses.
package paths

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// maxLinks bounds symlink resolution, so a loop ends. For a loop, the
// path Real returns differs from Python's (each stops at a different link
// of the loop); neither resolves it, and no check relies on that path.
const maxLinks = 255

// Real returns the physical form of p: every symlink along the way is
// resolved and "." and ".." are applied to the resolved path, as Python's
// os.path.realpath does. Unlike filepath.EvalSymlinks it works for paths
// that do not exist: from the first missing part on, the rest is kept as
// written. A relative p is taken from the current folder.
func Real(p string) string {
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			wd = "/"
		}
		p = wd + "/" + p
	}
	rest := strings.Split(p, "/")
	cur, links := "/", 0
	for len(rest) > 0 {
		name := rest[0]
		rest = rest[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, name)
		fi, err := os.Lstat(next)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 || links >= maxLinks {
			cur = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			cur = next
			continue
		}
		links++
		if filepath.IsAbs(target) {
			cur = "/"
		}
		rest = append(strings.Split(target, "/"), rest...)
	}
	return cur
}

// Home is the user's home folder, physical. HOME wins, so tests can use a
// fake one.
func Home() string {
	h := os.Getenv("HOME")
	if h == "" {
		h, _ = os.UserHomeDir()
	}
	return Real(h)
}

// Within reports whether path is dir or inside it. Both should be physical.
func Within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// Inside reports whether path is the folder dir or inside it. Folders are
// compared by identity (os.SameFile) as well as by spelling, so another
// spelling of the same folder matches: on macOS's case-insensitive disk,
// ~/.CADREI/WORK is ~/.cadrei/work. When dir isn't there yet, its first
// parent that is there is compared by identity, and the rest of it by
// spelling in any case, as that disk would.
func Inside(path, dir string) bool {
	if Within(path, dir) {
		return true
	}
	base, rest := filepath.Clean(dir), []string{}
	target, err := os.Stat(base)
	for err != nil {
		parent := filepath.Dir(base)
		if parent == base {
			return false
		}
		rest = append([]string{filepath.Base(base)}, rest...)
		base = parent
		target, err = os.Stat(base)
	}
	var below []string // path's folders under p
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		if st, err := os.Stat(p); err == nil && os.SameFile(st, target) && len(below) >= len(rest) {
			same := true
			for i := range rest {
				same = same && strings.EqualFold(below[i], rest[i])
			}
			if same {
				return true
			}
		}
		if p == "/" || p == "." {
			return false
		}
		below = append([]string{filepath.Base(p)}, below...)
	}
}

// Holds reports whether the folder dir holds path (or is it): by Inside,
// or through a folder that shows the whole disk again, as macOS's
// /System/Volumes/Data shows the home folders.
func Holds(dir, path string) bool {
	if Inside(path, dir) {
		return true
	}
	again, err1 := os.Stat(filepath.Join(dir, path))
	st, err2 := os.Stat(path)
	return err1 == nil && err2 == nil && os.SameFile(again, st)
}

// Getwd is the current folder as the kernel has it, not $PWD, which keeps
// whatever spelling the user typed.
func Getwd() (string, error) { return unix.Getwd() }
