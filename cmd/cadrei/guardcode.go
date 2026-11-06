//go:build !windows

package main

import (
	"regexp"
	"strings"
)

// Code an interpreter runs (python's -c, perl's -e, awk's program, a
// script on python's stdin) is read for what it runs, not for what it
// only names: a code body that names tmux or cadrei's commands is flagged
// only when it can run a program (execLike). A note a python heredoc
// writes, a perl edit whose patterns name cadrei answer, or an awk search
// for tmux runs nothing. The interpreter's other words, and every word of
// a program the guard doesn't know, are read the strict way still: a
// script could run them.

// interpreters are the programs whose code the guard reads this way, with
// the options that take code and the ones it can't read past.
type interpreter struct {
	code   string          // the letters of the short options that take code (c, e)
	long   map[string]bool // the long options that take code
	unsafe map[string]bool // options that load code the guard can't see: no reading past them
	values string          // the letters of the short options that take a value
	stdin  bool            // with no code or script given, it reads its code on stdin
}

var interpreters = map[string]interpreter{
	"python":  {code: "c", values: "WX", unsafe: setOf("-m"), stdin: true},
	"python3": {code: "c", values: "WX", unsafe: setOf("-m"), stdin: true},
	"perl":    {code: "eE", values: "I", unsafe: setOf("-M", "-m", "-d", "-x"), stdin: true},
	"ruby":    {code: "e", values: "IECrx", unsafe: setOf("-r", "-x"), stdin: true},
	"node":    {code: "ep", long: setOf("--eval", "--print"), unsafe: setOf("-r", "--require", "--import", "--loader", "--experimental-loader"), stdin: true},
	"php":     {code: "r", values: "cdz", unsafe: setOf("-d", "-c", "-z"), stdin: true},
	"awk":     {values: "Fv", unsafe: setOf("-f", "-l", "-i", "--load", "--include", "--file")},
	"gawk":    {values: "Fv", unsafe: setOf("-f", "-l", "-i", "--load", "--include", "--file")},
	"mawk":    {values: "Fv", unsafe: setOf("-f")},
	"nawk":    {values: "Fv", unsafe: setOf("-f")},
}

// interpreterOf is the interpreter a program is, by its name: python3.12
// is python3.
func interpreterOf(name string) (interpreter, bool) {
	if i, ok := interpreters[name]; ok {
		return i, true
	}
	if strings.HasPrefix(name, "python3.") && strings.Trim(name[len("python3."):], "0123456789") == "" {
		return interpreters["python3"], true
	}
	return interpreter{}, false
}

// codeOf reads an interpreter's arguments: which of them is its code (an
// index into args, or -1), and whether its stdin is. ok is false when an
// option loads code the guard can't see, or one it doesn't know comes
// before the code: then nothing is read this way.
func codeOf(name string, args []shWord) (code int, stdin, ok bool) {
	in, known := interpreterOf(name)
	if !known {
		return -1, false, false
	}
	awk := in.code == ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a.expands {
			return -1, false, false
		}
		t := a.text
		switch {
		case t == "-" && in.stdin:
			return -1, true, true
		case t == "--":
			if awk && i+1 < len(args) {
				return i + 1, false, true
			}
			return -1, false, false
		case strings.HasPrefix(t, "--"):
			opt, _, _ := strings.Cut(t, "=")
			switch {
			case in.unsafe[opt]:
				return -1, false, false
			case in.long[opt] && !strings.Contains(t, "="):
				if i+1 < len(args) {
					return i + 1, false, true
				}
				return -1, false, false
			}
			// Another long option: python's --version and the like take no
			// value; one that does would only be read as the code here.
			continue
		case strings.HasPrefix(t, "-") && len(t) > 1:
			for j := 1; j < len(t); j++ {
				f := t[j : j+1]
				switch {
				case in.unsafe["-"+f]:
					return -1, false, false
				case strings.Contains(in.code, f):
					if j < len(t)-1 {
						return -1, false, false // code joined to its option: read it the strict way
					}
					if i+1 < len(args) {
						return i + 1, false, true
					}
					return -1, false, false
				case name == "perl" && f == "i":
					j = len(t) // the rest is -i's extension
				case strings.Contains(in.values, f):
					if j == len(t)-1 {
						i++ // its value is the next word
					}
					j = len(t)
				}
			}
			continue
		}
		// The first other word: awk's program, or the interpreter's script
		// file, whose code the guard can't see.
		if awk {
			return i, false, true
		}
		return -1, false, false
	}
	return -1, in.stdin, in.stdin
}

// execLike reports whether code can run a program: a call that runs one
// (system, exec, popen, spawn, a backtick, a pipe to or from a command), a
// module that does (subprocess, child_process, pty, ctypes), or a way to
// reach one by a name it builds (getattr, __import__, eval, require).
func execLike(code string) bool {
	return execCall.MatchString(code) || execUse.MatchString(code) || newFunction.MatchString(code) || execImport.MatchString(code)
}

// execImport is a python import of what runs a program, under any name:
// from os import system as s.
var execImport = regexp.MustCompile(`(?i)\bfrom\s+(?:os|posix|nt|pty|subprocess|commands)\s+import\b|\bimport\b[^\n;]*\b(?:system|popen[0-9]?|spawn[a-z]*|exec[a-z]*|fork[a-z]*)\b`)

const execNames = `system|popen[0-9]?|spawn[a-z]*|posix_spawnp?|exec|exec[lv]p?e?|execsync|execfile(?:sync)?|spawnsync|fork|forkpty|passthru|shell_exec|proc_open|pcntl_[a-z_]+|readpipe|getoutput|getstatusoutput|check_output|check_call|doshellscript|eval|instance_eval|class_eval|module_eval|__import__|getattr|attrgetter|methodcaller|require|import|send|public_send|__send__|const_get|method|syscall|dlopen`

// newFunction is JavaScript's Function, which makes code of a string.
var newFunction = regexp.MustCompile(`\bFunction\s*\(`)

var (
	// execCall is one of execNames called, or reached as an attribute:
	// os.system(, system "x", exec('x'), o.popen, require('x').
	execCall = regexp.MustCompile(`(?i)(?:\.\s*(?:` + execNames + `)\b|\b(?:` + execNames + `)\s*[(\["'$@` + "`" + `]|\b(?:system|exec|spawn)\s+["'$@])`)
	// execUse is a backtick, perl's qx and ruby's %x, a pipe to or from a
	// command in a string (open(F, "| cmd"), print | "cmd", "cmd" |
	// getline), gawk's coprocess and @load, or a module that runs
	// programs.
	execUse = regexp.MustCompile("(?i)`" + `|\bqx\s*[^\w\s]|%x\s*[^\w\s]|["']\s*\||\|\s*["']|\|&|@load\b|\bgetline\b|\b(?:subprocess|child_process|pty|ctypes|cffi|ffi|importlib|builtins|__builtins__|__dict__|sys\.modules|pickle|marshal|runpy|open[23]|ipc::\w+|processbuilder|runtime\.getruntime|process\.binding|process\.dlopen|do\s+shell\s+script)\b`)
)
