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
    if "com.kakao.talk:id/login_kakao_account" in ids:
        return "login-form"
    if any("verification code cannot be sent" in s.lower() for s in labels):
        return "verification-unavailable"
    if any("verification code" in s.lower() for s in labels):
        return "verification-required"
    # Both navigation labels plus Kakao-owned nodes are required. This is a
    # readiness hint, not proof of server authentication or message delivery.
    if {"Friends", "Chats"} <= labels and any(
            i.startswith("com.kakao.talk:id/") for i in ids):
        return "home-visible"
    return "unknown"


if __name__ == "__main__":
    try:
        print("state=" + classify(sys.stdin.read()))
    except ET.ParseError:
        print("state=invalid-ui")
        sys.exit(1)
