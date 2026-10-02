package sessionlogin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tokenHelperVector struct {
	Name                         string   `json:"name"`
	Kind                         string   `json:"kind"`
	Current                      *int64   `json:"current"`
	Incoming                     *int64   `json:"incoming"`
	ExistingLossCheckPositive    bool     `json:"existingLossCheckPositive"`
	ExistingTokenEqualsLossCheck bool     `json:"existingTokenEqualsLossCheck"`
	NestedContext                string   `json:"nestedContext"`
	Execution                    string   `json:"execution"`
	Expect                       []string `json:"expect"`
}

type tokenHelperVectors struct {
	Schema  string              `json:"schema"`
	Status  string              `json:"status"`
	Vectors []tokenHelperVector `json:"vectors"`
}

func TestTokenHelperUnresolvedVectorsSchema(t *testing.T) {
	path := filepath.Join("testdata", "token-helper-unresolved.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var got tokenHelperVectors
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "session-login-token-helper-v1" {
		t.Fatalf("schema = %q", got.Schema)
	}
	if got.Status != "implemented pure effect selector; durable policy unresolved" {
		t.Fatalf("status = %q", got.Status)
	}
	if len(got.Vectors) < 8 {
		t.Fatalf("vector count = %d", len(got.Vectors))
	}
	seen := map[string]bool{}
	for _, v := range got.Vectors {
		if v.Name == "" || (v.Kind != "token" && v.Kind != "blind") || len(v.Expect) == 0 || (v.Execution != "implemented" && v.Execution != "unresolved") {
			t.Fatalf("incomplete vector: %#v", v)
		}
		if seen[v.Name] {
			t.Fatalf("duplicate vector %q", v.Name)
		}
		seen[v.Name] = true
	}
	for _, name := range []string{
		"strict token advance dispatches token write",
		"negative token to zero is typed assertion error",
		"strict blind advance dispatches blind write",
		"missing nested context exposes storage gap",
		"commit and restart durability remain unresolved",
	} {
		if !seen[name] {
			t.Fatalf("missing required vector %q", name)
		}
	}
	validEffects := map[string]bool{
		"compare_strict_greater": true, "open_nested_context": true, "dispatch_context_block": true,
		"set_token": true, "set_blind": true, "no_effect": true, "observed_assertion_boundary": true,
		"typed_error_decision": true, "existing_loss_check_positive_guard": true, "existing_token_equality_probe": true,
		"set_loss_check_if_equal": true, "skip_loss_check_setter": true, "perform_blind_write": true,
		"invoke_block_inline": true, "wrap_write_operation": true, "invoke_block": true,
		"process_changed_objects": true, "wait_for_operation": true, "skip_block": true,
		"context_failure_behavior_unresolved": true, "durable_commit_unresolved": true, "reset_policy_unresolved": true,
	}
	for _, vector := range got.Vectors {
		if vector.Name == "negative token to zero is typed assertion error" {
			if vector.Current == nil || vector.Incoming == nil {
				t.Fatalf("%q: assertion vector requires current and incoming", vector.Name)
			}
			effects, err := SelectTokenHelperEffects(vector.Kind, *vector.Current, *vector.Incoming, vector.ExistingLossCheckPositive, vector.ExistingTokenEqualsLossCheck, vector.NestedContext)
			if !errors.Is(err, ErrTokenCursorAssertion) || effects != nil {
				t.Fatalf("%q: expected typed assertion error", vector.Name)
			}
			continue
		}
		if vector.Execution == "unresolved" {
			continue
		}
		if vector.Current == nil || vector.Incoming == nil {
			t.Fatalf("%q: implemented vector requires current and incoming", vector.Name)
		}
		gotEffects, err := SelectTokenHelperEffects(vector.Kind, *vector.Current, *vector.Incoming, vector.ExistingLossCheckPositive, vector.ExistingTokenEqualsLossCheck, vector.NestedContext)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", vector.Name, err)
		}
		for _, expected := range vector.Expect {
			if expected == "perform_blind_write" {
				expected = "set_blind"
			}
			if !validEffects[expected] {
				t.Fatalf("%q: unknown expected effect %q", vector.Name, expected)
			}
		}
		for _, expected := range vector.Expect {
			if expected == "perform_blind_write" {
				expected = "set_blind"
			}
			found := false
			for i, effect := range gotEffects {
				if effect == expected {
					gotEffects = gotEffects[i+1:]
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%q: expected ordered effect %q in %v", vector.Name, expected, gotEffects)
			}
		}
	}

}
