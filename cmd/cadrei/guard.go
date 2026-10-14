//go:build !windows

package main

import (
	"regexp"
	"strconv"
	"strings"
)

// removesCrews reports whether a command line may run cadrei's uninstall
// or restore. It reads the line as a shell does, into the commands it
// runs and their words, with quotes and backslashes read through
// (un"inst"all is uninstall). Case doesn't count, as a case-insensitive
// disk doesn't count it. Text that only goes to a program that never runs
// it (a heredoc to cat, grep's pattern, gh's or git commit's message) is
// passed over. Text a shell, eval or another program could run is read
// too, and a line it can't read whole is read the blunt way, as words.
func removesCrews(command string) bool { return guardLine(removalRule, command, 0) }

// guardRule is one thing the guard looks for in a command line, as
// guardLine reads it.
type guardRule interface {
	// runs reports whether one command, its program at words[p] past any
	// wrapper, runs it; fed is whether what pipes into it mentions it.
	runs(c *shCommand, words []shWord, p int, fed bool, line *lineText) bool
	// calls reports whether code another program runs (an argument, a
	// heredoc) runs it.
	calls(text string) bool
	// mentions reports whether text that may become code (fed to a shell,
	// or behind a program that is an expansion) names it.
	mentions(text string) bool
	// blunt reads a line the guard can't read as commands, or one that
	// hands a shell a script file it can't see.
	blunt(text string) bool
}

// writeRule is a rule about what a command writes, which it checks even
// for a program that runs none of its arguments.
type writeRule interface {
	// outputs reports whether a command's output files are its kind.
	outputs(files []shWord) bool
	// writes reports whether the command, its program at words[p], writes
	// there; fed is whether what pipes into it mentions it.
	writes(words []shWord, p int, fed bool, line *lineText) bool
}

// guardLine checks a command line, or a shell's code inside one, for rule.
func guardLine(rule guardRule, text string, depth int) bool {
	if depth > 16 {
		return true
	}
	line, ok := parseShell(text)
	if !ok {
		return rule.blunt(text)
	}
	for _, s := range line.subs {
		if guardLine(rule, s, depth+1) {
			return true
		}
	}
	whole := &lineText{rule: rule, text: text}
	for _, pipe := range line.pipes {
		fed := false
		for _, c := range pipe {
			if guardCommand(rule, c, fed, whole, depth) {
				return true
			}
			fed = feeds(rule, c, fed)
			for _, w := range c.words {
				fed = fed || rule.mentions(w.text)
			}
		}
	}
	return false
}

// lineText is a whole line, read the blunt way at most once, however many
// of its commands ask.
type lineText struct {
	rule                   guardRule
	text                   string
	mention, bluntly       bool
	readMention, readBlunt bool
}

func (l *lineText) mentions() bool {
	if !l.readMention {
		l.mention, l.readMention = l.rule.mentions(l.text), true
	}
	return l.mention
}

func (l *lineText) blunt() bool {
	if !l.readBlunt {
		l.bluntly, l.readBlunt = l.rule.blunt(l.text), true
	}
	return l.bluntly
}

// guardCommand checks one command of a pipeline for rule: fed is whether
// what pipes into it mentions it, line the whole line it's on.
func guardCommand(rule guardRule, c *shCommand, fed bool, line *lineText, depth int) bool {
	// Where its output goes, whatever the program.
	w, writing := rule.(writeRule)
	if writing && w.outputs(c.outputs) {
		return true
	}
	words := c.words
	for len(words) > 0 && assignment.MatchString(words[0].text) {
		words = words[1:]
	}
	// The program: past keywords and the programs that run another (env,
	// sudo, nohup and the like), with their options.
	p, wrapped := 0, false
	for p < len(words) {
		name := wordName(words[p])
		if shKeywords[name] {
			p++
			continue
		}
		if runsAnother[name] {
			p, wrapped = p+1, true
			for p < len(words) && (strings.HasPrefix(words[p].bare, "-") || assignment.MatchString(words[p].text) || guardDuration.MatchString(words[p].bare)) {
				p++
			}
			continue
		}
		break
	}
	if p == len(words) {
		return false
	}
	if wrapped && !shells[wordName(words[p])] {
		// sudo -u someone sh -c ...: the option's value isn't the program.
		for k := p + 1; k < len(words); k++ {
			if shells[wordName(words[k])] {
				p = k
				break
			}
		}
	}
	if writing && w.writes(words, p, fed, line) {
		return true
	}
	prog, name := words[p], wordName(words[p])
	// A program that is itself an expansion, such as $(printf 'cadrei
	// uninstall'), could be anything the line spells.
	if prog.expands && line.mentions() {
		return true
	}
	if inert(name, words[p+1:]) {
		return false
	}
	if rule.runs(c, words, p, fed, line) {
		return true
	}
	// Code in an argument or on stdin.
	for _, w := range words {
		if rule.calls(w.text) {
			return true
		}
	}
	for _, in := range c.input {
		if rule.calls(in) {
			return true
		}
	}
	// A shell that another program runs (find -exec, xargs, flock, ssh):
	// its -c code is a shell's.
	for k := p + 1; k < len(words); k++ {
		if shellPrograms[wordName(words[k])] && codeFlag(words[k+1:]) && shellRuns(rule, c, words[k+1:], fed, line, depth) {
			return true
		}
	}
	// watch runs its words as a shell command.
	watched := false
	for _, w := range words[:p] {
		watched = watched || wordName(w) == "watch"
	}
	switch {
	case watched:
		var code []string
		for _, w := range words[p:] {
			code = append(code, w.text)
		}
		return guardLine(rule, strings.Join(code, " "), depth+1)
	case name == "eval":
		var code []string
		for _, w := range words[p+1:] {
			code = append(code, w.text)
		}
		return guardLine(rule, strings.Join(code, " "), depth+1)
	case name == "source" || name == ".":
		return len(words) > p+1 && line.blunt()
	case shells[name] || prog.expands:
		return shellRuns(rule, c, words[p+1:], fed, line, depth)
	}
	return false
}

// shellRuns checks what a shell runs: its -c code, a script file (which
// the guard can't see, so the line is read the blunt way), or its stdin.
func shellRuns(rule guardRule, c *shCommand, args []shWord, fed bool, line *lineText, depth int) bool {
	code := false
	for _, a := range args {
		if strings.HasPrefix(a.bare, "-") || strings.HasPrefix(a.bare, "+") {
			if !strings.HasPrefix(a.bare, "--") && strings.Contains(a.bare[1:], "c") {
				code = true
			}
			continue
		}
		if code {
			return guardLine(rule, a.text, depth+1)
		}
		return line.blunt()
	}
	if c.fromFile {
		return line.blunt()
	}
	for _, in := range c.input {
		if guardLine(rule, in, depth+1) {
			return true
		}
	}
	return feeds(rule, c, fed)
}

// codeFlag reports whether a shell's options, the words before its first
// other one, take code to run (-c, -lc).
func codeFlag(args []shWord) bool {
	for _, a := range args {
		if !strings.HasPrefix(a.bare, "-") && !strings.HasPrefix(a.bare, "+") {
			return false
		}
		if !strings.HasPrefix(a.bare, "--") && strings.Contains(a.bare[1:], "c") {
			return true
		}
	}
	return false
}

// feeds reports whether a command's stdin mentions what rule looks for:
// fed, from what pipes into it, or its heredocs.
func feeds(rule guardRule, c *shCommand, fed bool) bool {
	for _, in := range c.input {
		fed = fed || rule.mentions(in)
	}
	return fed
}

// cadreiRule looks for cadrei's program running one of subs.
type cadreiRule struct{ subs map[string]bool }

// The cadrei commands the guard looks for.
var (
	removalRule = cadreiRule{setOf("uninstall", "restore")}
	answerRule  = cadreiRule{setOf("answer")}
	compactRule = cadreiRule{setOf("compact")}
)

func (r cadreiRule) runs(c *shCommand, words []shWord, p int, fed bool, line *lineText) bool {
	if r.subs[wordName(words[p])] {
		return true
	}
	afterCadrei, afterEither := false, false
	for _, w := range words {
		// cadrei, or a word that could expand to it, then the command.
		if afterEither && (r.subs[strings.ToLower(w.bare)] || r.subs[strings.ToLower(w.text)]) {
			return true
		}
		// cadrei $x, where x could be the command.
		if afterCadrei && w.expands && line.mentions() {
			return true
		}
		cadrei := isCadrei(w)
		afterCadrei = afterCadrei || cadrei
		afterEither = afterEither || cadrei || w.expands
	}
	// xargs running cadrei, a shell, or a program it can't name, with
	// words from its stdin.
	if wordName(words[p]) == "xargs" {
		for _, a := range words[p+1:] {
			if isCadrei(a) || a.expands || shells[wordName(a)] {
				return c.fromFile || feeds(r, c, fed)
			}
		}
	}
	return false
}

var (
	programs = map[string]bool{"cadrei": true, "cadre": true}
	shells   = setOf("sh", "bash", "zsh", "dash", "ksh", "mksh", "yash", "fish", "csh", "tcsh", "busybox", "eval", "source", ".")
	// shellPrograms are the shells another program can run.
	shellPrograms = setOf("sh", "bash", "zsh", "dash", "ksh", "mksh", "yash", "fish", "csh", "tcsh")
	// runsAnother: programs that run the command after them.
	runsAnother = setOf("env", "command", "builtin", "exec", "nohup", "time", "nice", "sudo", "doas", "timeout", "stdbuf", "caffeinate", "setsid", "unbuffer", "watch", "chronic")
	shKeywords  = setOf("if", "then", "else", "elif", "fi", "do", "done", "while", "until", "for", "in", "case", "esac", "select", "function", "{", "}", "!", "[[", "]]")
	// inertPrograms never run their arguments or their stdin.
	inertPrograms = setOf("echo", "printf", "cat", "grep", "egrep", "fgrep", "ls", "head", "tail", "wc", "uniq", "cut", "tr", "diff", "cmp", "comm", "tee", "jq", "mkdir", "cp", "mv", "rm", "rmdir", "ln", "touch", "chmod", "stat", "test", "[", "true", "false", "pwd", "cd", "basename", "dirname", "realpath", "readlink", "which", "whoami", "date", "sleep", "shasum", "od", "hexdump", "column", "nl", "paste", "rev", "git", "gh")
	// gitInert: git commands that run nothing of the line's (hooks in
	// the repo aside, which are files).
	gitInert = setOf("add", "am", "apply", "blame", "branch", "checkout", "cherry-pick", "clone", "commit", "describe", "diff", "fetch", "grep", "init", "log", "ls-files", "merge", "mv", "notes", "pull", "push", "remote", "reset", "restore", "rev-parse", "revert", "rm", "shortlog", "show", "stash", "status", "switch", "tag", "worktree")
	// ghRuns: gh commands that can run a command of the line's.
	ghRuns = setOf("alias", "extension", "ext")
	// bluntHides: words that can hide the program's name, to the blunt
	// reading.
	bluntHides = setOf("sh", "bash", "zsh", "dash", "ksh", "fish", "eval", "source", "xargs", "exec", "env")

	assignment    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\+?=`)
	guardDuration = regexp.MustCompile(`^[0-9.]+[smhd]?$`)
)

func setOf(words ...string) map[string]bool {
	m := map[string]bool{}
	for _, w := range words {
		m[w] = true
	}
	return m
}

// inert reports whether a program runs none of its arguments: git only
// with no -c and a command from gitInert, gh with none of ghRuns.
func inert(name string, args []shWord) bool {
	if !inertPrograms[name] {
		return false
	}
	switch name {
	case "git":
		for i := 0; i < len(args); i++ {
			a := args[i].bare
			switch {
			case a == "-C":
				i++
			case a == "-c" || strings.HasPrefix(a, "--config-env") || strings.HasPrefix(a, "--exec-path"):
				return false
			case strings.HasPrefix(a, "-"):
			default:
				return gitInert[strings.ToLower(a)]
			}
		}
	case "gh":
		for _, a := range args {
			if ghRuns[strings.ToLower(a.bare)] {
				return false
			}
		}
	}
	return true
}

// wordName is a word as a program's name: its last path part, in lower case.
func wordName(w shWord) string {
	b := strings.ToLower(w.bare)
	return b[strings.LastIndex(b, "/")+1:]
}

// isCadrei reports whether a word names cadrei's program (or cadre
// 0.1.x's), by any path.
func isCadrei(w shWord) bool {
	t := strings.ToLower(w.text)
	return programs[wordName(w)] || programs[t[strings.LastIndex(t, "/")+1:]]
}

// expansion is a shell expansion the guard can't know the value of.
var expansion = regexp.MustCompile(`\$\{[^}]*\}|\$\([^)]*\)|\$[a-z_][a-z0-9_]*|\$[0-9@*#?$!-]|` + "`[^`]*`")

// guardWord is a word of a command line, as far as the blunt reading
// reads one.
var (
	guardWord = regexp.MustCompile(`[a-z0-9_.\-/]+`)
	// guardSplit is a word, or a ; as tmux splits its commands by.
	guardSplit = regexp.MustCompile(`[a-z0-9_.\-/]+|;`)
)

// bluntWords are text's words, read the blunt way: lower case, quotes and
// backslashes taken out, and read again with each expansion taken out, as
// an empty one expands (unin${x}stall runs uninstall). Each is its last
// path part.
func bluntWords(text string) []string { return readWords(text, guardWord) }

// splitWords are bluntWords, with each ; a word of its own.
func splitWords(text string) []string { return readWords(text, guardSplit) }

func readWords(text string, word *regexp.Regexp) []string {
	text = strings.ToLower(text)
	text = strings.NewReplacer(`"`, "", `'`, "", `\`, "").Replace(text)
	text += "\n" + expansion.ReplaceAllString(text, "")
	words := word.FindAllString(text, -1)
	for i, w := range words {
		words[i] = w[strings.LastIndex(w, "/")+1:]
	}
	return words
}

// blunt is the blunt reading, for text the guard can't read as commands:
// one of the commands, with cadrei's program by any path, or with
// something that can hide the program's name: an expansion ($, a
// backtick), or another shell, eval, source or xargs.
func (r cadreiRule) blunt(text string) bool {
	hides := strings.ContainsAny(text, "$`")
	program, sub := false, false
	for _, w := range bluntWords(text) {
		switch {
		case programs[w]:
			program = true
		case r.subs[w]:
			sub = true
		case bluntHides[w]:
			hides = true
		}
	}
	return sub && (program || hides)
}

// mentions reports whether text has one of the commands as a word.
func (r cadreiRule) mentions(text string) bool {
	for _, w := range bluntWords(text) {
		if r.subs[w] {
			return true
		}
	}
	return false
}

// calls reports whether code another program runs (python's -c, a heredoc
// to node) has cadrei, then one of the commands within the next three
// words: "cadrei uninstall", or ["cadrei", "restore"].
func (r cadreiRule) calls(text string) bool {
	words := bluntWords(text)
	for i, w := range words {
		if !programs[w] {
			continue
		}
		for _, v := range words[i+1 : min(i+4, len(words))] {
			if r.subs[v] {
				return true
			}
		}
	}
	return false
}

// A command line, read as a shell reads it.
type (
	shWord struct {
		text    string // quotes and backslashes read through, expansions as written
		bare    string // the same, without its expansions
		quoted  bool   // part of it was quoted
		expands bool   // it has an expansion, whose value the guard can't know
	}
	shCommand struct {
		words    []shWord
		input    []string // the heredocs and here-strings it reads
		fromFile bool     // it reads a file on stdin (<)
		outputs  []shWord // the files its output goes to (>, >>, &>)
	}
	shLine struct {
		pipes [][]*shCommand
		// subs are the command and process substitutions, which run
		// on their own.
		subs []string
	}
)

// parseShell reads a command line into its pipelines of commands. It
// reports false for one it can't read whole: an open quote, an unclosed
// $( or a redirection with nothing after it.
func parseShell(s string) (shLine, bool) {
	s = strings.ReplaceAll(s, "\\\n", "")
	type doc struct {
		cmd     *shCommand
		delim   string
		tabs    bool // <<- takes the tabs off each line
		literal bool // a quoted delimiter: no expansions in the body
	}
	var (
		line         shLine
		pipe         []*shCommand
		cmd          = &shCommand{}
		text, bare   []byte
		in, q, x     bool   // in a word, quoted, expands
		target       string // what the next word is for: a redirection's operator, or "" for an argument
		docs         []doc
		bad          bool
		addc         = func(c byte) { text, bare, in = append(text, c), append(bare, c), true }
		addq         = func(s string) { text, bare, in, q = append(text, s...), append(bare, s...), true, true }
		addx         = func(s string) { text, in, x = append(text, s...), true, true }
		sub          = func(s string) { line.subs = append(line.subs, s) }
		endWord      func()
		endCmd       func()
		endPipe      func()
		dollar       func(i int) int
		redirect     func(i int) int
		readBacktick func(i int) int
	)
	endWord = func() {
		if !in {
			return
		}
		w := shWord{text: string(text), bare: string(bare), quoted: q, expands: x}
		switch target {
		case "":
			cmd.words = append(cmd.words, w)
		case "<<<":
			cmd.input = append(cmd.input, w.text)
		case "<<", "<<-":
			docs = append(docs, doc{cmd, w.bare, target == "<<-", w.quoted})
		case "<":
			cmd.fromFile = true
		case ">":
			cmd.outputs = append(cmd.outputs, w)
		}
		text, bare, in, q, x, target = nil, nil, false, false, false, ""
	}
	endCmd = func() {
		endWord()
		if target != "" {
			bad = true
		}
		if len(cmd.words) > 0 || len(cmd.input) > 0 || cmd.fromFile || len(cmd.outputs) > 0 {
			pipe = append(pipe, cmd)
		}
		cmd = &shCommand{}
	}
	endPipe = func() {
		endCmd()
		if len(pipe) > 0 {
			line.pipes = append(line.pipes, pipe)
		}
		pipe = nil
	}
	// readBacktick reads `...` from i, the opening backtick, and returns
	// the index of the closing one.
	readBacktick = func(i int) int {
		for k := i + 1; k < len(s); k++ {
			switch s[k] {
			case '\\':
				k++
			case '`':
				sub(s[i+1 : k])
				addx(s[i : k+1])
				return k
			}
		}
		bad = true
		return len(s)
	}
	// dollar reads an expansion from i, its $, and returns the index of
	// its last byte.
	dollar = func(i int) int {
		if i+1 >= len(s) {
			addc('$')
			return i
		}
		switch c := s[i+1]; {
		case c == '(':
			body, end, ok := balanced(s, i+1)
			if !ok {
				bad = true
				return len(s)
			}
			sub(body)
			addx(s[i : end+1])
			return end
		case c == '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				bad = true
				return len(s)
			}
			// ${x:-$(cmd)} runs cmd.
			subs, ok := substitutions(s[i+2 : i+end])
			if !ok {
				bad = true
			}
			line.subs = append(line.subs, subs...)
			addx(s[i : i+end+1])
			return i + end
		case c == '\'':
			// $'...': a backslash in it can spell any letter (\x75 is u).
			// One the guard can't spell counts as unknown.
			for k := i + 2; k < len(s); k++ {
				switch s[k] {
				case '\\':
					k++
				case '\'':
					if body, ok := ansiC(s[i+2 : k]); ok {
						addq(body)
					} else {
						addx(s[i : k+1])
					}
					return k
				}
			}
			bad = true
			return len(s)
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			k := i + 1
			for k < len(s) && (s[k] == '_' || s[k] >= 'a' && s[k] <= 'z' || s[k] >= 'A' && s[k] <= 'Z' || s[k] >= '0' && s[k] <= '9') {
				k++
			}
			addx(s[i:k])
			return k - 1
		case c >= '0' && c <= '9' || strings.IndexByte("@*#?$!-", c) >= 0:
			addx(s[i : i+2])
			return i + 1
		}
		addc('$')
		return i
	}
	// redirect reads a redirection's operator from i and returns the
	// index of its last byte.
	redirect = func(i int) int {
		// An fd number before it (2>) isn't a word.
		if in && !q && !x && strings.Trim(string(text), "0123456789") == "" {
			text, bare, in = nil, nil, false
		}
		endWord()
		if target != "" {
			bad = true
		}
		rest := s[i:]
		if strings.HasPrefix(rest, "<(") || strings.HasPrefix(rest, ">(") {
			body, end, ok := balanced(s, i+1)
			if !ok {
				bad = true
				return len(s)
			}
			sub(body)
			addx(s[i : end+1])
			return end
		}
		for _, op := range []string{"<<<", "<<-", "<<", "<>", "<&", "<", ">>", ">&", ">|", ">"} {
			if strings.HasPrefix(rest, op) {
				switch op {
				case "<<<", "<<-", "<<", "<":
					target = op
				default:
					target = ">"
				}
				return i + len(op) - 1
			}
		}
		return i
	}
	for i := 0; i < len(s) && !bad; i++ {
		switch c := s[i]; {
		case c == ' ' || c == '\t':
			endWord()
		case c == '\n':
			endPipe()
			for _, d := range docs {
				body, next := heredoc(s, i+1, d.delim, d.tabs)
				d.cmd.input = append(d.cmd.input, body)
				if !d.literal {
					subs, ok := substitutions(body)
					if !ok {
						return line, false
					}
					line.subs = append(line.subs, subs...)
				}
				i = next - 1
			}
			docs = nil
		case c == '#' && !in:
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
		case c == '&' && i+1 < len(s) && s[i+1] == '>':
			i = redirect(i + 1)
		case c == '|' && (i+1 >= len(s) || s[i+1] != '|'):
			endCmd()
			if i+1 < len(s) && s[i+1] == '&' {
				i++
			}
		case c == ';' || c == '&' || c == '|' || c == '(' || c == ')':
			endPipe()
		case c == '<' || c == '>':
			i = redirect(i)
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return line, false
			}
			addq(s[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			in, q = true, true
			k := i + 1
			for ; k < len(s) && s[k] != '"'; k++ {
				switch s[k] {
				case '\\':
					if k+1 < len(s) && strings.IndexByte("$`\"\\", s[k+1]) >= 0 {
						k++
					}
					addc(s[k])
				case '$':
					k = dollar(k)
				case '`':
					k = readBacktick(k)
				default:
					addc(s[k])
				}
			}
			if k >= len(s) {
				return line, false
			}
			i = k
		case c == '\\':
			if i+1 < len(s) {
				i++
				addc(s[i])
			}
		case c == '$':
			i = dollar(i)
		case c == '`':
			i = readBacktick(i)
		default:
			addc(c)
		}
	}
	endPipe()
	if bad || len(docs) > 0 {
		return line, false
	}
	return line, true
}

// ansiC spells the body of a $'...' as the shell does: \x75, \165 and
// u are all u. It reports false for an escape it can't spell.
func ansiC(body string) (string, bool) {
	var out strings.Builder
	for k := 0; k < len(body); k++ {
		if body[k] != '\\' {
			out.WriteByte(body[k])
			continue
		}
		rest := body[k+1:]
		n, base := 0, 0
		switch {
		case rest == "":
			return "", false
		case rest[0] == 'x':
			rest, base = rest[1:], 16
			for n < 2 && n < len(rest) && strings.IndexByte("0123456789abcdefABCDEF", rest[n]) >= 0 {
				n++
			}
			k += 1
		case rest[0] == 'u' || rest[0] == 'U':
			rest, base = rest[1:], 16
			for n < 8 && n < len(rest) && strings.IndexByte("0123456789abcdefABCDEF", rest[n]) >= 0 {
				n++
			}
			k += 1
		case rest[0] >= '0' && rest[0] <= '7':
			base = 8
			for n < 3 && n < len(rest) && rest[n] >= '0' && rest[n] <= '7' {
				n++
			}
		case strings.IndexByte(`\'"?`, rest[0]) >= 0:
			out.WriteByte(rest[0])
			k++
			continue
		default:
			// \n, \t and the like: no letter of a word.
			out.WriteByte(' ')
			k++
			continue
		}
		v, err := strconv.ParseUint(rest[:n], base, 32)
		if n == 0 || err != nil {
			return "", false
		}
		out.WriteRune(rune(v))
		k += n
	}
	return out.String(), true
}

// balanced reads the parentheses opened at s[open], past quotes and
// heredocs inside (a body's "can't" opens no quote), and returns what's
// between them and the index of the closing one.
func balanced(s string, open int) (string, int, bool) {
	type doc struct {
		delim string
		tabs  bool
	}
	var docs []doc
	depth := 0
	for k := open; k < len(s); k++ {
		switch s[k] {
		case '<':
			if strings.HasPrefix(s[k:], "<<<") {
				k += 2
				continue
			}
			if !strings.HasPrefix(s[k:], "<<") {
				continue
			}
			j, d := k+2, doc{}
			if j < len(s) && s[j] == '-' {
				j, d.tabs = j+1, true
			}
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			start := j
			for j < len(s) && strings.IndexByte(" \t\n;|&()<>", s[j]) < 0 {
				j++
			}
			d.delim = strings.NewReplacer(`'`, "", `"`, "", `\`, "").Replace(s[start:j])
			docs = append(docs, d)
			k = j - 1
		case '\n':
			for _, d := range docs {
				_, next := heredoc(s, k+1, d.delim, d.tabs)
				k = next - 1
			}
			docs = nil
		case '\\':
			k++
		case '\'':
			end := strings.IndexByte(s[k+1:], '\'')
			if end < 0 {
				return "", 0, false
			}
			k += end + 1
		case '"':
			for k++; k < len(s) && s[k] != '"'; k++ {
				if s[k] == '\\' {
					k++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : k], k, true
			}
		}
	}
	return "", 0, false
}

// substitutions are the $(...) and `...` in text a shell expands, such as
// a heredoc's body with a bare delimiter.
func substitutions(text string) ([]string, bool) {
	var out []string
	for k := 0; k < len(text); k++ {
		switch {
		case text[k] == '\\':
			k++
		case strings.HasPrefix(text[k:], "$("):
			body, end, ok := balanced(text, k+1)
			if !ok {
				return nil, false
			}
			out = append(out, body)
			k = end
		case text[k] == '`':
			end := strings.IndexByte(text[k+1:], '`')
			if end < 0 {
				return nil, false
			}
			out = append(out, text[k+1:k+1+end])
			k += end + 1
		}
	}
	return out, true
}

// heredoc reads a heredoc's body from start, up to the line that is its
// delimiter, and returns it and where the line after that starts. A body
// with no such line runs to the end, as a shell runs it.
func heredoc(s string, start int, delim string, tabs bool) (string, int) {
	for k := start; k < len(s); {
		end := strings.IndexByte(s[k:], '\n')
		next := len(s)
		if end >= 0 {
			end += k
			next = end + 1
		} else {
			end = len(s)
		}
		l := s[k:end]
		if tabs {
			l = strings.TrimLeft(l, "\t")
		}
		if l == delim {
			return s[start:k], next
		}
		k = next
	}
	return s[start:], len(s)
}
