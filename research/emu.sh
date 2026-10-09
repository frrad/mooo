#!/bin/bash
# Owned A/B emulator preparation. Configuration lives in .lab/emu-config.sh.
# Usage: emu.sh up|status|down|login|finish|verify|ready|qr-approve|qr-code|qr-finish|send-text|send-photo|send-video|send-audio|send-profile|send-contact|send-file|send-album|send-sticker a|b [--login]
# ready (or up --login) permits one password submission and normal verification.
# Configure explicit login and required-policy authorization before using auth.
set -euo pipefail
umask 077

SDK=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}
ADB=$SDK/platform-tools/adb
EMULATOR=$SDK/emulator/emulator
PKG=com.kakao.talk
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_DIR=$(cd "$SCRIPT_DIR/.." && pwd)
LAB_DIR=$REPO_DIR/.lab
TOOL_DIR=$REPO_DIR/tools/lab
LOGDIR=${EMU_RUNTIME_DIR:-"$HOME/Library/Caches/mooo-lab/emu"}

usage() { echo "usage: $0 up|status|down|login|finish|verify|ready|qr-approve|qr-code|qr-finish|send-text|send-photo|send-video|send-audio|send-profile|send-contact|send-file|send-album|send-sticker a|b [--login]" >&2; exit 2; }

profile() {
  case "$1" in
    a) AVD=${AVD_A:?configure AVD_A}; SERVICE=${KEYCHAIN_A:?configure KEYCHAIN_A} ;;
    b) AVD=${AVD_B:?configure AVD_B}; SERVICE=${KEYCHAIN_B:?configure KEYCHAIN_B} ;;
    *) usage ;;
  esac
}

# find_serial: serial of the running emulator whose AVD name is $AVD.
find_serial() {
  SERIAL=""
  for s in $("$ADB" devices | awk 'NR>1 && $2=="device" {print $1}'); do
    if [ "$("$ADB" -s "$s" emu avd name 2>/dev/null | head -1 | tr -d '\r')" = "$AVD" ]; then
      SERIAL=$s
      return 0
    fi
  done
  return 1
}

adb() { "$ADB" -s "$SERIAL" "$@"; }

dump() {
  local remote="/sdcard/mooo-ui-$$-$RANDOM.xml" ui attempt
  for attempt in 1 2 3; do
    if adb shell uiautomator dump "$remote" >/dev/null 2>&1; then
      ui=$(adb exec-out cat "$remote" 2>/dev/null) || ui=""
      adb shell rm -f "$remote" >/dev/null 2>&1
      if python3 -c 'import sys,xml.etree.ElementTree as E; E.fromstring(sys.stdin.read())' <<<"$ui" 2>/dev/null; then
        printf '%s\n' "$ui"
        return 0
      fi
    fi
    adb shell rm -f "$remote" >/dev/null 2>&1
    sleep 1
  done
  echo "UI dump failed" >&2
  return 1
}

# center <xml> <resource-id> <index>: tap target for the nth matching node.
center() {
  python3 -c '
import sys, re, xml.etree.ElementTree as E
root = E.fromstring(sys.stdin.read())
nodes = [n for n in root.iter("node") if n.get("resource-id") == sys.argv[1]]
index = int(sys.argv[2])
if len(nodes) <= index:
    sys.exit(1)
x1, y1, x2, y2 = map(int, re.findall(r"\d+", nodes[index].get("bounds")))
print((x1 + x2) // 2, (y1 + y2) // 2)
' "$2" "$3" <<<"$1"
}

# type_secret: read one line from stdin and type it on the device. Input is
# single-quoted for the device shell; spaces become %s for `input text`.
type_secret() {
  python3 -c '
import sys
value = sys.stdin.readline().rstrip("\n").replace(" ", "%s")
print("input text \x27" + value.replace("\x27", "\x27\\\x27\x27") + "\x27")
' | adb shell
}

hide_keyboard() {
  # Auth controls remain above the IME. Never send BACK: hardware-keyboard
  # emulators may report an active IME even when it is not onscreen.
  :
}

clear_field() {
  adb shell input keycombination 113 29
  adb shell input keyevent KEYCODE_DEL
}

launch() {
  adb shell am start -W -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -n "$PKG/.activity.SplashActivity" >/dev/null
  sleep 5
}

# Classification prints no user-controlled labels or field values.
state() {
  local ui
  ui=$(dump)
  python3 "$TOOL_DIR/emu_state.py" <<<"$ui"
}

# Explicit DNS is useful when a long-running emulator retains an old host
# resolver after a network change. Never substitute a resolver silently.
emulator_boot_command() {
  local args=("$EMULATOR" -avd "$AVD" -no-snapshot -no-boot-anim)
  if [ -n "${EMU_DNS_SERVER:-}" ]; then
    python3 -c 'import ipaddress,sys; ipaddress.ip_address(sys.argv[1])' "$EMU_DNS_SERVER" >/dev/null 2>&1 || {
      echo "EMU_DNS_SERVER must be a numeric IP address" >&2; return 1;
    }
    args+=(-dns-server "$EMU_DNS_SERVER")
  fi
  printf '%q ' "${args[@]}"
  printf '> %q 2>&1' "$LOGDIR/emu-$AVD.log"
}

cmd_up() {
  if ! find_serial; then
    echo "booting $AVD"
    command -v tmux >/dev/null || { echo "tmux is required for persistent emulator startup" >&2; exit 1; }
    local boot_command
    : > "$LOGDIR/emu-$AVD.log"
    chmod 600 "$LOGDIR/emu-$AVD.log"
    boot_command=$(emulator_boot_command) || return 1
    tmux new-session -d -s "mooo-emu-$WHO" "$boot_command"
    for _ in $(seq 1 60); do find_serial && break; sleep 3; done
    find_serial || { echo "$AVD did not come up; see $LOGDIR/emu-$AVD.log" >&2; exit 1; }
  fi
  local booted=false
  for _ in $(seq 1 100); do
    if [ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = 1 ]; then booted=true; break; fi
    sleep 3
  done
  "$booted" || { echo "boot timed out" >&2; exit 1; }
  adb shell input keyevent KEYCODE_WAKEUP
  adb shell wm dismiss-keyguard
  [ -n "$(adb shell pm path "$PKG")" ] || { echo "KakaoTalk is not installed" >&2; exit 1; }
  echo "emulator ready"
  launch
  local report
  report=$(state)
  echo "$report"
  if [ "${1:-}" = "--login" ]; then
    cmd_ready_steps
  fi
}

cmd_status() {
  find_serial || { echo "$AVD is not running" >&2; exit 1; }
  echo "serial=$SERIAL avd=$AVD"
  state
}

# Explicit per-lab authorization allows automation without interactive secrets.
require_login_authorization() {
  [ "${ALLOW_LOGIN:-0}" = 1 ] || { echo "configure ALLOW_LOGIN=1 for the owned lab profiles" >&2; exit 1; }
}

assert_client_version() {
  local version
  version=$(adb shell dumpsys package "$PKG" | sed -n 's/.*versionName=//p' | tr -d '\r')
  [ "$version" = 26.8.2 ] || { echo "unsupported KakaoTalk version for login automation" >&2; return 1; }
}

cmd_login() {
  require_login_authorization
  find_serial || { echo "$AVD is not running; use: $0 up ${WHO}" >&2; exit 1; }
  assert_client_version
  launch
  local ui account ax ay px py lx ly
  ui=$(dump)
  grep -q "$PKG:id/login_kakao_account" <<<"$ui" || { echo "KakaoTalk is not on the login form; aborting" >&2; exit 1; }
  account=$(security find-generic-password -s "$SERVICE" | sed -n 's/.*"acct"<blob>="\(.*\)"/\1/p')
  [ -n "$account" ] || { echo "Keychain item $SERVICE not found" >&2; exit 1; }
  read -r ax ay < <(center "$ui" "$PKG:id/edit_text" 0)
  read -r px py < <(center "$ui" "$PKG:id/edit_text" 1)
  read -r lx ly < <(center "$ui" "$PKG:id/login_kakao_account" 0)
  adb shell input tap "$ax" "$ay"
  clear_field
  printf '%s\n' "$account" | type_secret
  adb shell input tap "$px" "$py"
  clear_field
  security find-generic-password -s "$SERVICE" -a "$account" -w | type_secret
  hide_keyboard
  sleep 1
  adb shell input tap "$lx" "$ly"
  sleep 6
  state
  echo "password submission finished; ready continues recognized login steps"
}

# Continue an authorized normal login through known account-independent screens.
# Unknown screens stop; terms beyond the required Privacy Policy are not accepted.
cmd_finish() {
  require_login_authorization
  find_serial || { echo "emulator is not running" >&2; exit 1; }
  assert_client_version
  local ui x y account report
  ui=$(dump)
  report=$(python3 "$TOOL_DIR/emu_state.py" <<<"$ui")
  if [ "$report" = state=chat-visible ] || [ "$report" = state=finder-visible ] || [ "$report" = state=media-viewer-visible ]; then
    adb shell input keyevent KEYCODE_BACK
    sleep 2
    state
    return
  elif [ "$report" = state=navigation-visible ]; then
    read -r x y < <(center "$ui" "$PKG:id/iv_tab_icon" 0)
    adb shell input tap "$x" "$y"
    sleep 2
    state
    return
  fi
  if grep -q 'verification information has expired' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" OK)
    adb shell input tap "$x" "$y"
    sleep 2
    launch
    ui=$(dump)
  fi
  if grep -q 'Google Password Manager'  <<<"$ui"; then
    read -r x y < <(tap_text "$ui" 'Not now')
    adb shell input tap "$x" "$y"
    sleep 2
    ui=$(dump)
  fi
  if grep -q 'Phone permission' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" OK)
    adb shell input tap "$x" "$y"
    sleep 2
    ui=$(dump)
  fi
  if grep -q 'com.android.permissioncontroller:id/permission_allow_button' <<<"$ui"; then
    read -r x y < <(python3 "$TOOL_DIR/emu_state.py" --permission-target <<<"$ui") || { echo "unknown permission; stop" >&2; return 1; }
    adb shell input tap "$x" "$y"
    sleep 2
    ui=$(dump)
  fi
  if grep -q 'Enter Your Phone Number' <<<"$ui" && ! grep -q 'You will receive a verification code' <<<"$ui"; then
    # Existing lab profiles use +1; refuse an unexpected country selector.
    python3 -c 'import sys,xml.etree.ElementTree as E; r=E.fromstring(sys.stdin.read()); sys.exit(0 if any(n.get("resource-id")=="com.kakao.talk:id/country_code" and n.get("text")=="+1" for n in r.iter("node")) else 1)' <<<"$ui" || { echo "unexpected country code; stop" >&2; exit 1; }
    account=$(security find-generic-password -s "$SERVICE" | sed -n 's/.*"acct"<blob>="\(.*\)"/\1/p')
    [[ "$account" =~ ^\+1[0-9]{10}$ ]] || { echo "unexpected Keychain phone format" >&2; exit 1; }
    read -r x y < <(center "$ui" "$PKG:id/edit_text" 0)
    adb shell input tap "$x" "$y"
    sleep 1
    clear_field
    printf '%s\n' "${account:2}" | type_secret
    sleep 1
    hide_keyboard
    ui=$(dump)
    printf '%s\n' "$ui" | python3 -c 'import sys,xml.etree.ElementTree as E; r=E.fromstring(sys.stdin.read()); sys.exit(0 if any(n.get("resource-id")=="com.kakao.talk:id/edit_text" and n.get("text")==sys.argv[1] for n in r.iter("node")) else 1)' "${account:2}" || { echo "phone field mismatch; stopped before verification request" >&2; exit 1; }
    [ "${ALLOW_REQUIRED_PRIVACY_POLICY:-0}" = 1 ] || { echo "required Privacy Policy needs lab authorization" >&2; exit 1; }
    if grep -q 'Privacy Policy' <<<"$ui"; then
      if python3 -c 'import sys,xml.etree.ElementTree as E; r=E.fromstring(sys.stdin.read()); sys.exit(0 if any(n.get("resource-id")=="com.kakao.talk:id/check_terms" and n.get("checked")=="false" for n in r.iter("node")) else 1)' <<<"$ui"; then
        read -r x y < <(center "$ui" "$PKG:id/check_terms" 0)
        adb shell input tap "$x" "$y"
      fi
    else
      echo "unexpected terms; stop" >&2; exit 1
    fi
    read -r x y < <(center "$ui" "$PKG:id/submit" 0)
    adb shell input tap "$x" "$y"
    sleep 3
  fi
  ui=$(dump)
  if grep -q 'Skip and Start' <<<"$ui" && grep -q 'Chat Restoration' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" 'Skip and Start')
    adb shell input tap "$x" "$y"
    sleep 3
    ui=$(dump)
  fi
  if grep -q 'previous chats cannot be retrieved' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" OK)
    adb shell input tap "$x" "$y"
  elif grep -q 'Please add a profile photo' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" 'Default Image')
    adb shell input tap "$x" "$y"
  elif grep -q 'Sync Contacts' <<<"$ui" && grep -q 'Get Started' <<<"$ui"; then
    # Preserve the existing profile name; only disable contact sync.
    if python3 -c 'import sys,xml.etree.ElementTree as E; sys.exit(0 if any(n.get("resource-id")=="com.kakao.talk:id/checkbox" and n.get("checked")=="true" for n in E.fromstring(sys.stdin.read()).iter("node")) else 1)' <<<"$ui"; then
      read -r x y < <(center "$ui" "$PKG:id/checkbox" 0)
      adb shell input tap "$x" "$y"
    fi
    read -r x y < <(tap_text "$ui" 'Get Started')
    adb shell input tap "$x" "$y"
  elif grep -q 'Check the access permissions below' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" OK)
    adb shell input tap "$x" "$y"
  elif grep -q 'Contacts permission' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" Cancel)
    adb shell input tap "$x" "$y"
  fi
  sleep 2
  state
}

# The provider receives profile and ready-marker path. Its stdout is exactly one
# fresh code; all errors must be sanitized. The FIFO never stores code on disk.
cmd_verify() (
  require_login_authorization
  find_serial || { echo "emulator is not running" >&2; exit 1; }
  assert_client_version
  declare -F read_sms_code >/dev/null || { echo "configure read_sms_code" >&2; exit 1; }
  local ui x y work reader= code account
  ui=$(dump)
  account=$(security find-generic-password -s "$SERVICE" | sed -n 's/.*"acct"<blob>="\(.*\)"/\1/p')
  python3 -c 'import sys,re,xml.etree.ElementTree as E; expected=sys.argv[1].lstrip("+"); r=E.fromstring(sys.stdin.read()); sys.exit(0 if any(re.sub(r"[^0-9]","",n.get("text","")) in (expected,expected[1:]) for n in r.iter("node") if not n.get("class","").endswith("EditText")) else 1)' "$account" <<<"$ui" || { echo "verification phone mismatch" >&2; exit 1; }
  work=$(mktemp -d "$LOGDIR/sms.XXXXXX")
  trap 'if [ -n "$reader" ]; then kill "$reader" 2>/dev/null || true; wait "$reader" 2>/dev/null || true; fi; exec 3>&-; rm -rf "$work"' EXIT
  mkfifo "$work/code"
  exec 3<>"$work/code"
  read_sms_code "$WHO" "$work/ready" >&3 &
  reader=$!
  for _ in $(seq 1 30); do
    [ -f "$work/ready" ] && break
    kill -0 "$reader" 2>/dev/null || { echo "SMS receiver failed" >&2; exit 1; }
    sleep 1
  done
  [ -f "$work/ready" ] || { echo "SMS receiver readiness timed out" >&2; exit 1; }
  python3 -c 'import time; print(time.time())' > "$work/ready.request"
  if grep -q 'You will receive a verification code' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" OK)
    adb shell input tap "$x" "$y"
  elif grep -q 'Receive via SMS' <<<"$ui"; then
    read -r x y < <(tap_text "$ui" 'Receive via SMS')
    adb shell input tap "$x" "$y"
  fi
  # No automatic resend. The receiver is ready before requesting the first code.
  read -r -t 190 code <&3 || { echo "SMS verification timed out; no resend performed" >&2; exit 1; }
  [[ "$code" =~ ^[0-9]{4,6}$ ]] || { echo "invalid SMS provider output" >&2; exit 1; }
  ui=$(dump)
  read -r x y < <(center_class "$ui" EditText 0) || { echo "code field missing" >&2; exit 1; }
  adb shell input tap "$x" "$y"
  clear_field
  printf '%s\n' "$code" | type_secret
  unset code
  ui=$(dump)
  read -r x y < <(tap_text "$ui" OK)
  adb shell input tap "$x" "$y"
  sleep 3
  state
)

cmd_ready_steps() {
  local report attempted_login=false attempted_verify=false
  for _ in $(seq 1 20); do
    report=$(state)
    echo "$report"
    case "$report" in
      state=home-visible) return 0 ;;
      state=login-form)
        "$attempted_login" && { echo "login did not advance; stop" >&2; return 1; }
        attempted_login=true; cmd_login ;;
      state=verification-required)
        "$attempted_verify" && { echo "verification did not advance; stop" >&2; return 1; }
        attempted_verify=true; cmd_verify ;;
      state=verification-unavailable|state=unknown|state=invalid-ui)
        echo "preparation stopped on an unsupported screen" >&2; return 1 ;;
      *) cmd_finish ;;
    esac
  done
  echo "preparation transition limit reached" >&2; return 1
}

# tap_text <xml> <label>: center of the first clickable node whose text is label.
tap_text() {
  python3 -c '
import sys, re, xml.etree.ElementTree as E
for n in E.fromstring(sys.stdin.read()).iter("node"):
    if (n.get("text") or "").strip() == sys.argv[1] and n.get("enabled") != "false":
        x1, y1, x2, y2 = map(int, re.findall(r"\d+", n.get("bounds")))
        print((x1 + x2) // 2, (y1 + y2) // 2)
        sys.exit(0)
sys.exit(1)
' "$2" <<<"$1"
}

# center_class <xml> <class-suffix> <index>: center of the nth node of a class.
center_class() {
  python3 -c '
import sys, re, xml.etree.ElementTree as E
nodes = [n for n in E.fromstring(sys.stdin.read()).iter("node") if n.get("class", "").endswith(sys.argv[1])]
if len(nodes) <= int(sys.argv[2]):
    sys.exit(1)
index = int(sys.argv[2])
if len(nodes) <= index:
    sys.exit(1)
x1, y1, x2, y2 = map(int, re.findall(r"\d+", nodes[index].get("bounds")))
print((x1 + x2) // 2, (y1 + y2) // 2)
' "$2" "$3" <<<"$1"
}

cmd_down() {
  find_serial || { echo "$AVD is not running"; return 0; }
  adb emu kill >/dev/null
  echo "stopped $AVD ($SERIAL)"
}

# These commands consume an already-scanned challenge; they never generate,
# resend, or retry enrollment. qr-code reads one code from a private stdin pipe.
cmd_qr_phone() {
  [ "${ALLOW_QR_ENROLLMENT:-0}" = 1 ] || { echo "configure explicit QR enrollment authorization" >&2; return 1; }
  find_serial </dev/null || { echo "owned emulator is not running" >&2; return 1; }
  assert_client_version </dev/null
  python3 "$TOOL_DIR/qr_phone.py" "$ADB" "$SERIAL" "$1"
}

# JSON on private stdin: peer, receipt, and text or exact selected-photo bounds.
cmd_chat_phone() {
  [ "${ALLOW_TEST_MESSAGES:-0}" = 1 ] || { echo "configure explicit owned-chat test authorization" >&2; return 1; }
  find_serial </dev/null || { echo "owned emulator is not running" >&2; return 1; }
  assert_client_version </dev/null
  python3 "$TOOL_DIR/chat_phone.py" "$ADB" "$SERIAL" "$1"
}

main() {
  [ $# -ge 2 ] || usage
  CMD=$1 WHO=$2
  shift 2
  local config=${EMU_CONFIG:-$LAB_DIR/emu-config.sh}
  [ -f "$config" ] || { echo "missing private emulator config" >&2; exit 1; }
  source "$config"
  SDK=${SDK:-${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}}
  [ -n "$SDK" ] || { echo "configure SDK or ANDROID_HOME" >&2; exit 1; }
  ADB=$SDK/platform-tools/adb
  EMULATOR=$SDK/emulator/emulator
  mkdir -p "$LOGDIR"
  chmod 700 "$LOGDIR"
  profile "$WHO"
  case "$CMD" in
    up) [ $# -eq 0 ] || { [ $# -eq 1 ] && [ "$1" = --login ]; } || usage
        if [ "${1:-}" = --login ]; then require_login_authorization; fi ;;
    *) [ $# -eq 0 ] || usage ;;
  esac
  case "$CMD" in
    up) cmd_up "$@" ;;
    ready) require_login_authorization; cmd_up; cmd_ready_steps ;;
    status) cmd_status ;;
    login) cmd_login ;;
    finish) cmd_finish ;;
    verify) cmd_verify ;;
    qr-approve) cmd_qr_phone approve ;;
    qr-code) cmd_qr_phone code ;;
    qr-finish) cmd_qr_phone finish ;;
    send-text) cmd_chat_phone text ;;
    send-photo) cmd_chat_phone photo ;;
    send-video) cmd_chat_phone video ;;
    send-profile) cmd_chat_phone profile ;;
    send-contact) cmd_chat_phone contact ;;
    send-audio) cmd_chat_phone audio ;;
    send-file) cmd_chat_phone file ;;
    send-album) cmd_chat_phone album ;;
    send-sticker) cmd_chat_phone sticker ;;
    down) cmd_down ;;
    *) usage ;;
  esac
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then main "$@"; fi
