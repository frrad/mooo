package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type receiveHeaderTimeoutContract struct {
	Status    string   `json:"status"`
	Questions []string `json:"questions"`
	Cases     []struct {
		Name           string   `json:"name"`
		Kind           string   `json:"kind"`
		Evidence       []string `json:"evidence"`
		TimeoutSeconds *int64   `json:"timeout_seconds"`
		Tag            *int64   `json:"tag"`
		Expect         []string `json:"expect"`
		RemainingGaps  []string `json:"remaining_gaps"`
	} `json:"cases"`
}

func TestReceiveHeaderTimeoutContractSchema(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-timeout-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v receiveHeaderTimeoutContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Status != "reviewed-static-unexecuted-runtime" {
		t.Fatalf("status=%q", v.Status)
	}
	if len(v.Questions) != 1 || v.Questions[0] != "RC-Q5" {
		t.Fatalf("questions=%v", v.Questions)
	}
	if len(v.Cases) != 7 {
		t.Fatalf("cases=%d", len(v.Cases))
	}
	wantKinds := []string{"timeout-admission", "timeout-admission", "timeout-enable", "timeout-disable", "disconnect-fanout", "completion-disarm", "packet-production"}
	for i, c := range v.Cases {
		if c.Kind != wantKinds[i] {
			t.Errorf("case %d kind=%q want %q", i, c.Kind, wantKinds[i])
		}
		if len(c.Evidence) == 0 || len(c.Expect) == 0 {
			t.Errorf("case %q lacks evidence/effects", c.Name)
		}
	}
	if got := v.Cases[4].Expect; len(got) != 7 || got[4] != "error_domain_locoagent" || got[5] != "error_code_minus_one" || got[6] != "error_userinfo_nil" {
		t.Fatalf("fanout=%v", got)
	}
	if got := v.Cases[5].Expect; len(got) != 4 || got[0] != "lookup_unsigned_packet_id" || got[3] != "disable_timeout" {
		t.Fatalf("disarm=%v", got)
	}
}
