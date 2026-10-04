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

type kickoutContract struct {
	Status   string        `json:"status"`
	Evidence []string      `json:"evidence"`
	Cases    []kickoutCase `json:"cases"`
}
type kickoutCase struct {
	Name         string   `json:"name"`
	CallbackMain bool     `json:"callback_on_main_thread"`
	ReasonCode   int      `json:"reason_code"`
	ErrMsg       bool     `json:"err_msg_present"`
	ErrURL       bool     `json:"err_url_present"`
	ErrURLLabel  bool     `json:"err_url_label_present"`
	Expect       []string `json:"expect"`
}

var kickoutEffects = map[string]bool{"dispatch_main_queue": true, "invoke_projection": true, "invoke_projection_inline": true, "insert_reason_code": true, "insert_err_msg": true, "insert_err_url": true, "insert_err_url_label": true, "create_error_type_36": true, "post_kicked_out_notification": true}

func loadKickoutContract(path string) (kickoutContract, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return kickoutContract{}, e
	}
	var c kickoutContract
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if e = validateKickoutContract(c); e != nil {
		return c, e
	}
	return c, nil
}
func validateKickoutContract(c kickoutContract) error {
	if c.Status != "reviewed-static-unexecuted-runtime" || len(c.Evidence) != 1 || c.Evidence[0] != "RC-BIN-034" || len(c.Cases) != 3 {
		return fmt.Errorf("header")
	}
	seen := map[string]bool{}
	for _, tc := range c.Cases {
		if tc.Name == "" || seen[tc.Name] {
			return fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		for _, e := range tc.Expect {
			if !kickoutEffects[e] {
				return fmt.Errorf("unknown effect %q", e)
			}
		}
		want := []string{"invoke_projection_inline"}
		if !tc.CallbackMain {
			want = []string{"dispatch_main_queue", "invoke_projection"}
		}
		want = append(want, "insert_reason_code")
		if tc.ErrMsg {
			want = append(want, "insert_err_msg")
		}
		if tc.ErrURL {
			want = append(want, "insert_err_url")
		}
		if tc.ErrURLLabel {
			want = append(want, "insert_err_url_label")
		}
		want = append(want, "create_error_type_36", "post_kicked_out_notification")
		if !reflect.DeepEqual(tc.Expect, want) {
			return fmt.Errorf("%s effects=%v want=%v", tc.Name, tc.Expect, want)
		}
	}
	return nil
}
func TestKickoutNotificationContractSchema(t *testing.T) {
	if _, e := loadKickoutContract(filepath.Join("testdata", "reconnect", "rc-q5-kickout-notification.json")); e != nil {
		t.Fatal(e)
	}
}
