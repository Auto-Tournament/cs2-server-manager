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
# stays clear of numbered servers), WORK (default ~/csm-instance-it/run), FARM
# (default ~/csm-instance-it/master-farm).
#
# The instances do not run on MASTER itself but on FARM, a test copy of it
# owned by the calling user: every file except the VPKs is copied (~0.7 GB),
# the VPKs are symlinks into MASTER. csm treats FARM as its master install,
# so the CS2 update steps can hardlink it into new game versions; they run a
# fake SteamCMD (a small update: an in-place append, a new file, a deleted
# file), never a real one.
#
# Isolation: everything lives under WORK (csm root, instances, logs) and FARM,
# csm runs as the calling user with CS2_USER set to it, only the fake SteamCMD
# ever runs (CSM_INSTANCE_MASTER_READONLY=1 everywhere else), no GSLT is ever
# set, fleet.cfg is never written (no platform), and the game runs at nice 10.
# It never touches server-N folders, readyup-test* or ru-ci, and checks that
# MASTER and FARM are unchanged at the end.
set -euo pipefail

: "${CSM:?CSM=path to the csm binary}"
: "${RU_INSTALLER:?RU_INSTALLER=path to Ready Up install.sh}"
: "${RU_ZIP1:?RU_ZIP1=Ready Up bundle zip (version A)}"
: "${RU_ZIP2:?RU_ZIP2=Ready Up bundle zip (version B)}"
MASTER="${MASTER:-/home/cs2servermanager/master-install}"
BASE_PORT="${BASE_PORT:-27200}"
WORK="${WORK:-$HOME/csm-instance-it/run}"
FARM="${FARM:-$HOME/csm-instance-it/master-farm}"
STEAMCLIENT="${STEAMCLIENT:-/home/cs2servermanager/.local/share/Steam/steamcmd/linux64/steamclient.so}"
BUNDLE="${BUNDLE:-essentials}"

(( BASE_PORT >= 27100 )) || { echo "BASE_PORT $BASE_PORT < 27100: refusing (numbered servers live below)" >&2; exit 2; }
for d in "$WORK" "$FARM"; do
  case "$d" in
    */server-*|*readyup-test*|*ru-ci*|/home/cs2servermanager*|"$MASTER"*) echo "refusing $d" >&2; exit 2 ;;
  esac
done
CSM="$(readlink -f "$CSM")"
for f in "$CSM" "$RU_INSTALLER" "$RU_ZIP1" "$RU_ZIP2"; do [[ -f "$f" ]] || { echo "missing $f" >&2; exit 2; }; done

# FARM: rebuilt when MASTER's build changes.
if ! cmp -s "$MASTER/game/csgo/steam.inf" "$FARM/game/csgo/steam.inf"; then
  echo "== building the test master $FARM from $MASTER"
  rm -rf "$FARM" "$FARM.tmp"
  (cd "$MASTER" && find . -type d -print0) | (cd "$(dirname "$FARM")" && mkdir -p "$FARM.tmp" && cd "$FARM.tmp" && xargs -0 mkdir -p)
  (cd "$MASTER" && find . -type f ! -name '*.vpk' -print0) | (cd "$MASTER" && xargs -0 cp --parents -p -t "$FARM.tmp")
  (cd "$MASTER" && find . -type f -name '*.vpk' -printf '%P\n') | while IFS= read -r f; do ln -s "$MASTER/$f" "$FARM.tmp/$f"; done
  echo "removed by the fake CS2 update" > "$FARM.tmp/game/csm_fake_removed.txt"
  chmod -R u+w "$FARM.tmp"
  mv "$FARM.tmp" "$FARM"
fi

rm -rf "$WORK"
mkdir -p "$WORK/root" "$WORK/bin"
export CSM_ROOT="$WORK/root" CSM_LOG_DIR="$WORK/logs" CSM_INSTANCE_ROOT="$WORK/instances"
export CSM_MASTER_DIR="$FARM" CSM_INSTANCE_BASE_PORT="$BASE_PORT" CSM_INSTANCE_NICE=10

# The fake SteamCMD: a small CS2 "update" in +force_install_dir. The append
# writes in place, like SteamCMD trimming a file, so a version sharing the
# file by hardlink would see it change.
cat > "$WORK/bin/steamcmd" <<EOF
#!/usr/bin/env bash
set -euo pipefail
dir=""
while (( \$# )); do [[ \$1 == +force_install_dir ]] && { dir=\$2; shift; }; shift; done
case "\$dir" in "$WORK"/instances/games/.update-*/mnt) ;; *) echo "fake steamcmd: refusing install dir \$dir" >&2; exit 1 ;; esac
n=\$(( \$(cat "$WORK/fake-steamcmd.count" 2>/dev/null || echo 0) + 1 )); echo "\$n" > "$WORK/fake-steamcmd.count"
echo "// csm fake update \$n" >> "\$dir/game/thirdpartylegalnotices.txt"
echo "fake update \$n" > "\$dir/game/csm_fake_update.txt"
rm -f "\$dir/game/csm_fake_removed.txt"
echo "Success! App '730' fully installed. (fake \$n)"
EOF
chmod +x "$WORK/bin/steamcmd"
fake_update_game() { # fake_update_game LOG: csm instance update-game with the fake SteamCMD
  PATH="$WORK/bin:$PATH" CSM_INSTANCE_MASTER_READONLY=0 "$CSM" instance update-game > "$1" 2>&1
}
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
inuse_layer() { sed -n 1p "$(inst "$1")/layer.inuse"; }
inuse_game() { sed -n 2p "$(inst "$1")/layer.inuse"; }
tree_snapshot() { find "$1" -printf '%p %y %m %s %T@ %i %l\n' | sort | sha256sum | cut -c1-16; }
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
FARM_BEFORE=$(tree_snapshot "$FARM")
ok "test master snapshot $FARM_BEFORE"
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
  [[ "$(inuse_layer "$n")" == "$L2" ]] || fail "instance $n runs $(inuse_layer "$n")"
done
[[ "$(tree_sum "$L1")" == "$L1_SUM" ]] || fail "the old layer was written to"
ok "both instances on $(basename "$L2"); old layer untouched"

step "update hold: csm monitor never restarts onto a new layer while held"
csm instance layer use "$(basename "$L1")" >/dev/null
csm updates hold on >/dev/null
csm monitor >/dev/null 2>&1 || true
grep -q "waits: updates are on hold" "$CSM_LOG_DIR/csm.log" || fail "monitor did not report the hold"
for n in 1 2; do [[ "$(inuse_layer "$n")" == "$L2" ]] || fail "instance $n restarted during a hold"; done
ok "held: both still on $(basename "$L2")"
csm updates hold off >/dev/null
csm monitor >/dev/null 2>&1 || true   # starts the 1-minute idle grace
sleep 65
csm monitor >/dev/null 2>&1 || true
for n in 1 2; do
  wait_status "$n" >/dev/null || fail "instance $n not back after the monitor restart"
  [[ "$(inuse_layer "$n")" == "$L1" ]] || { tail -30 "$CSM_LOG_DIR/csm.log"; fail "instance $n not rolled onto $(basename "$L1")"; }
done
ok "hold off + idle grace: monitor rolled both onto $(basename "$L1")"

step "CS2 update: a new game version; the running one is never written"
[[ "$(inuse_game 1)" == "$FARM" ]] || fail "instance 1 game $(inuse_game 1), want $FARM"
fake_update_game "$WORK/update-game1.log" || { cat "$WORK/update-game1.log"; fail "update-game"; }
G1=$(readlink -f "$CSM_INSTANCE_ROOT/games/current")
[[ -d "$G1" && "$G1" != "$FARM" ]] || { cat "$WORK/update-game1.log"; fail "no new game version"; }
grep -q "csm fake update 1" "$G1/game/thirdpartylegalnotices.txt" || fail "the in-place append did not reach the new version"
grep -q "csm fake update" "$FARM/game/thirdpartylegalnotices.txt" && fail "the in-place append wrote through to the running version"
[[ -f "$G1/game/csm_fake_update.txt" ]] || fail "new file missing in the new version"
[[ ! -e "$G1/game/csm_fake_removed.txt" && -e "$FARM/game/csm_fake_removed.txt" ]] || fail "deletion (whiteout) not applied, or applied to the running version"
[[ "$(stat -c %i "$FARM/game/csgo/gameinfo.gi")" == "$(stat -c %i "$G1/game/csgo/gameinfo.gi")" ]] || fail "unchanged files are not hardlinked"
[[ "$(json_field "$G1.json" copy)" == hardlink ]] || fail "copy mode $(json_field "$G1.json" copy)"
[[ "$(tree_snapshot "$FARM")" == "$FARM_BEFORE" ]] || fail "the running game version changed"
ok "game version $(basename "$G1"): hardlinked, update applied, running version untouched"
L3=$(readlink -f "$CSM_INSTANCE_ROOT/layers/current")
[[ "$L3" != "$L1" && "$(cat "$L3.game")" == "$G1" ]] || fail "layer not rebuilt on $(basename "$G1")"
grep -q "Instance-1: restarted" "$WORK/update-game1.log" && grep -q "Instance-2: restarted" "$WORK/update-game1.log" || { cat "$WORK/update-game1.log"; fail "idle instances not restarted"; }
for n in 1 2; do
  wait_status "$n" >/dev/null || fail "instance $n not back after the CS2 update"
  [[ "$(inuse_layer "$n")" == "$L3" && "$(inuse_game "$n")" == "$G1" ]] || fail "instance $n runs $(inuse_layer "$n") on $(inuse_game "$n")"
done
ok "layer $(basename "$L3") on $(basename "$G1"); both instances restarted onto it"

step "CS2 update while held: busy instances keep their game version until they move"
csm updates hold on >/dev/null
fake_update_game "$WORK/update-game2.log" || { cat "$WORK/update-game2.log"; fail "update-game (held)"; }
G2=$(readlink -f "$CSM_INSTANCE_ROOT/games/current")
[[ "$G2" != "$G1" ]] || fail "no second game version"
grep -q "waits: updates are on hold" "$WORK/update-game2.log" || { cat "$WORK/update-game2.log"; fail "held instances were not left alone"; }
for n in 1 2; do [[ "$(inuse_game "$n")" == "$G1" ]] || fail "instance $n left $(basename "$G1") during a hold"; done
[[ -d "$G1" ]] || fail "$(basename "$G1") removed while instances run on it"
csm instance game | tee "$WORK/games.txt" | sed 's/^/    /'
grep "^$(basename "$G1") " "$WORK/games.txt" | grep -q "in use" || fail "game report does not show $(basename "$G1") in use"
csm status --no-color > "$WORK/status-plain.txt"
grep -q "restart pending" "$WORK/status-plain.txt" || { cat "$WORK/status-plain.txt"; fail "csm status does not show the pending restarts"; }
ok "held: both still on $(basename "$G1"), kept; $(basename "$G2") is current"
csm updates hold off >/dev/null
csm monitor >/dev/null 2>&1 || true   # starts the 1-minute idle grace
sleep 65
csm monitor >/dev/null 2>&1 || true
for n in 1 2; do
  wait_status "$n" >/dev/null || fail "instance $n not back after the monitor restart"
  [[ "$(inuse_game "$n")" == "$G2" ]] || { tail -30 "$CSM_LOG_DIR/csm.log"; fail "instance $n not rolled onto $(basename "$G2")"; }
done
[[ ! -e "$G1" ]] || fail "$(basename "$G1") not collected once no instance used it"
[[ -d "$FARM/game" ]] || fail "the master install was collected"
ok "rolled onto $(basename "$G2"); $(basename "$G1") collected, master kept"

step "plain csm status / stop / start / logs act on instances (backend=instances)"
csm status --no-color > "$WORK/status-plain2.txt"
grep -q "CS2 instances" "$WORK/status-plain2.txt" && grep -q "on CS2 game version $(basename "$G2")" "$WORK/status-plain2.txt" || { cat "$WORK/status-plain2.txt"; fail "csm status"; }
csm stop 2 >/dev/null || fail "csm stop 2"
tmux has-session -t "=cs2-inst-2" 2>/dev/null && fail "csm stop 2 left instance 2 running"
tmux has-session -t "=cs2-inst-1" 2>/dev/null || fail "csm stop 2 stopped instance 1"
csm start 2 >/dev/null || fail "csm start 2"
wait_status 2 >/dev/null || fail "instance 2 not back after csm start 2"
csm logs 2 5000 > "$WORK/logs2.txt" 2>&1 || fail "csm logs 2"
grep -q "\[csm\] instance mounted on layer" "$WORK/logs2.txt" || fail "csm logs 2 lacks the mount line"
csm stop 9 >/dev/null 2>&1 && fail "csm stop 9 accepted a missing instance"
ok "status, stop 2, start 2, logs 2"

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
[[ "$(tree_snapshot "$FARM")" == "$FARM_BEFORE" ]] || fail "the test master changed"
ok "test master unchanged ($FARM_BEFORE)"

echo
echo "PASS ($PASS checks)"
