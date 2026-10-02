package sessionlogin

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanSendOrderExecutesCanonicalVectors(t *testing.T) {
	contract, err := loadSendOrderContract(filepath.Join("testdata", "reconnect", "rc-q5-send-order.json"))
	if err != nil {
		t.Fatal(err)
	}
	const packetTag = uint32(0xffffffff)
	for _, vector := range contract.Cases {
		uniqueID := "VECTOR.4294967295"
		effects := PlanSendOrder(SendOrderInput{
			ProducerStatus:    vector.ProducerStatus,
			CompletionPresent: vector.Completion,
			CryptoPresent:     vector.CryptoPresent,
			PacketTag:         packetTag,
			UniqueID:          uniqueID,
		})
		gotKinds := make([]string, len(effects))
		for i, effect := range effects {
			gotKinds[i] = string(effect.Kind)
		}
		wantKinds := make([]string, 0, len(vector.ExpectedEffects))
		for i, expected := range vector.ExpectedEffects {
			if expected == "forward_nil_packet" || expected == "forward_producer_error" {
				if i == 0 || vector.ExpectedEffects[i-1] == expected {
					continue
				}
				wantKinds = append(wantKinds, string(EffectForwardCompletion))
				continue
			}
			wantKinds = append(wantKinds, expected)
		}
		if !reflect.DeepEqual(gotKinds, wantKinds) {
			t.Fatalf("vector=%q effects=%v want=%v", vector.Name, gotKinds, wantKinds)
		}
		for _, effect := range effects {
			switch effect.Kind {
			case EffectDerivePacketTag, EffectArmReceiveHeaderTimeout:
				if effect.Tag != int64(packetTag) {
					t.Fatalf("vector=%q effect=%q tag=%d", vector.Name, effect.Kind, effect.Tag)
				}
			case EffectRegisterCompletionByUID, EffectRegisterPacketUID:
				if effect.UniqueID != uniqueID || effect.Tag != int64(packetTag) {
					t.Fatalf("vector=%q effect=%q uid=%q", vector.Name, effect.Kind, effect.UniqueID)
				}
			case EffectForwardCompletion:
				if effect.PacketPresent {
					t.Fatalf("vector=%q nil packet marked present", vector.Name)
				}
				if !effect.ErrorPresent {
					t.Fatalf("vector=%q producer error absent", vector.Name)
				}
			case EffectSocketWrite:
				if effect.Timeout != -1 {
					t.Fatalf("vector=%q socket timeout=%d", vector.Name, effect.Timeout)
				}
			}
		}
	}
}
