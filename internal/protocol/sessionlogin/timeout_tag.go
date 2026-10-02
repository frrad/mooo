package sessionlogin

import "strconv"

// TimeoutDisarmInput is the source-backed identity boundary between a packet
// ID association and a completion's unique ID. Map lookup and ownership stay
// with the caller; this helper only evaluates the reviewed equality guard.
type TimeoutDisarmInput struct {
	PacketID         uint32
	StoredUniqueID   string
	IncomingUniqueID string
}

// FormatPacketUniqueID constructs the reviewed packet identity from a header
// method string and unsigned packet identifier. Header decoding and the valid
// method domain are outside this helper; an empty method is preserved as an
// empty prefix rather than assigned semantics here.
func FormatPacketUniqueID(method string, packetID uint32) string {
	return method + "." + strconv.FormatUint(uint64(packetID), 10)
}

// TimeoutDisarmResult describes the planned timeout action. The packet ID is
// preserved exactly when converted to the signed tag argument domain.
type TimeoutDisarmResult struct {
	Tag     int64
	Disable bool
}

// ResolveTimeoutDisarm derives the timeout tag only when the stored and
// incoming packet unique IDs match. It does not remove packet-ID mappings.
func ResolveTimeoutDisarm(input TimeoutDisarmInput) TimeoutDisarmResult {
	if input.StoredUniqueID == "" || input.StoredUniqueID != input.IncomingUniqueID {
		return TimeoutDisarmResult{}
	}
	return TimeoutDisarmResult{Tag: int64(input.PacketID), Disable: true}
}
