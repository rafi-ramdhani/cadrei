package conf

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	values, warnings := Parse(`# Permission mode for member sessions
PERMISSION_MODE=default

ORCHESTRATOR_PERMISSION_MODE='auto'
RESUME="no"
EMPTY=
touch /tmp/marker
NAME=$(touch /tmp/marker)
A=b; touch /tmp/marker
B=x y
C="a$HOME"
export D=1
`)
	want := map[string]string{"PERMISSION_MODE": "default", "ORCHESTRATOR_PERMISSION_MODE": "auto", "RESUME": "no", "EMPTY": ""}
	if !reflect.DeepEqual(values, want) {
		t.Errorf("values %v, want %v", values, want)
	}
	if len(warnings) != 6 {
		t.Fatalf("warnings: %q", warnings)
	}
	for _, w := range []string{"line 7 ignored", "touch /tmp/marker", "line 8", "line 9", "line 10", "line 11", "line 12"} {
		if !strings.Contains(strings.Join(warnings, "\n"), w) {
			t.Errorf("no warning with %q in %q", w, warnings)
		}
	}
}

func TestTheTemplateReadsAsBefore(t *testing.T) {
	values, warnings := Parse("# Permission mode for member sessions: default, acceptEdits, auto, ...\nPERMISSION_MODE=default\n")
	if values["PERMISSION_MODE"] != "default" || len(warnings) != 0 {
		t.Errorf("%v %q", values, warnings)
	}
}

// Audit qual-bugs-6: Set changes what Parse reads, even when the key is
// set twice (a hand edit, a merge): the first line takes the value and the
// later ones go.
func TestSetWinsOverADuplicate(t *testing.T) {
	data := "# mine\nORCHESTRATOR=tmux\nMODEL=sonnet\nORCHESTRATOR=tmux\n"
	got := Set(data, "ORCHESTRATOR", "terminal")
	if got != "# mine\nORCHESTRATOR=terminal\nMODEL=sonnet\n" {
		t.Errorf("set: %q", got)
	}
	if v, _ := Parse(got); v["ORCHESTRATOR"] != "terminal" || v["MODEL"] != "sonnet" {
		t.Errorf("parse: %v", v)
	}
	if got := Set("A=1", "B", "2"); got != "A=1\nB=2\n" {
		t.Errorf("added: %q", got)
	}
}
