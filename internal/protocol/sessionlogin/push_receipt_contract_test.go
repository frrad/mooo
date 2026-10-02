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

type pushReceiptContract struct {
	Status   string            `json:"status"`
	Evidence []string          `json:"evidence"`
	Cases    []pushReceiptCase `json:"cases"`
}

type pushReceiptCase struct {
	Name            string   `json:"name"`
	ExecutionStatus int      `json:"execution_status"`
	PacketID        uint32   `json:"packet_id"`
	ExpectedTag     *uint64  `json:"expected_tag"`
	Expect          []string `json:"expect"`
}

var pushReceiptEffects = map[string]bool{
	"enqueue_owner_queue":          true,
	"read_execution_status":        true,
	"derive_unsigned_packet_tag":   true,
	"send_packet_with_derived_tag": true,
	"no_packet_send":               true,
}

func loadPushReceiptContract(path string) (pushReceiptContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return pushReceiptContract{}, err
	}
	var c pushReceiptContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	if err := validatePushReceiptContract(c); err != nil {
		return c, err
	}
	return c, nil
}

func validatePushReceiptContract(c pushReceiptContract) error {
	if c.Status != "reviewed-static-unexecuted-runtime" || len(c.Evidence) != 1 || c.Evidence[0] != "RC-BIN-032" || len(c.Cases) != 4 {
		return fmt.Errorf("status/evidence/cases: %q/%v/%d", c.Status, c.Evidence, len(c.Cases))
	}
	seen := map[string]bool{}
	for _, tc := range c.Cases {
		if tc.Name == "" || seen[tc.Name] || tc.ExpectedTag == nil || *tc.ExpectedTag != uint64(tc.PacketID) || seen[tc.Name] || len(tc.Expect) == 0 {
			return fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		for _, effect := range tc.Expect {
			if !pushReceiptEffects[effect] {
				return fmt.Errorf("unknown effect %q", effect)
			}
		}
		want := []string{"enqueue_owner_queue", "read_execution_status"}
		if tc.ExecutionStatus == 3 {
			want = append(want, "derive_unsigned_packet_tag", "send_packet_with_derived_tag")
		} else {
			want = append(want, "no_packet_send")
		}
		if !reflect.DeepEqual(tc.Expect, want) {
			return fmt.Errorf("%s effects=%v want=%v", tc.Name, tc.Expect, want)
		}
	}
	return nil
}

func TestPushReceiptContractSchema(t *testing.T) {
	if _, err := loadPushReceiptContract(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt.json")); err != nil {
		t.Fatal(err)
	}
}
