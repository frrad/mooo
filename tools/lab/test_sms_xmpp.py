import asyncio
import contextlib
import io
from pathlib import Path
import tempfile
import time
import types
import unittest
from unittest.mock import patch

import sms_xmpp


class SMSReceiverTests(unittest.TestCase):
    def test_only_transport_sms_and_kakao_codes(self):
        self.assertEqual(sms_xmpp.sms_code('+15555550123@cheogram.com', 'KakaoTalk [123456] verification code'), '123456')
        self.assertIsNone(sms_xmpp.sms_code('someone@example.org', 'KakaoTalk [123456] verification code'))
        self.assertIsNone(sms_xmpp.sms_code('+15555550123@cheogram.com.evil', 'KakaoTalk [123456] verification code'))
        self.assertIsNone(sms_xmpp.sms_code('+15555550123@cheogram.com', 'Other service [123456] verification code'))

    def test_live_sms_with_unavailable_archive(self):
        instances = []

        class Archive:
            async def iterate(self, **kwargs):
                await asyncio.sleep(0)
                raise RuntimeError('archive disabled')
                yield

        class XMPP:
            def __init__(self, *args):
                self.plugin = {'xep_0313': Archive()}
                self.closed = False
                instances.append(self)

            def register_plugin(self, name):
                pass

            def add_event_handler(self, name, callback):
                self.callback = callback

            def connect(self):
                pass

            async def wait_until(self, *args, **kwargs):
                pass

            def send_presence(self):
                # An offline queued code before a new request must be ignored.
                asyncio.get_running_loop().call_soon(self.callback,
                    {'from': '+15555550123@cheogram.com', 'body': 'KakaoTalk [111111] verification code'})
                asyncio.get_running_loop().call_later(.01,
                    lambda: Path(str(marker) + '.request').write_text(str(time.time())))
                asyncio.get_running_loop().call_later(.02, self.callback,
                    {'from': '+15555550123@cheogram.com', 'body': 'KakaoTalk [123456] verification code'})

            def disconnect(self):
                self.closed = True

        with tempfile.TemporaryDirectory() as folder:
            marker = Path(folder) / 'ready'
            output = io.StringIO()
            with patch.dict('sys.modules', {'slixmpp': types.SimpleNamespace(ClientXMPP=XMPP)}), patch.dict('os.environ', {'SMS_XMPP_JID': 'test@example.org', 'SMS_KEYCHAIN_SERVICE': 'synthetic'}), patch('sms_xmpp.subprocess.run', return_value=types.SimpleNamespace(stdout='synthetic-password\n')), contextlib.redirect_stdout(output):
                asyncio.run(sms_xmpp.run(marker))
            self.assertTrue(marker.is_file())
            self.assertEqual(output.getvalue(), '123456\n')
            self.assertTrue(instances[0].closed)
