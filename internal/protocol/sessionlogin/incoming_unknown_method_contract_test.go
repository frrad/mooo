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

type incomingUnknownHeader struct {
	Method   string
	PacketID uint32
}

func deriveIncomingOwnerSelector(className string) string {
	suffix := "(null)"
	if className != "" {
		if len(className) < 4 {
			return ""
		}
		suffix = className[4:]
	}
	return "handle" + suffix + ":packetHeader:"
}

type incomingUnknownMethodHooks struct {
	responds func(string) bool
	perform  func(string, any, any)
}

func dispatchUnknownIncomingMethod(method string, classes map[string]string, header any, hooks incomingUnknownMethodHooks) []string {
	effects := []string{"read_header_method"}
	className := classes[method]
	if className == "" {
		effects = append(effects, "lookup_class_absent")
	}
	notice := any(nil)
	selector := deriveIncomingOwnerSelector(className)
	if className != "" {
		effects = append(effects, "lookup_class_present", "construct_notice")
	} else {
		effects = append(effects, "alloc_nil_class_is_nil_safe", "init_with_packet_on_nil_is_nil_safe")
	}
	effects = append(effects, "derive_selector_"+selector, "owner_gate_evaluated")
	if hooks.responds != nil && hooks.responds(selector) {
		effects = append(effects, "owner_gate_true_perform_selector_with_nil_notice")
		if hooks.perform != nil {
			hooks.perform(selector, notice, header)
		}
	} else {
		effects = append(effects, "owner_gate_false_no_perform_selector")
	}
	return effects
}

func modelUnknownIncomingMethod(method string, classes map[string]string, ownerResponds bool) []string {
	return dispatchUnknownIncomingMethod(method, classes, nil, incomingUnknownMethodHooks{
		responds: func(string) bool { return ownerResponds },
	})
}

func TestUnknownIncomingMethodSelectorGateIsConditional(t *testing.T) {
	classes := map[string]string{"HINT": "LocoHintPushNotice"}
	withoutOwner := modelUnknownIncomingMethod("UNRECOGNIZED", classes, false)
	withOwner := modelUnknownIncomingMethod("UNRECOGNIZED", classes, true)
	if reflect.DeepEqual(withoutOwner, withOwner) {
		t.Fatalf("owner gate did not affect unknown-method model: %#v", withoutOwner)
	}
	if got := withoutOwner[len(withoutOwner)-1]; got != "owner_gate_false_no_perform_selector" {
		t.Fatalf("owner=false tail=%q", got)
	}
	if got := withOwner[len(withOwner)-1]; got != "owner_gate_true_perform_selector_with_nil_notice" {
		t.Fatalf("owner=true tail=%q", got)
	}
}

func TestUnknownIncomingMethodSelectorArguments(t *testing.T) {
	if got := deriveIncomingOwnerSelector(""); got != "handle(null):packetHeader:" {
		t.Fatalf("nil class selector=%q", got)
	}
	if got := deriveIncomingOwnerSelector("LocoHintPushNotice"); got != "handleHintPushNotice:packetHeader:" {
		t.Fatalf("known class selector=%q", got)
	}
	header := &incomingUnknownHeader{Method: "UNRECOGNIZED", PacketID: 23}
	var respondedSelector, performedSelector string
	var performCount int
	var notice, gotHeader any
	effects := dispatchUnknownIncomingMethod("UNRECOGNIZED", map[string]string{}, header, incomingUnknownMethodHooks{
		responds: func(got string) bool {
			respondedSelector = got
			return true
		},
		perform: func(got string, gotNotice any, gotOriginalHeader any) {
			performedSelector = got
			performCount++
			notice, gotHeader = gotNotice, gotOriginalHeader
		},
	})
	if respondedSelector != "handle(null):packetHeader:" || performedSelector != respondedSelector || performCount != 1 || notice != nil || gotHeader != header {
		t.Fatalf("selector callback responds=%q perform=%q count=%d notice=%#v header=%p want=%p", respondedSelector, performedSelector, performCount, notice, gotHeader, header)
	}
	if got := effects[len(effects)-2:]; !reflect.DeepEqual(got, []string{"owner_gate_evaluated", "owner_gate_true_perform_selector_with_nil_notice"}) {
		t.Fatalf("selector callback effects tail=%v", got)
	}
	var falseRespondedSelector string
	falsePerformCount := 0
	dispatchUnknownIncomingMethod("UNRECOGNIZED", map[string]string{}, header, incomingUnknownMethodHooks{
		responds: func(got string) bool { falseRespondedSelector = got; return false },
		perform:  func(string, any, any) { falsePerformCount++ },
	})
	if falseRespondedSelector != "handle(null):packetHeader:" || falsePerformCount != 0 {
		t.Fatalf("owner=false responds=%q perform count=%d", falseRespondedSelector, falsePerformCount)
	}
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
		got := modelUnknownIncomingMethod(c.Method, map[string]string{"HINT": "LocoHintPushNotice", "BLOCKSYNC": "LocoBlockSyncPushNotice"}, false)
		if !reflect.DeepEqual(got, c.ExpectedEffects) {
			t.Fatalf("%s effects=%v want %v", c.Name, got, c.ExpectedEffects)
		}
	}
}
