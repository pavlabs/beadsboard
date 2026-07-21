#!/bin/sh
# Claude Code SessionStart/SessionEnd hook: registers an EXTERNAL claude session
# in beadsboard's agent registry so it shows up on the bead it's working. Wire it
# for both events in .claude/settings.json (see README).
#
# Association: the session's bead comes from a `bead/<id>` git branch — sessions
# on any other branch are ignored, so ad-hoc work never registers. Sessions that
# beadsboard itself launched (BEADSBOARD_AGENT_ID set) are skipped; beadsboard
# owns those records. The session id keys the record because it is the only field
# present in BOTH SessionStart and SessionEnd, so cleanup is deterministic.
set -eu

payload=$(cat)
event=$(printf '%s' "$payload" | jq -r '.hook_event_name // empty')
sid=$(printf '%s' "$payload" | jq -r '.session_id // empty')
cwd=$(printf '%s' "$payload" | jq -r '.cwd // empty')
[ -n "$sid" ] || exit 0
[ -n "$cwd" ] || cwd=$PWD

case "$event" in
SessionEnd)
	beadsboard agent unregister --id "$sid" --cwd "$cwd" 2>/dev/null || true
	;;
SessionStart)
	# beadsboard-launched sessions are already registered by beadsboard.
	[ -n "${BEADSBOARD_AGENT_ID:-}" ] && exit 0
	branch=$(git -C "$cwd" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
	case "$branch" in
	bead/*) bead=${branch#bead/} ;;
	*) exit 0 ;; # not a bead branch: ignore ad-hoc sessions
	esac
	# PID is best-effort ($PPID may be an intermediate shell); SessionEnd is the
	# reliable cleanup, liveness only a fallback.
	beadsboard agent register \
		--id "$sid" --bead "$bead" --mode coding --source external \
		--tool claude --session "$sid" --cwd "$cwd" --pid "$PPID" 2>/dev/null || true
	;;
esac
