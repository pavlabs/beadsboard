#!/usr/bin/env bash
# Seeds the throwaway beads project the demo GIF is recorded against, so the
# recording never depends on anyone's real board. Re-run before `vhs docs/demo.tape`.
set -euo pipefail

DIR=${1:-/tmp/payments-platform}
rm -rf "$DIR"
mkdir -p "$DIR"
cd "$DIR"
git init -q
bd init --prefix demo >/dev/null

epic() { bd create --type=epic --priority="$1" --title="$2" --description="$3" --json | jq -r .id; }
task() { bd create --type=task --priority="$1" --parent="$2" --title="$3" --json | jq -r .id; }

pay=$(epic 0 "Payments platform" "Ship the v2 payments stack: ledger, gateway, payouts.")
obs=$(epic 1 "Observability" "Traces, metrics and logs for the payments stack.")
dx=$(epic 2 "Developer experience" "Make the stack pleasant to work on locally.")

ledger=$(task 0 "$pay" "Design double-entry ledger schema")
gateway=$(task 1 "$pay" "Stripe gateway adapter")
payouts=$(task 2 "$pay" "Payout scheduler")

tracing=$(task 1 "$obs" "Trace the payment path end to end")
task 2 "$obs" "Dashboards for gateway latency" >/dev/null
stack=$(task 2 "$dx" "One-command local stack")

# One cross-epic task dependency, which derives the epic-level edge on its own —
# adding the epic edge too would read as a cycle on the board.
bd dep add "$gateway" "$tracing"

bd update "$ledger" --status in_progress
# Blocked beads so the attention inbox has something real to show.
bd update "$payouts" --status blocked
bd update "$stack" --status blocked

echo "seeded $DIR"
