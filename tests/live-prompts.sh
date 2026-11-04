#!/usr/bin/env bash
# Live end-to-end check of every kind of member prompt (cadrei 0.2.3, a
# release gate). It drives a real Claude Code member to each kind, then
# checks what tests/smoke.sh checks with a stand-in: the prompt reaches
# cadrei answer --wait with a code, cadrei answer <code> shows it, the
# answer by its code presses the right key, the request ends, and the
# prompt log says it ended by its code. At the end, no prompt of this run
# may have ended in the member's window.
#
# Run it yourself, in your own terminal, never from a Claude session: it
# types the member's tasks into its tmux window, which cadrei's guard
# refuses a session anyway. It needs a logged-in Claude Code, costs real
# model turns, and takes a while.
#
#   CADREI_LIVE=yes CADREI_LIVE_CREW=<crew> tests/live-prompts.sh [case ...]
#
# <crew> is a crew you made for this with cadrei new, with nothing else in
# it: the script adds a team (live), a member (prober) and a project
# (live-e2e, a fresh folder under $TMPDIR) to it, and starts and stops
# that member. With no case named, every case runs; name some to run only
# those (see cases below). Cases that need something set up by hand print
# what to do and wait for Enter.
set -euo pipefail

if [ "${CADREI_LIVE:-}" != yes ] || [ -z "${CADREI_LIVE_CREW:-}" ]; then
  echo "This drives a real Claude Code member and costs model turns."
  echo "Run it with CADREI_LIVE=yes CADREI_LIVE_CREW=<a crew you made for this>."
  exit 2
fi
if [ -n "${CLAUDECODE:-}" ] || [ -n "${CADREI_MEMBER:-}" ]; then
  echo "Run this in your own terminal, not from a Claude session."
  exit 2
fi
for tool in cadrei tmux python3 git; do
  command -v "$tool" >/dev/null || { echo "Needs $tool on the PATH."; exit 2; }
done

C="$HOME/.cadrei/$CADREI_LIVE_CREW"
[ -d "$C" ] || { echo "No crew at $C. Make one with: cadrei new $CADREI_LIVE_CREW"; exit 2; }
P=$(mktemp -d "${TMPDIR:-/tmp}/cadrei-live-XXXXXX")
P=$(cd "$P" && pwd -P)
git -C "$P" init -q
LOG="$C/.claude/build/state/prompts.log"
mkdir -p "$(dirname "$LOG")"
touch "$LOG"
START_LINE=$(wc -l < "$LOG")
WAIT=${CADREI_LIVE_WAIT:-180s}
pass=0
failed=()
skipped=()

say() { printf '\n== %s\n' "$*"; }
ok() { pass=$((pass + 1)); printf '  ok   %s\n' "$1"; }
bad() { failed+=("$1"); printf '  FAIL %s\n' "$1"; }
crew() { (cd "$C" && cadrei "$@"); }
json() { crew ls --json | python3 -c "$1"; }

# The prober's own window, from cadrei ls --json.
session=""
window() {
  session=$(json 'import json,sys
for t in json.load(sys.stdin).get("running", []):
    if t.get("team") == "live":
        print(t["session"]); break')
}
state() {
  json 'import json,sys
for t in json.load(sys.stdin).get("running", []):
    for m in t.get("members", []):
        if m.get("role") == "prober": print(m.get("state", ""))'
}
idle() {
  for _ in $(seq 300); do [ "$(state)" = idle ] && return 0; sleep 1; done
  return 1
}
tell() {
  window
  tmux send-keys -t "=$session:=prober" -l "$1"
  sleep 0.5
  tmux send-keys -t "=$session:=prober" Enter
}
by_code() { tail -n +"$((START_LINE + 1))" "$LOG" | grep -q "\"code\":\"$1\".*\"ended\":\"by_code\""; }

# check_kind <name> <task> <choice> [<what the listing must show>]: tells
# the prober the task, waits for its request, answers it by its code and
# checks each step.
check_kind() {
  local name=$1 task=$2 choice=$3 shows=${4:-} out code
  say "$name"
  idle || { bad "$name: the prober never went idle first"; return; }
  tell "$task"
  out=$(crew answer --wait --timeout "$WAIT" || true)
  code=$(grep -oE '^  [a-z0-9]{3}  ' <<<"$out" | head -1 | tr -d ' ')
  if [ -z "$code" ]; then
    bad "$name: no code reached the wait"
    printf '%s\n' "$out" | sed 's/^/       /'
    return
  fi
  ok "$name: the wait shows it under $code"
  if crew answer "$code" | grep -q "$code" && { [ -z "$shows" ] || crew answer "$code" | grep -qF -- "$shows"; }; then
    ok "$name: cadrei answer $code shows it"
  else
    bad "$name: cadrei answer $code doesn't show it as expected"
  fi
  local picks
  read -ra picks <<<"$choice" # one answer per question
  if crew answer "$code" "${picks[@]}"; then
    ok "$name: answered $choice by its code"
  else
    bad "$name: the answer by its code failed"
    return
  fi
  sleep 2
  if crew answer "$code" >/dev/null 2>&1; then bad "$name: $code still waits after the answer"; else ok "$name: the request ended"; fi
  if by_code "$code"; then ok "$name: the prompt log says by its code"; else bad "$name: the prompt log doesn't say by its code"; fi
}

# by_hand <name> <what to do>: a case that needs the user's hands.
by_hand() {
  say "$1 (by hand)"
  printf '  %s\n' "$2"
  printf '  Press Enter when done, or s and Enter to skip: '
  local r
  read -r r
  if [ "$r" = s ]; then skipped+=("$1"); else ok "$1: done by hand, see the log check at the end"; fi
}

long_command() {
  local c="echo live-long-start"
  for i in $(seq 40); do c="$c && echo step-$i-of-the-live-check"; done
  printf '%s && echo live-long-end' "$c"
}

mkdir -p "$C/members/live"
cat > "$C/members/live/prober.md" <<'EOF'
# Member: prober

You help test cadrei, live. Each message asks for one step: do exactly
that step, with the tool it names, and nothing else. Don't explain, don't
ask in text, and don't retry a step that was stopped or denied: report it
in one line. When a message says to ask the user, use AskUserQuestion
with exactly the question and options it gives.
EOF
crew project add live-e2e --path "$P" --no-trust >/dev/null

# The cases, in order. A name on the command line runs only those.
all=(startup bash long wrapped heredoc tall write edit multiedit notebook longpath read fetch search mcp plan question wrapdesc multi twoq parallel first twoprompts guard denied safeguard otherscreens orchestrator)
want=("$@")
[ ${#want[@]} -gt 0 ] || want=("${all[@]}")
runs() { local c; for c in "${want[@]}"; do [ "$c" = "$1" ] && return 0; done; return 1; }

say "starting the prober on live-e2e, in a folder it doesn't trust yet"
crew up live/prober live-e2e | sed 's/^/  /'
if runs startup; then
  # Case 10: the trust screen of a folder cadrei may trust, by its code.
  out=$(crew answer --wait --timeout 60s || true)
  code=$(grep -oE '^  [a-z0-9]{3}  ' <<<"$out" | head -1 | tr -d ' ')
  if [ -n "$code" ] && grep -q 'start-up screen' <<<"$out"; then
    ok "startup: the trust screen has code $code"
    yes_n=$(crew answer "$code" | grep -E '^ +[0-9]\. Yes' | head -1 | grep -oE '[0-9]' | head -1)
    if [ -n "$yes_n" ] && crew answer "$code" "$yes_n"; then ok "startup: trusted by its code"; else bad "startup: the trust answer failed"; fi
    if by_code "$code"; then ok "startup: the prompt log says by its code"; else bad "startup: the prompt log doesn't say by its code"; fi
  else
    bad "startup: no trust screen code (already trusted?)"
  fi
fi

runs bash && check_kind "bash" "Run this with the Bash tool: mkdir live-e2e-dir" yes "mkdir live-e2e-dir"
runs long && check_kind "long" "Run this exact command with the Bash tool, as one command: $(long_command)" yes "live-long-end"
runs wrapped && check_kind "wrapped" "Run this exact command with the Bash tool: echo $(printf 'wrapped-at-eighty-columns-%.0s' $(seq 8))" yes "wrapped-at-eighty"
runs heredoc && check_kind "heredoc" "Run this with the Bash tool, as one command: cat > live-notes.txt <<'END' then the lines alpha, beta and gamma, then END on its own line" yes "live-notes.txt"
runs tall && check_kind "tall" "Run this with the Bash tool, as one command: a heredoc that writes the numbers 1 to 90, one per line, into live-tall.txt, written out in full in the command" yes "live-tall.txt"
runs write && check_kind "write" "Use the Write tool to create live-write.txt holding the word hello." yes "live-write.txt"
runs edit && check_kind "edit" "Use the Edit tool to change hello to goodbye in live-write.txt." yes "live-write.txt"
runs multiedit && check_kind "multiedit" "Use the MultiEdit tool (or Edit, if you have no MultiEdit) to change goodbye to one and add a line two in live-write.txt, in one call." yes "live-write.txt"
runs notebook && check_kind "notebook" "Use the Write tool to create live.ipynb as an empty Jupyter notebook with one code cell holding print(1), then the NotebookEdit tool to change that cell to print(2)." yes "live.ipynb"
runs longpath && check_kind "longpath" "Use the Write tool to create $(printf 'a-rather-long-folder-name-%.0s/' $(seq 6))deep.txt holding the word deep." yes "deep.txt"
runs read && check_kind "read" "Use the Read tool to read /etc/hosts." yes "/etc/hosts"
runs fetch && check_kind "fetch" "Use the WebFetch tool to fetch https://example.com and say its title." yes "example.com"
runs search && check_kind "search" "Use the WebSearch tool to search for: cadrei tmux crew" yes "cadrei tmux crew"
if runs mcp; then
  if [ -n "${CADREI_LIVE_MCP:-}" ]; then
    check_kind "mcp" "Call the MCP tool $CADREI_LIVE_MCP once, with no arguments or the simplest ones it takes." yes
  else
    skipped+=("mcp (set CADREI_LIVE_MCP to an MCP tool the member has)")
  fi
fi
runs plan && by_hand "plan" "In the prober's window (cadrei attach live live-e2e), press Shift-Tab until it's in plan mode, then ask it to plan a one-line change to live-write.txt. When it shows its plan, answer the plan's code from here with cadrei answer <code> <n> (it has no plain Yes). Then press Shift-Tab back to the normal mode."
runs question && check_kind "question" "Ask the user with AskUserQuestion: question 'Keep the live notes?', options Yes and No." 1 "Keep the live notes?"
runs wrapdesc && check_kind "wrapdesc" "Ask the user with AskUserQuestion: question 'Which way?', options 'Left' with the description '$(printf 'a long description that wraps onto a second line %.0s' 1 2 3)' and 'Right' with the description 'short'." 1 "Which way?"
runs multi && check_kind "multi" "Ask the user with AskUserQuestion, with multiSelect on: question 'Which files go?', options a.txt, b.txt and c.txt." "1,3" "Which files go?"
runs twoq && check_kind "twoq" "Ask the user with AskUserQuestion, two questions in one call: 'First?' with options Yes and No, and 'Second?' with options Red and Blue." "1 2" "First?"
runs parallel && check_kind "parallel" "In one turn, in parallel: run echo live-parallel with the Bash tool, and ask the user with AskUserQuestion: question 'Go on?', options Yes and No." 1 "Go on?"
runs first && check_kind "first" "Your first act in this turn: ask the user with AskUserQuestion: question 'First thing?', options Yes and No. Then say done." 1 "First thing?"
if runs twoprompts; then
  say "twoprompts"
  idle || bad "twoprompts: the prober never went idle first"
  tell "In one turn, in parallel, two Bash calls: mkdir live-two-a and mkdir live-two-b"
  out=$(crew answer --wait --timeout "$WAIT" || true)
  codes=$(grep -oE '^  [a-z0-9]{3}  ' <<<"$out" | tr -d ' ' | sort -u)
  sleep 3
  codes=$( (printf '%s\n' "$codes"; crew answer | grep -oE '^  [a-z0-9]{3}  ' | tr -d ' ') | sort -u | grep . || true)
  if [ "$(wc -l <<<"$codes")" -ge 2 ]; then ok "twoprompts: two codes"; else bad "twoprompts: not two codes: $codes"; fi
  for code in $codes; do
    if crew answer "$code" yes >/dev/null 2>&1 && by_code "$code"; then ok "twoprompts: $code by its code"; else bad "twoprompts: $code"; fi
  done
fi
runs guard && check_kind "guard" "Run this with the Bash tool: cadrei compact live/prober live-e2e" no "Cadrei's guard stopped"
runs denied && by_hand "denied" "Set PERMISSION_MODE=auto in $C/cadrei.conf, restart the prober (cadrei stop live/prober live-e2e, then cadrei up live/prober live-e2e), and ask it in its window to run: git push --force origin HEAD:nowhere. Auto mode should deny it. Check cadrei answer lists it as denied, that cadrei answer <code> yes shows the ! line and cadrei answer <code> no closes it. Then set PERMISSION_MODE back and restart it."
runs safeguard && by_hand "safeguard" "A safeguard stop can't be called up on purpose. If one happened during this release's use, check its code started the member again on another model with cadrei answer <code> 1 <model>, and that the stop didn't come back on that chat. If it did come back, note that 2 is the way out."
runs otherscreens && by_hand "otherscreens" "Live-only checks from PR C: (1) with an .mcp.json in $P naming a server, restart the prober and check the MCP server screen gets a code before any hook runs, and that it isn't dropped when SessionStart fires; (2) with a CLAUDE.md that imports a file outside $P, check how the imports screen draws each file line (cut or wrapped), and whether its No is saved; (3) restart the prober (it resumes) and check it keeps one session id, so an old safeguard stop doesn't come back under a new code."
runs orchestrator && by_hand "orchestrator" "Open this crew's orchestrator in plan mode (PERMISSION_MODE=plan in $C/cadrei.conf, then cadrei), leave a prober prompt waiting (ask it to run mkdir live-orch), and check the orchestrator offers ! cadrei answer <code> yes for you to type, and that typing it works. Then set the mode back."

say "the prompt log"
in_window=$(tail -n +"$((START_LINE + 1))" "$LOG" | grep -c '"ended":"in_window"' || true)
if [ "$in_window" = 0 ]; then ok "no prompt of this run ended in the member's window"; else bad "$in_window prompts of this run ended in the member's window (see $LOG)"; fi

say "cleaning up"
crew stop live/prober live-e2e --yes >/dev/null 2>&1 || true
crew project unlink live-e2e >/dev/null 2>&1 || true
rm -f "$C/members/live/prober.md"
rmdir "$C/members/live" 2>/dev/null || true
echo "  The project folder stays at $P for a look. Remove it when you're done."

printf '\n%d checks passed' "$pass"
[ ${#skipped[@]} -eq 0 ] || printf ', %d skipped: %s' "${#skipped[@]}" "$(IFS=,; echo "${skipped[*]}")"
printf '\n'
if [ ${#failed[@]} -gt 0 ]; then
  printf '%d failed:\n' "${#failed[@]}"
  printf '  %s\n' "${failed[@]}"
  exit 1
fi
