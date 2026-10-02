package sessionlogin

import "time"

const defaultPingIntervalSeconds int32 = 180

type SocketTimeouts struct {
	Connect, ReceiveHeader, InSegment, OutSegment time.Duration
}

// RecoveredManagerTimeouts returns manager configuration values observed in the booking initializer.
func RecoveredManagerTimeouts() SocketTimeouts {
	return SocketTimeouts{Connect: 15 * time.Second, ReceiveHeader: 20 * time.Second, InSegment: 10 * time.Second, OutSegment: 10 * time.Second}
}

// PingInterval applies the recovered signed-seconds fallback: nonpositive
// configuration selects 180 seconds, while every positive value is retained.
// Timer lifecycle and ownership remain outside this pure policy.
func PingInterval(configured int32) int32 {
	if configured <= 0 {
		return defaultPingIntervalSeconds
	}
	return configured
}

// ShouldArmReceiveHeaderTimeout is the recovered signed-tag and positive-timeout gate.
func ShouldArmReceiveHeaderTimeout(tag int64, timeout time.Duration) bool {
	return tag >= 0 && timeout > 0
}
