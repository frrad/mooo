package connector

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

var errInvalidID = errors.New("connector: invalid Kakao identifier")

// Kakao chat and user IDs are positive 64-bit integers. They are carried in
// the bridge's string identifiers in canonical decimal form.

func makePortalKey(chatID int64, login networkid.UserLoginID) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(strconv.FormatInt(chatID, 10)),
		Receiver: login,
	}
}

func parseChatID(portalID networkid.PortalID) (int64, error) {
	return parseID(string(portalID))
}

func makeUserID(userID int64) networkid.UserID {
	return networkid.UserID(strconv.FormatInt(userID, 10))
}

func makeUserLoginID(userID int64) networkid.UserLoginID {
	return networkid.UserLoginID(strconv.FormatInt(userID, 10))
}

func parseUserID(value string) (int64, error) {
	return parseID(value)
}

func networkIDString[T ~string](value T) string {
	return string(value)
}

// makeMessageID scopes a Kakao log ID to its chat. The official client keys
// messages by (chat, log), so the pair is the identity the bridge persists.
func makeMessageID(chatID, logID int64) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("%d:%d", chatID, logID))
}

func parseMessageID(messageID networkid.MessageID) (chatID, logID int64, err error) {
	chatPart, logPart, found := strings.Cut(string(messageID), ":")
	if !found {
		return 0, 0, errInvalidID
	}
	if chatID, err = parseID(chatPart); err != nil {
		return 0, 0, err
	}
	if logID, err = parseID(logPart); err != nil {
		return 0, 0, err
	}
	return chatID, logID, nil
}

func parseID(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != value {
		return 0, errInvalidID
	}
	return parsed, nil
}

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var errInvalidProfileName = errors.New("connector: profile name must be 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit")

// profileStatePath resolves a profile name inside the configured profile
// directory. Names are restricted to a single safe path component so a login
// request can never address a file outside that directory.
func profileStatePath(profileDir, name string) (string, error) {
	if profileDir == "" {
		return "", errors.New("connector: profile directory is not configured")
	}
	if !profileNamePattern.MatchString(name) {
		return "", errInvalidProfileName
	}
	return filepath.Join(profileDir, name), nil
}
