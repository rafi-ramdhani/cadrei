//go:build !windows

package main

import (
	"regexp"
	"strings"
)

// memoryRule looks for a command that writes into a .memory folder: the
// orchestrator's memory, which loads into each of its sessions, so a
// note a member planted there would be read as the orchestrator's own.
// It reads the forms it can see: a redirection, a program that writes or
// removes files (tee, cp, mv, rm, ln, sed -i and the like), xargs or find
// running one, and code another program runs that names the folder and
// writes. Copying out of the folder counts too.
type memoryRule struct{}

// memoryWriters are programs that write, move or remove the files they
// name.
var memoryWriters = setOf("tee", "cp", "mv", "rm", "ln", "touch", "install", "rsync", "dd", "truncate", "mkdir", "rmdir", "chmod", "chown",
	"unlink", "shred", "patch", "tar", "unzip", "curl", "wget", "split", "csplit", "ditto", "scp")

// inPlace are programs that write the files they name only with -i.
var inPlace = setOf("sed", "gsed", "perl", "ruby")

// memoryPath matches .memory as a folder in a path or a word.
var memoryPath = regexp.MustCompile(`(^|[\s/'"=:(,;<>])\.memory($|[/\s'"*,;)|&<>])`)

// writesCode matches code that writes or removes files.
var writesCode = regexp.MustCompile(`(?i)open\([^)]*,\s*['"]?[^'")]*[wax+]|write|unlink|rename|remove|rmtree|copy|truncate|symlink|>|\btee\b|\bcp\b|\bmv\b|\brm\b|\bln\b|sed\s+-i`)

func (memoryRule) outputs(files []shWord) bool {
	for _, f := range files {
		if memoryPath.MatchString(f.text) {
			return true
		}
	}
	return false
}

func (memoryRule) writes(words []shWord, p int, fed bool, line *lineText) bool {
	writer := false
	for k, w := range words[p:] {
		name := wordName(w)
		switch {
		case memoryWriters[name]:
			writer = true
		case inPlace[name]:
			writer = writer || inPlaceFlag(words[p+k+1:])
		case name == "find" || name == "fd":
			for _, a := range words[p+k+1:] {
				writer = writer || a.bare == "-delete" || a.bare == "--exec" || a.bare == "-X"
			}
		}
	}
	if !writer {
		return false
	}
	for _, w := range words {
		if memoryPath.MatchString(w.text) {
			return true
		}
	}
	// What it writes came down the pipe (ls .memory | xargs rm), or the
	// line went into the folder first (cd .memory && rm a.md).
	return fed || line.mentions()
}

// inPlaceFlag reports whether sed, perl or ruby's options edit in place.
func inPlaceFlag(args []shWord) bool {
	for _, a := range args {
		b := a.bare
		if strings.HasPrefix(b, "--in-place") || (strings.HasPrefix(b, "-") && !strings.HasPrefix(b, "--") && strings.Contains(b, "i")) {
			return true
		}
	}
	return false
}

func (memoryRule) runs(c *shCommand, words []shWord, p int, fed bool, line *lineText) bool {
	return false
}

// calls: code another program runs that names the folder and writes.
func (memoryRule) calls(text string) bool {
	return memoryPath.MatchString(text) && writesCode.MatchString(text)
}

func (memoryRule) mentions(text string) bool { return memoryPath.MatchString(text) }

func (memoryRule) blunt(text string) bool { return memoryPath.MatchString(text) }
