package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type incomingPushNoticeFixture struct {
	Status           string                   `json:"status"`
	Source           map[string]string        `json:"source"`
	Cases            []incomingPushNoticeCase `json:"cases"`
	BlockSyncMapping [][2]string              `json:"block_sync_mapping"`
	Gaps             []string                 `json:"gaps"`
}

type incomingPushNoticeCase struct {
	Name            string           `json:"name"`
	Model           string           `json:"model"`
	Body            string           `json:"body"`
	InputFields     map[string]int32 `json:"input_fields"`
	ExpectedFields  map[string]int32 `json:"expected_fields"`
	ExpectedEffects []string         `json:"expected_effects"`
}

func applyIncomingBlockSyncMapping(input map[string]int32, mappings [][2]string) (map[string]int32, []string) {
	out := make(map[string]int32, len(input))
	for key, value := range input {
		out[key] = value
	}
	for _, mapping := range mappings {
		source, destination := mapping[0], mapping[1]
		value, present := out[source]
		if !present {
			continue
		}
		out[destination] = value
		delete(out, source)
	}
	return out, nil
}

func TestIncomingPushNoticeSourceContractFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-incoming-push-notices.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture incomingPushNoticeFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-source-unexecuted-runtime" || len(fixture.Cases) != 4 {
		t.Fatalf("fixture header=%#v", fixture)
	}
	if fixture.Source["block_sync_name_mapping_dictionary"] != "0x101350748" {
		t.Fatalf("mapping provenance=%q", fixture.Source["block_sync_name_mapping_dictionary"])
	}
	wantMapping := [][2]string{
		{"dl", "unblockIds"}, {"f", "isFull"}, {"l", "blockIds"},
		{"pdl", "plusUnblockIds"}, {"pf", "plusIsFull"}, {"pl", "plusBlockIds"},
		{"pr", "plusRevision"}, {"pts", "plusBlockTypes"}, {"r", "revision"}, {"ts", "blockTypes"},
	}
	if !reflect.DeepEqual(fixture.BlockSyncMapping, wantMapping) {
		t.Fatalf("incoming mapping=%v want %v", fixture.BlockSyncMapping, wantMapping)
	}
	seen := map[string]bool{}
	wantModels := map[string][2]string{
		"hint_nil_body_stops_before_delegate_or_receipt":                    {"hint", "nil"},
		"hint_empty_dictionary_constructs_nested_chat_log_from_same_object": {"hint", "empty_dictionary"},
		"block_sync_nsnull_fields_keep_defaults":                            {"block_sync", "dictionary_with_nsnull_fields"},
		"block_sync_typed_signed_int32_mapping":                             {"block_sync", "dictionary_with_typed_int32_fields"},
	}
	wantEffects := map[string][]string{
		"hint_nil_body_stops_before_delegate_or_receipt": {
			"method_lookup", "construct_notice", "read_packet_body", "loco_model_passes_nil_to_super",
			"sgjson_raises_nil_json_exception", "stop_before_nested_chat_log_delegate_or_receipt",
		},
		"hint_empty_dictionary_constructs_nested_chat_log_from_same_object": {
			"method_lookup", "construct_notice", "read_packet_body", "sgjson_dictionary",
			"missing_and_nsnull_fields_keep_defaults", "hint_super_init_succeeds",
			"construct_nested_chat_log_with_same_json_object", "delegate_hint_notice", "construct_receipt", "send_receipt",
		},
		"block_sync_nsnull_fields_keep_defaults": {
			"method_lookup", "construct_notice", "read_packet_body", "sgjson_dictionary", "nsnull_fields_skipped",
			"signed_int32_fields_remain_zero_defaults", "delegate_block_sync_notice", "construct_receipt", "send_receipt",
		},
		"block_sync_typed_signed_int32_mapping": {
			"method_lookup", "construct_notice", "read_packet_body", "mapping_source_r_to_destination_revision",
			"mapping_source_pr_to_destination_plusRevision", "remove_source_r_before_continue", "remove_source_pr_before_continue",
			"delegate_block_sync_notice", "construct_receipt", "send_receipt",
		},
	}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		wantModelBody, ok := wantModels[c.Name]
		if !ok || c.Model != wantModelBody[0] || c.Body != wantModelBody[1] {
			t.Fatalf("%s model/body=(%q,%q) want (%q,%q)", c.Name, c.Model, c.Body, wantModelBody[0], wantModelBody[1])
		}
		if got, want := c.ExpectedEffects, wantEffects[c.Name]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s effects=%v want %v", c.Name, got, want)
		}
		if c.Body == "nil" && c.Model != "hint" {
			t.Fatalf("%s nil-body case unexpectedly uses %s", c.Name, c.Model)
		}
	}
}

func TestIncomingBlockSyncMappingRenamesAndRemovesSourceKeys(t *testing.T) {
	fixtureBody, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-incoming-push-notices.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture incomingPushNoticeFixture
	if err := json.Unmarshal(fixtureBody, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		if c.Name != "block_sync_typed_signed_int32_mapping" {
			continue
		}
		got, _ := applyIncomingBlockSyncMapping(c.InputFields, fixture.BlockSyncMapping)
		if !reflect.DeepEqual(got, c.ExpectedFields) {
			t.Fatalf("mapped fields=%v want %v", got, c.ExpectedFields)
		}
		for _, source := range []string{"r", "pr"} {
			if _, ok := got[source]; ok {
				t.Errorf("source key %q survived mapping: %v", source, got)
			}
		}
		return
	}
	t.Fatal("typed mapping case missing")
}
