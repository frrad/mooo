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

type locoAgentReadContract struct {
	Status   string              `json:"status"`
	Evidence []string            `json:"evidence"`
	Cases    []locoAgentReadCase `json:"cases"`
}

type locoAgentReadCase struct {
	Name               string   `json:"name"`
	Kind               string   `json:"kind"`
	CryptoPresent      *bool    `json:"crypto_present"`
	Tag                *int     `json:"tag"`
	HeaderLength       *uint64  `json:"header_length"`
	BodyLength         *uint64  `json:"body_length"`
	ExpectedReadLength *uint64  `json:"expected_read_length"`
	Expect             []string `json:"expect"`
}

var locoAgentReadKinds = map[string]bool{
	"header_request": true, "header_result": true, "body_request": true,
	"partial_callback": true, "complete_callback": true, "body_delivery": true,
}

var locoAgentReadEffects = map[string]bool{
	"socket_read_length_4": true, "socket_read_length_22": true,
	"socket_read_timeout_minus_one": true, "socket_read_tag_zero": true,
	"header_zero_read_header": true, "header_nonzero_read_body": true,
	"body_request_timeout_minus_one": true, "body_request_tag_one": true,
	"body_request_length": true, "body_request_enable_timer_after_schedule": true,
	"partial_timer_unchanged": true, "partial_timer_disable": true,
	"partial_timer_enable": true, "complete_timer_disable": true,
	"complete_body_delivery": true, "complete_read_header": true,
	"complete_header_delivery": true, "decrypt_then_supply_raw": true,
	"supply_raw_without_decrypt": true,
}

func loadLocoAgentReadContract(path string) (locoAgentReadContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return locoAgentReadContract{}, err
	}
	var c locoAgentReadContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	if err := validateLocoAgentReadContract(c); err != nil {
		return c, err
	}
	return c, nil
}

func validateLocoAgentReadContract(c locoAgentReadContract) error {
	if c.Status != "reviewed-static-unexecuted-runtime" || len(c.Evidence) != 1 || c.Evidence[0] != "RC-BIN-031" || len(c.Cases) != 13 {
		return fmt.Errorf("status/cases: %q/%d", c.Status, len(c.Cases))
	}
	seen := map[string]bool{}
	for _, tc := range c.Cases {
		if tc.Name == "" || seen[tc.Name] || !locoAgentReadKinds[tc.Kind] || len(tc.Expect) == 0 {
			return fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		for _, e := range tc.Expect {
			if !locoAgentReadEffects[e] {
				return fmt.Errorf("unknown effect %q", e)
			}
		}
		var want []string
		switch tc.Kind {
		case "header_request":
			if tc.CryptoPresent == nil || tc.Tag == nil || *tc.Tag != 0 {
				return fmt.Errorf("header inputs %q", tc.Name)
			}
			length := "socket_read_length_22"
			if *tc.CryptoPresent {
				length = "socket_read_length_4"
			}
			want = []string{length, "socket_read_timeout_minus_one", "socket_read_tag_zero"}
		case "header_result":
			if tc.CryptoPresent == nil || !*tc.CryptoPresent || tc.HeaderLength == nil {
				return fmt.Errorf("header result inputs %q", tc.Name)
			}
			if *tc.HeaderLength == 0 {
				want = []string{"header_zero_read_header"}
			} else {
				want = []string{"header_nonzero_read_body"}
			}
		case "body_request":
			if tc.BodyLength == nil || tc.ExpectedReadLength == nil || *tc.BodyLength != *tc.ExpectedReadLength {
				return fmt.Errorf("body request length inputs %q", tc.Name)
			}
			want = []string{"body_request_length", "body_request_timeout_minus_one", "body_request_tag_one", "body_request_enable_timer_after_schedule"}
		case "partial_callback":
			if tc.Tag == nil {
				return fmt.Errorf("partial tag %q", tc.Name)
			}
			if *tc.Tag == 0 {
				want = []string{"partial_timer_unchanged"}
			} else {
				want = []string{"partial_timer_disable", "partial_timer_enable"}
			}
		case "complete_callback":
			if tc.Tag == nil {
				return fmt.Errorf("complete tag %q", tc.Name)
			}
			if *tc.Tag == 0 {
				want = []string{"complete_header_delivery"}
			} else {
				want = []string{"complete_timer_disable", "complete_body_delivery", "complete_read_header"}
			}
		case "body_delivery":
			if tc.CryptoPresent == nil {
				return fmt.Errorf("body crypto input %q", tc.Name)
			}
			if *tc.CryptoPresent {
				want = []string{"decrypt_then_supply_raw"}
			} else {
				want = []string{"supply_raw_without_decrypt"}
			}
		}
		if !reflect.DeepEqual(tc.Expect, want) {
			return fmt.Errorf("%s effects=%v want=%v", tc.Name, tc.Expect, want)
		}
	}
	return nil
}

func TestLocoAgentReadTransitionContractSchema(t *testing.T) {
	if _, err := loadLocoAgentReadContract(filepath.Join("testdata", "reconnect", "rc-q5-locoagent-read-transitions.json")); err != nil {
		t.Fatal(err)
	}
}
