#!/usr/bin/env python3
"""Bounded owned-phone QR approval. No challenge or code is logged or saved.

Called by research/emu.sh after selecting a fresh QR in the official scanner.
The caller owns challenge generation, expiry and final bridge verification.
"""
import re
import subprocess
import sys
import time
import uuid
import xml.etree.ElementTree as ET

PREFIX = "com.kakao.talk:id/"


def screen(root):
    nodes = list(root.iter("node"))
    labels = {n.get("text", "") for n in nodes
              if not n.get("class", "").endswith("EditText")}
    ids = {n.get("resource-id") for n in nodes}
    if {"Successfully logged in", "Close", "Manage Devices"} <= labels:
        return "qr-authorized"
    if any("Logins are restricted from sub devices" in s for s in labels):
        return "secondary-login-restricted"
    if "You cannot use this QR code." in labels:
        return "invalid-qr"
    if PREFIX + "login_pass_code_layout" in ids and {
            PREFIX + "edit_text", PREFIX + "btn_confirm"} <= ids:
        return "device-code-required"
    if PREFIX + "btn_login_with_pc" in ids:
        return "qr-approval"
    return "unknown"


def target(root, name):
    nodes = [n for n in root.iter("node")
             if n.get("resource-id") == PREFIX + name]
    if len(nodes) != 1 or nodes[0].get("enabled") == "false":
        raise ValueError("QR control unavailable")
    return node_target(nodes[0])


def node_target(node):
    if node.get("enabled") == "false":
        raise ValueError("QR control unavailable")
    match = re.fullmatch(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", node.get("bounds", ""))
    if not match:
        raise ValueError("QR control bounds unavailable")
    x1, y1, x2, y2 = map(int, match.groups())
    if x2 <= x1 or y2 <= y1:
        raise ValueError("QR control bounds unavailable")
    return node, ((x1 + x2) // 2, (y1 + y2) // 2)


class Phone:
    def __init__(self, adb, serial):
        self.command = [adb, "-s", serial]

    def adb(self, *args, text=None):
        return subprocess.run(self.command + list(args), input=text,
                              stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                              check=True, timeout=10).stdout

    def dump(self):
        path = "/sdcard/mooo-qr-" + uuid.uuid4().hex + ".xml"
        for attempt in range(3):
            try:
                self.adb("shell", "uiautomator", "dump", path)
                return ET.fromstring(self.adb("exec-out", "cat", path))
            except (ET.ParseError, subprocess.SubprocessError):
                if attempt == 2:
                    raise ValueError("UI dump unavailable") from None
                time.sleep(.25)
            finally:
                self.adb("shell", "rm", "-f", path)

    def tap(self, root, name):
        _, (x, y) = target(root, name)
        self.adb("shell", "input", "tap", str(x), str(y))

    def wait(self, expected, timeout=15):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            root = self.dump()
            state = screen(root)
            if state in {"invalid-qr", "secondary-login-restricted"}:
                raise ValueError(state)
            if state == expected:
                return root
            time.sleep(.25)
        raise ValueError("QR screen timed out")

    def approve(self):
        root = self.wait("qr-approval")
        # Persistent PC verification is the maintainer's standing preference.
        self.tap(root, "btn_login_with_pc")
        self.wait("device-code-required")
        return "device-code-required"

    def enter_code(self, code):
        if not re.fullmatch(r"[A-Za-z0-9]{4}", code):
            raise ValueError("invalid device code")
        root = self.wait("device-code-required")
        field, _ = target(root, "edit_text")
        if field.get("text", ""):
            raise ValueError("device code field is not empty")
        self.tap(root, "edit_text")
        # The code exists only in this process and ADB stdin, never argv.
        self.adb("shell", 'IFS= read -r code; input text "$code"',
                 text=(code + "\n").encode())
        root = self.dump()
        if screen(root) != "device-code-required":
            raise ValueError("device code screen changed")
        field, _ = target(root, "edit_text")
        button, _ = target(root, "btn_confirm")
        if field.get("text") != code or button.get("enabled") != "true":
            raise ValueError("device code entry not verified")
        self.tap(root, "btn_confirm")
        return "device-code-submitted"

    def finish(self):
        root = self.wait("qr-authorized")
        nodes = [n for n in root.iter("node") if n.get("text") == "Close"]
        if len(nodes) != 1:
            raise ValueError("QR success close control unavailable")
        _, (x, y) = node_target(nodes[0])
        self.adb("shell", "input", "tap", str(x), str(y))
        return "qr-success-closed"


def main():
    if len(sys.argv) != 4 or sys.argv[3] not in {"approve", "code", "finish"}:
        raise ValueError("invalid QR helper arguments")
    phone = Phone(sys.argv[1], sys.argv[2])
    if sys.argv[3] == "approve":
        result = phone.approve()
    elif sys.argv[3] == "finish":
        result = phone.finish()
    else:
        code = sys.stdin.buffer.readline(32).decode("ascii").strip()
        result = phone.enter_code(code)
    print("state=" + result)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, ET.ParseError, subprocess.SubprocessError) as error:
        # Exception representations can expose device paths or field data.
        if isinstance(error, ValueError) and str(error) in {"invalid-qr", "secondary-login-restricted"}:
            print("state=" + str(error))
        print("QR phone preparation failed; inspect the owned screen privately", file=sys.stderr)
        sys.exit(1)
