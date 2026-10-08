#!/usr/bin/env python3
"""Classify an ephemeral UIAutomator dump without exposing screen contents.

Unknown screens deliberately remain unknown; labels alone cannot prove auth.
"""
import sys
import xml.etree.ElementTree as ET


def classify(xml):
    root = ET.fromstring(xml)
    nodes = list(root.iter("node"))
    ids = {n.get("resource-id", "") for n in nodes}
    labels = {n.get("text", "").strip() for n in nodes
              if not n.get("class", "").endswith("EditText")}
    if any("verification information has expired" in s.lower() or
           "currently logged in on another device" in s.lower() for s in labels):
        return "session-expired"
    if any("Google Password Manager" in s for s in labels):
        return "password-manager"
    if "Contacts permission" in labels:
        return "contacts-permission"
    if "Phone permission" in labels:
        return "phone-permission"
    if any(i.endswith(":id/permission_allow_button") for i in ids):
        return "system-permission"
    if "Chat Restoration" in labels or "Skip and Start" in labels:
        return "restore-skip"
    if any("Please add a profile photo" in s for s in labels):
        return "default-photo"
    if "Sync Contacts" in labels and "Get Started" in labels:
        return "profile-setup"
    if any("Check the access permissions below" in s for s in labels):
        return "permissions-intro"
    if "com.kakao.talk:id/chat_room_root" not in ids:
        if any("verification code cannot be sent" in s.lower() for s in labels):
            return "verification-unavailable"
        if any("verification code" in s.lower() for s in labels):
            return "verification-required"
    if "Enter Your Phone Number" in labels:
        return "phone-verification-required"
    if "com.kakao.talk:id/login_kakao_account" in ids:
        return "login-form"
    if "com.kakao.talk:id/txt_message" in ids:
        return "unknown"
    if "com.kakao.talk:id/chat_room_root" in ids:
        return "chat-visible"
    if {"com.kakao.talk.finder:id/finder_nav_host_fragment",
            "com.kakao.talk.finder:id/input_focus"} <= ids:
        return "finder-visible"
    # Both navigation labels plus Kakao-owned nodes are required. This is a
    # readiness hint, not proof of server authentication or message delivery.
    if "com.kakao.talk:id/sliding_tabs" in ids and "Friends" in labels:
        return "home-visible"
    if {"Friends", "Chats"} <= labels and any(
            i.startswith("com.kakao.talk:id/") for i in ids):
        return "home-visible"
    if "com.kakao.talk:id/sliding_tabs" in ids:
        return "navigation-visible"
    return "unknown"


def permission_target(xml):
    nodes = list(ET.fromstring(xml).iter("node"))
    message = " ".join(n.get("text", "") for n in nodes
                       if n.get("resource-id", "").endswith(":id/permission_message"))
    if "access your contacts" in message:
        suffixes = (":id/permission_deny_button",
                    ":id/permission_deny_and_dont_ask_again_button")
    elif "make and manage phone calls" in message or "send you notifications" in message:
        suffixes = (":id/permission_allow_button",)
    else:
        return None
    for node in nodes:
        if any(node.get("resource-id", "").endswith(s) for s in suffixes):
            import re
            bounds = list(map(int, re.findall(r"\d+", node.get("bounds", ""))))
            if len(bounds) == 4 and node.get("enabled") != "false":
                x1, y1, x2, y2 = bounds
                return (x1 + x2) // 2, (y1 + y2) // 2
    return None


if __name__ == "__main__":
    try:
        xml = sys.stdin.read()
        if sys.argv[1:] == ["--permission-target"]:
            target = permission_target(xml)
            if target is None:
                sys.exit(1)
            print(*target)
        else:
            print("state=" + classify(xml))
    except ET.ParseError:
        print("state=invalid-ui")
        sys.exit(1)
