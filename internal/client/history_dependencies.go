package client

// These read-only LOCO dependencies share the same account gate as source
// history. Primary SYNCMSG/MCHATLOGS admission is already handled by callers;
// writes, explicit read acknowledgements and keepalives are separate.
func pacedMetadataCommand(command string) bool {
	switch command {
	case "CHATINFO", "MEMBER", "MEMLIST", "GETMOMETA", "GETMCMETA", "CHATONROOM":
		return true
	default:
		return false
	}
}
