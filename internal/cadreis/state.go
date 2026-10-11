package cadreis

import (
	"encoding/json"
	"os"

	"github.com/rafi-ramdhani/cadrei/internal/fsx"
)

// State is ~/.cadrei/config/state.json: answers and versions cadrei
// remembers between runs (N.1), as string keys and values.

// State keys.
const (
	// CheckedVersion is the version the last full health check ran for.
	CheckedVersion = "checked_version"
	// HookKept is "<program> -> <binary>": the user kept the orchestrator
	// hook on program when asked to switch it to binary.
	HookKept = "hook_kept"
	// SkillKept is the skill link's state the user kept when asked to link
	// it to this cadrei: its target, or "missing".
	SkillKept = "skill_kept"
	// LastFindings identifies the fast health findings of the last run, so
	// the full check runs again only when one is new.
	LastFindings = "last_findings"
	// HookOffer is "skipped" once the user said no to the every-chat hook,
	// so the orchestrator doesn't offer it again.
	HookOffer = "hook_offer"
	// IdentityWarned is set once the first run said git has no identity.
	IdentityWarned = "identity_warned"
	// FirstTaskPending is set by the first run and cleared by the first
	// cadrei up that starts a member: until then the orchestrator greets
	// as on a first run.
	FirstTaskPending = "first_task_pending"
)

// oldOrchestratorKey is where 0.2.0 kept a cadrei's orchestrator id.
func oldOrchestratorKey(cadrei string) string { return "orchestrator_id:" + cadrei }

// OldLinkKept is the key that remembers the user kept a 0.1.x link when
// asked to remove it.
func OldLinkKept(link string) string { return "old_link_kept:" + link }

// GetState returns a remembered value, or "".
func GetState(key string) string { return readState()[key] }

// StateKeys lists the keys in the state file.
func StateKeys() []string {
	var out []string
	for k := range readState() {
		out = append(out, k)
	}
	return out
}

func readState() map[string]string {
	m := map[string]string{}
	raw, err := os.ReadFile(Config("state.json"))
	if err == nil {
		json.Unmarshal(raw, &m)
	}
	return m
}

// SetState remembers a value; "" forgets it.
func SetState(key, value string) error {
	l, err := lockConfig()
	if err != nil {
		return err
	}
	defer l.Release()
	m := readState()
	if value == "" {
		delete(m, key)
	} else {
		m[key] = value
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(Config("state.json"), append(data, '\n'), 0o600)
}
