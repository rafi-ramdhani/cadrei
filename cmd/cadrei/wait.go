//go:build !windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rafi-ramdhani/cadrei/internal/cadreis"
	"github.com/rafi-ramdhani/cadrei/internal/fsx"
	"github.com/rafi-ramdhani/cadrei/internal/orchestrator"
	"github.com/rafi-ramdhani/cadrei/internal/session"
)

// cadrei answer --wait: it waits until a request comes in that no wait
// has shown yet, shows it as the listing does, and ends. The orchestrator
// keeps one running in the background while members run, so a new prompt
// wakes it even when the user isn't typing: its runtime tells it when a
// background command ends. It reads the members' records only, and
// answers nothing.

// waitEvery is how often a wait looks at the members' records.
// waitGrace is how long a wait starting waits for the lock, so the team
// status's own quick look at it doesn't turn the wait away.
var (
	waitEvery = time.Second
	waitGrace = 200 * time.Millisecond
)

// waitedPath is the file of the requests a wait already showed, one key a
// line, so one that comes in between two waits is still shown by the
// next. waitLockPath is the lock a running wait holds, so the team status
// can tell whether one runs.
func waitedPath(build string) string   { return filepath.Join(build, "state", "waited") }
func waitLockPath(build string) string { return filepath.Join(build, "state", "wait.lock") }

// waitOwnerPath holds the orchestrator a running wait is for, as
// waitOwner names it. The lock file itself is never written.
func waitOwnerPath(build string) string { return filepath.Join(build, "state", "wait.owner") }

// waitOwner names the orchestrator open for build now, by its lock's pid
// and start time, or "none" when none is open. A wait is for the one open
// when it started: once another opens, or that one is gone, it ends.
func waitOwner(build string) string {
	if l := orchestrator.ReadLock(orchestrator.LockPath(build)); l != nil {
		return strconv.Itoa(l.PID) + " " + l.Start
	}
	return "none"
}

// readWaitOwner is the orchestrator the running wait noted, or "".
func readWaitOwner(build string) string {
	raw, _ := fsx.ReadOwn(waitOwnerPath(build), 512)
	return strings.TrimSpace(string(raw))
}

// waitKey names one request for the waited file: the member and the code,
// and what it asks, so a code given again later to another request isn't
// taken for one already shown.
func waitKey(w *waitingOn) string {
	sum := sha256.Sum256([]byte(w.req.Kind + "\x00" + w.req.Tool + "\x00" + w.req.Summary + "\x00" + w.req.Question))
	return w.name + " " + w.req.Code + " " + hex.EncodeToString(sum[:8])
}

// readWaited is the keys in the waited file.
func readWaited(build string) map[string]bool {
	seen := map[string]bool{}
	raw, _ := fsx.ReadOwn(waitedPath(build), 64<<10)
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			seen[l] = true
		}
	}
	return seen
}

// writeWaited keeps the keys of open, the requests open now, that seen or
// shown holds: one that ended is dropped.
func writeWaited(build string, open []waitingOn, seen map[string]bool) error {
	var keep []string
	for i := range open {
		if k := waitKey(&open[i]); seen[k] && !slices.Contains(keep, k) {
			keep = append(keep, k)
		}
	}
	text := strings.Join(keep, "\n")
	if text != "" {
		text += "\n"
	}
	return fsx.WriteFile(waitedPath(build), []byte(text), 0o600)
}

// waitRunning reports whether a wait for build's open orchestrator is
// running: one holds the lock, and noted the orchestrator open now. One
// left by an orchestrator that's gone doesn't count: it ends on its next
// look.
func waitRunning(build string) bool {
	l, err := fsx.Acquire(waitLockPath(build), 0)
	if err != nil {
		return errors.Is(err, fsx.ErrBusy) && readWaitOwner(build) == waitOwner(build)
	}
	l.Release()
	return false
}

// runWait is cadrei answer --wait [--timeout <time>].
func (e *env) runWait(r *cadreis.Resolved, t session.Tmux, build string, args []string) int {
	var limit time.Duration
	var limitText string
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--timeout":
		d, err := time.ParseDuration(args[1])
		if err != nil || d <= 0 {
			return e.failWith("Give --timeout a time like 30s, 10m or 2h.")
		}
		limit, limitText = d, args[1]
	default:
		return e.failWith("Use " + cmd("cadrei answer --wait") + ", or " + cmd("cadrei answer --wait --timeout 30m") + ".")
	}
	if err := os.MkdirAll(filepath.Join(build, "state"), 0o755); err != nil {
		return e.fail("%s", err)
	}
	lock, err := fsx.Acquire(waitLockPath(build), waitGrace)
	if errors.Is(err, fsx.ErrBusy) && !waitRunning(build) {
		// The one running is for an orchestrator that's gone, and ends on
		// its next look.
		lock, err = fsx.Acquire(waitLockPath(build), 3*waitEvery)
	}
	if errors.Is(err, fsx.ErrBusy) {
		return e.failWith("A " + cmd("cadrei answer --wait") + " is already running for this crew, so this one isn't needed.")
	}
	if err != nil {
		return e.fail("%s", err)
	}
	owner := waitOwner(build)
	if err := fsx.WriteFile(waitOwnerPath(build), []byte(owner+"\n"), 0o600); err != nil {
		lock.Release()
		return e.fail("%s", err)
	}
	defer func() {
		os.Remove(waitOwnerPath(build))
		lock.Release()
	}()
	seen := readWaited(build)
	start := time.Now()
	shown := -1 // the count this wait last gave the badge
	for {
		if waitOwner(build) != owner {
			e.say("The orchestrator this wait was for isn't open anymore, so the wait ended.")
			return 0
		}
		if len(runningOf(r.Cadrei, r.Default, t)) == 0 {
			writeWaited(build, nil, seen)
			e.say("No member is running, so there's nothing to wait on.")
			return 0
		}
		open := waitingRequests(r, t)
		// A held record past its time ends with no write, so no hook sets
		// the badge for it: the wait's looks do.
		if len(open) != shown {
			showBadge(build, len(open))
			shown = len(open)
		}
		var fresh []waitingOn
		for i := range open {
			if k := waitKey(&open[i]); !seen[k] {
				fresh = append(fresh, open[i])
				seen[k] = true
			}
		}
		if len(fresh) > 0 {
			// Noted first: shown and not noted would show again.
			if err := writeWaited(build, open, seen); err != nil {
				return e.fail("%s", err)
			}
			e.say("New and waiting on you:")
			e.listRequests(fresh)
			switch more := len(open) - len(fresh); {
			case more == 1:
				e.say("")
				e.say("%s", "One more, shown before, still waits. See them all with "+cmd("cadrei answer")+".")
			case more > 1:
				e.say("")
				e.say("%s", fmt.Sprintf("%d more, shown before, still wait. See them all with ", more)+cmd("cadrei answer")+".")
			}
			return 0
		}
		// Ones that ended leave the file, so a code given again is new.
		for k := range seen {
			if !slices.ContainsFunc(open, func(w waitingOn) bool { return waitKey(&w) == k }) {
				delete(seen, k)
			}
		}
		if limit > 0 && time.Since(start) >= limit {
			writeWaited(build, open, seen)
			e.say("%s", "Nothing new came in for "+limitText+".")
			return 0
		}
		time.Sleep(waitEvery)
	}
}
