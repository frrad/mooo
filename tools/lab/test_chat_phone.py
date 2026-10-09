import tempfile
import hashlib
import unittest
from unittest.mock import patch
import xml.etree.ElementTree as ET
from pathlib import Path

from chat_phone import clickable_label, send_text, photo_send, send_sticker, send_album, send_selected_media, send_file, send_audio, ensure_silent_emulator, send_profile


def frame(body):
    return ET.fromstring('<hierarchy>' + body + '</hierarchy>')


def chat(value='', title='owned peer'):
    return frame('<node resource-id="com.kakao.talk:id/chat_room_root"/>'
                 '<node resource-id="com.kakao.talk:id/toolbar_default_title_text" content-desc="' + title + '"/>'
                 '<node resource-id="com.kakao.talk:id/message_edit_text" text="' + value + '" bounds="[0,0][20,20]"/>'
                 '<node resource-id="com.kakao.talk:id/send_button_layout" bounds="[20,0][40,20]"/>')


class FakePhone:
    def __init__(self, frames):
        self.frames = iter(frames)
        self.calls = []

    def dump(self):
        return next(self.frames)

    def adb(self, *args, text=None):
        self.calls.append((args, text))


class ChatTests(unittest.TestCase):
    def test_text_uses_layout_and_receipt_blocks_repeat(self):
        with tempfile.TemporaryDirectory() as d:
            receipt = Path(d) / 'send.json'
            p = FakePhone([chat(), chat('synthetic'), chat('synthetic'), chat()])
            send_text(p, 'owned peer', 'synthetic', receipt)
            self.assertEqual(sum(args == ('shell', 'input', 'tap', '30', '10') for args, _ in p.calls), 1)
            self.assertEqual([data for _, data in p.calls if data], [b'synthetic\n'])
            with self.assertRaises(ValueError):
                send_text(p, 'owned peer', 'synthetic', receipt)

    def test_text_waits_for_keyboard_to_stop_moving_send(self):
        with tempfile.TemporaryDirectory() as d:
            initial = chat('synthetic')
            moved = chat('synthetic')
            moved.find("node[@resource-id='com.kakao.talk:id/send_button_layout']").set('bounds', '[20,100][40,120]')
            p = FakePhone([chat(), initial, moved, moved, chat()])
            send_text(p, 'owned peer', 'synthetic', Path(d) / 'send.json')
            taps = [args for args, _ in p.calls if args[:3] == ('shell', 'input', 'tap')]
            self.assertEqual(taps[-1], ('shell', 'input', 'tap', '30', '110'))

    def test_text_moving_send_never_reserves_or_taps(self):
        with tempfile.TemporaryDirectory() as d:
            frames = [chat()]
            for y in [40, 80, 120, 160]:
                root = chat('synthetic')
                root.find("node[@resource-id='com.kakao.talk:id/send_button_layout']").set('bounds', '[20,' + str(y) + '][40,' + str(y + 20) + ']')
                frames.append(root)
            p = FakePhone(frames)
            receipt = Path(d) / 'send.json'
            with self.assertRaises(ValueError):
                send_text(p, 'owned peer', 'synthetic', receipt)
            self.assertFalse(receipt.exists())
            self.assertEqual(sum(args[:3] == ('shell', 'input', 'tap') for args, _ in p.calls), 1)

    def test_wrong_peer_or_existing_draft_never_sends(self):
        for root in [chat(title='other'), chat('existing')]:
            with tempfile.TemporaryDirectory() as d:
                p = FakePhone([root])
                with self.assertRaises(ValueError):
                    send_text(p, 'owned peer', 'synthetic', Path(d) / 'send.json')
                self.assertEqual(p.calls, [])

    def test_duplicate_parent_child_label_uses_clickable_parent(self):
        r = frame('<node clickable="true" content-desc="Reply" bounds="[0,0][20,20]"><node text="Reply" clickable="false"/></node>')
        self.assertEqual(clickable_label(r, 'Reply').get('content-desc'), 'Reply')

    def test_sticker_requires_preview_and_blocks_repeat(self):
        with tempfile.TemporaryDirectory() as d:
            receipt = Path(d) / 'sticker.json'
            root = chat()
            ET.SubElement(root, 'node', {'resource-id': 'com.kakao.talk.emoticon:id/emoticon_preview_root', 'bounds': '[0,0][20,20]'})
            p = FakePhone([root])
            self.assertEqual(send_sticker(p, 'owned peer', receipt), 'selected-sticker-submitted-once')
            self.assertEqual(len(p.calls), 1)
            with self.assertRaises(ValueError):
                send_sticker(p, 'owned peer', receipt)
            p = FakePhone([chat()])
            with self.assertRaises(ValueError):
                send_sticker(p, 'owned peer', Path(d) / 'missing-preview.json')
            self.assertEqual(p.calls, [])

    def test_selected_video_exact_bounds_and_receipt(self):
        with tempfile.TemporaryDirectory() as d:
            root = chat()
            for y in [40, 80]:
                ET.SubElement(root, 'node', {'resource-id': 'com.kakao.talk:id/send_button', 'clickable': 'true', 'content-desc': '1 Selected, Send', 'bounds': '[0,' + str(y) + '][20,' + str(y + 20) + ']'})
            receipt = Path(d) / 'video.json'
            p = FakePhone([root])
            self.assertEqual(send_selected_media(p, 'owned peer', '[0,40][20,60]', receipt, 'video'), 'selected-video-submitted-once')
            self.assertEqual(len(p.calls), 1)
            with self.assertRaises(ValueError):
                send_selected_media(p, 'owned peer', '[0,40][20,60]', receipt, 'video')

    def test_profile_exact_description_selection_and_receipt(self):
        initial = chat()
        ET.SubElement(initial, 'node', {'resource-id': 'com.kakao.talk:id/media_send_layout', 'bounds': '[0,20][20,40]'})
        sheet = frame('<node resource-id="com.kakao.talk:id/handle_container" bounds="[0,100][40,120]"/>')
        menu = frame('<node clickable="true" bounds="[0,40][20,60]"><node text="Contacts"/></node>')
        chooser = frame('<node clickable="true" bounds="[0,60][20,80]"><node text="Send KakaoTalk Profile"/></node>')
        picker = frame('<node resource-id="com.kakao.talk:id/friends_pickerLayout" bounds="[0,0][200,200]"/><node clickable="true" bounds="[0,80][20,100]"><node content-desc="owned synthetic profile"/><node content-desc="owned synthetic profile"/></node>')
        selected = frame('<node resource-id="com.kakao.talk:id/friends_pickerLayout" bounds="[0,0][200,200]"/><node checked="true"/><node text="OK" clickable="true" enabled="true" bounds="[20,80][40,100]"/>')
        with tempfile.TemporaryDirectory() as d:
            receipt = Path(d) / 'profile.json'
            class ProfilePhone(FakePhone):
                def adb(self, *args, text=None):
                    if args == ('shell', 'input', 'tap', '30', '90') and not receipt.exists():
                        raise AssertionError('send without receipt')
                    return super().adb(*args, text=text)
            frames = [initial, sheet, menu, chooser, picker, selected, chat()]
            p = ProfilePhone(frames)
            self.assertEqual(send_profile(p, 'owned peer', '[0,80][20,100]', 'owned synthetic profile', receipt), 'owned-profile-submitted-once')
            with self.assertRaises(ValueError):
                send_profile(p, 'owned peer', '[0,80][20,100]', 'owned synthetic profile', receipt)
            p = FakePhone(frames)
            wrong = Path(d) / 'wrong.json'
            with self.assertRaises(ValueError):
                send_profile(p, 'owned peer', '[0,80][20,100]', 'other profile', wrong)
            self.assertFalse(wrong.exists())

    def test_audio_host_input_guard_fails_closed(self):
        class EmulatorPhone(FakePhone):
            def adb(self, *args, text=None):
                return b'mooo-lab\nOK\n'
        p = EmulatorPhone([])
        line = '/sdk/qemu-system-aarch64 -avd mooo-lab -no-audio'
        with patch('chat_phone.subprocess.check_output', return_value=line):
            ensure_silent_emulator(p)
        for output in ['', line + ' -allow-host-audio', line + '\n' + line]:
            with patch('chat_phone.subprocess.check_output', return_value=output):
                with self.assertRaises(ValueError):
                    ensure_silent_emulator(p)

    def test_audio_stops_without_dumping_animated_recording(self):
        initial = chat()
        ET.SubElement(initial, 'node', {'resource-id': 'com.kakao.talk:id/media_send_layout', 'bounds': '[0,20][20,40]'})
        sheet = frame('<node resource-id="com.kakao.talk:id/handle_container" bounds="[0,100][40,120]"/>')
        menu = frame('<node clickable="true" bounds="[0,40][20,60]"><node text="Voice Memo"/></node>')
        idle = frame('<node text="Voice Memo"/><node resource-id="com.kakao.talk:id/record_control" bounds="[0,60][20,80]"/>')
        stopped = frame('<node text="Voice Memo"/><node resource-id="com.kakao.talk:id/play" bounds="[0,0][20,20]"/><node resource-id="com.kakao.talk:id/send" bounds="[0,80][20,100]"/>')
        with tempfile.TemporaryDirectory() as d:
            receipt = Path(d) / 'audio.json'
            class AudioPhone(FakePhone):
                recording = False
                def dump(self):
                    if self.recording:
                        raise AssertionError('idle dump while recorder animated')
                    return super().dump()
                def adb(self, *args, text=None):
                    if args == ('shell', 'input', 'tap', '10', '70'):
                        if not receipt.exists():
                            raise AssertionError('recording without receipt')
                        self.recording = not self.recording
                    return super().adb(*args, text=text)
            p = AudioPhone([initial, sheet, menu, idle, stopped, chat()])
            with patch('chat_phone.ensure_silent_emulator'), patch('chat_phone.time.sleep'):
                self.assertEqual(send_audio(p, 'owned peer', 3, receipt), 'silent-audio-submitted-once')
            self.assertFalse(p.recording)
            self.assertEqual(sum(a == ('shell', 'input', 'tap', '10', '70') for a, _ in p.calls), 2)
            with self.assertRaises(ValueError):
                send_audio(p, 'owned peer', 3, receipt)

    def file_frames(self, duplicate=False):
        initial = chat()
        ET.SubElement(initial, 'node', {'resource-id': 'com.kakao.talk:id/media_send_layout', 'bounds': '[0,20][20,40]'})
        sheet = frame('<node resource-id="com.kakao.talk:id/handle_container" bounds="[0,100][40,120]"/>')
        expanded = frame('<node clickable="true" bounds="[0,40][20,60]"><node text="File"/></node>')
        menu = frame('<node clickable="true" bounds="[0,60][20,80]"><node text="Select from File"/></node>')
        item = '<node resource-id="com.google.android.documentsui:id/item_root" clickable="true" bounds="[0,80][20,100]"><node text="mooo-synthetic.txt"/></node>'
        documents = frame(item + (item if duplicate else ''))
        delivered = chat()
        ET.SubElement(delivered, 'node', {'text': 'mooo-synthetic.txt'})
        return [initial, sheet, expanded, menu, documents, delivered]

    def test_file_verifies_initial_peer_hash_and_reserves_before_selection(self):
        data = b'synthetic file'
        with tempfile.TemporaryDirectory() as d:
            receipt = Path(d) / 'file.json'
            class FilePhone(FakePhone):
                def adb(self, *args, text=None):
                    if args[:2] == ('exec-out', 'head'):
                        self.calls.append((args, text))
                        return data
                    if args == ('shell', 'input', 'tap', '10', '90'):
                        if not receipt.exists():
                            raise AssertionError('selection occurred before durable receipt')
                    return super().adb(*args, text=text)
            p = FilePhone(self.file_frames())
            self.assertEqual(send_file(p, 'owned peer', 'mooo-synthetic.txt', hashlib.sha256(data).hexdigest(), receipt), 'file-submitted-once')
            self.assertEqual(sum(args == ('shell', 'input', 'tap', '10', '90') for args, _ in p.calls), 1)
            with self.assertRaises(ValueError):
                send_file(p, 'owned peer', 'mooo-synthetic.txt', hashlib.sha256(data).hexdigest(), receipt)
            p = FilePhone(self.file_frames(duplicate=True))
            with self.assertRaises(ValueError):
                send_file(p, 'owned peer', 'mooo-synthetic.txt', hashlib.sha256(data).hexdigest(), Path(d) / 'duplicate.json')
            self.assertFalse((Path(d) / 'duplicate.json').exists())

    def test_file_missing_card_preserves_receipt_and_never_retries(self):
        data = b'synthetic file'
        class MissingCardPhone(FakePhone):
            def adb(self, *args, text=None):
                self.calls.append((args, text))
                return data if args[:2] == ('exec-out', 'head') else None
        with tempfile.TemporaryDirectory() as d:
            receipt = Path(d) / 'file.json'
            frames = self.file_frames()
            frames[-1] = chat()
            p = MissingCardPhone(frames + [chat()] * 3)
            with self.assertRaisesRegex(ValueError, 'unconfirmed'):
                send_file(p, 'owned peer', 'mooo-synthetic.txt', hashlib.sha256(data).hexdigest(), receipt)
            self.assertTrue(receipt.exists())
            self.assertEqual(sum(args == ('shell', 'input', 'tap', '10', '90') for args, _ in p.calls), 1)
            with self.assertRaisesRegex(ValueError, 'previous attempt'):
                send_file(p, 'owned peer', 'mooo-synthetic.txt', hashlib.sha256(data).hexdigest(), receipt)

    def test_file_wrong_peer_or_hash_never_selects(self):
        with tempfile.TemporaryDirectory() as d:
            p = FakePhone([chat(title='other')])
            with self.assertRaises(ValueError):
                send_file(p, 'owned peer', 'mooo-synthetic.txt', 'a' * 64, Path(d) / 'wrong-peer.json')
            self.assertEqual(p.calls, [])
            class WrongHashPhone(FakePhone):
                def adb(self, *args, text=None):
                    self.calls.append((args, text))
                    return b'wrong contents'
            p = WrongHashPhone(self.file_frames())
            with self.assertRaises(ValueError):
                send_file(p, 'owned peer', 'mooo-synthetic.txt', 'a' * 64, Path(d) / 'wrong-hash.json')
            self.assertEqual(len(p.calls), 1)
            self.assertFalse((Path(d) / 'wrong-hash.json').exists())

    def test_album_requires_collage_count_and_exact_send_bounds(self):
        with tempfile.TemporaryDirectory() as d:
            root = chat()
            ET.SubElement(root, 'node', {'resource-id': 'com.kakao.talk:id/send_bundle_checkbox', 'checked': 'true', 'bounds': '[0,0][20,20]'})
            for y in [40, 80]:
                ET.SubElement(root, 'node', {'resource-id': 'com.kakao.talk:id/send_button', 'clickable': 'true', 'content-desc': '2 Selected, Send', 'bounds': '[0,' + str(y) + '][20,' + str(y + 20) + ']'})
            p = FakePhone([root])
            receipt = Path(d) / 'album.json'
            self.assertEqual(send_album(p, 'owned peer', '[0,40][20,60]', 2, receipt), 'selected-album-submitted-once')
            self.assertEqual(len(p.calls), 1)
            with self.assertRaises(ValueError):
                send_album(p, 'owned peer', '[0,40][20,60]', 2, receipt)
            root.find("node[@resource-id='com.kakao.talk:id/send_bundle_checkbox']").set('checked', 'false')
            p = FakePhone([root])
            with self.assertRaises(ValueError):
                send_album(p, 'owned peer', '[0,40][20,60]', 2, Path(d) / 'other.json')
            self.assertEqual(p.calls, [])

    def test_gallery_duplicate_send_fails_without_explicit_bounds(self):
        r = frame('<node resource-id="com.kakao.talk:id/send_button" clickable="true" enabled="true" content-desc="1 Selected, Send" bounds="[0,0][20,20]"/>'
                  '<node resource-id="com.kakao.talk:id/send_button" clickable="true" enabled="true" content-desc="1 Selected, Send" bounds="[0,40][20,60]"/>')
        with self.assertRaises(ValueError):
            photo_send(r)
        self.assertEqual(photo_send(r, '[0,0][20,20]').get('bounds'), '[0,0][20,20]')


if __name__ == '__main__':
    unittest.main()
