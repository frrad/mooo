# Copy to .lab/emu-config.sh (gitignored), then fill owned-profile references.
SDK=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}
# Optional numeric resolver for a cold boot; leave empty to use host DNS.
EMU_DNS_SERVER=
AVD_A=owned-test-a
AVD_B=owned-test-b
AVD_C=owned-test-c
KEYCHAIN_A=owned-kakao-a
KEYCHAIN_B=owned-kakao-b
KEYCHAIN_C=owned-kakao-c
# Set only for profiles whose normal login/required-policy steps are authorized.
ALLOW_LOGIN=0
ALLOW_REQUIRED_PRIVACY_POLICY=0
# Separately authorize secondary-device QR approval for these owned profiles.
ALLOW_QR_ENROLLMENT=0
# Explicit synthetic message sends in an already-selected owned chat.
ALLOW_TEST_MESSAGES=0

# Optional provider contract: start listening, create the ready marker, then
# emit exactly one fresh code on stdout. Errors must not contain message bodies.
# Example using the bundled XMPP provider and a private slixmpp environment:
read_sms_code() {
  local profile=$1 marker=$2
  case "$profile" in
    a) export SMS_XMPP_JID="test-a@example.org" SMS_KEYCHAIN_SERVICE="owned-xmpp-a" ;;
    b) export SMS_XMPP_JID="test-b@example.org" SMS_KEYCHAIN_SERVICE="owned-xmpp-b" ;;
    c) export SMS_XMPP_JID="test-c@example.org" SMS_KEYCHAIN_SERVICE="owned-xmpp-c" ;;
    *) return 1 ;;
  esac
  exec "$LAB_DIR/venv/bin/python" "$TOOL_DIR/sms_xmpp.py" "$marker"
}
