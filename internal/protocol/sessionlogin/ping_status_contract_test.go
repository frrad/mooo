package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type pingStatusVectors struct {
	Evidence []string `json:"evidence"`
	Status   string   `json:"status"`
	Cases    []struct {
		Name  string `json:"name"`
		Input struct {
			CallbackAgent    string `json:"callback_agent"`
			CurrentAgent     string `json:"current_agent"`
			Status           int32  `json:"status"`
			InternalStatus   int32  `json:"internal_status"`
			HandlerInstalled bool   `json:"handler_installed"`
			Latch            bool   `json:"latch"`
		} `json:"input"`
		WantEffects []string `json:"want_effects"`
		WantState   struct {
			Agent            *string `json:"agent"`
			InternalStatus   int32   `json:"internal_status"`
			HandlerInstalled bool    `json:"handler_installed"`
			Latch            bool    `json:"latch"`
		} `json:"want_state"`
	} `json:"cases"`
}

func TestPingStatusVectorsHaveStrictObservedShape(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-bin-003-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var vectors pingStatusVectors
	if err := dec.Decode(&vectors); err != nil {
		t.Fatal(err)
	}
	if vectors.Status != "implemented-status-reducer" {
		t.Fatalf("status=%q want implemented-status-reducer", vectors.Status)
	}
	if len(vectors.Evidence) != 1 || vectors.Evidence[0] != "RC-BIN-003" {
		t.Fatalf("evidence=%v want [RC-BIN-003]", vectors.Evidence)
	}
	if len(vectors.Cases) != 7 {
		t.Fatalf("case count=%d want 7", len(vectors.Cases))
	}
	for _, tc := range vectors.Cases {
		if tc.Input.CallbackAgent == "" || tc.Input.CurrentAgent == "" {
			t.Errorf("%s has empty identity", tc.Name)
		}
		if tc.Input.Status != 0 && tc.Input.Status != 1 && tc.Input.Status != 3 {
			t.Errorf("%s has unreviewed status=%d", tc.Name, tc.Input.Status)
		}
		if len(tc.WantEffects) == 0 && tc.Input.CallbackAgent == tc.Input.CurrentAgent && (tc.Input.Status == 0 || tc.Input.Status == 3) {
			t.Errorf("%s unexpectedly has no effect for matching reviewed status", tc.Name)
		}
	}
}
