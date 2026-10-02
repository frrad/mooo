package sessionlogin

import (
	"reflect"
	"strconv"
	"testing"
)

func TestReduceStatusHandlerMatchesApprovedContract(t *testing.T) {
	contract, err := loadStatusHandlerContract("testdata/reconnect/rc-q5-status-handler.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range contract.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			state := StatusHandlerState{
				HandlerPresent: boolValue(tc.HandlerPresent),
				Latch:          boolValue(tc.LatchInitial),
				DefaultHandler: boolValue(tc.DefaultHandlerPresent),
				AgentID:        "agent",
				ManagerID:      "manager",
				AgentStatus:    int8Value(tc.OldStatus),
				Pending:        make(map[string]struct{}),
			}
			for id := 1; id <= intValue(tc.PendingCount); id++ {
				state.Pending["uid-"+intString(id)] = struct{}{}
			}
			input := StatusHandlerInput{CallbackAgent: "agent", CallbackPresent: boolValue(tc.CallbackPresent)}
			switch tc.Kind {
			case "status-setter":
				input.Kind = StatusHandlerSetStatus
				input.NewStatus = int8Value(tc.NewStatus)
			case "manager-status-zero", "manager-status-three", "manager-status-gate":
				input.Kind = StatusHandlerManagerStatus
				input.Status = map[string]int8{"manager-status-zero": 0, "manager-status-three": 3, "manager-status-gate": 3}[tc.Kind]
				if !boolValue(tc.AgentCurrent) {
					input.CallbackAgent = "stale-agent"
				}
			case "disconnect-fanout-order":
				input.Kind = StatusHandlerDisconnect
				input.NewStatus = 0
			case "owner-capture":
				input.Kind = StatusHandlerOwnerCapture
			case "pending-response":
				input.Kind = StatusHandlerPendingResponse
				if boolValue(tc.CompletionMatch) {
					input.CompletionID = "uid-1"
					input.ResultPresent = true
				} else {
					input.CompletionID = "missing"
					input.ResultPresent = true
				}
			}
			beforePending := clonePending(state.Pending)
			gotState, effects := ReduceStatusHandler(state, input)
			got := formatStatusHandlerEffects(effects)
			if !reflect.DeepEqual(got, tc.Expect) {
				t.Fatalf("effects=%v want=%v", got, tc.Expect)
			}
			if !reflect.DeepEqual(state.Pending, beforePending) {
				t.Fatalf("reducer mutated input pending map: got=%v want=%v", state.Pending, beforePending)
			}
			if tc.Kind == "pending-response" && boolValue(tc.CompletionMatch) {
				if _, ok := gotState.Pending["uid-1"]; ok {
					t.Fatalf("matched completion remained pending: %v", gotState.Pending)
				}
			}
			if tc.Kind == "status-setter" && gotState.AgentStatus != int8Value(tc.NewStatus) {
				t.Fatalf("setter changed wrong status field: got agent=%d want=%d", gotState.AgentStatus, int8Value(tc.NewStatus))
			}
			if tc.Kind == "manager-status-zero" && boolValue(tc.AgentCurrent) {
				if effects[len(effects)-2].Kind != StatusEffectQueueCancel || effects[len(effects)-2].AgentID != "manager" || effects[len(effects)-2].Selector != "sendPingRequest:" {
					t.Fatalf("manager cancellation tuple=%+v", effects[len(effects)-2])
				}
			}
		})
	}
}

func TestReduceStatusHandlerPreservesUnresolvedDisconnectState(t *testing.T) {
	state := StatusHandlerState{AgentID: "agent", AgentStatus: 23, HandlerPresent: true, Pending: map[string]struct{}{"uid": {}}}
	got, effects := ReduceStatusHandler(state, StatusHandlerInput{Kind: StatusHandlerDisconnect, NewStatus: 23, Error: "disconnect"})
	if !reflect.DeepEqual(got.Pending, state.Pending) || !got.HandlerPresent {
		t.Fatalf("disconnect invented cleanup: got=%+v", got)
	}
	if len(effects) != 5 || effects[0].AgentID != "agent" || effects[0].OldStatus != 23 || effects[0].NewStatus != 0 || effects[0].Error != "disconnect" {
		t.Fatalf("disconnect setter tuple=%+v", effects)
	}
	if effects[1].AgentID != "agent" || effects[1].OldStatus != 23 || effects[1].NewStatus != 0 || effects[1].Error != "disconnect" {
		t.Fatalf("disconnect handler tuple=%+v", effects[1])
	}
	if effects[4].ErrorDomain != "LocoAgent" || effects[4].ErrorCode != -1 || effects[4].ResultPresent || effects[4].ErrorUserInfoPresent {
		t.Fatalf("disconnect fanout tuple=%+v", effects[4])
	}
	if effects[2].AgentID != "agent" || effects[2].Selector != "" {
		t.Fatalf("disconnect owner tuple=%+v", effects[2])
	}

}

func TestReduceStatusHandlerUsesManagerCancelTarget(t *testing.T) {
	state := StatusHandlerState{AgentID: "carriage", ManagerID: "manager", Pending: map[string]struct{}{}}
	_, effects := ReduceStatusHandler(state, StatusHandlerInput{Kind: StatusHandlerManagerStatus, CallbackAgent: "carriage", CallbackPresent: true, Status: 0})
	if effects[len(effects)-2].AgentID != "manager" || effects[len(effects)-2].Selector != "sendPingRequest:" {
		t.Fatalf("cancel target=%+v", effects[len(effects)-2])
	}
}

func TestReduceStatusHandlerUsesStringCompletionIdentity(t *testing.T) {
	state := StatusHandlerState{Pending: map[string]struct{}{"uid-a": {}}}
	got, effects := ReduceStatusHandler(state, StatusHandlerInput{Kind: StatusHandlerPendingResponse, CompletionID: "uid-a", ResultPresent: true})
	if _, ok := got.Pending["uid-a"]; ok || len(effects) != 3 || effects[1].CompletionID != "uid-a" || effects[2].CompletionID != "uid-a" || !effects[2].ResultPresent {
		t.Fatalf("matched string completion got state=%v effects=%v", got.Pending, effects)
	}
	if _, ok := state.Pending["uid-a"]; !ok {
		t.Fatal("input completion map was mutated")
	}
}

func TestReduceStatusHandlerRoutesUnmatchedPacketTuple(t *testing.T) {
	state := StatusHandlerState{DefaultHandler: true, Pending: map[string]struct{}{}}
	_, effects := ReduceStatusHandler(state, StatusHandlerInput{Kind: StatusHandlerPendingResponse, CompletionID: "uid-missing", ResultPresent: true})
	if len(effects) != 2 || effects[1].Kind != StatusEffectRouteDefault || effects[1].CompletionID != "uid-missing" || !effects[1].ResultPresent {
		t.Fatalf("default route tuple=%+v", effects)
	}
}

func TestReduceStatusHandlerRejectsNilManagerCallback(t *testing.T) {
	state := StatusHandlerState{AgentID: "agent", ManagerID: "manager", Pending: map[string]struct{}{}}
	got, effects := ReduceStatusHandler(state, StatusHandlerInput{Kind: StatusHandlerManagerStatus, CallbackAgent: "agent", Status: 3})
	if !reflect.DeepEqual(got, state) || len(effects) != 2 || effects[1].Kind != StatusEffectInvalidInput {
		t.Fatalf("nil callback accepted: state=%+v effects=%+v", got, effects)
	}
}

func TestReduceStatusHandlerRejectsMissingManagerTarget(t *testing.T) {
	state := StatusHandlerState{AgentID: "carriage", Pending: map[string]struct{}{}}
	got, effects := ReduceStatusHandler(state, StatusHandlerInput{Kind: StatusHandlerManagerStatus, CallbackAgent: "carriage", CallbackPresent: true, Status: 0})
	if !reflect.DeepEqual(got, state) || len(effects) != 2 || effects[1].Kind != StatusEffectInvalidInput {
		t.Fatalf("missing manager target accepted: state=%+v effects=%+v", got, effects)
	}
}

func TestReduceStatusHandlerRejectsInvalidPendingResponse(t *testing.T) {
	state := StatusHandlerState{DefaultHandler: true, Pending: map[string]struct{}{"uid": {}}}
	for _, input := range []StatusHandlerInput{
		{Kind: StatusHandlerPendingResponse, CompletionID: "uid"},
		{Kind: StatusHandlerPendingResponse, ResultPresent: true},
	} {
		got, effects := ReduceStatusHandler(state, input)
		if !reflect.DeepEqual(got, state) || len(effects) != 2 || effects[1].Kind != StatusEffectInvalidInput {
			t.Fatalf("invalid response accepted: input=%+v state=%+v effects=%+v", input, got, effects)
		}
	}
}

func formatStatusHandlerEffects(effects []StatusHandlerEffect) []string {
	output := make([]string, 0, len(effects))
	for _, effect := range effects {
		switch effect.Kind {
		case StatusEffectWriteByte:
			switch effect.Status {
			case 0x16:
				output = append(output, "write_status_0x16")
			case 0x1a:
				output = append(output, "write_status_0x1a")
			case 0x17:
				output = append(output, "write_status_0x17")
			default:
				output = append(output, "write_status_byte")
			}
		case StatusEffectInvokeHandler:
			output = append(output, "invoke_handler_agent_old_new_error")
		case StatusEffectNoHandler:
			output = append(output, "no_handler_callback")
		case StatusEffectNoDefaultHandler:
			output = append(output, "no_default_handler")
		case StatusEffectCompareAgent:
			output = append(output, "compare_current_agent")
		case StatusEffectClearAgent:
			output = append(output, "clear_agent_slot")
		case StatusEffectCallback:
			if effect.Value {
				output = append(output, "callback_true")
			} else {
				output = append(output, "callback_false")
			}
		case StatusEffectNoCallback:
			if effect.Value {
				output = append(output, "no_true_callback")
			} else {
				output = append(output, "no_false_callback")
			}
		case StatusEffectQueueCancel:
			output = append(output, "queue_ping_cancel")
		case StatusEffectClearHandler:
			output = append(output, "clear_status_handler")
		case StatusEffectSetLatch:
			output = append(output, "set_latch")
		case StatusEffectNoLatch:
			output = append(output, "no_latch_write")
		case StatusEffectNoEffects:
			output = append(output, "no_effects")
		case StatusEffectCancelOwner:
			output = append(output, "cancel_owner_delayed_work")
		case StatusEffectEnumeratePending:
			output = append(output, "enumerate_pending")
		case StatusEffectFanoutPending:
			output = append(output, "fanout_each_nil_locoagent_error")
		case StatusEffectWeakSendCapture:
			output = append(output, "weak_send_capture")
		case StatusEffectStrongTimeoutCapture:
			output = append(output, "strong_timeout_capture")
		case StatusEffectNoIdentityProof:
			output = append(output, "no_identity_equality_proof")
		case StatusEffectLookupCompletion:
			output = append(output, "lookup_completion_by_unique_id")
		case StatusEffectRemoveCompletion:
			output = append(output, "remove_completion_before_callback")
		case StatusEffectInvokeCompletion:
			output = append(output, "invoke_completion_packet_nil_error")
		case StatusEffectRouteDefault:
			output = append(output, "route_unmatched_to_default_handler")
		case StatusEffectInvalidInput:
			output = append(output, "invalid_input")
		}
	}
	return output
}

func intString(value int) string {
	return strconv.Itoa(value)
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func int8Value(value *int8) int8 {
	if value == nil {
		return 0
	}
	return *value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
