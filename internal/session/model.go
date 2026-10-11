package session

import (
	"bytes"
	"os"
	"regexp"
	"strings"
)

// A member's model and effort (7.11): each set by the first of these that
// sets it, or by nothing, and then the runtime uses its own default.
const (
	FromStart   = "start"   // --model or --effort on this cadrei up
	FromMember  = "member"  // the member file's front matter
	FromMachine = "machine" // the cadrei's local cadrei.conf
	FromCrew    = "crew"    // its cadrei.conf
)

// Efforts are the effort levels, lowest first.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// Setting is a model or an effort and where it was set ("" for unset).
type Setting struct{ Value, From string }

// Choice is a member's model and effort for one start.
type Choice struct{ Model, Effort Setting }

// Over fills what c leaves unset from below: c wins.
func (c Choice) Over(below Choice) Choice {
	if c.Model.Value == "" {
		c.Model = below.Model
	}
	if c.Effort.Value == "" {
		c.Effort = below.Effort
	}
	return c
}

// ChoiceFrom is the model and effort a set of values gives (MODEL and
// EFFORT in a cadrei.conf, or model and effort in front matter), from from.
func ChoiceFrom(model, effort, from string) Choice {
	var c Choice
	if model != "" {
		c.Model = Setting{model, from}
	}
	if effort != "" {
		c.Effort = Setting{strings.ToLower(effort), from}
	}
	return c
}

// MemberChoice is the model and effort a member file's front matter sets.
func MemberChoice(file string) Choice {
	raw, err := os.ReadFile(file)
	if err != nil {
		return Choice{}
	}
	keys, _ := FrontMatter(raw)
	return ChoiceFrom(keys["model"], keys["effort"], FromMember)
}

// FrontMatter splits a member file into its front matter's keys and the
// rest. There's front matter only when the first line is --- and a closing
// --- comes within the first 20 lines; the rest then starts at the first
// line after it that isn't blank. Values may be quoted, and # starts a
// comment.
func FrontMatter(raw []byte) (map[string]string, []byte) {
	lines := bytes.SplitAfter(raw, []byte("\n"))
	if len(lines) == 0 || strings.TrimRight(string(lines[0]), "\r\n") != "---" {
		return nil, raw
	}
	for end := 1; end < len(lines) && end < 20; end++ {
		if strings.TrimRight(string(lines[end]), "\r\n") != "---" {
			continue
		}
		keys := map[string]string{}
		for _, l := range lines[1:end] {
			k, v, ok := strings.Cut(string(l), ":")
			k = strings.TrimSpace(k)
			if !ok || k == "" || strings.HasPrefix(k, "#") {
				continue
			}
			keys[k] = yamlValue(v)
		}
		rest := lines[end+1:]
		for len(rest) > 0 && strings.TrimSpace(string(rest[0])) == "" {
			rest = rest[1:]
		}
		return keys, bytes.Join(rest, nil)
	}
	return nil, raw
}

// yamlValue is a front matter value: quotes removed, or a # comment cut.
func yamlValue(v string) string {
	v = strings.TrimSpace(v)
	for _, q := range []string{`"`, `'`} {
		if strings.HasPrefix(v, q) {
			if end := strings.Index(v[1:], q); end >= 0 {
				return v[1 : 1+end]
			}
		}
	}
	if i := strings.Index(v, "#"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// modelRule is a model name cadrei passes on: letters, digits and
// . _ - : / @ [ ], at most 200, not starting with -.
var modelRule = regexp.MustCompile(`^[A-Za-z0-9._:/@\[\]][A-Za-z0-9._:/@\[\]-]{0,199}$`)

// GoodModel reports whether m is a model name cadrei may pass on. Whether
// the runtime knows it is the runtime's to say.
func GoodModel(m string) bool { return modelRule.MatchString(m) }

// GoodEffort reports whether e is an effort level.
func GoodEffort(e string) bool {
	for _, x := range Efforts {
		if strings.EqualFold(e, x) {
			return true
		}
	}
	return false
}
