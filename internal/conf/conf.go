// Package conf reads a cadrei's cadrei.conf: KEY=VALUE lines. It is parsed,
// never run as shell code (section N.1), so a line that would run a command
// is ignored with a warning instead.
package conf

import (
	"fmt"
	"regexp"
	"strings"
)

var assignment = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// plain is a value that needs no quotes: nothing the shell would expand,
// split or run.
var plain = regexp.MustCompile(`^[A-Za-z0-9_./:@%+,=-]*$`)

// Parse reads KEY=VALUE lines, # comments and blank lines. A value may be
// in single or double quotes, which are removed; nothing is expanded. Any
// other line, or a value with shell syntax outside quotes (or $ or ` inside
// double quotes), is left out, with a warning naming its line.
func Parse(data string) (map[string]string, []string) {
	values := map[string]string{}
	var warnings []string
	for i, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := assignment.FindStringSubmatch(line)
		value, ok := "", m != nil
		if ok {
			value, ok = unquote(m[2])
		}
		if !ok {
			warnings = append(warnings, fmt.Sprintf("cadrei.conf line %d ignored (only KEY=VALUE lines are read): %s", i+1, line))
			continue
		}
		values[m[1]] = value
	}
	return values, warnings
}

func unquote(v string) (string, bool) {
	switch {
	case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
		inner := v[1 : len(v)-1]
		return inner, !strings.Contains(inner, "'")
	case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
		inner := v[1 : len(v)-1]
		return inner, !strings.ContainsAny(inner, "\"$`\\")
	}
	return v, plain.MatchString(v)
}

// Set returns data with KEY=value: the first line that sets key is
// replaced, and later ones that set it too are dropped (Parse lets the last
// one win, so a second one would undo the change), or the line is added at
// the end. Every other line, comments and keys cadrei doesn't know
// included, stays as it is.
func Set(data, key, value string) string {
	lines := strings.Split(data, "\n")
	out, set := lines[:0:0], false
	for _, raw := range lines {
		if m := assignment.FindStringSubmatch(strings.TrimSpace(raw)); m != nil && m[1] == key {
			if !set {
				out = append(out, key+"="+value)
				set = true
			}
			continue
		}
		out = append(out, raw)
	}
	if set {
		return strings.Join(out, "\n")
	}
	if data != "" && !strings.HasSuffix(data, "\n") {
		data += "\n"
	}
	return data + key + "=" + value + "\n"
}

// Unset removes every line that assigns key, keeping the rest as written.
func Unset(data, key string) string {
	lines := strings.Split(data, "\n")
	out := lines[:0]
	for _, raw := range lines {
		if m := assignment.FindStringSubmatch(strings.TrimSpace(raw)); m != nil && m[1] == key {
			continue
		}
		out = append(out, raw)
	}
	return strings.Join(out, "\n")
}
