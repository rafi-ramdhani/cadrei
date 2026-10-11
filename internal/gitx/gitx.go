// Package gitx runs git on folders cadrei doesn't vouch for yet: a repo
// about to be trusted, or an archive the user hasn't agreed to bring back.
package gitx

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Reading runs git so that nothing in the repo's own config runs a
// program: git runs core.fsmonitor whenever it reads the index, and hooks
// from core.hooksPath. cadrei reads with it, and makes its own commits in
// a crew with it too: hooks are the user's, not cadrei's. A push uses
// Pushing, so cadrei's own pre-push check still runs. git uses only the
// repo at dir (pinned).
func Reading(dir string, args ...string) *exec.Cmd {
	argv := append([]string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull}, args...)
	return pinned(exec.Command("git", argv...), dir)
}

// Pushing runs git for a push with no fsmonitor, keeping the hooks: the
// pre-push one is cadrei's check for passwords and keys. Like Reading, it
// uses only the repo at dir.
func Pushing(ctx context.Context, dir string, args ...string) *exec.Cmd {
	return pinned(exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.fsmonitor=false"}, args...)...), dir)
}

// pinned keeps git from looking for a repo above dir: with no repo at dir
// (no .git, or one git won't take), git would search the folders above
// and use the first repo it finds, with that repo's settings, such as a
// ~/.cadrei/.git that came from somewhere else. A repo's own .git file
// (a worktree or a submodule) still leads git to its folder.
func pinned(c *exec.Cmd, dir string) *exec.Cmd {
	if abs, err := filepath.Abs(dir); err == nil {
		c.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(abs))
	}
	return c
}

// Runners lists what in a repo's own git settings could run a program
// when git is used there. It names every config key that isn't on a short
// list of harmless ones (so an include, an alias, a filter, a pager or a
// signing, transport or fsmonitor command all count, and so does anything
// git adds later), a remote that isn't a network URL (a local repo's hooks
// run on a push to it), hooks that aren't git's samples, and every layout
// that sends git to settings somewhere else: a .git that is a file, a
// commondir, worktrees, a worktree config, submodules (their own settings
// run on a plain git status), a .git anywhere below the top, and a top
// that is itself a git folder. Only files are read: no git runs in the
// repo, and includes aren't followed (an include is named itself).
func Runners(repo string) []string {
	var out []string
	gitDir := filepath.Join(repo, ".git")
	st, err := os.Lstat(gitDir)
	switch {
	case err != nil:
		// No .git: a top that holds HEAD and config is a git folder itself.
		if exists(filepath.Join(repo, "HEAD")) && exists(filepath.Join(repo, "config")) {
			return []string{"HEAD and config at the top (a git folder itself)"}
		}
		return nil
	case !st.IsDir():
		out = append(out, ".git (a file that sends git elsewhere)")
	default:
		for _, f := range []string{"commondir", "modules", "worktrees", "config.worktree"} {
			if exists(filepath.Join(gitDir, f)) {
				out = append(out, ".git/"+f)
			}
		}
		// git's old files for remotes: one can name a local repo as
		// origin, past the config's URL check. Older git makes an empty
		// branches folder in every repo.
		for _, f := range []string{"remotes", "branches"} {
			if entries, _ := os.ReadDir(filepath.Join(gitDir, f)); len(entries) > 0 {
				out = append(out, ".git/"+f)
			}
		}
		out = append(out, configRunners(filepath.Join(gitDir, "config"))...)
		entries, _ := os.ReadDir(filepath.Join(gitDir, "hooks"))
		for _, e := range entries {
			if !e.IsDir() && !strings.HasSuffix(e.Name(), ".sample") {
				out = append(out, "hooks/"+e.Name())
			}
		}
	}
	return append(out, nested(repo)...)
}

// configRunners lists a config file's keys that aren't harmless.
func configRunners(file string) []string {
	var out []string
	// git config --file reads that one file as plain settings; it runs
	// nothing from them, and with no system or global file it reads only
	// this one.
	cfg := exec.Command("git", "config", "--file", file, "--list", "-z")
	cfg.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	raw, err := cfg.Output()
	if err != nil {
		// A config git can't read can't be checked: it counts. None at
		// all is a repo with no settings.
		if exists(file) {
			return []string{"config (git can't read it)"}
		}
		return nil
	}
	// -z: each entry is the key, a newline, the value, then a NUL.
	for _, entry := range strings.Split(string(raw), "\x00") {
		key, value, _ := strings.Cut(entry, "\n")
		if key != "" && !harmless(strings.ToLower(key), value) && !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	return out
}

// nestedLimit is how many entries nested looks at: past it, what wasn't
// looked at counts.
var nestedLimit = 200000

// nested lists a .gitmodules or a .git below the top of repo (a submodule
// or another repo, which git can run from), as paths relative to repo.
func nested(repo string) []string {
	var out []string
	seen := 0
	filepath.WalkDir(repo, func(p string, e fs.DirEntry, err error) error {
		if p == repo {
			return nil
		}
		if err != nil {
			// A folder that can't be read can't be checked: it counts.
			rel, _ := filepath.Rel(repo, p)
			out = append(out, filepath.ToSlash(rel)+" (cadrei can't read it)")
			return nil
		}
		if seen++; seen > nestedLimit {
			out = append(out, "more files than cadrei can check")
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(repo, p)
		switch {
		case rel == ".git":
			return filepath.SkipDir
		case strings.EqualFold(e.Name(), ".gitmodules") && !e.IsDir():
			out = append(out, filepath.ToSlash(rel))
		case strings.EqualFold(e.Name(), ".git"):
			out = append(out, filepath.ToSlash(rel))
			if e.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return out
}

// exists reports whether there's anything at p.
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// harmlessKeys are the config keys git and cadrei write in a plain
// repository, or a user sets for it, that run nothing.
var harmlessKeys = map[string]bool{
	"core.repositoryformatversion": true, "core.filemode": true, "core.bare": true, "core.logallrefupdates": true,
	"core.ignorecase": true, "core.precomposeunicode": true, "core.symlinks": true, "core.autocrlf": true,
	"core.eol": true, "core.safecrlf": true, "core.quotepath": true, "init.defaultbranch": true,
	"user.name": true, "user.email": true, "pull.rebase": true, "pull.ff": true, "push.default": true,
	"push.autosetupremote": true, "fetch.prune": true, "gc.auto": true, "extensions.objectformat": true,
}

// harmless reports whether a config key (lower case) and its value run
// nothing: a key on the list, a remote's or branch's plain settings, or a
// color or advice setting.
func harmless(key, value string) bool {
	if harmlessKeys[key] || strings.HasPrefix(key, "color.") || strings.HasPrefix(key, "advice.") {
		return true
	}
	section, rest, ok := strings.Cut(key, ".")
	if !ok {
		return false
	}
	i := strings.LastIndex(rest, ".")
	if i < 0 {
		return false
	}
	name := rest[i+1:]
	switch section {
	case "remote":
		switch name {
		case "url", "pushurl":
			return networkURL(value)
		case "fetch", "push", "prune", "tagopt", "mirror":
			return true
		}
	case "branch":
		switch name {
		case "merge", "rebase":
			return true
		case "remote", "pushremote":
			// A remote's name, or "." for this repo: a path or URL here is
			// a remote of its own, and goes through the same check.
			return value == "." || remoteName.MatchString(value)
		}
	}
	return false
}

// remoteName is a plain remote name: no path, URL or transport in it.
var remoteName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// scpURL is git's host:path form, as user@host:path.
var scpURL = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/]`)

// networkURL reports whether a remote's URL reaches another machine over
// a network transport, which runs nothing here. A local path (or file://)
// is another repo on this machine, whose hooks run on a push or fetch, and
// a transport like ext:: runs the command its URL holds.
func networkURL(url string) bool {
	if strings.Contains(url, "::") {
		return false
	}
	if scheme, _, ok := strings.Cut(url, "://"); ok {
		switch strings.ToLower(scheme) {
		case "https", "http", "ssh", "git", "git+ssh", "ssh+git":
			return true
		}
		return false
	}
	return scpURL.MatchString(url)
}
