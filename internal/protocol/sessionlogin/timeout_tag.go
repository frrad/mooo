package sessionlogin

import "strconv"

// FormatPacketUniqueID constructs the reviewed packet identity from a header
// method string and unsigned packet identifier. Header decoding and the valid
// method domain are outside this helper; an empty method is preserved as an
// empty prefix rather than assigned semantics here.
func FormatPacketUniqueID(method string, packetID uint32) string {
	return method + "." + strconv.FormatUint(uint64(packetID), 10)
}
