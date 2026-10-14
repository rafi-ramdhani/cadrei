//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rafi-ramdhani/cadrei/internal/paths"
)

// memoryRule looks for a command that writes into a .memory folder: the
// orchestrator's memory, which loads into each of its sessions, so a
// note a member planted there would be read as the orchestrator's own.
// It reads the forms it can see: a redirection, a program that writes,
// moves or removes the files it names when one of them is in the folder
// (cp, ln, rsync and the like only by their destination), sed -i and the
// like, xargs or find running one on the folder's files, a line that
// goes into the folder first, and code another program runs that names
// the folder and writes. Reading the folder, copying out of it included,
// is never refused.
type memoryRule struct{}

// memoryWriters write, move or remove every file they name.
var memoryWriters = setOf("tee", "mv", "rm", "touch", "truncate", "mkdir", "rmdir", "chmod", "chown", "unlink", "shred", "patch", "split", "csplit")

// toWriters copy or link into the last file they name (or -t's folder).
var toWriters = setOf("cp", "ln", "install", "rsync", "ditto", "scp")

// optionWriters write where one of their options says.
var optionWriters = map[string][]string{
	"dd": {"of="}, "tar": {"-C", "--directory"}, "unzip": {"-d"}, "curl": {"-o", "--output"}, "wget": {"-O", "-P", "--output-document", "--directory-prefix"},
}

// inPlace are programs that write the files they name only with -i.
var inPlace = setOf("sed", "gsed", "perl", "ruby")

// memoryPath matches .memory as a folder in a path or a word.
var memoryPath = regexp.MustCompile(`(^|[\s/'"=:(,;<>])\.memory($|[/\s'"*,;)|&<>])`)

// goesIn matches a line that changes into the folder (cd .memory && ...).
var goesIn = regexp.MustCompile(`(^|[\s;&|(])(cd|pushd)\s+[^;&|]*\.memory($|[/\s'";&|)])`)

// writesCode matches code that writes or removes files.
var writesCode = regexp.MustCompile(`(?i)open\([^)]*,\s*['"]?[^'")]*[wax+]|write|unlink|rename|remove|rmtree|copy|truncate|symlink|>|\btee\b|\bcp\b|\bmv\b|\brm\b|\bln\b|sed\s+-i`)

func (memoryRule) outputs(files []shWord, line *lineText) bool {
	for _, f := range files {
		// Named, or relative on a line that went into the folder first
		// (cd .memory && echo fact > a.md).
		if memoryPath.MatchString(f.text) || (goesIn.MatchString(line.text) && !elsewhere(f.text)) {
			return true
		}
	}
	return false
}

// elsewhere reports whether a redirection's target is a full path, or a
// file descriptor (2>&1, >&-), so it doesn't land in the folder a line
// went into.
func elsewhere(path string) bool {
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~") || path == "-" || strings.Trim(path, "0123456789") == ""
}

// crewRoot matches a crew's own folder, ~/.cadrei/<name>, as a path ends.
var crewRoot = regexp.MustCompile(`(^|/)\.cadrei/[^/]+/?$`)

// intoCrew reports whether a copy's destination is a crew's own folder:
// one under ~/.cadrei, or the crew this session is in (CADREI_HOME).
func intoCrew(dest string) bool {
	if crewRoot.MatchString(dest) {
		return true
	}
	home := os.Getenv("CADREI_HOME")
	if home == "" {
		return false
	}
	if rest, ok := strings.CutPrefix(dest, "~/"); ok {
		dest = filepath.Join(paths.Home(), rest)
	}
	return filepath.IsAbs(dest) && paths.Real(dest) == paths.Real(home)
}

func (memoryRule) writes(words []shWord, p int, fed bool, line *lineText) bool {
	// sudo -u someone tee ...: the option's value isn't the program.
	if !writer(wordName(words[p]), words[p+1:]) && p+1 < len(words) {
		for _, w := range words[:p] {
			if runsAnother[wordName(w)] {
				p++
				break
			}
		}
	}
	name := wordName(words[p])
	if writer(name, words[p+1:]) && (writesInto(name, words[p+1:]) || (goesIn.MatchString(line.text) && staysIn(name, words[p+1:]))) {
		return true
	}
	if !runners[name] {
		return false
	}
	// xargs, parallel or find -exec running a writer: on what came down
	// the pipe (ls .memory | xargs rm), or on what find finds there.
	found := false
	for _, w := range words[p+1:] {
		if strings.HasPrefix(w.bare, "-") {
			break
		}
		found = found || memoryPath.MatchString(w.text)
	}
	for k := p + 1; k < len(words); k++ {
		if w := wordName(words[k]); writer(w, words[k+1:]) {
			// A copy out of the folder isn't a write into it.
			return ((fed || found) && !toWriters[w]) || writesInto(w, words[k+1:]) || (goesIn.MatchString(line.text) && staysIn(w, words[k+1:]))
		}
		if (name == "find" || name == "fd") && (words[k].bare == "-delete" || words[k].bare == "-X") && found {
			return true
		}
	}
	return false
}

// writer reports whether a program writes the files it names.
func writer(name string, args []shWord) bool {
	_, opt := optionWriters[name]
	return memoryWriters[name] || toWriters[name] || opt || (inPlace[name] && inPlaceFlag(args))
}

// writesInto reports whether a writer's arguments put a file in the
// folder: any it names, its destination, or its output option's value.
func writesInto(name string, args []shWord) bool {
	var files []shWord
	for i := 0; i < len(args); i++ {
		a := args[i]
		if flags, ok := optionWriters[name]; ok {
			for _, f := range flags {
				switch {
				case a.bare == f && i+1 < len(args):
					if memoryPath.MatchString(args[i+1].text) {
						return true
					}
				case strings.HasPrefix(a.bare, f) && len(a.bare) > len(f) && memoryPath.MatchString(" "+strings.TrimPrefix(a.text, f)):
					return true
				}
			}
			continue
		}
		if toWriters[name] && (a.bare == "-t" || a.bare == "--target-directory") && i+1 < len(args) {
			if memoryPath.MatchString(args[i+1].text) {
				return true
			}
			i++
			continue
		}
		if toWriters[name] && strings.HasPrefix(a.bare, "--target-directory=") && memoryPath.MatchString(" "+strings.TrimPrefix(a.text, "--target-directory=")) {
			return true
		}
		if strings.HasPrefix(a.bare, "-") {
			continue
		}
		files = append(files, a)
	}
	if toWriters[name] {
		if len(files) == 0 {
			return false
		}
		dest := files[len(files)-1].text
		if memoryPath.MatchString(dest) {
			return true
		}
		// A folder named .memory copied into a crew lands as its memory
		// (cp -r ~/notes/.memory ~/.cadrei/main/).
		if intoCrew(dest) {
			for _, f := range files[:len(files)-1] {
				if filepath.Base(strings.TrimRight(f.text, "/")) == ".memory" {
					return true
				}
			}
		}
		return false
	}
	for _, f := range files {
		if memoryPath.MatchString(f.text) {
			return true
		}
	}
	return false
}

// staysIn reports whether a writer run inside the folder (after cd
// .memory) writes there: any writer but a copy to a full path elsewhere.
func staysIn(name string, args []shWord) bool {
	if !toWriters[name] {
		return true
	}
	var last string
	for _, a := range args {
		if !strings.HasPrefix(a.bare, "-") {
			last = a.text
		}
	}
	return !strings.HasPrefix(last, "/") && !strings.HasPrefix(last, "~")
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
