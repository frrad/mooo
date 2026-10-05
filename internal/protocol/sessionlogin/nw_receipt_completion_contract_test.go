package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type nwCompletionFixture struct {
	Status string             `json:"status"`
	Cases  []nwCompletionCase `json:"cases"`
}

type nwCompletionCase struct {
	Name              string   `json:"name"`
	OwnerPresent      bool     `json:"owner_present"`
	ErrorKind         string   `json:"error_kind"`
	ConnectionPresent bool     `json:"connection_present"`
	ExpectedEffects   []string `json:"expected_effects"`
}

func expectedNWCompletionEffects(c nwCompletionCase) []string {
	if !c.OwnerPresent {
		return []string{"weak_owner_load", "owner_nil_return"}
	}
	effects := []string{"weak_owner_load", "toggle_out_segment_timeout"}
	switch c.ErrorKind {
	case "success":
		return append(effects, "success_cleanup")
	case "posix_0x59":
		return append(effects, "posix_0x59_cleanup")
	case "other_error":
		effects = append(effects, "error_log")
		if c.ConnectionPresent {
			effects = append(effects, "nw_connection_cancel")
		} else {
			effects = append(effects, "no_connection_cancel")
		}
		return append(effects, "error_cleanup")
	default:
		return nil
	}
}

func TestNWReceiptCompletionFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-nw-completion.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f nwCompletionFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 5 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty %q", c.Name)
		}
		seen[c.Name] = true
		if c.ErrorKind != "success" && c.ErrorKind != "posix_0x59" && c.ErrorKind != "other_error" {
			t.Fatalf("%s error_kind=%q", c.Name, c.ErrorKind)
		}
		if c.OwnerPresent && c.ErrorKind == "" {
			t.Fatalf("%s missing error kind", c.Name)
		}
		if got, want := c.ExpectedEffects, expectedNWCompletionEffects(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s effects=%v want %v", c.Name, got, want)
		}
	}
}
