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

type changeSvrLogoutContract struct {
	Status string                `json:"status"`
	Cases  []changeSvrLogoutCase `json:"cases"`
}

type changeSvrLogoutCase struct {
	Name           string   `json:"name"`
	CallbackMain   bool     `json:"callback_on_main_thread"`
	ManagerPresent bool     `json:"manager_present"`
	HasMoreTickets bool     `json:"has_more_ticket_addresses"`
	Expected       []string `json:"expected_effects"`
}

func loadChangeSvrLogoutContract(path string) (changeSvrLogoutContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return changeSvrLogoutContract{}, err
	}
	var contract changeSvrLogoutContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		return contract, err
	}
	if contract.Status != "reviewed-static-unexecuted-runtime" || len(contract.Cases) != 3 {
		return contract, fmt.Errorf("invalid status=%q cases=%d", contract.Status, len(contract.Cases))
	}
	seen := map[string]bool{}
	for _, tc := range contract.Cases {
		if tc.Name == "" || seen[tc.Name] {
			return contract, fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		want := []string{"invoke_logout_for_change_server_inline", "clear_carriage_address", "check_more_ticket_addresses"}
		if !tc.CallbackMain {
			want = []string{"dispatch_main_queue", "invoke_logout_for_change_server", "clear_carriage_address", "check_more_ticket_addresses"}
		}
		if !tc.ManagerPresent {
			want = []string{}
		} else if tc.HasMoreTickets {
			want = append(want, "move_ticket_address_cursor")
		}
		if tc.ManagerPresent {
			want = append(want, "send_logout")
		}
		if !reflect.DeepEqual(tc.Expected, want) {
			return contract, fmt.Errorf("effects=%v want=%v case=%q", tc.Expected, want, tc.Name)
		}
	}
	return contract, nil
}

func TestChangeSvrLogoutContractSchema(t *testing.T) {
	if _, err := loadChangeSvrLogoutContract(filepath.Join("testdata", "reconnect", "rc-q5-changesvr-logout.json")); err != nil {
		t.Fatal(err)
	}
}
