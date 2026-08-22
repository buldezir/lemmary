#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BACKEND_PID=""
FRONTEND_PID=""

kill_tree() {
  local pid="$1"
  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    return 0
  fi
  # Kill children first (e.g. go-run binary, vite under npm).
  local child
  for child in $(pgrep -P "$pid" 2>/dev/null || true); do
    kill_tree "$child"
  done
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

cleanup() {
  local code=$?
  trap - EXIT INT TERM
  echo ""
  echo "Stopping dev servers..."
  kill_tree "$FRONTEND_PID"
  kill_tree "$BACKEND_PID"
  exit "$code"
}

trap cleanup EXIT INT TERM

# A run that was SIGKILLed (or whose terminal just went away) leaves the backend
# and the vite/vitepress servers behind. They keep the ports and the SQLite DB,
# so the next run silently comes up half-broken: vite drifts to another port and
# the backend blocks on the locked database instead of listening. Reap anything
# left over from an earlier run of this script before starting a new one.

proc_cmdline() {
  # The process can vanish mid-scan; swallow the failed redirection too.
  { tr '\0' ' ' < "/proc/$1/cmdline"; } 2>/dev/null || true
}

# Never touch our own process or the shell that launched us -- it usually sits
# in $ROOT too.
self_and_ancestors() {
  local pid=$$
  while [[ -n "$pid" && "$pid" -gt 1 ]]; do
    printf '%s\n' "$pid"
    pid="$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d '[:space:]')"
  done
}

# A process is ours if it runs out of this repo AND its argv is one of the
# programs this script starts. Both halves matter: the cwd check keeps other
# projects' dev servers safe, and matching argv entries exactly (never a
# substring of the whole command line) keeps shells, editors and greps that
# merely mention "dev.sh" or "vite" from being mistaken for the real thing.
is_stale_dev_proc() {
  local pid="$1" cwd
  cwd="$(readlink -f "/proc/$pid/cwd" 2>/dev/null)" || return 1
  [[ "$cwd" == "$ROOT" || "$cwd" == "$ROOT"/* ]] || return 1
  [[ -r "/proc/$pid/cmdline" ]] || return 1

  local -a argv=()
  mapfile -t argv < <({ tr '\0' '\n' < "/proc/$pid/cmdline"; } 2>/dev/null)
  [[ ${#argv[@]} -gt 0 ]] || return 1

  local a0="${argv[0]##*/}" a1="${argv[1]:-}" arg
  a1="${a1##*/}"

  case "$a0" in
    backend|vite|vitepress|concurrently) return 0 ;;
    go)
      if [[ "${argv[1]:-}" == run ]]; then return 0; fi
      ;;
    node|node[0-9]*|npm|npx)
      case "$a1" in vite|vitepress|concurrently) return 0 ;; esac
      ;;
    bash|sh|dash|zsh|ksh)
      # Only an argv entry that *is* the script counts, not a -c string
      # quoting it.
      for arg in "${argv[@]:1}"; do
        case "$arg" in
          */scripts/dev.sh|scripts/dev.sh) return 0 ;;
        esac
      done
      ;;
  esac
  return 1
}

collect_tree() {
  local pid="$1" child
  printf '%s\n' "$pid"
  for child in $(pgrep -P "$pid" 2>/dev/null || true); do
    collect_tree "$child"
  done
}

kill_stale() {
  [[ -d /proc ]] || return 0

  local skip pid entry child cmd
  skip=" $(self_and_ancestors | tr '\n' ' ')"

  declare -A seen=()
  local -a victims=()
  for entry in /proc/[0-9]*; do
    pid="${entry##*/}"
    [[ "$skip" == *" $pid "* ]] && continue
    kill -0 "$pid" 2>/dev/null || continue
    is_stale_dev_proc "$pid" || continue
    # Sweep descendants too (esbuild, the go-run binary, vite workers); they do
    # not match the patterns above but die with their parent.
    for child in $(collect_tree "$pid"); do
      [[ "$skip" == *" $child "* ]] && continue
      [[ -n "${seen[$child]:-}" ]] && continue
      seen[$child]=1
      victims+=("$child")
    done
  done

  [[ ${#victims[@]} -gt 0 ]] || return 0

  echo "Reaping ${#victims[@]} stale dev process(es) from an earlier run:"
  for pid in "${victims[@]}"; do
    cmd="$(proc_cmdline "$pid")"
    printf '  %s  %.70s\n' "$pid" "${cmd:-<exited>}"
    kill "$pid" 2>/dev/null || true
  done

  local waited=0 alive=1
  while [[ $waited -lt 50 ]]; do
    alive=0
    for pid in "${victims[@]}"; do
      if kill -0 "$pid" 2>/dev/null; then alive=1; fi
    done
    [[ $alive -eq 0 ]] && break
    sleep 0.1
    waited=$((waited + 1))
  done

  if [[ $alive -ne 0 ]]; then
    for pid in "${victims[@]}"; do
      kill -0 "$pid" 2>/dev/null && echo "  SIGKILL $pid" || true
      kill -9 "$pid" 2>/dev/null || true
    done
    sleep 0.5
  fi
}

kill_stale

# Bind to the machine's current LAN IP so the dev servers are reachable from
# other devices (phone, tablet, another laptop). Override with DEV_HOST=...
detect_host() {
  local ip=""
  if command -v ip >/dev/null 2>&1; then
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null \
      | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')"
  fi
  if [[ -z "$ip" ]] && command -v hostname >/dev/null 2>&1; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  fi
  printf '%s' "${ip:-127.0.0.1}"
}

HOST="${DEV_HOST:-$(detect_host)}"
export DEV_HOST="$HOST"
# Browser-side PocketBase URL must be reachable from the client device too.
export VITE_POCKETBASE_URL="${VITE_POCKETBASE_URL:-http://$HOST:8090}"

if [[ ! -f .env ]]; then
  echo "warning: .env not found; copy from .env.example if needed" >&2
fi

prefix() {
  local name="$1"
  local color="$2"
  local reset=$'\033[0m'
  while IFS= read -r line || [[ -n "$line" ]]; do
    printf '%b[%s]%b %s\n' "$color" "$name" "$reset" "$line"
  done
}

echo "Starting backend on http://$HOST:8090"
(cd "$ROOT/backend" && go run . serve --http="$HOST:8090") \
  > >(prefix "backend" $'\033[36m') 2>&1 &
BACKEND_PID=$!

echo "Starting frontend + docs on http://$HOST:5173 and http://$HOST:5174"
(cd "$ROOT/frontend" && npm run dev) \
  > >(prefix "frontend" $'\033[33m') 2>&1 &
FRONTEND_PID=$!

echo ""
echo "Dev servers running. Press Ctrl+C to stop."
echo "  Backend:  http://$HOST:8090"
echo "  Docs:     http://$HOST:5174/docs/"
echo "  Frontend: http://$HOST:5173"
echo ""

while kill -0 "$BACKEND_PID" 2>/dev/null && kill -0 "$FRONTEND_PID" 2>/dev/null; do
  sleep 1
done

if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
  wait "$BACKEND_PID" || true
  echo "backend exited" >&2
  exit 1
fi
if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
  wait "$FRONTEND_PID" || true
  echo "frontend exited" >&2
  exit 1
fi
