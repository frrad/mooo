package sessionlogin

// StatusHandlerInputKind identifies one reviewed status-handler operation.
type StatusHandlerInputKind uint8

const (
	StatusHandlerSetStatus StatusHandlerInputKind = iota + 1
	StatusHandlerManagerStatus
	StatusHandlerDisconnect
	StatusHandlerOwnerCapture
	StatusHandlerPendingResponse
)

// StatusHandlerInput carries the bounded inputs for one pure status-handler
// transition. It contains no socket, timer, callback, or owner object.
type StatusHandlerInput struct {
	Kind            StatusHandlerInputKind
	NewStatus       int8
	CallbackAgent   string
	Status          int8
	CallbackPresent bool
	CompletionID    string
	Error           string
	ResultPresent   bool
}

// StatusHandlerState is the durable-free projection needed by the reviewed
// status and pending-response cases. Pending entries are copied on output.
type StatusHandlerState struct {
	AgentID        string
	ManagerID      string
	AgentStatus    int8
	InternalStatus int8
	HandlerPresent bool
	Latch          bool
	Pending        map[string]struct{}
	DefaultHandler bool
}

type StatusHandlerEffectKind uint8

const (
	StatusEffectWriteByte StatusHandlerEffectKind = iota + 1
	StatusEffectInvokeHandler
	StatusEffectNoHandler
	StatusEffectNoDefaultHandler
	StatusEffectCompareAgent
	StatusEffectClearAgent
	StatusEffectCallback
	StatusEffectNoCallback
	StatusEffectQueueCancel
	StatusEffectClearHandler
	StatusEffectSetLatch
	StatusEffectNoLatch
	StatusEffectNoEffects
	StatusEffectCancelOwner
	StatusEffectEnumeratePending
	StatusEffectFanoutPending
	StatusEffectWeakSendCapture
	StatusEffectStrongTimeoutCapture
	StatusEffectNoIdentityProof
	StatusEffectSameReceiverCapture
	StatusEffectLookupCompletion
	StatusEffectRemoveCompletion
	StatusEffectInvokeCompletion
	StatusEffectRouteDefault
	StatusEffectInvalidInput
)

// StatusHandlerEffect is an ordered planned side effect. Applying it to a
// network/session owner is deliberately outside this package.
type StatusHandlerEffect struct {
	Kind                 StatusHandlerEffectKind
	AgentID              string
	OldStatus            int8
	NewStatus            int8
	Status               int8
	Value                bool
	Count                int
	Error                string
	ErrorDomain          string
	ErrorCode            int
	ErrorUserInfoPresent bool
	ResultPresent        bool
	CompletionID         string
	Selector             string
}

// ReduceStatusHandler applies the reviewed status-handler and pending-response
// transitions. Unknown input kinds and stale agent callbacks are no-ops.
func ReduceStatusHandler(state StatusHandlerState, input StatusHandlerInput) (StatusHandlerState, []StatusHandlerEffect) {
	state.Pending = clonePending(state.Pending)
	switch input.Kind {
	case StatusHandlerSetStatus:
		effects := []StatusHandlerEffect{{Kind: StatusEffectWriteByte, AgentID: state.AgentID, OldStatus: state.AgentStatus, NewStatus: input.NewStatus, Error: input.Error}}
		if state.HandlerPresent {
			effects = append(effects, StatusHandlerEffect{Kind: StatusEffectInvokeHandler, AgentID: state.AgentID, OldStatus: state.AgentStatus, NewStatus: input.NewStatus, Error: input.Error})
		} else {
			effects = append(effects, StatusHandlerEffect{Kind: StatusEffectNoHandler})
		}
		state.AgentStatus = input.NewStatus
		return state, effects

	case StatusHandlerManagerStatus:
		effects := []StatusHandlerEffect{{Kind: StatusEffectCompareAgent}}
		if state.AgentID == "" || input.CallbackAgent == "" || state.AgentID != input.CallbackAgent {
			return state, append(effects, StatusHandlerEffect{Kind: StatusEffectNoEffects})
		}
		if state.ManagerID == "" {
			return state, append(effects, StatusHandlerEffect{Kind: StatusEffectInvalidInput})
		}
		switch input.Status {
		case 0:
			if !input.CallbackPresent {
				return state, append(effects, StatusHandlerEffect{Kind: StatusEffectInvalidInput})
			}
			priorLatch := state.Latch
			agent := state.AgentID
			state.AgentID = ""
			state.HandlerPresent = false
			state.InternalStatus = 0x16
			if priorLatch {
				state.InternalStatus = 0x1a
			}
			effects = append(effects,
				StatusHandlerEffect{Kind: StatusEffectClearAgent, AgentID: agent},
				StatusHandlerEffect{Kind: StatusEffectWriteByte, Status: state.InternalStatus},
			)
			if !priorLatch {
				effects = append(effects, StatusHandlerEffect{Kind: StatusEffectCallback, Value: false})
			} else {
				effects = append(effects, StatusHandlerEffect{Kind: StatusEffectNoCallback})
			}
			effects = append(effects,
				StatusHandlerEffect{Kind: StatusEffectQueueCancel, AgentID: state.ManagerID, Selector: "sendPingRequest:"},
				StatusHandlerEffect{Kind: StatusEffectClearHandler, AgentID: agent},
			)
			return state, effects
		case 3:
			if !input.CallbackPresent {
				return state, append(effects, StatusHandlerEffect{Kind: StatusEffectInvalidInput})
			}
			state.InternalStatus = 0x17
			effects = append(effects, StatusHandlerEffect{Kind: StatusEffectWriteByte, Status: 0x17})
			if !state.Latch {
				state.Latch = true
				effects = append(effects, StatusHandlerEffect{Kind: StatusEffectSetLatch})
				if input.CallbackPresent {
					effects = append(effects, StatusHandlerEffect{Kind: StatusEffectCallback, Value: true})
				} else {
					effects = append(effects, StatusHandlerEffect{Kind: StatusEffectNoCallback})
				}
			} else {
				effects = append(effects, StatusHandlerEffect{Kind: StatusEffectNoLatch})
				effects = append(effects, StatusHandlerEffect{Kind: StatusEffectNoCallback, Value: true})
			}
			return state, effects
		default:
			return state, append(effects, StatusHandlerEffect{Kind: StatusEffectNoEffects})
		}

	case StatusHandlerDisconnect:
		effects := []StatusHandlerEffect{{Kind: StatusEffectWriteByte, AgentID: state.AgentID, OldStatus: state.AgentStatus, NewStatus: 0, Error: input.Error}}
		if state.HandlerPresent {
			effects = append(effects, StatusHandlerEffect{Kind: StatusEffectInvokeHandler, AgentID: state.AgentID, OldStatus: state.AgentStatus, NewStatus: 0, Error: input.Error})
		}
		effects = append(effects,
			StatusHandlerEffect{Kind: StatusEffectCancelOwner, AgentID: state.AgentID},
			StatusHandlerEffect{Kind: StatusEffectEnumeratePending, Count: len(state.Pending)},
			StatusHandlerEffect{Kind: StatusEffectFanoutPending, Count: len(state.Pending), ErrorDomain: "LocoAgent", ErrorCode: -1, ErrorUserInfoPresent: false, ResultPresent: false},
		)
		state.AgentStatus = 0
		return state, effects

	case StatusHandlerOwnerCapture:
		return state, []StatusHandlerEffect{
			{Kind: StatusEffectWeakSendCapture},
			{Kind: StatusEffectStrongTimeoutCapture},
			{Kind: StatusEffectSameReceiverCapture},
		}

	case StatusHandlerPendingResponse:
		effects := []StatusHandlerEffect{{Kind: StatusEffectLookupCompletion}}
		if input.CompletionID == "" || !input.ResultPresent {
			return state, append(effects, StatusHandlerEffect{Kind: StatusEffectInvalidInput})
		}
		if input.CompletionID != "" {
			if _, ok := state.Pending[input.CompletionID]; ok {
				delete(state.Pending, input.CompletionID)
				return state, append(effects,
					StatusHandlerEffect{Kind: StatusEffectRemoveCompletion, CompletionID: input.CompletionID},
					StatusHandlerEffect{Kind: StatusEffectInvokeCompletion, CompletionID: input.CompletionID, ResultPresent: input.ResultPresent},
				)
			}
		}
		if state.DefaultHandler {
			return state, append(effects, StatusHandlerEffect{Kind: StatusEffectRouteDefault, CompletionID: input.CompletionID, ResultPresent: input.ResultPresent})
		}
		return state, append(effects, StatusHandlerEffect{Kind: StatusEffectNoDefaultHandler})
	default:
		return state, nil
	}
}

func clonePending(input map[string]struct{}) map[string]struct{} {
	if input == nil {
		return nil
	}
	output := make(map[string]struct{}, len(input))
	for key := range input {
		output[key] = struct{}{}
	}
	return output
}
