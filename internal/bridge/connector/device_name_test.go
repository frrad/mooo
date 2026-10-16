package connector

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestQRDeviceNameFollowsOfficialMacFixture(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/device-registration/device-name.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name  string `json:"name"`
			Input struct {
				ComputerName *string `json:"computer_name"`
				HWModel      *string `json:"hw_model"`
			} `json:"input"`
			Device struct {
				Name string `json:"name"`
			} `json:"device"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			computerName := func(context.Context) (string, bool) {
				if tc.Input.ComputerName == nil {
					return "", false
				}
				return *tc.Input.ComputerName, true
			}
			hardwareModel := func(context.Context) (string, bool) {
				if tc.Input.HWModel == nil {
					return "", false
				}
				return *tc.Input.HWModel, true
			}
			if got := qrDeviceName(context.Background(), "", computerName, hardwareModel); got != tc.Device.Name {
				t.Fatalf("qrDeviceName = %q, want %q", got, tc.Device.Name)
			}
		})
	}
}

func TestQRDeviceNameUsesConfiguredNameWithoutReadingTheHost(t *testing.T) {
	readHost := func(context.Context) (string, bool) {
		t.Fatal("a configured device name must not read the host")
		return "", false
	}
	if got := qrDeviceName(context.Background(), "  Living-room bridge.local  ", readHost, readHost); got != "  Living-room bridge.local  " {
		t.Fatalf("qrDeviceName = %q, want the configured name unchanged", got)
	}
}
