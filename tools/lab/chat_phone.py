#!/usr/bin/env python3
"""Single-attempt sends in an already-open, explicitly selected owned chat.

The private stdin JSON supplies peer, receipt, and text (or selected photo
Send bounds). No navigation, recipient search, permission grant, or retry.
"""
import json
import math
import hashlib
import os
from pathlib import Path
import re
import sys
import subprocess
import time

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


def clickable_text_parent(root, text):
    parents = {child: node for node in root.iter() for child in node}
    targets = []
    for node in root.iter('node'):
        if node.get('text') != text:
            continue
        while node.get('clickable') != 'true' and node in parents:
            node = parents[node]
        if node.get('clickable') == 'true' and node not in targets:
            targets.append(node)
    return unique(targets)


def document_item(root, filename):
    return unique([node for node in root.iter('node')
                   if node.get('resource-id') == 'com.google.android.documentsui:id/item_root'
                   and node.get('clickable') == 'true'
                   and any(child.get('text') == filename for child in node.iter('node'))])


def send_file(phone, peer, filename, sha256, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if not re.fullmatch(r'mooo-[A-Za-z0-9._-]{1,120}', filename) or not re.fullmatch(r'[A-Fa-f0-9]{64}', sha256):
        raise ValueError('synthetic filename and expected hash required')
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    if any(n.get('text') == filename for n in root.iter('node')):
        raise ValueError('fixture already visible; inspect without resending')
    data = phone.adb('exec-out', 'head', '-c', '4194305', '/sdcard/Download/' + filename)
    if not data or len(data) > 4 << 20 or hashlib.sha256(data).hexdigest() != sha256.lower():
        raise ValueError('pushed synthetic fixture unconfirmed')
    tap(phone, control(root, 'media_send_layout'))
    root = phone.dump()
    _, (x, y) = node_target(control(root, 'handle_container'))
    phone.adb('shell', 'input', 'swipe', str(x), str(y), str(x), str(max(200, y // 4)), '450')
    root = phone.dump()
    tap(phone, clickable_text_parent(root, 'File'))
    root = phone.dump()
    tap(phone, clickable_text_parent(root, 'Select from File'))
    root = phone.dump()
    item = document_item(root, filename)
    reserve(receipt)  # Document selection sends immediately in the observed client.
    tap(phone, item)
    for _ in range(4):
        root = phone.dump()
        owned_chat(root, peer)
        if any(n.get('text') == filename for n in root.iter('node')):
            return 'file-submitted-once'
    raise ValueError('file outcome unconfirmed; preserve receipt and do not retry')


def send_location(phone, peer, latitude, longitude, address, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if any(isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v)
           for v in [latitude, longitude]) or not -90 <= latitude <= 90 or not -180 <= longitude <= 180:
        raise ValueError('synthetic GPS coordinates invalid')
    if not address or len(address) > 4096:
        raise ValueError('expected synthetic location address required')
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    tap(phone, control(root, 'media_send_layout'))
    root = phone.dump()
    _, (x, y) = node_target(control(root, 'handle_container'))
    phone.adb('shell', 'input', 'swipe', str(x), str(y), str(x), str(max(200, y // 4)), '450')
    tap(phone, clickable_text_parent(phone.dump(), 'Location'))
    root = phone.dump()
    button = control(root, 'btn_my_location')  # Unknown permission screens stop.
    # Reapply after opening: the observed picker initially kept Android's default.
    phone.adb('emu', 'geo', 'fix', str(longitude), str(latitude))
    tap(phone, button)
    for _ in range(4):
        root = phone.dump()
        bubbles = [n for n in root.iter('node') if n.get('resource-id') == PREFIX + 'location_bubble']
        addresses = [n for n in root.iter('node') if n.get('resource-id') == PREFIX + 'location_address']
        if not bubbles or not addresses:
            continue  # Recentering temporarily hides controls; bounded wait only.
        bubble = unique(bubbles)
        address_node = unique(addresses)
        bx1, by1, bx2, by2 = map(int, re.findall(r'\d+', bubble.get('bounds', '')))
        ax1, ay1, ax2, ay2 = map(int, re.findall(r'\d+', address_node.get('bounds', '')))
        if address_node.get('text') == address and bx1 <= ax1 < ax2 <= bx2 and by1 <= ay1 < ay2 <= by2:
            reserve(receipt)  # Bubble selection sends immediately.
            tap(phone, bubble)
            owned_chat(phone.dump(), peer)
            return 'synthetic-location-submitted-once'
    raise ValueError('location fixture unconfirmed; no send performed')


def send_contact(phone, peer, name, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if not name.startswith('Mooo-Synthetic-') or len(name) > 128:
        raise ValueError('precreated synthetic contact required')
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    tap(phone, control(root, 'media_send_layout'))
    root = phone.dump()
    _, (x, y) = node_target(control(root, 'handle_container'))
    phone.adb('shell', 'input', 'swipe', str(x), str(y), str(x), str(max(200, y // 4)), '450')
    tap(phone, clickable_text_parent(phone.dump(), 'Contacts'))
    tap(phone, clickable_text_parent(phone.dump(), 'Send Contact'))
    root = phone.dump()
    if not any(n.get('package') == 'com.google.android.contacts' for n in root.iter('node')):
        raise ValueError('owned contact picker unavailable; inspect permissions')
    tap(phone, clickable_text_parent(root, name))
    root = phone.dump()
    if not all(any(n.get('text') == label for n in root.iter('node'))
               for label in [name, 'Phone Number', 'Cell']):
        raise ValueError('synthetic contact preview unconfirmed')
    # The observed preview omits phone numbers from accessibility. The operator
    # must precreate and verify this synthetic fixture; never select other names.
    button = clickable_label(root, 'SEND')
    reserve(receipt)
    tap(phone, button)
    owned_chat(phone.dump(), peer)
    return 'synthetic-contact-submitted-once'


def send_profile(phone, peer, bounds, description, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if not description or len(description) > 1024:
        raise ValueError('exact owned profile description required')
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    tap(phone, control(root, 'media_send_layout'))
    root = phone.dump()
    _, (x, y) = node_target(control(root, 'handle_container'))
    phone.adb('shell', 'input', 'swipe', str(x), str(y), str(x), str(max(200, y // 4)), '450')
    tap(phone, clickable_text_parent(phone.dump(), 'Contacts'))
    tap(phone, clickable_text_parent(phone.dump(), 'Send KakaoTalk Profile'))
    root = phone.dump()
    control(root, 'friends_pickerLayout')
    if any(n.get('checked') == 'true' for n in root.iter('node')):
        raise ValueError('existing profile selection')
    row = unique([n for n in root.iter('node') if n.get('clickable') == 'true'
                  and n.get('bounds') == bounds
                  and any(c.get('content-desc') == description for c in n.iter('node'))])
    tap(phone, row)
    root = phone.dump()
    control(root, 'friends_pickerLayout')
    if sum(n.get('checked') == 'true' for n in root.iter('node')) != 1:
        raise ValueError('single profile selection unconfirmed')
    button = clickable_label(root, 'OK')
    reserve(receipt)
    tap(phone, button)
    owned_chat(phone.dump(), peer)
    return 'owned-profile-submitted-once'


def ensure_silent_emulator(phone):
    avd = phone.adb('emu', 'avd', 'name').decode().splitlines()[0].strip()
    if avd not in ('mooo-lab', 'mooo-lab-b'):
        raise ValueError('owned audio emulator unavailable')
    lines = subprocess.check_output(['ps', '-axo', 'comm=,args='], text=True).splitlines()
    matches = [line for line in lines if 'qemu-system-aarch64' in line
               and re.search(r'-avd\s+' + re.escape(avd) + r'(?:\s|$)', line)
               and 'python' not in line]
    if len(matches) != 1 or '-allow-host-audio' in matches[0]:
        raise ValueError('host microphone disabled state unconfirmed')


def send_audio(phone, peer, seconds, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if type(seconds) is not int or not 1 <= seconds <= 10:
        raise ValueError('bounded synthetic recording required')
    ensure_silent_emulator(phone)
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    tap(phone, control(root, 'media_send_layout'))
    root = phone.dump()
    _, (x, y) = node_target(control(root, 'handle_container'))
    phone.adb('shell', 'input', 'swipe', str(x), str(y), str(x), str(max(200, y // 4)), '450')
    root = phone.dump()
    tap(phone, clickable_text_parent(root, 'Voice Memo'))
    root = phone.dump()
    if not any(n.get('text') == 'Voice Memo' for n in root.iter('node')):
        raise ValueError('voice memo modal unavailable')
    record = control(root, 'record_control')
    if any(n.get('resource-id') == PREFIX + 'play' for n in root.iter('node')):
        raise ValueError('existing recording; inspect without resending')
    reserve(receipt)
    tap(phone, record)
    # Stop uses the validated unchanged record control. Never idle-dump animation.
    try:
        time.sleep(seconds)
    finally:
        tap(phone, record)
    root = phone.dump()
    if not any(n.get('text') == 'Voice Memo' for n in root.iter('node')):
        raise ValueError('stopped recording unavailable; do not retry')
    control(root, 'play')
    tap(phone, control(root, 'send'))
    owned_chat(phone.dump(), peer)
    return 'silent-audio-submitted-once'


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


def return_board_to_chat(phone, peer):
    # The chat More screen has an unlabeled Back control. An immediate keyevent
    # during the preceding activity transition can be lost; wait for its UI.
    for _ in range(4):
        root = phone.dump()
        if any(n.get('resource-id') == PREFIX + 'chat_room_root' for n in root.iter('node')):
            owned_chat(root, peer)
            return
        labels = {n.get('text') for n in root.iter('node')}
        headers = [n for n in root.iter('node') if n.get('text') == peer
                   and re.fullmatch(r'\[\d+,\d+\]\[\d+,\d+\]', n.get('bounds', ''))
                   and list(map(int, re.findall(r'\d+', n.get('bounds'))))[3] < 700]
        if {'Events', 'Polls', 'Boards'} <= labels and len(headers) == 1:
            tap(phone, unique([n for n in root.iter('node') if n.get('clickable') == 'true'
                               and n.get('enabled') != 'false' and n.get('bounds') == '[0,136][147,283]']))
            for _ in range(4):
                root = phone.dump()
                if any(n.get('resource-id') == PREFIX + 'chat_room_root' for n in root.iter('node')):
                    owned_chat(root, peer)
                    return
            raise ValueError('owned chat return unconfirmed; no further navigation')
    raise ValueError('poll return screen unconfirmed; no further navigation')


def send_post(phone, peer, text, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if not isinstance(text, str) or not re.fullmatch(r'Mooo-Synthetic-[A-Za-z0-9._-]{1,256}', text):
        raise ValueError('synthetic post text required')
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    tap(phone, clickable_label(root, 'More'))
    tap(phone, clickable_text_parent(phone.dump(), 'Boards'))
    root = phone.dump()
    if any(n.get('text') == text for n in root.iter('node')):
        raise ValueError('synthetic post text already exists')
    tap(phone, clickable_label(root, 'Create New'))
    prefix = 'com.kakao.talk.moim:id/'

    def field(root, name):
        return unique([n for n in root.iter('node') if n.get('resource-id') == prefix + name])

    root = phone.dump()
    node = field(root, 'content_edit')
    if node.get('text') != 'Write something...' or field(root, 'notice_check').get('checked') != 'false':
        raise ValueError('empty non-announcement post unconfirmed')
    tap(phone, node)
    phone.adb('shell', 'IFS= read -r value; input text "$value"', text=(text + '\n').encode())
    phone.adb('shell', 'input', 'keyevent', '4')
    previous = None
    button = None
    for _ in range(4):
        root = phone.dump()
        if field(root, 'content_edit').get('text') != text or field(root, 'notice_check').get('checked') != 'false':
            raise ValueError('post entry or announcement setting unconfirmed')
        current = clickable_label(root, 'DONE')
        if current.get('bounds') == previous:
            button = current
            break
        previous = current.get('bounds')
    if button is None:
        raise ValueError('post submit control still moving')
    reserve(receipt)
    tap(phone, button)
    for _ in range(4):
        root = phone.dump()
        if any(n.get('content-desc') == 'Create New' and n.get('clickable') == 'true' for n in root.iter('node')) and any(n.get('text') == text and n.get('resource-id') != prefix + 'content_edit' for n in root.iter('node')):
            tap(phone, clickable_label(root, 'Back'))
            return_board_to_chat(phone, peer)
            return 'synthetic-post-submitted-once'
    raise ValueError('post outcome unconfirmed; preserve receipt and do not retry')


def send_poll(phone, peer, title, options, receipt):
    if Path(receipt).exists():
        raise ValueError('previous attempt exists; inspect outcome without resending')
    if not re.fullmatch(r'Mooo-Synthetic-[A-Za-z0-9._-]{1,80}', title):
        raise ValueError('synthetic poll title required')
    if not isinstance(options, list) or len(options) != 3 or not all(isinstance(s, str) and re.fullmatch(r'[A-Za-z0-9._-]{1,80}', s) for s in options) or len(set(options)) != 3:
        raise ValueError('three distinct synthetic text options required')
    root = phone.dump()
    owned_chat(root, peer)
    if control(root, 'message_edit_text').get('text', '') not in ('', 'Message'):
        raise ValueError('composer not empty')
    tap(phone, clickable_label(root, 'More'))
    tap(phone, clickable_text_parent(phone.dump(), 'Polls'))
    root = phone.dump()
    if any(n.get('text') == title or n.get('content-desc', '').startswith(title + ',') for n in root.iter('node')):
        raise ValueError('synthetic poll title already exists')
    tap(phone, clickable_label(root, 'Create New'))
    prefix = 'com.kakao.talk.moim:id/'

    def field(root, name):
        return unique([n for n in root.iter('node') if n.get('resource-id') == prefix + name])

    root = phone.dump()
    subject = field(root, 'poll_subject_edit')
    items = [n for n in root.iter('node') if n.get('resource-id') == prefix + 'poll_item_title_edit']
    if subject.get('text') != 'Title' or len(items) != 3 or any(n.get('text') != 'Enter an option.' for n in items):
        raise ValueError('new empty poll form unconfirmed')
    for name in ('poll_multi_select_check', 'poll_secret_check', 'poll_item_addable_check'):
        if field(root, name).get('checked') != 'false':
            raise ValueError('unexpected poll settings')
    for index, value in enumerate([title] + options):
        root = phone.dump()
        node = field(root, 'poll_subject_edit') if index == 0 else unique([n for n in root.iter('node') if n.get('resource-id') == prefix + 'poll_item_title_edit'][index - 1:index])
        tap(phone, node)
        phone.adb('shell', 'IFS= read -r value; input text "$value"', text=(value + '\n').encode())
        phone.adb('shell', 'input', 'keyevent', '4')
    previous = None
    button = None
    for _ in range(4):
        root = phone.dump()
        if field(root, 'poll_subject_edit').get('text') != title or [n.get('text') for n in root.iter('node') if n.get('resource-id') == prefix + 'poll_item_title_edit'] != options:
            raise ValueError('poll entry unconfirmed')
        current = clickable_label(root, 'DONE')
        bounds = current.get('bounds')
        if bounds == previous:
            button = current
            break
        previous = bounds
    if button is None:
        raise ValueError('poll submit control still moving')
    reserve(receipt)
    tap(phone, button)
    for _ in range(4):
        root = phone.dump()
        if any(n.get('resource-id') == prefix + 'info_container' and n.get('content-desc', '').startswith(title + ',') for n in root.iter('node')):
            tap(phone, clickable_label(root, 'Back'))
            return_board_to_chat(phone, peer)
            return 'synthetic-poll-submitted-once'
    raise ValueError('poll outcome unconfirmed; preserve receipt and do not retry')


def main():
    try:
        adb, serial, action = sys.argv[1:]
        request = json.load(sys.stdin)
        phone = Phone(adb, serial)
        if action == 'text':
            result = send_text(phone, request['peer'], request['text'], request['receipt'])
        elif action == 'sticker':
            result = send_sticker(phone, request['peer'], request['receipt'])
        elif action == 'poll':
            result = send_poll(phone, request['peer'], request['title'], request['options'], request['receipt'])
        elif action == 'post':
            result = send_post(phone, request['peer'], request['text'], request['receipt'])
        elif action == 'location':
            result = send_location(phone, request['peer'], request['latitude'], request['longitude'], request['address'], request['receipt'])
        elif action == 'contact':
            result = send_contact(phone, request['peer'], request['name'], request['receipt'])
        elif action == 'profile':
            result = send_profile(phone, request['peer'], request['bounds'], request['description'], request['receipt'])
        elif action == 'audio':
            result = send_audio(phone, request['peer'], request['seconds'], request['receipt'])
        elif action == 'file':
            result = send_file(phone, request['peer'], request['filename'], request['sha256'], request['receipt'])
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
