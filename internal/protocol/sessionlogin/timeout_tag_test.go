package sessionlogin

import "testing"

func TestResolveTimeoutDisarmPreservesUnsignedPacketID(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   uint32
		want int64
	}{
		{name: "zero", id: 0, want: 0},
		{name: "ordinary", id: 17, want: 17},
		{name: "maximum uint32", id: ^uint32(0), want: 4294967295},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveTimeoutDisarm(TimeoutDisarmInput{PacketID: tc.id, StoredUniqueID: "uid", IncomingUniqueID: "uid"})
			if got.Tag != tc.want || !got.Disable {
				t.Fatalf("result=%+v want tag=%d enabled", got, tc.want)
			}
		})
	}
}

func TestResolveTimeoutDisarmFailsClosedOnUIDMismatch(t *testing.T) {
	for _, tc := range []TimeoutDisarmInput{
		{PacketID: 17, StoredUniqueID: "stored", IncomingUniqueID: "incoming"},
		{PacketID: 17, StoredUniqueID: "", IncomingUniqueID: "incoming"},
	} {
		if got := ResolveTimeoutDisarm(tc); got.Disable || got.Tag != 0 {
			t.Fatalf("mismatched identity produced action: %+v", got)
		}
	}
}
