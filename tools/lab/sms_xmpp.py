#!/usr/bin/env python3
"""Receive a fresh Kakao SMS on one explicitly configured owned XMPP line.

Credentials are Keychain references in environment, never command arguments.
Stdout must be a pipe/FIFO and carries exactly one code. Readiness is a marker
file in the caller's private temporary directory. No message body is logged.
"""
import asyncio
from datetime import UTC, datetime
import os
from pathlib import Path
import re
import subprocess
import sys

PATTERNS = (
    re.compile(r"\[([0-9]{4,6})\]\s*verification code", re.I),
    re.compile(r"(?:verification code|code)[^0-9]{0,24}([0-9]{4,6})", re.I),
)


def find_code(body):
    if "kakao" not in body.lower():
        return None
    for pattern in PATTERNS:
        match = pattern.search(body)
        if match:
            return match.group(1)
    return None


def sms_code(sender, body):
    if not sender.split("/", 1)[0].endswith("@cheogram.com"):
        return None
    return find_code(body)


async def run(marker):
    import slixmpp
    jid = os.environ["SMS_XMPP_JID"]
    service = os.environ["SMS_KEYCHAIN_SERVICE"]
    password = subprocess.run(
        ["security", "find-generic-password", "-s", service, "-a", jid, "-w"],
        check=True, capture_output=True, text=True,
    ).stdout.rstrip("\r\n")
    if not password:
        raise ValueError("empty credential")
    xmpp = slixmpp.ClientXMPP(jid, password)
    for plugin in ("xep_0030", "xep_0199", "xep_0313"):
        xmpp.register_plugin(plugin)
    live = asyncio.get_running_loop().create_future()
    request_marker = Path(str(marker) + ".request")

    def request_since():
        try:
            return datetime.fromtimestamp(float(request_marker.read_text()), UTC)
        except (OSError, ValueError):
            return None

    def on_message(message):
        since = request_since()
        if since is None:
            return
        delay = message.xml.find("{urn:xmpp:delay}delay") if hasattr(message, "xml") else None
        if delay is not None:
            try:
                stamp = datetime.fromisoformat(delay.get("stamp", "").replace("Z", "+00:00"))
                if stamp < since:
                    return
            except (ValueError, TypeError):
                return
        code = sms_code(str(message["from"]), str(message["body"]))
        if code and not live.done():
            live.set_result(code)

    xmpp.add_event_handler("message", on_message)
    try:
        xmpp.connect()
        await xmpp.wait_until("session_start", timeout=30)
        xmpp.send_presence()
        descriptor = os.open(marker, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        os.close(descriptor)
        deadline = asyncio.get_running_loop().time() + 180
        archive_available = True
        while asyncio.get_running_loop().time() < deadline:
            if live.done():
                print(live.result(), flush=True)
                return
            # Archiving may be disabled. Always receive live SMS as well.
            since = request_since()
            if archive_available and since is not None:
                try:
                    async with asyncio.timeout(10):
                        async for message in xmpp.plugin["xep_0313"].iterate(
                                start=since, reverse=True, rsm={"max": 20}, total=20):
                            forwarded = message.xml.find(
                                ".//{jabber:client}message")
                            if forwarded is None:
                                continue
                            body = forwarded.find("{jabber:client}body")
                            code = sms_code(forwarded.get("from", ""),
                                            body.text or "" if body is not None else "")
                            if code:
                                print(code, flush=True)
                                return
                except Exception:
                    archive_available = False
            try:
                await asyncio.wait_for(asyncio.shield(live), timeout=5)
            except TimeoutError:
                pass
        raise TimeoutError("SMS deadline")
    finally:
        xmpp.disconnect()


if __name__ == "__main__":
    if len(sys.argv) != 2 or sys.stdout.isatty():
        sys.exit("SMS helper requires a readiness marker and piped stdout")
    try:
        asyncio.run(run(Path(sys.argv[1])))
    except KeyboardInterrupt:
        sys.exit("SMS wait cancelled")
    except Exception:
        sys.exit("SMS receiver failed or timed out")
