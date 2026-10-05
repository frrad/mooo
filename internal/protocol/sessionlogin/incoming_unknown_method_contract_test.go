package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type incomingUnknownMethodFixture struct {
	Status string                      `json:"status"`
	Source map[string]string           `json:"source"`
	Cases  []incomingUnknownMethodCase `json:"cases"`
	Gaps   []string                    `json:"gaps"`
}

type incomingUnknownMethodCase struct {
	Name            string   `json:"name"`
	Method          string   `json:"method"`
	ClassLookup     string   `json:"class_lookup"`
	ExpectedEffects []string `json:"expected_effects"`
}

func modelUnknownIncomingMethod(method string, classes map[string]string) []string {
	effects := []string{"read_header_method"}
	className, ok := classes[method]
	if !ok {
		className = ""
		effects = append(effects, "lookup_class_absent")
	}
	if className == "" {
		effects = append(effects, "alloc_nil_class_is_nil_safe", "init_with_packet_on_nil_is_nil_safe", "derive_selector_from_nil_class_name", "owner_responds_to_selector_gate_false", "no_delegate_or_receipt_call")
		return effects
	}
	return append(effects, "lookup_class_present", "construct_notice", "selector_derivation_and_owner_gate")
}

func TestIncomingUnknownMethodSourceContractFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-incoming-unknown-method.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture incomingUnknownMethodFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-source-unexecuted-runtime" || len(fixture.Cases) != 1 {
		t.Fatalf("fixture header=%#v", fixture)
	}
	for _, c := range fixture.Cases {
		if c.ClassLookup != "absent" || c.Method == "" {
			t.Fatalf("invalid unknown case=%#v", c)
		}
		got := modelUnknownIncomingMethod(c.Method, map[string]string{"HINT": "LocoHintPushNotice", "BLOCKSYNC": "LocoBlockSyncPushNotice"})
		if !reflect.DeepEqual(got, c.ExpectedEffects) {
			t.Fatalf("%s effects=%v want %v", c.Name, got, c.ExpectedEffects)
		}
	}
}
