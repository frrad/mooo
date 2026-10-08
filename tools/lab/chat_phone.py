#!/usr/bin/env python3
"""Single-attempt sends in an already-open, explicitly selected owned chat.

The private stdin JSON supplies peer, receipt, and text (or selected photo
Send bounds). No navigation, recipient search, permission grant, or retry.
"""
import json
import os
from pathlib import Path
import re
import sys

from qr_phone import Phone, PREFIX, node_target


def unique(nodes):
    if len(nodes) != 1:
        raise ValueError('control unavailable or ambiguous')
    node_target(nodes[0])
    return nodes[0]


def clickable_label(root, label, bounds=None, resource=None):
    return unique([n for n in root.iter('node')
                   if n.get('clickable') == 'true'
                   and n.get('enabled') != 'false'
                   and label in (n.get('text'), n.get('content-desc'))
                   and (bounds is None or n.get('bounds') == bounds)
                   and (resource is None or n.get('resource-id') == resource)])


def control(root, name):
    return unique([n for n in root.iter('node')
                   if n.get('resource-id') == PREFIX + name])


def owned_chat(root, peer):
    nodes = list(root.iter('node'))
    if not peer or not any(n.get('resource-id') == PREFIX + 'chat_room_root' for n in nodes):
        raise ValueError('owned chat unavailable')
    if sum(n.get('resource-id') == PREFIX + 'toolbar_default_title_text'
           and n.get('content-desc') == peer for n in nodes) != 1:
        raise ValueError('owned peer mismatch')


def tap(phone, node):
    _, (x, y) = node_target(node)
    phone.adb('shell', 'input', 'tap', str(x), str(y))


def reserve(receipt):
    receipt = Path(receipt)
    if not receipt.is_absolute() or not receipt.parent.is_dir() or receipt.parent.stat().st_mode & 0o077:
        raise ValueError('receipt requires a private absolute directory')
    fd = os.open(receipt, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump({'attempted': True, 'automatic_retry': False}, f)
        f.flush()
        os.fsync(f.fileno())


def send_text(phone, peer, text, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if not re.fullmatch(r'[A-Za-z0-9._-]{1,512}', text):
        raise ValueError('use an ASCII synthetic fixture')
    root = phone.dump()
    owned_chat(root, peer)
    field = control(root, 'message_edit_text')
    if field.get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    tap(phone, field)
    phone.adb('shell', 'IFS= read -r value; input text "$value"', text=(text + '\n').encode())
    previous = None
    button = None
    for _ in range(4):
        root = phone.dump()
        owned_chat(root, peer)
        if control(root, 'message_edit_text').get('text') != text:
            raise ValueError('fixture entry unconfirmed')
        current = control(root, 'send_button_layout')
        bounds = current.get('bounds')
        if bounds == previous:
            button = current
            break
        previous = bounds
    if button is None:
        raise ValueError('send control still moving; no send attempted')
    reserve(receipt)
    tap(phone, button)
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('send outcome unconfirmed; do not retry')
    return 'text-submitted-once'


def photo_send(root, bounds=None):
    return clickable_label(root, '1 Selected, Send', bounds,
                           PREFIX + 'send_button')


def send_selected_media(phone, peer, bounds, receipt, kind):
    if kind not in ('photo', 'video'):
        raise ValueError('unsupported selected media')
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    root = phone.dump()
    owned_chat(root, peer)
    button = photo_send(root, bounds)
    reserve(receipt)
    tap(phone, button)
    return 'selected-' + kind + '-submitted-once'


def send_album(phone, peer, bounds, count, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if type(count) is not int or not 2 <= count <= 30:
        raise ValueError('album count must be 2 through 30')
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'send_bundle_checkbox').get('checked') != 'true':
        raise ValueError('collage selection required')
    button = clickable_label(root, str(count) + ' Selected, Send', bounds,
                             PREFIX + 'send_button')
    reserve(receipt)
    tap(phone, button)
    return 'selected-album-submitted-once'


def send_sticker(phone, peer, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    root = phone.dump()
    owned_chat(root, peer)
    unique([n for n in root.iter('node') if n.get('resource-id') ==
            'com.kakao.talk.emoticon:id/emoticon_preview_root'])
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    button = control(root, 'send_button_layout')
    reserve(receipt)
    tap(phone, button)
    return 'selected-sticker-submitted-once'


def main():
    try:
        adb, serial, action = sys.argv[1:]
        request = json.load(sys.stdin)
        phone = Phone(adb, serial)
        if action == 'text':
            result = send_text(phone, request['peer'], request['text'], request['receipt'])
        elif action == 'sticker':
            result = send_sticker(phone, request['peer'], request['receipt'])
        elif action == 'album':
            result = send_album(phone, request['peer'], request['bounds'], request['count'], request['receipt'])
        elif action in ('photo', 'video'):
            result = send_selected_media(phone, request['peer'], request['bounds'], request['receipt'], action)
        else:
            raise ValueError('unsupported action')
        print(result)
    except Exception:
        # Never print private request fields, ADB command data, or screen labels.
        print('chat action stopped; inspect private state before any further action', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
