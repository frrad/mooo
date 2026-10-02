package sessionlogin

import (
	"path/filepath"
	"testing"
)

func TestTimeoutTagHelpersMatchCanonicalVectors(t *testing.T) {
	contract, err := loadReceiveHeaderTimeoutContract(filepath.Join("testdata", "reconnect", "rc-q5-timeout-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range contract.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			switch tc.Kind {
			case "completion-disarm":
				if tc.PacketID == nil || tc.IncomingUniqueID == nil {
					t.Fatal("canonical completion-disarm case missing packet or incoming UID")
				}
				stored := ""
				if tc.StoredUniqueID != nil {
					stored = *tc.StoredUniqueID
				}
				got := ResolveTimeoutDisarm(TimeoutDisarmInput{PacketID: *tc.PacketID, StoredUniqueID: stored, IncomingUniqueID: *tc.IncomingUniqueID})
				wantDisable := containsEffect(tc.Expect, "disable_timeout")
				wantTag := int64(0)
				if tc.ExpectedTag != nil {
					wantTag = *tc.ExpectedTag
				}
				if got.Tag != wantTag || got.Disable != wantDisable {
					t.Fatalf("result=%+v want tag=%d disable=%t", got, wantTag, wantDisable)
				}
			case "unique-id-format":
				if tc.PacketMethod == nil || tc.PacketID == nil || tc.ExpectedUniqueID == nil {
					t.Fatal("canonical unique-id case missing required fields")
				}
				if got := FormatPacketUniqueID(*tc.PacketMethod, *tc.PacketID); got != *tc.ExpectedUniqueID {
					t.Fatalf("unique id=%q want %q", got, *tc.ExpectedUniqueID)
				}
			}
		})
	}
}

func containsEffect(effects []string, want string) bool {
	for _, effect := range effects {
		if effect == want {
			return true
		}
	}
	return false
}
