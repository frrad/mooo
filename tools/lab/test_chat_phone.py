import tempfile
import unittest
import xml.etree.ElementTree as ET
from pathlib import Path

from chat_phone import clickable_label, send_text, photo_send, send_sticker, send_album


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
            p = FakePhone([chat(), chat('synthetic'), chat()])
            send_text(p, 'owned peer', 'synthetic', receipt)
            self.assertEqual(sum(args == ('shell', 'input', 'tap', '30', '10') for args, _ in p.calls), 1)
            self.assertEqual([data for _, data in p.calls if data], [b'synthetic\n'])
            with self.assertRaises(ValueError):
                send_text(p, 'owned peer', 'synthetic', receipt)

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
