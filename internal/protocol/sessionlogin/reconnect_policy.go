package sessionlogin

import "time"

const minPingInterval = 180 * time.Second

type SocketTimeouts struct {
	Connect, ReceiveHeader, InSegment, OutSegment time.Duration
}

func DefaultSocketTimeouts() SocketTimeouts {
	return SocketTimeouts{Connect: 15 * time.Second, ReceiveHeader: 20 * time.Second, InSegment: 10 * time.Second, OutSegment: 10 * time.Second}
}

// PingInterval applies the recovered positive-greater-than-floor configuration
// rule. Timer lifecycle and ownership remain outside this pure policy.
func PingInterval(configured time.Duration) time.Duration {
	if configured > minPingInterval {
		return configured
	}
	return minPingInterval
}

// ShouldArmReceiveHeaderTimeout is the recovered signed-tag and positive-timeout gate.
func ShouldArmReceiveHeaderTimeout(tag int64, timeout time.Duration) bool {
	return tag >= 0 && timeout > 0
}
