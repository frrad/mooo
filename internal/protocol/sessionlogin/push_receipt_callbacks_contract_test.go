package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pushReceiptCallbacksFixture struct {
	Status string                    `json:"status"`
	Cases  []pushReceiptCallbackCase `json:"cases"`
}
type pushReceiptCallbackCase struct {
	Name                 string   `json:"name"`
	Callback             string   `json:"callback"`
	Tag                  int64    `json:"tag"`
	ExpectedForwardedTag int64    `json:"expected_forwarded_tag"`
	PendingState         int      `json:"pending_state"`
	ExpectedEffects      []string `json:"expected_effects"`
}

func expectedPushReceiptCallback(c pushReceiptCallbackCase) []string {
	switch c.Callback {
	case "write":
		return []string{"toggle_out_timeout_false", "forward_did_write_tag", "did_write_noop"}
	case "pending_admission":
		return []string{"synchronize_pending_map", "pending_state_two_branch", "record_pending_values", "update_pending_count"}
	case "read":
		if c.Tag == 0 {
			return []string{"receive_zero_tag_update", "did_read_header"}
		}
		return []string{"toggle_in_timeout_false", "receive_tagged_update", "read_header"}
	case "disconnect":
		return []string{"disconnect_failure_fanout", "pending_entries_failure_path"}
	default:
		return nil
	}
}
func TestPushReceiptCallbacksFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-callbacks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f pushReceiptCallbacksFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 7 {
		t.Fatalf("fixture header = %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		if c.Callback != "write" && c.Callback != "read" && c.Callback != "disconnect" && c.Callback != "pending_admission" {
			t.Fatalf("invalid callback %q", c.Callback)
		}
		if got, want := c.ExpectedEffects, expectedPushReceiptCallback(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", c.Name, got, want)
		}
		if c.Callback == "write" && c.ExpectedForwardedTag != c.Tag {
			t.Errorf("%s forwarded tag = %d, want %d", c.Name, c.ExpectedForwardedTag, c.Tag)
		}
	}
}
