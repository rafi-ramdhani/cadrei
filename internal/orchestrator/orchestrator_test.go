package orchestrator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/proc"
)

func TestLock(t *testing.T) {
	path := LockPath(t.TempDir())
	if ReadLock(path) != nil {
		t.Error("a lock without a file")
	}
	cmd := exec.Command("sleep", "30")
	cmd.Start()
	defer cmd.Process.Kill()
	if _, err := WriteLock(path, cmd.Process.Pid, Terminal, "/dev/ttys001", ""); err != nil {
		t.Fatal(err)
	}
	l := ReadLock(path)
	if l == nil || l.PID != cmd.Process.Pid || l.Mode != Terminal || l.TTY != "/dev/ttys001" || l.Command != "sleep" {
		t.Fatalf("lock %+v", l)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	// Another start time under the same pid (a reused pid) is stale.
	os.WriteFile(path, []byte(strings.Replace(mustRead(t, path), l.Start, "1.000000", 1)), 0o600)
	if ReadLock(path) != nil {
		t.Error("a reused pid was taken for the orchestrator")
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("reading a lock removed it")
	}
	ClearStale(path)
	if _, err := os.Stat(path); err == nil {
		t.Error("a stale lock was not cleared")
	}
	// A dead process is stale.
	WriteLock(path, cmd.Process.Pid, Tmux, "", "cadrei-work")
	cmd.Process.Kill()
	cmd.Wait()
	if ReadLock(path) != nil {
		t.Error("a dead process's lock is live")
	}
	os.WriteFile(path, []byte("not json"), 0o600)
	if ReadLock(path) != nil {
		t.Error("an unreadable lock is live")
	}
}

func mustRead(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPrompt(t *testing.T) {
	p := string(Prompt([]byte("TEXT\n"), "work", "/h/.cadrei/work"))
	if p != "TEXT\n\nYou are the orchestrator of the crew `work` (`/h/.cadrei/work`).\n" {
		t.Errorf("%q", p)
	}
}

// A wrapper that execs the real program (npm's #!/usr/bin/env node, a
// version manager's shim) changes the command name but not the process:
// its lock stays live.
func TestALockSurvivesExec(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)
	script := filepath.Join(dir, "wrapper")
	os.WriteFile(script, []byte("#!/usr/bin/env sh\nexec sleep 30\n"), 0o755)
	cmd := exec.Command(script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if _, err := WriteLock(path, cmd.Process.Pid, Terminal, "", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // env, then sh, then sleep
	if ReadLock(path) == nil {
		t.Error("the lock of a process that exec'd was taken for stale")
	}
}

// Only a regular file of the user's, of a lock's size, is read; anything
// else is removed as stale, and a FIFO never blocks.
func TestALockCadreiDidNotWriteIsStale(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)
	cmd := exec.Command("sleep", "30")
	cmd.Start()
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	good, _ := WriteLock(path, cmd.Process.Pid, Terminal, "", "")
	raw, _ := os.ReadFile(path)
	os.Remove(path)
	other := filepath.Join(dir, "real.lock")
	os.WriteFile(other, raw, 0o600)
	cases := map[string]func(){
		"a FIFO":            func() { syscall.Mkfifo(path, 0o600) },
		"a symlink":         func() { os.Symlink(other, path) },
		"a symlink to FIFO": func() { syscall.Mkfifo(other+".fifo", 0o600); os.Symlink(other+".fifo", path) },
		"a large file":      func() { os.WriteFile(path, append(raw, make([]byte, 8192)...), 0o600) },
		"a hard link":       func() { os.Link(other, path) },
	}
	for name, plant := range cases {
		plant()
		done := make(chan *Lock, 1)
		go func() { done <- ReadLock(path) }()
		select {
		case l := <-done:
			if l != nil {
				t.Errorf("%s was read as a lock", name)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s blocked ReadLock", name)
		}
		ClearStale(path)
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("%s was left in place", name)
			os.Remove(path)
		}
	}
	if b, _ := os.ReadFile(other); string(b) != string(raw) {
		t.Error("the file a link pointed to was changed")
	}
	// RemoveLock removes only the lock it was given.
	WriteLock(path, cmd.Process.Pid, Terminal, "", "")
	stranger := *good
	stranger.Start = "1.000000"
	RemoveLock(path, &stranger)
	if _, err := os.Stat(path); err != nil {
		t.Error("another run's lock was removed")
	}
	RemoveLock(path, good)
	if _, err := os.Stat(path); err == nil {
		t.Error("our own lock was not removed")
	}
}

// Lock fields are printed: a lock with a mode, terminal or session cadrei
// does not write is not read, so it cannot put escape sequences on the
// user's terminal or skip the session check.
func TestOnlyLocksCadreiWritesAreRead(t *testing.T) {
	path := LockPath(t.TempDir())
	cmd := exec.Command("sleep", "30")
	cmd.Start()
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	info, _ := proc.Of(cmd.Process.Pid)
	write := func(mode, tty, session string) {
		raw, _ := json.Marshal(Lock{PID: cmd.Process.Pid, Start: info.Start, Mode: mode, TTY: tty, Session: session})
		os.WriteFile(path, raw, 0o600)
	}
	write(Terminal, "/dev/ttys004", "")
	if ReadLock(path) == nil {
		t.Fatal("a lock cadrei writes was not read")
	}
	write(Tmux, "", "cadrei-work")
	if ReadLock(path) == nil {
		t.Fatal("a tmux lock cadrei writes was not read")
	}
	for _, bad := range []struct{ mode, tty, session string }{
		{"x", "", ""},
		{"", "", ""},
		{Terminal, "/dev/tty\x1b]0;evil\x07", ""},
		{Terminal, "ttys004", ""},
		{Terminal, "", "cadrei-work"},
		{Tmux, "", "evil\x1b[2J"},
		{Tmux, "", ""},
		{Tmux, "/dev/ttys004", "cadrei-work"},
	} {
		write(bad.mode, bad.tty, bad.session)
		if ReadLock(path) != nil {
			t.Errorf("read a lock with mode %q, tty %q, session %q", bad.mode, bad.tty, bad.session)
		}
	}
}

func TestCleanTTY(t *testing.T) {
	for in, want := range map[string]string{"/dev/ttys004": "/dev/ttys004", "/dev/pts/3": "/dev/pts/3",
		"pipe:[123]": "", "/dev/fd/0": "/dev/fd/0", "": "", "/dev/tty\x1b[2J": ""} {
		if got := CleanTTY(in); got != want {
			t.Errorf("CleanTTY(%q) = %q, want %q", in, got, want)
		}
	}
}
