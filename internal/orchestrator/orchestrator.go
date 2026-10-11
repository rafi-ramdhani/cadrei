// Package orchestrator holds what cadrei keeps about a cadrei's orchestrator:
// its prompt and the lock that says one is open (section M.3).
package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/proc"
)

// Modes an orchestrator runs in.
const (
	Terminal = "terminal" // plain cadrei: a child process in the user's terminal
	Tmux     = "tmux"     // cadrei --tmux: a tmux session
)

// SessionName is the orchestrator's tmux session (K-Q6).
func SessionName(cadrei string) string { return "cadrei-" + cadrei }

// Name is the orchestrator's session name, its messaging address.
func Name(cadrei string) string { return cadrei + "-orchestrator" }

// Lock records an open orchestrator: the process cadrei started, by pid
// and start time, so a reused pid is never taken for it.
type Lock struct {
	PID     int       `json:"pid"`
	Start   string    `json:"start"`   // the process's start time
	Command string    `json:"command"` // its command name when cadrei started it, for people reading the file
	TTY     string    `json:"tty,omitempty"`
	Mode    string    `json:"mode"`
	Session string    `json:"session,omitempty"` // the tmux session, in tmux mode
	Since   time.Time `json:"since"`
}

// LockPath is the lock's file in a cadrei's build folder.
func LockPath(build string) string { return filepath.Join(build, "orchestrator.lock") }

// maxLock is more than a lock cadrei writes ever holds.
const maxLock = 4096

// Lock fields cadrei prints: a terminal device and a tmux session name.
var (
	ttyRule     = regexp.MustCompile(`^/dev/[A-Za-z0-9._/-]+$`)
	sessionRule = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// CleanTTY returns a terminal device as a lock records it, or "" for
// anything that is not a plain /dev/ path (a pipe, a socket).
func CleanTTY(tty string) string {
	if ttyRule.MatchString(tty) {
		return tty
	}
	return ""
}

// read reads the lock file as it is, or nil for anything cadrei would not
// have written: a file that is not the user's own regular file (a
// symlink, a FIFO), too large, not a lock, or with a mode, terminal or
// session cadrei does not write. What it returns is safe to print.
func read(path string) *Lock {
	raw, err := fsx.ReadOwn(path, maxLock)
	if err != nil {
		return nil
	}
	var l Lock
	if json.Unmarshal(raw, &l) != nil || l.PID <= 0 {
		return nil
	}
	switch {
	case l.Mode == Terminal && l.Session == "" && (l.TTY == "" || ttyRule.MatchString(l.TTY)):
	case l.Mode == Tmux && l.TTY == "" && sessionRule.MatchString(l.Session):
	default:
		return nil
	}
	return &l
}

// ReadLock returns the open orchestrator, or nil when the lock is missing,
// stale (its process is gone or restarted under the same pid) or not one
// cadrei wrote. It changes nothing: only a run holding Guard clears a lock
// (ClearStale), so a cadrei ls can never remove a lock being written.
func ReadLock(path string) *Lock {
	l := read(path)
	if l == nil || !proc.Same(l.PID, proc.Info{Start: l.Start}) {
		return nil
	}
	return l
}

// ClearStale removes the lock file unless it records a live orchestrator.
// The caller holds Guard.
func ClearStale(path string) {
	if _, err := os.Lstat(path); err == nil && ReadLock(path) == nil {
		os.Remove(path)
	}
}

// WriteLock records the orchestrator running as pid.
func WriteLock(path string, pid int, mode, tty, session string) (*Lock, error) {
	info, err := proc.Of(pid)
	if err != nil {
		return nil, err
	}
	l := Lock{PID: pid, Start: info.Start, Command: info.Command, TTY: tty, Mode: mode, Session: session, Since: time.Now()}
	raw, err := json.Marshal(l)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &l, fsx.WriteFile(path, append(raw, '\n'), 0o600)
}

// RemoveLock removes the lock only while it still records the process l
// does, so a run never removes another run's lock. The caller holds Guard.
func RemoveLock(path string, l *Lock) {
	if cur := read(path); cur != nil && cur.PID == l.PID && cur.Start == l.Start {
		os.Remove(path)
	}
}

// Guard serializes opening a cadrei's orchestrator (reading the lock,
// starting, writing the lock), so two cadrei runs cannot both start one.
func Guard(build string) (*fsx.Lock, error) {
	l, err := fsx.Acquire(filepath.Join(build, "orchestrator.start.lock"), 10*time.Second)
	if errors.Is(err, fsx.ErrBusy) {
		return nil, errors.New("another cadrei is opening this orchestrator; try again")
	}
	return l, err
}

// Prompt is the orchestrator's prompt: the framework's orchestrator text
// and which cadrei it leads. It holds only what doesn't change between
// runs, since a resumed chat keeps the prompt it started with; the live
// snapshot (where cadrei was opened from, among the rest) comes from a
// session start hook.
func Prompt(text []byte, cadrei, path string) []byte {
	out := append([]byte{}, text...)
	return append(out, fmt.Sprintf("\nYou are the orchestrator of the crew `%s` (`%s`).\n", cadrei, path)...)
}
