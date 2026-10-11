package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Runners names every setting that isn't known to be harmless, hooks that
// aren't samples, and a .git that is a file, read from the files alone; a
// plain repository with a remote has none (review of fix round 4, item 4:
// includeIf, a gitdir file and keys like gpg.ssh.defaultKeyCommand got
// through a list of known dangerous keys).
func TestRunners(t *testing.T) {
	repo := t.TempDir()
	exec.Command("git", "-C", repo, "init", "-q").Run()
	set := func(k, v string) { exec.Command("git", "-C", repo, "config", k, v).Run() }
	set("remote.origin.url", "https://example.com/me/crew.git")
	set("remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	set("branch.main.remote", "origin")
	set("branch.main.merge", "refs/heads/main")
	set("user.name", "T")
	if got := Runners(repo); len(got) != 0 {
		t.Errorf("a plain repo: %v", got)
	}
	ran := filepath.Join(t.TempDir(), "ran")
	set("core.fsmonitor", "touch "+ran)
	set("alias.st", "!touch "+ran)
	set("filter.x.clean", "touch "+ran)
	set("gpg.ssh.defaultKeyCommand", "touch "+ran)
	set("includeIf.gitdir:**.path", "evil.inc")
	set("remote.evil.url", "ext::sh -c touch% "+ran)
	set("pager.log", "touch "+ran)
	os.WriteFile(repo+"/.git/hooks/post-checkout", []byte("#!/bin/sh\n"), 0o755)
	got := Runners(repo)
	for _, want := range []string{"core.fsmonitor", "alias.st", "filter.x.clean", "gpg.ssh.defaultkeycommand", "includeif.gitdir:**.path", "remote.evil.url", "pager.log", "hooks/post-checkout"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s isn't named: %v", want, got)
		}
	}
	if slices.Contains(got, "hooks/pre-commit.sample") || slices.Contains(got, "user.name") || slices.Contains(got, "remote.origin.url") {
		t.Errorf("named too much: %v", got)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("something ran")
	}
	// A .git file sends git to settings elsewhere.
	moved := t.TempDir()
	os.Rename(repo+"/.git", moved+"/real")
	os.WriteFile(repo+"/.git", []byte("gitdir: "+moved+"/real\n"), 0o644)
	if got := Runners(repo); len(got) != 1 || !strings.Contains(got[0], ".git") {
		t.Errorf("a .git file: %v", got)
	}
	if Runners(t.TempDir()) != nil {
		t.Error("not a repo")
	}
}

// Reading runs nothing from the repo's own settings.
func TestReading(t *testing.T) {
	repo := t.TempDir()
	exec.Command("git", "-C", repo, "init", "-q").Run()
	ran := filepath.Join(t.TempDir(), "ran")
	exec.Command("git", "-C", repo, "config", "core.fsmonitor", "touch "+ran+"; false").Run()
	os.WriteFile(repo+"/f", []byte("x\n"), 0o644)
	exec.Command("git", "-C", repo, "-c", "core.fsmonitor=false", "add", "f").Run()
	if out, err := Reading(repo, "ls-files").Output(); err != nil || string(out) != "f\n" {
		t.Errorf("ls-files: %q %v", out, err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("fsmonitor ran")
	}
}

// Runners names every layout that sends git to settings somewhere else,
// made the way git makes them where it can (review of fix round 5, N1 and
// N2): a commondir, a submodule's own settings, worktrees, a worktree
// config, a .gitmodules, a .git below the top, and a top that is itself a
// git folder.
func TestRunnersLayouts(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "protocol.file.allow=always", "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	fresh := func() string {
		repo := t.TempDir()
		git(repo, "init", "-q")
		os.WriteFile(repo+"/notes.md", []byte("x\n"), 0o644)
		git(repo, "add", "notes.md")
		git(repo, "commit", "-qm", "first")
		return repo
	}
	names := func(repo, want string) {
		t.Helper()
		if got := Runners(repo); !slices.Contains(got, want) {
			t.Errorf("%s isn't named: %v", want, got)
		}
	}

	t.Run("commondir", func(t *testing.T) {
		repo := fresh()
		os.WriteFile(repo+"/.git/commondir", []byte(t.TempDir()+"\n"), 0o644)
		names(repo, ".git/commondir")
	})
	t.Run("submodule", func(t *testing.T) {
		sub := fresh()
		repo := fresh()
		git(repo, "submodule", "add", "-q", sub, "inner")
		names(repo, ".git/modules")
		names(repo, ".gitmodules")
		names(repo, "inner/.git")
	})
	t.Run("worktrees", func(t *testing.T) {
		repo := fresh()
		git(repo, "worktree", "add", "-q", filepath.Join(t.TempDir(), "wt"))
		names(repo, ".git/worktrees")
	})
	t.Run("worktree config", func(t *testing.T) {
		repo := fresh()
		git(repo, "config", "extensions.worktreeConfig", "true")
		git(repo, "config", "--worktree", "core.editor", "vi")
		names(repo, ".git/config.worktree")
		names(repo, "extensions.worktreeconfig")
	})
	// Review of fix round 6 (Runners, Low): git's old remote files, a
	// config git can't read and a folder cadrei can't read each count; an
	// empty branches folder, as older git makes, doesn't.
	t.Run("old remote files", func(t *testing.T) {
		repo := fresh()
		os.MkdirAll(repo+"/.git/branches", 0o755)
		if got := Runners(repo); len(got) != 0 {
			t.Errorf("a fresh repo with an empty branches folder: %v", got)
		}
		os.MkdirAll(repo+"/.git/remotes", 0o755)
		os.WriteFile(repo+"/.git/remotes/origin", []byte("URL: /elsewhere/repo\n"), 0o644)
		os.WriteFile(repo+"/.git/branches/origin", []byte("/elsewhere/repo\n"), 0o644)
		names(repo, ".git/remotes")
		names(repo, ".git/branches")
	})
	t.Run("unreadable", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root reads anything")
		}
		repo := fresh()
		os.WriteFile(repo+"/.git/config", []byte("[core\n"), 0o644)
		names(repo, "config (git can't read it)")
		os.MkdirAll(repo+"/locked", 0o755)
		os.Chmod(repo+"/locked", 0)
		t.Cleanup(func() { os.Chmod(repo+"/locked", 0o755) })
		names(repo, "locked (cadrei can't read it)")
	})
	t.Run("gitmodules alone", func(t *testing.T) {
		repo := fresh()
		os.WriteFile(repo+"/.gitmodules", []byte("[submodule \"x\"]\n\tpath = x\n"), 0o644)
		names(repo, ".gitmodules")
	})
	t.Run("nested repo", func(t *testing.T) {
		repo := fresh()
		os.MkdirAll(repo+"/deep/down", 0o755)
		git(repo+"/deep/down", "init", "-q")
		names(repo, "deep/down/.git")
	})
	t.Run("nested .git file", func(t *testing.T) {
		repo := fresh()
		os.MkdirAll(repo+"/deep", 0o755)
		os.WriteFile(repo+"/deep/.git", []byte("gitdir: /elsewhere\n"), 0o644)
		names(repo, "deep/.git")
	})
	t.Run("a git folder at the top", func(t *testing.T) {
		bare := t.TempDir()
		git(bare, "init", "-q", "--bare")
		if got := Runners(bare); len(got) != 1 || !strings.Contains(got[0], "HEAD and config") {
			t.Errorf("a bare repo: %v", got)
		}
	})
	t.Run("a plain repo has none", func(t *testing.T) {
		if got := Runners(fresh()); len(got) != 0 {
			t.Errorf("a plain repo: %v", got)
		}
	})
}

// A remote on this machine is another repo whose hooks run on a push, so
// only a network URL is harmless (review of fix round 5, N3). The local
// repo here has a pre-receive hook that a push would run.
func TestRunnersLocalRemote(t *testing.T) {
	far := t.TempDir()
	exec.Command("git", "-C", far, "init", "-q", "--bare").Run()
	os.WriteFile(far+"/hooks/pre-receive", []byte("#!/bin/sh\ntouch ran\n"), 0o755)
	for _, url := range []string{far, "file://" + far, "../far", "./far", "far", "C:/far"} {
		repo := t.TempDir()
		exec.Command("git", "-C", repo, "init", "-q").Run()
		exec.Command("git", "-C", repo, "config", "remote.origin.url", url).Run()
		if got := Runners(repo); !slices.Contains(got, "remote.origin.url") {
			t.Errorf("%s isn't named: %v", url, got)
		}
	}
	for _, url := range []string{"https://example.com/me/crew.git", "ssh://git@example.com/me/crew.git", "git@example.com:me/crew.git", "git://example.com/crew.git"} {
		repo := t.TempDir()
		exec.Command("git", "-C", repo, "init", "-q").Run()
		exec.Command("git", "-C", repo, "config", "remote.origin.url", url).Run()
		if got := Runners(repo); len(got) != 0 {
			t.Errorf("%s: %v", url, got)
		}
	}
	// A branch's remote that is a path or URL is a remote of its own.
	repo := t.TempDir()
	exec.Command("git", "-C", repo, "init", "-q").Run()
	exec.Command("git", "-C", repo, "config", "branch.main.remote", far).Run()
	exec.Command("git", "-C", repo, "config", "branch.dev.pushRemote", "origin").Run()
	got := Runners(repo)
	if !slices.Contains(got, "branch.main.remote") || slices.Contains(got, "branch.dev.pushremote") {
		t.Errorf("branch remotes: %v", got)
	}
}
