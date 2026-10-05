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
	ErrorPresent      bool     `json:"error_present"`
	ErrorDomain       string   `json:"error_domain,omitempty"`
	ErrorCode         int      `json:"error_code,omitempty"`
	ConnectionPresent bool     `json:"connection_present"`
	ExpectedEffects   []string `json:"expected_effects"`
}

func expectedNWCompletionEffects(c nwCompletionCase) []string {
	if !c.OwnerPresent {
		return []string{"weak_owner_load", "owner_nil_return"}
	}
	effects := []string{"weak_owner_load", "toggle_out_segment_timeout_false"}
	if !c.ErrorPresent {
		return append(effects, "success_cleanup")
	}
	if c.ErrorDomain == "posix" && c.ErrorCode == 89 {
		return append(effects, "posix_89_cleanup")
	}
	effects = append(effects, "error_log")
	if c.ConnectionPresent {
		effects = append(effects, "nw_connection_cancel")
	} else {
		effects = append(effects, "no_connection_cancel")
	}
	return append(effects, "error_cleanup")
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
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 8 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty %q", c.Name)
		}
		seen[c.Name] = true
		if c.ErrorDomain != "" && c.ErrorDomain != "posix" && c.ErrorDomain != "nonposix" {
			t.Fatalf("%s error_domain=%q", c.Name, c.ErrorDomain)
		}
		if c.ErrorPresent && c.ErrorDomain == "" {
			t.Fatalf("%s missing error domain", c.Name)
		}
		if got, want := c.ExpectedEffects, expectedNWCompletionEffects(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s effects=%v want %v", c.Name, got, want)
		}
	}
}
