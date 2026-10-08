import unittest
import xml.etree.ElementTree as ET
from unittest.mock import patch

from qr_phone import Phone, screen


def xml(content):
    return ET.fromstring("<hierarchy>" + content + "</hierarchy>")


APPROVAL = xml('<node resource-id="com.kakao.talk:id/btn_login_with_pc" enabled="true" bounds="[10,20][30,40]"/>')


def code_form(value="", enabled="true"):
    return xml('<node resource-id="com.kakao.talk:id/login_pass_code_layout"/>'
               '<node resource-id="com.kakao.talk:id/edit_text" class="android.widget.EditText" '
               'text="' + value + '" bounds="[10,50][30,70]"/>'
               '<node resource-id="com.kakao.talk:id/btn_confirm" enabled="' + enabled + '" bounds="[10,80][30,100]"/>')


class FakePhone(Phone):
    def __init__(self, frames):
        self.frames = iter(frames)
        self.calls = []

    def dump(self):
        return next(self.frames)

    def adb(self, *args, text=None):
        self.calls.append((args, text))
        return b""


class QRPhoneTests(unittest.TestCase):
    def test_dump_retries_transient_xml_and_removes_each_capture(self):
        phone = Phone('synthetic-adb', 'owned-emulator')
        reads = iter([b'not XML', b'<hierarchy/>'])
        calls = []
        def adb(*args, text=None):
            calls.append(args)
            return next(reads) if args[0] == 'exec-out' else b''
        phone.adb = adb
        with patch('qr_phone.time.sleep'):
            self.assertEqual(phone.dump().tag, 'hierarchy')
        self.assertEqual(sum(args[:3] == ('shell', 'rm', '-f') for args in calls), 2)

    def test_success_overlay_closes_without_opening_device_management(self):
        root = xml('<node text="Successfully logged in"/><node text="Close" bounds="[10,20][30,40]"/>'
                   '<node text="Manage Devices" bounds="[40,20][90,40]"/>')
        phone = FakePhone([root])
        self.assertEqual(phone.finish(), "qr-success-closed")
        self.assertEqual(phone.calls, [(('shell', 'input', 'tap', '20', '30'), None)])

    def test_waits_for_async_approval_and_code_form(self):
        phone = FakePhone([xml(""), APPROVAL, xml(""), code_form()])
        with patch("qr_phone.time.sleep"):
            self.assertEqual(phone.approve(), "device-code-required")
        self.assertEqual(phone.calls, [(('shell', 'input', 'tap', '20', '30'), None)])

    def test_form_uses_layout_not_old_title(self):
        self.assertEqual(screen(code_form()), "device-code-required")

    def test_restriction_overlay_stops_without_tapping(self):
        root = xml('<node text="Logins are restricted from sub devices due to user protection measures."/>'
                   '<node resource-id="com.kakao.talk:id/btn_login_with_pc"/>')
        phone = FakePhone([root])
        with self.assertRaisesRegex(ValueError, "secondary-login-restricted"):
            phone.approve()
        self.assertEqual(phone.calls, [])

    def test_invalid_qr_stops_without_retry(self):
        phone = FakePhone([xml('<node text="You cannot use this QR code."/>')])
        with self.assertRaisesRegex(ValueError, "invalid-qr"):
            phone.approve()
        self.assertEqual(phone.calls, [])

    def test_unknown_screen_wait_is_bounded(self):
        phone = FakePhone([xml("")])
        with patch("qr_phone.time.monotonic", side_effect=[0, 0, 16]), patch("qr_phone.time.sleep"):
            with self.assertRaisesRegex(ValueError, "timed out"):
                phone.wait("qr-approval")
        self.assertEqual(phone.calls, [])

    def test_code_is_stdin_only_and_confirmed_once(self):
        phone = FakePhone([code_form(), code_form("A1B2")])
        self.assertEqual(phone.enter_code("A1B2"), "device-code-submitted")
        self.assertTrue(all("A1B2" not in " ".join(args) for args, _ in phone.calls))
        self.assertEqual([data for _, data in phone.calls if data], [b"A1B2\n"])
        self.assertEqual(sum(args == ('shell', 'input', 'tap', '20', '90') for args, _ in phone.calls), 1)

    def test_code_entry_failure_never_confirms(self):
        for form in [code_form("WRNG"), code_form("A1B2", "false")]:
            phone = FakePhone([code_form(), form])
            with self.assertRaises(ValueError):
                phone.enter_code("A1B2")
            self.assertFalse(any(args == ('shell', 'input', 'tap', '20', '90') for args, _ in phone.calls))

    def test_existing_field_is_not_overwritten(self):
        phone = FakePhone([code_form("OLD1")])
        with self.assertRaisesRegex(ValueError, "not empty"):
            phone.enter_code("A1B2")
        self.assertEqual(phone.calls, [])

    def test_invalid_code_never_reaches_adb(self):
        phone = FakePhone([])
        with self.assertRaises(ValueError):
            phone.enter_code("a;sh")
        self.assertEqual(phone.calls, [])
