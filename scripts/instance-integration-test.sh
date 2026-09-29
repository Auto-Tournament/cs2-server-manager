#!/usr/bin/env bash
# Integration test for csm instance mode (overlay instances from one read-only
# CS2 install). Runs real CS2 + Ready Up servers, so it needs a Linux host with a
# CS2 install, kernel overlayfs in user namespaces (5.11+), tmux and two Ready Up
# bundle zips of different versions (scripts/package-release.sh in ready-up).
#
#   CSM=./csm RU_INSTALLER=.../install.sh \
#   RU_ZIP1=.../ready-up-essentials-A-linuxsteamrt64.zip \
#   RU_ZIP2=.../ready-up-essentials-B-linuxsteamrt64.zip \
#   scripts/instance-integration-test.sh
#
# Optional: MASTER (default /home/cs2servermanager/master-install; only read),
# STEAMCLIENT (steamclient.so), BASE_PORT (default 27200; must be >= 27100 so it
# stays clear of numbered servers), WORK (default ~/csm-instance-it/run).
#
# Isolation: everything lives under WORK (csm root, instances, logs), csm runs as
# the calling user with CS2_USER set to it, CSM_INSTANCE_MASTER_READONLY=1 keeps
# SteamCMD off the master, no GSLT is ever set, fleet.cfg is never written (no
# platform), and the game runs at nice 10. It never touches server-N folders,
# readyup-test* or ru-ci.
set -euo pipefail

: "${CSM:?CSM=path to the csm binary}"
: "${RU_INSTALLER:?RU_INSTALLER=path to Ready Up install.sh}"
: "${RU_ZIP1:?RU_ZIP1=Ready Up bundle zip (version A)}"
: "${RU_ZIP2:?RU_ZIP2=Ready Up bundle zip (version B)}"
MASTER="${MASTER:-/home/cs2servermanager/master-install}"
BASE_PORT="${BASE_PORT:-27200}"
WORK="${WORK:-$HOME/csm-instance-it/run}"
STEAMCLIENT="${STEAMCLIENT:-/home/cs2servermanager/.local/share/Steam/steamcmd/linux64/steamclient.so}"
BUNDLE="${BUNDLE:-essentials}"

(( BASE_PORT >= 27100 )) || { echo "BASE_PORT $BASE_PORT < 27100: refusing (numbered servers live below)" >&2; exit 2; }
case "$WORK" in
  */server-*|*readyup-test*|*ru-ci*|/home/cs2servermanager*) echo "refusing WORK=$WORK" >&2; exit 2 ;;
esac
CSM="$(readlink -f "$CSM")"
for f in "$CSM" "$RU_INSTALLER" "$RU_ZIP1" "$RU_ZIP2"; do [[ -f "$f" ]] || { echo "missing $f" >&2; exit 2; }; done

rm -rf "$WORK"
mkdir -p "$WORK/root"
export CSM_ROOT="$WORK/root" CSM_LOG_DIR="$WORK/logs" CSM_INSTANCE_ROOT="$WORK/instances"
export CSM_MASTER_DIR="$MASTER" CSM_INSTANCE_BASE_PORT="$BASE_PORT" CSM_INSTANCE_NICE=10
export CSM_STEAMCLIENT="$STEAMCLIENT" CSM_INSTANCE_MASTER_READONLY=1
export CS2_USER="$(id -un)" AT_ACCEPT_LICENSE=noncommercial CSM_SERVER_BACKEND=instances
unset CSM_PLATFORM_URL CSM_PLATFORM_TOKEN

PASS=0
ok()   { PASS=$((PASS + 1)); echo "  ok  $*"; }
fail() { echo "  FAIL $*" >&2; echo "--- csm status"; "$CSM" instance status || true; exit 1; }
step() { echo; echo "== $*"; }
csm()  { "$CSM" "$@"; }

cleanup() {
  for n in 1 2; do csm instance stop "$n" >/dev/null 2>&1 || true; done
  for n in 1 2; do tmux kill-session -t "=cs2-inst-$n" 2>/dev/null || true; done
}
trap cleanup EXIT

inst() { echo "$CSM_INSTANCE_ROOT/instance-$1"; }
status_json() { echo "$(inst "$1")/upper/game/csgo/readyup/status.json"; }
json_field() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get(sys.argv[2], ""))' "$1" "$2"; }
wait_status() { # wait_status N [old_pid]: Ready Up wrote status.json with a new live pid
  local n=$1 old=${2:-} f pid
  f=$(status_json "$n")
  for _ in $(seq 180); do
    if [[ -f "$f" ]]; then
      pid=$(json_field "$f" pid)
      if [[ -n "$pid" && "$pid" != "$old" && -d /proc/$pid ]]; then echo "$pid"; return 0; fi
    fi
    sleep 1
  done
  return 1
}
http_status() { # GET /status with the discovery token; prints update_safe
  local f; f=$(status_json "$1")
  python3 - "$f" <<'PY'
import json, sys, urllib.request
d = json.load(open(sys.argv[1]))
req = urllib.request.Request("http://127.0.0.1:%d/status" % d["port"], headers={"Authorization": "Bearer " + d.get("token", "")})
print(json.load(urllib.request.urlopen(req, timeout=3)).get("update_safe"))
PY
}
wait_path() { for _ in $(seq 90); do [[ -e "$1" ]] && return 0; sleep 1; done; return 1; }
master_snapshot() { find "$MASTER" -printf '%p %y %m %U %s %T@ %C@ %i\n' 2>/dev/null | sort | sha256sum | cut -c1-16; }
tree_sum() { (cd "$1" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-16); }

step "setup"
MASTER_BEFORE=$(master_snapshot)
ok "master snapshot $MASTER_BEFORE"
csm plugins auto off >/dev/null
csm updates hold off >/dev/null
csm updates grace 1 >/dev/null

step "Ready Up layer (version A)"
csm instance layer build --zip "$RU_ZIP1" --installer "$RU_INSTALLER" --bundle "$BUNDLE" > "$WORK/layer1.log" 2>&1 || { cat "$WORK/layer1.log"; fail "layer build"; }
L1=$(readlink -f "$CSM_INSTANCE_ROOT/layers/current")
[[ -f "$L1/game/csgo/readyup/installed.json" ]] || fail "layer has no installed.json"
grep -q 'csgo/readyup' "$L1/game/csgo/gameinfo.gi" || fail "layer gameinfo.gi has no Ready Up line"
[[ -d "$L1/game/csgo/maps" && -z "$(find "$L1/game/csgo/maps" -type f | head -1)" ]] || fail "layer lacks the master's (file-less) directory skeleton"
[[ ! -e "$L1/game/csgo/pak01_dir.vpk" ]] || fail "layer holds master files"
ok "layer $(basename "$L1"): $(json_field "$L1/game/csgo/readyup/installed.json" components)"
L1_SUM=$(tree_sum "$L1")

step "create + start two instances"
csm instance create 1 >/dev/null && csm instance create 2 >/dev/null || fail "create"
csm instance start all || fail "start"
P1=$(wait_status 1) || fail "instance 1: no Ready Up status.json"
P2=$(wait_status 2) || fail "instance 2: no Ready Up status.json"
ok "running: pids $P1 $P2"
for n in 1 2; do
  gp=$(json_field "$(status_json "$n")" game_port)
  [[ "$gp" == "$((BASE_PORT + 10 * n))" ]] || fail "instance $n game_port $gp"
  [[ "$(json_field "$(status_json "$n")" port)" == "$((BASE_PORT + 10 * n + 7))" ]] || fail "instance $n status port"
  [[ -f "$(inst "$n")/upper/game/bin/linuxsteamrt64/steam_appid.txt" ]] || fail "instance $n: steam_appid.txt not in its upper"
  wait_path "$(inst "$n")/home/Steam" || fail "instance $n: Steam did not use the instance HOME"
done
ok "ports base+10N (+7 status), per-instance upper + HOME"
csm instance status | tee "$WORK/status1.txt" | sed 's/^/    /'
grep -Eq '^1 +running' "$WORK/status1.txt" && grep -Eq '^2 +running' "$WORK/status1.txt" || fail "status table"
for n in 1 2; do
  for _ in $(seq 60); do s=$(http_status "$n" 2>/dev/null || true); [[ "$s" == True ]] && break; sleep 2; done
  [[ "$s" == True ]] || fail "instance $n: Ready Up /status update_safe=$s"
done
ok "Ready Up /status answers on both (update_safe=true while idle)"
[[ -z "$(find /dev/shm -maxdepth 1 -user "$(id -un)" -name 'u0-*' -newer "$WORK/layer1.log" 2>/dev/null)" ]] || fail "Steam shm files leaked into the host /dev/shm"
ok "private /dev/shm: nothing in the host's"

step "crash restart"
kill -9 "$P1"
NP1=$(wait_status 1 "$P1") || fail "instance 1 did not come back after kill -9"
grep -q '\[csm\] restarting in' "$(inst 1)/console.log" || fail "no crash-restart line in the console log"
ok "instance 1 restarted by its supervisor: pid $P1 -> $NP1"

step "shadowed update files"
cfg="game/csgo/cfg/ReadyUp/warmup.cfg"
[[ -f "$L1/$cfg" ]] || cfg=$(cd "$L1" && find game/csgo/cfg/ReadyUp -name '*.cfg' ! -name fleet.cfg | head -1)
mkdir -p "$(dirname "$(inst 2)/upper/$cfg")"
{ cat "$L1/$cfg"; echo "// edited in instance 2"; } > "$(inst 2)/upper/$cfg"
csm instance status > "$WORK/status2.txt"
grep -q "instance 2 has its own copy" "$WORK/status2.txt" && grep -q "$cfg" "$WORK/status2.txt" || { cat "$WORK/status2.txt"; fail "no shadow warning"; }
ok "shadow warning for $cfg"

step "Ready Up update (version B) restarts idle instances onto the new layer"
csm instance update --zip "$RU_ZIP2" --installer "$RU_INSTALLER" --bundle "$BUNDLE" > "$WORK/update.log" 2>&1 || { cat "$WORK/update.log"; fail "update"; }
L2=$(readlink -f "$CSM_INSTANCE_ROOT/layers/current")
[[ "$L2" != "$L1" ]] || fail "current layer did not move"
grep -q "Instance-1: restarted" "$WORK/update.log" && grep -q "Instance-2: restarted" "$WORK/update.log" || { cat "$WORK/update.log"; fail "idle instances not restarted"; }
grep -q "instance 2 has its own copy" "$WORK/update.log" || fail "restart did not warn about instance 2's shadowed file"
for n in 1 2; do
  wait_status "$n" >/dev/null || fail "instance $n not back after the update"
  [[ "$(cat "$(inst "$n")/layer.inuse")" == "$L2" ]] || fail "instance $n runs $(cat "$(inst "$n")/layer.inuse")"
done
[[ "$(tree_sum "$L1")" == "$L1_SUM" ]] || fail "the old layer was written to"
ok "both instances on $(basename "$L2"); old layer untouched"

step "update hold: csm monitor never restarts onto a new layer while held"
csm instance layer use "$(basename "$L1")" >/dev/null
csm updates hold on >/dev/null
csm monitor >/dev/null 2>&1 || true
grep -q "waits: updates are on hold" "$CSM_LOG_DIR/csm.log" || fail "monitor did not report the hold"
for n in 1 2; do [[ "$(cat "$(inst "$n")/layer.inuse")" == "$L2" ]] || fail "instance $n restarted during a hold"; done
ok "held: both still on $(basename "$L2")"
csm updates hold off >/dev/null
csm monitor >/dev/null 2>&1 || true   # starts the 1-minute idle grace
sleep 65
csm monitor >/dev/null 2>&1 || true
for n in 1 2; do
  wait_status "$n" >/dev/null || fail "instance $n not back after the monitor restart"
  [[ "$(cat "$(inst "$n")/layer.inuse")" == "$L1" ]] || { tail -30 "$CSM_LOG_DIR/csm.log"; fail "instance $n not rolled onto $(basename "$L1")"; }
done
ok "hold off + idle grace: monitor rolled both onto $(basename "$L1")"

step "stop + remove"
csm instance stop all >/dev/null
for n in 1 2; do tmux has-session -t "=cs2-inst-$n" 2>/dev/null && fail "instance $n still running"; done
! grep -q "$CSM_INSTANCE_ROOT" /proc/self/mountinfo || fail "an instance mount leaked into the host namespace"
csm instance remove 1 >/dev/null && csm instance remove 2 >/dev/null || fail "remove"
[[ ! -e "$(inst 1)" && ! -e "$(inst 2)" ]] || fail "instance dirs left behind"
ok "stopped, no leaked mounts, removed"

step "master install"
[[ "$(master_snapshot)" == "$MASTER_BEFORE" ]] || fail "the master install changed"
ok "master unchanged ($MASTER_BEFORE)"

echo
echo "PASS ($PASS checks)"
