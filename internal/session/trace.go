package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// A member's trace is what its state hook did, run by run, so a prompt
// that never got a code can be explained afterwards: each run notes a
// start line before it waits for the member's lock, and an end line after,
// with how long it took and the state before and after. A run that starts
// and never ends was killed (the runtime's hook timeout) or crashed.
//
// It notes only the hook's own names and ids, timings, states and codes:
// never what a tool was given or what a member asks.
//
// It lives in the build folder's state/<member>.trace, and is bounded: past
// traceLimit, the hook that holds the lock moves it to <member>.trace.1
// (replacing the one before), so a member keeps two files at most.

// TraceLine is one line of a member's trace.
type TraceLine struct {
	T      time.Time `json:"t"`
	Run    string    `json:"run"`              // the hook run's own id, which its start and end share
	Phase  string    `json:"phase"`            // start or end
	Hook   string    `json:"hook,omitempty"`   // the runtime's name for the event
	Tool   string    `json:"tool,omitempty"`   // the tool's name, for a tool's event
	ID     string    `json:"id,omitempty"`     // the runtime's id for the tool call
	Notice string    `json:"notice,omitempty"` // a notification's type
	MS     int64     `json:"ms,omitempty"`     // how long the run took, from its start
	Before string    `json:"before,omitempty"` // the state before: <state>/<open requests>
	After  string    `json:"after,omitempty"`  // and after
	Codes  []string  `json:"codes,omitempty"`  // the open requests' codes after
	Err    string    `json:"err,omitempty"`    // what went wrong, when the run couldn't do its work
}

// Trace phases.
const (
	TraceStart = "start"
	TraceEnd   = "end"
)

// traceLimit is how big a trace file grows before it's moved aside.
const traceLimit = 32 << 10

// TracePath is a member's trace in a cadrei's build folder, or "" for a
// name cadrei does not make.
func TracePath(build, name string) string {
	p := StatePath(build, name)
	if p == "" {
		return ""
	}
	return strings.TrimSuffix(p, ".json") + ".trace"
}

// AppendTrace adds one line to a trace, in one write: lines from hooks
// running at once don't mix. It never follows a symlink.
func AppendTrace(path string, l TraceLine) error {
	if path == "" {
		return nil
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// RotateTrace moves a trace past traceLimit to <path>.1. Only the hook
// holding the member's lock calls it, so two never move it at once.
func RotateTrace(path string) {
	if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() && st.Size() > traceLimit {
		os.Rename(path, path+".1")
	}
}

// TraceState is how a trace notes a state: <state>/<open requests>, or
// "none".
func TraceState(s *State) string {
	if s == nil {
		return "none"
	}
	return s.State + "/" + strconv.Itoa(len(s.Requests()))
}

// TraceCounts sums up a member's trace.
type TraceCounts struct {
	Runs       int // hook runs that started
	Unfinished int // runs that started over a minute ago and never ended: killed or crashed
	Failed     int // runs that ended without doing their work (Err)
}

// unfinishedAfter is how long a run may go without an end line before it
// counts as unfinished: the runtime's hook timeout is seconds.
const unfinishedAfter = time.Minute

// CountTrace reads a member's trace, both files, at now.
func CountTrace(path string, now time.Time) TraceCounts {
	var c TraceCounts
	started := map[string]time.Time{}
	ended := map[string]bool{}
	for _, p := range []string{path + ".1", path} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var l TraceLine
			if json.Unmarshal(sc.Bytes(), &l) != nil || l.Run == "" {
				continue
			}
			switch l.Phase {
			case TraceStart:
				started[l.Run] = l.T
			case TraceEnd:
				ended[l.Run] = true
				if l.Err != "" {
					c.Failed++
				}
			}
		}
		f.Close()
	}
	for run, at := range started {
		c.Runs++
		if !ended[run] && now.Sub(at) > unfinishedAfter {
			c.Unfinished++
		}
	}
	return c
}
