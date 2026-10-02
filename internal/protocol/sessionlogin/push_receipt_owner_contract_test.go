package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pushReceiptOwnerContract struct {
	Status   string                 `json:"status"`
	Evidence []string               `json:"evidence"`
	Cases    []pushReceiptOwnerCase `json:"cases"`
}
type pushReceiptOwnerCase struct {
	Name         string   `json:"name"`
	AgentPresent bool     `json:"agent_present"`
	Expect       []string `json:"expect"`
}

var pushReceiptOwnerEffects = map[string]bool{"enqueue_cancel_ping": true, "inline_push_receipt": true, "enqueue_schedule_ping": true}

func loadPushReceiptOwnerContract(path string) (pushReceiptOwnerContract, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return pushReceiptOwnerContract{}, e
	}
	var c pushReceiptOwnerContract
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if e = validatePushReceiptOwnerContract(c); e != nil {
		return c, e
	}
	return c, nil
}
func validatePushReceiptOwnerContract(c pushReceiptOwnerContract) error {
	if c.Status != "reviewed-static-unexecuted-runtime" || len(c.Evidence) != 1 || c.Evidence[0] != "RC-BIN-033" || len(c.Cases) != 1 {
		return fmt.Errorf("header")
	}
	for _, tc := range c.Cases {
		if tc.Name == "" || !tc.AgentPresent {
			return fmt.Errorf("invalid case %q", tc.Name)
		}
		for _, e := range tc.Expect {
			if !pushReceiptOwnerEffects[e] {
				return fmt.Errorf("unknown effect %q", e)
			}
		}
		want := []string{"enqueue_cancel_ping", "inline_push_receipt", "enqueue_schedule_ping"}
		if !reflect.DeepEqual(tc.Expect, want) {
			return fmt.Errorf("effects=%v want=%v", tc.Expect, want)
		}
	}
	return nil
}
func TestPushReceiptOwnerContractSchema(t *testing.T) {
	if _, e := loadPushReceiptOwnerContract(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-owner.json")); e != nil {
		t.Fatal(e)
	}
}
