from pathlib import Path
import shlex
import subprocess
import unittest

SCRIPT = shlex.quote(str((Path(__file__).resolve().parents[2] / 'research' / 'emu.sh')))


def run(commands):
    import os
    import signal
    process = subprocess.Popen(['bash', '-c', 'source ' + SCRIPT + '\n' + commands],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               text=True, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=10)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        stdout, stderr = process.communicate()
        raise AssertionError('mocked command timed out: ' + stdout + stderr)
    return subprocess.CompletedProcess(process.args, process.returncode, stdout, stderr)


class EmulatorShellTests(unittest.TestCase):
    def test_private_stdin_survives_adb_preflight(self):
        for command in ['cmd_chat_phone text', 'cmd_qr_phone code']:
            result = run('SERIAL=synthetic; ALLOW_TEST_MESSAGES=1; ALLOW_QR_ENROLLMENT=1; find_serial() { cat >/dev/null; }; assert_client_version() { cat >/dev/null; }; python3() { cat; }; ' + command + ' <<<synthetic-input')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('synthetic-input', result.stdout)

    def test_video_player_returns_via_back(self):
        xml = '<hierarchy><node resource-id="com.kakao.talk:id/playerTouchPanel"/><node resource-id="com.kakao.talk:id/playPauseButton"/></hierarchy>'
        result = run('require_login_authorization() { :; }; assert_client_version() { :; }; find_serial() { return 0; }; sleep() { :; }; adb() { echo "$*" >&2; }; state() { echo state=chat-visible; }; dump() { printf "%s" ' + shlex.quote(xml) + '; }; cmd_finish')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('shell input keyevent KEYCODE_BACK', result.stderr)
        self.assertIn('state=chat-visible', result.stdout)

    def test_finder_after_qr_returns_via_back(self):
        xml = '<hierarchy><node resource-id="com.kakao.talk.finder:id/finder_nav_host_fragment"/><node resource-id="com.kakao.talk.finder:id/input_focus"/></hierarchy>'
        result = run('require_login_authorization() { :; }; assert_client_version() { :; }; find_serial() { return 0; }; sleep() { :; }; adb() { echo "$*" >&2; }; state() { echo state=home-visible; }; dump() { printf "%s" ' + shlex.quote(xml) + '; }; cmd_finish')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('shell input keyevent KEYCODE_BACK', result.stderr)
        self.assertIn('state=home-visible', result.stdout)

    def test_qr_approval_requires_explicit_authorization(self):
        result = run('ALLOW_QR_ENROLLMENT=0; find_serial() { echo BAD_DEVICE_ACTION; }; cmd_qr_phone approve')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('BAD_DEVICE_ACTION', result.stdout)

    def test_qr_approval_checks_client_version_before_actions(self):
        result = run('ALLOW_QR_ENROLLMENT=1; find_serial() { return 0; }; assert_client_version() { return 1; }; python3() { echo BAD_PHONE_ACTION; }; cmd_qr_phone approve')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('BAD_PHONE_ACTION', result.stdout)

    def test_example_sms_provider_resolves_after_launcher_move(self):
        result = run('source "$TOOL_DIR/emu-config.example.sh"; exec() { printf "%s\\n" "$@"; }; read_sms_code a synthetic-marker')
        self.assertEqual(result.returncode, 0, result.stderr)
        provider = str(Path(__file__).with_name('sms_xmpp.py').resolve())
        self.assertIn(provider, result.stdout)

    def test_launch_is_direct_and_failure_is_not_suppressed(self):
        result = run('adb() { echo "$*" >&2; return 1; }; sleep() { :; }; launch')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('shell am start', result.stderr)
        self.assertNotIn('monkey', result.stderr)

    def test_explicit_dns_is_passed_to_cold_boot(self):
        result = run('EMULATOR=/synthetic/emulator; AVD=synthetic; LOGDIR=/synthetic; EMU_DNS_SERVER=1.1.1.1; emulator_boot_command')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('-dns-server 1.1.1.1', result.stdout)
        self.assertIn('-no-snapshot', result.stdout)
        self.assertNotIn('-allow-host-audio', result.stdout)

    def test_invalid_dns_never_emits_boot_command(self):
        result = run('EMULATOR=/synthetic/emulator; AVD=synthetic; LOGDIR=/synthetic; EMU_DNS_SERVER=invalid; emulator_boot_command')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('/synthetic/emulator', result.stdout)

    def test_boot_timeout_never_launches(self):
        result = run('AVD=synthetic; find_serial() { SERIAL=synthetic; return 0; }; adb() { echo 0; }; sleep() { :; }; launch() { echo BAD_LAUNCH; }; cmd_up')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('boot timed out', result.stderr)
        self.assertNotIn('BAD_LAUNCH', result.stdout)

    def test_hardware_keyboard_does_not_send_back(self):
        result = run('adb() { echo BAD_INPUT; }; hide_keyboard')
        self.assertEqual(result.returncode, 0)
        self.assertNotIn('BAD_INPUT', result.stdout)

    def test_password_is_submitted_at_most_once(self):
        result = run('state() { echo state=login-form; }; cmd_login() { echo SUBMIT; }; cmd_ready_steps')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout.count('SUBMIT'), 1)

    def test_profile_overlay_selects_default_photo(self):
        xml = '<hierarchy><node text="Sync Contacts"/><node text="Get Started" bounds="[100,100][200,200]"/><node text="Please add a profile photo."/><node text="Default Image" bounds="[10,10][30,30]"/></hierarchy>'
        result = run('require_login_authorization() { :; }; assert_client_version() { :; }; find_serial() { return 0; }; sleep() { :; }; adb() { echo "$*" >&2; }; dump() { printf "%s" ' + shlex.quote(xml) + '; }; cmd_finish')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('shell input tap 20 20', result.stderr)
        self.assertNotIn('shell input tap 150 150', result.stderr)

    def test_sms_listener_ready_before_request_and_code_never_printed(self):
        import tempfile
        with tempfile.TemporaryDirectory() as folder:
            confirm = '<hierarchy><node resource-id="com.kakao.talk:id/txt_title" text="5555550123"/><node text="You will receive a verification code"/><node text="OK" bounds="[0,0][20,20]"/></hierarchy>'
            code_form = '<hierarchy><node class="android.widget.EditText" bounds="[20,20][40,40]"/><node text="OK" bounds="[100,100][120,120]"/></hierarchy>'
            commands = '''
require_login_authorization() { :; }
assert_client_version() { :; }
find_serial() { return 0; }
sleep() { command sleep .01; }
security() { echo '\"acct\"<blob>=\"+15555550123\"'; }
state() { echo state=restore-skip; }
adb() {
  if [ ! -f "$LOGDIR/request" ]; then
    [ -f "$LOGDIR/listening" ] || return 1
    touch "$LOGDIR/request"
  elif [ "$*" = shell ]; then
    cat >/dev/null
  fi
}
read_sms_code() {
  exec python3 -c 'import sys,time,pathlib; root=pathlib.Path(sys.argv[1]); (root/"listening").touch(); pathlib.Path(sys.argv[2]).touch();
while not (root/"request").exists(): time.sleep(.01)
print("123456", flush=True)' "$LOGDIR" "$2"
}
'''
            commands += 'SERVICE=synthetic WHO=a\n'
            commands += 'LOGDIR=' + shlex.quote(folder) + '\n'
            commands += 'dump() { if [ -f "$LOGDIR/request" ]; then printf "%s" ' + shlex.quote(code_form) + '; else printf "%s" ' + shlex.quote(confirm) + '; fi; }\ncmd_verify'
            result = run(commands)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('state=restore-skip', result.stdout)
            self.assertNotIn('123456', result.stdout + result.stderr)
            self.assertFalse(list(Path(folder).glob('sms.*')))
