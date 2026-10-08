from pathlib import Path
import shlex
import subprocess
import unittest

SCRIPT = shlex.quote(str(Path(__file__).with_name('emu.sh').resolve()))


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
    def test_launch_is_direct_and_failure_is_not_suppressed(self):
        result = run('adb() { echo "$*" >&2; return 1; }; sleep() { :; }; launch')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('shell am start', result.stderr)
        self.assertNotIn('monkey', result.stderr)

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
