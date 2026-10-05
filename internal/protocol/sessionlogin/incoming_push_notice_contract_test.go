package sessionlogin

import (
	"encoding/json"
	"errors"
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
	MappingInput    map[string]any   `json:"mapping_input"`
	MappingExpected map[string]any   `json:"mapping_expected"`
	DecodedExpected map[string]int32 `json:"decoded_expected"`
	ExpectedEffects []string         `json:"expected_effects"`
}

func applyIncomingBlockSyncMapping(input map[string]any, mappings [][2]string) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	for _, mapping := range mappings {
		source, destination := mapping[0], mapping[1]
		value, present := out[source]
		if !present {
			continue
		}
		if value == nil {
			delete(out, source)
			continue
		}
		out[destination] = value
		delete(out, source)
	}
	return out
}

type incomingNoticeHooks struct {
	super    func(any) (any, error)
	nested   func(any) error
	delegate func(any, *incomingNoticeHeader)
	receipt  func(*incomingNoticeHeader, any)
}

type incomingNoticeHeader struct {
	Method   string
	PacketID uint32
}

// constructIncomingNotice is the constructor half of the traced call chain.
// The callbacks make object identity executable rather than encoding it in
// effect labels. A nil result is kept separate from caller dispatch policy.
func constructIncomingNotice(model string, body any, hooks incomingNoticeHooks) (any, error) {
	if hooks.super == nil {
		return nil, errors.New("incomplete source-chain hooks")
	}
	decoded, err := hooks.super(body)
	if err != nil {
		return nil, err
	}
	if decoded == nil {
		return nil, nil
	}
	if model == "hint" && hooks.nested != nil {
		if err := hooks.nested(body); err != nil {
			return nil, err
		}
	}
	return decoded, nil
}

// receiveIncomingNotice models the caller-owned boundary: constructor errors
// stop dispatch, while a nil constructor result remains a valid value for the
// separately traced caller policy.
func receiveIncomingNotice(model string, body any, header *incomingNoticeHeader, hooks incomingNoticeHooks) error {
	notice, err := constructIncomingNotice(model, body, hooks)
	if err != nil {
		return err
	}
	return dispatchIncomingNotice(model, notice, header, hooks)
}

func dispatchIncomingNotice(model string, notice any, header *incomingNoticeHeader, hooks incomingNoticeHooks) error {
	if hooks.receipt == nil {
		return errors.New("missing receipt hook")
	}
	if hooks.delegate != nil {
		hooks.delegate(notice, header)
	}
	hooks.receipt(header, notice)
	return nil
}

func decodeIncomingBlockSyncFields(input map[string]any, mappings [][2]string) (map[string]int32, error) {
	mapped := applyIncomingBlockSyncMapping(input, mappings)
	decoded := map[string]int32{"revision": 0, "plusRevision": 0}
	for _, field := range []string{"revision", "plusRevision"} {
		value, present := mapped[field]
		if !present || value == nil {
			continue
		}
		// Fixture-domain guard: Foundation KVC coercion for other object types
		// remains untraced; this model accepts only explicit int32 vectors.
		intValue, ok := value.(int32)
		if !ok {
			return nil, errors.New("unsupported non-int32 BLOCKSYNC field")
		}
		decoded[field] = intValue
	}
	return decoded, nil
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

func TestIncomingNoticeModelStopsAndPreservesOrdering(t *testing.T) {
	fixtureBody, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-incoming-push-notices.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture incomingPushNoticeFixture
	if err := json.Unmarshal(fixtureBody, &fixture); err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]incomingPushNoticeCase, len(fixture.Cases))
	for _, c := range fixture.Cases {
		byName[c.Name] = c
	}
	nilCase, emptyCase, blockCase := byName["hint_nil_body_stops_before_delegate_or_receipt"], byName["hint_empty_dictionary_constructs_nested_chat_log_from_same_object"], byName["block_sync_nsnull_fields_keep_defaults"]
	var nilCalls []string
	nilHeader := &incomingNoticeHeader{Method: "HINT", PacketID: 1}
	nilResult := receiveIncomingNotice(nilCase.Model, nil, nilHeader, incomingNoticeHooks{
		super: func(body any) (any, error) {
			nilCalls = append(nilCalls, "super")
			return nil, errors.New("NSInternalInconsistencyException")
		},
		nested:   func(any) error { nilCalls = append(nilCalls, "nested"); return nil },
		delegate: func(any, *incomingNoticeHeader) { nilCalls = append(nilCalls, "delegate") },
		receipt:  func(*incomingNoticeHeader, any) { nilCalls = append(nilCalls, "receipt") },
	})
	if nilResult == nil || !reflect.DeepEqual(nilCalls, []string{"super"}) {
		t.Fatalf("nil body calls=%v err=%v", nilCalls, nilResult)
	}
	body := &struct{ fields map[string]any }{fields: map[string]any{}}
	var calls []string
	var nestedBody any
	notice, err := constructIncomingNotice(emptyCase.Model, body, incomingNoticeHooks{
		super:  func(got any) (any, error) { calls = append(calls, "super"); return got, nil },
		nested: func(got any) error { calls = append(calls, "nested"); nestedBody = got; return nil },
	})
	if err != nil || notice == nil || nestedBody != body || !reflect.DeepEqual(calls, []string{"super", "nested"}) {
		t.Fatalf("HINT body identity/order calls=%v nested=%p body=%p err=%v", calls, nestedBody, body, err)
	}
	header := &incomingNoticeHeader{Method: "HINT", PacketID: 17}
	err = dispatchIncomingNotice("hint", notice, header, incomingNoticeHooks{
		delegate: func(got any, gotHeader *incomingNoticeHeader) {
			if got != notice || gotHeader != header {
				t.Errorf("delegate args notice=%p header=%p", got, gotHeader)
			}
			calls = append(calls, "delegate")
		},
		receipt: func(gotHeader *incomingNoticeHeader, got any) {
			if got != notice || gotHeader != header {
				t.Errorf("receipt args notice=%p header=%p", got, gotHeader)
			}
			calls = append(calls, "receipt")
		},
	})
	if err != nil || !reflect.DeepEqual(calls, []string{"super", "nested", "delegate", "receipt"}) {
		t.Fatalf("delegate/receipt order calls=%v err=%v", calls, err)
	}
	var nestedErrorCalls []string
	if err := receiveIncomingNotice(emptyCase.Model, body, header, incomingNoticeHooks{
		super: func(got any) (any, error) { nestedErrorCalls = append(nestedErrorCalls, "super"); return got, nil },
		nested: func(any) error {
			nestedErrorCalls = append(nestedErrorCalls, "nested")
			return errors.New("nested ChatLog error")
		},
		delegate: func(any, *incomingNoticeHeader) { nestedErrorCalls = append(nestedErrorCalls, "delegate") },
		receipt:  func(*incomingNoticeHeader, any) { nestedErrorCalls = append(nestedErrorCalls, "receipt") },
	}); err == nil || !reflect.DeepEqual(nestedErrorCalls, []string{"super", "nested"}) {
		t.Fatalf("nested error continued downstream calls=%v err=%v", nestedErrorCalls, err)
	}
	var noDelegateCalls []string
	notice, err = constructIncomingNotice(blockCase.Model, body, incomingNoticeHooks{
		super: func(got any) (any, error) { noDelegateCalls = append(noDelegateCalls, "super"); return got, nil },
	})
	if err != nil || notice == nil {
		t.Fatalf("BLOCKSYNC construction err=%v", err)
	}
	err = dispatchIncomingNotice("block_sync", notice, header, incomingNoticeHooks{
		receipt: func(gotHeader *incomingNoticeHeader, got any) {
			if got != notice || gotHeader != header {
				t.Errorf("receipt args notice=%p header=%p", got, gotHeader)
			}
			noDelegateCalls = append(noDelegateCalls, "receipt")
		},
	})
	if err != nil || !reflect.DeepEqual(noDelegateCalls, []string{"super", "receipt"}) {
		t.Fatalf("missing delegate suppressed receipt calls=%v err=%v", noDelegateCalls, err)
	}
	var nilNoticeCalls []string
	if err := receiveIncomingNotice(emptyCase.Model, body, header, incomingNoticeHooks{
		super:  func(any) (any, error) { nilNoticeCalls = append(nilNoticeCalls, "super"); return nil, nil },
		nested: func(any) error { nilNoticeCalls = append(nilNoticeCalls, "nested"); return nil },
		delegate: func(got any, gotHeader *incomingNoticeHeader) {
			if got != nil || gotHeader != header {
				t.Errorf("nil delegate args notice=%p header=%p", got, gotHeader)
			}
			nilNoticeCalls = append(nilNoticeCalls, "delegate")
		},
		receipt: func(gotHeader *incomingNoticeHeader, got any) {
			if got != nil || gotHeader != header {
				t.Errorf("nil receipt args notice=%p header=%p", got, gotHeader)
			}
			nilNoticeCalls = append(nilNoticeCalls, "receipt")
		},
	}); err != nil || !reflect.DeepEqual(nilNoticeCalls, []string{"super", "delegate", "receipt"}) {
		t.Fatalf("nil initializer result was incorrectly suppressed calls=%v err=%v", nilNoticeCalls, err)
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
		input := make(map[string]any, len(c.InputFields))
		for key, value := range c.InputFields {
			input[key] = value
		}
		got := applyIncomingBlockSyncMapping(input, fixture.BlockSyncMapping)
		expected := make(map[string]any, len(c.ExpectedFields))
		for key, value := range c.ExpectedFields {
			expected[key] = value
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("mapped fields=%v want %v", got, expected)
		}
		decoded, err := decodeIncomingBlockSyncFields(input, fixture.BlockSyncMapping)
		if err != nil || !reflect.DeepEqual(decoded, c.DecodedExpected) {
			t.Fatalf("typed decoded=%v err=%v want %v", decoded, err, c.DecodedExpected)
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

func TestIncomingBlockSyncMappingHandlesNSNullAndExistingDestination(t *testing.T) {
	fixtureBody, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-incoming-push-notices.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture incomingPushNoticeFixture
	if err := json.Unmarshal(fixtureBody, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		if c.Name != "block_sync_nsnull_fields_keep_defaults" {
			continue
		}
		if got := applyIncomingBlockSyncMapping(c.MappingInput, fixture.BlockSyncMapping); !reflect.DeepEqual(got, c.MappingExpected) {
			t.Fatalf("fixture NSNull mapping=%v want %v", got, c.MappingExpected)
		}
		decoded, err := decodeIncomingBlockSyncFields(c.MappingInput, fixture.BlockSyncMapping)
		if err != nil || !reflect.DeepEqual(decoded, c.DecodedExpected) {
			t.Fatalf("fixture NSNull decoded=%v err=%v want %v", decoded, err, c.DecodedExpected)
		}
	}
	input := map[string]any{"r": nil, "revision": int32(7), "pr": int32(9)}
	got := applyIncomingBlockSyncMapping(input, [][2]string{{"r", "revision"}, {"pr", "plusRevision"}})
	want := map[string]any{"revision": int32(7), "plusRevision": int32(9)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NSNull mapping=%v want %v", got, want)
	}
	ordinary := applyIncomingBlockSyncMapping(map[string]any{"r": int32(11), "revision": int32(7)}, [][2]string{{"r", "revision"}})
	if got := ordinary["revision"]; got != int32(11) {
		t.Fatalf("ordinary source did not override destination: %v", ordinary)
	}
}

func TestIncomingBlockSyncTypedFieldsDecodeExplicitInt32Only(t *testing.T) {
	input := map[string]any{"r": int32(-2147483648), "pr": int32(2147483647)}
	got, err := decodeIncomingBlockSyncFields(input, [][2]string{{"r", "revision"}, {"pr", "plusRevision"}})
	if err != nil || !reflect.DeepEqual(got, map[string]int32{"revision": -2147483648, "plusRevision": 2147483647}) {
		t.Fatalf("typed int32 decoded=%v err=%v", got, err)
	}
	if _, err := decodeIncomingBlockSyncFields(map[string]any{"r": int64(1)}, [][2]string{{"r", "revision"}}); err == nil {
		t.Fatal("unsupported non-int32 input was accepted")
	}
}
