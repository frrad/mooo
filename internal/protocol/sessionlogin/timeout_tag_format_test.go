package sessionlogin

import "testing"

func TestFormatPacketUniqueIDUsesUnsignedDecimal(t *testing.T) {
	for _, tc := range []struct {
		method string
		id     uint32
		want   string
	}{
		{method: "LCHATLIST", id: 0, want: "LCHATLIST.0"},
		{method: "PING", id: 17, want: "PING.17"},
		{method: "GETCONF", id: ^uint32(0), want: "GETCONF.4294967295"},
	} {
		if got := FormatPacketUniqueID(tc.method, tc.id); got != tc.want {
			t.Fatalf("FormatPacketUniqueID(%q, %d)=%q want %q", tc.method, tc.id, got, tc.want)
		}
	}
}
