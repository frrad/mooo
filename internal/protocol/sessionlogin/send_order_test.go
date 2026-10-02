package sessionlogin

import (
	"fmt"
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
		gotKinds := make([]string, 0, len(effects)+1)
		for _, effect := range effects {
			if effect.Kind == EffectForwardCompletion {
				// The implementation keeps the nil-packet/producer-error callback
				// atomic; the source evidence names both tuple components.
				gotKinds = append(gotKinds, "forward_nil_packet", "forward_producer_error")
				continue
			}
			gotKinds = append(gotKinds, string(effect.Kind))
		}
		if !reflect.DeepEqual(gotKinds, vector.ExpectedEffects) {
			t.Fatalf("vector=%q effects=%v want=%v", vector.Name, gotKinds, vector.ExpectedEffects)
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
				if effect.Timeout != -1 || effect.Tag != int64(packetTag) {
					t.Fatalf("vector=%q socket tag=%d timeout=%d", vector.Name, effect.Tag, effect.Timeout)
				}
			}
		}
	}
}

func TestPlanSendOrderPreservesPacketTagDomain(t *testing.T) {
	for _, packetTag := range []uint32{0, 1, 0x80000000, 0xffffffff} {
		t.Run(fmt.Sprintf("%d", packetTag), func(t *testing.T) {
			effects := PlanSendOrder(SendOrderInput{
				ProducerStatus:    3,
				CompletionPresent: true,
				PacketTag:         packetTag,
				UniqueID:          "VECTOR.TAG",
			})
			for _, effect := range effects {
				switch effect.Kind {
				case EffectDerivePacketTag, EffectRegisterCompletionByUID, EffectRegisterPacketUID,
					EffectSocketWrite, EffectArmReceiveHeaderTimeout:
					if effect.Tag != int64(packetTag) {
						t.Fatalf("effect=%q tag=%d want=%d", effect.Kind, effect.Tag, packetTag)
					}
				}
			}
		})
	}
}
