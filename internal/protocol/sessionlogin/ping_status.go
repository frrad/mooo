package sessionlogin

// PingStatusState is the bounded state projection used by the reviewed
// connection-status callback contract. AgentID and CallbackAgent are required
// non-empty synthetic identities; absent-owner behavior is outside this API.
type PingStatusState struct {
	AgentID          string
	InternalStatus   int32
	HandlerInstalled bool
	Latch            bool
}

// PingStatusNotice carries the callback identity and numeric status branch.
type PingStatusNotice struct {
	CallbackAgent string
	Status        int32
}

// PingStatusEffectKind identifies one ordered planned side effect.
type PingStatusEffectKind uint8

const (
	PingStatusWriteStatus PingStatusEffectKind = iota + 1
	PingStatusSetLatch
	PingStatusCallback
	PingStatusClearAgent
	PingStatusQueuePingCancel
	PingStatusClearStatusHandler
)

// PingStatusEffect is a planned effect; it does not touch a socket, timer, or
// external owner. AgentID identifies effects scoped to the callback agent.
type PingStatusEffect struct {
	Kind    PingStatusEffectKind
	AgentID string
	Status  int32
	Value   bool
}

// ReducePingStatus applies only the reviewed status 0 and 3 branches when the
// callback identity matches the current agent. Unknown or stale notices leave
// state and effects unchanged.
func ReducePingStatus(state PingStatusState, notice PingStatusNotice) (PingStatusState, []PingStatusEffect) {
	if state.AgentID == "" || notice.CallbackAgent == "" || state.AgentID != notice.CallbackAgent {
		return state, nil
	}
	switch notice.Status {
	case 3:
		state.InternalStatus = 0x17
		effects := []PingStatusEffect{{Kind: PingStatusWriteStatus, Status: 0x17}}
		if !state.Latch {
			state.Latch = true
			effects = append(effects,
				PingStatusEffect{Kind: PingStatusSetLatch, Value: true},
				PingStatusEffect{Kind: PingStatusCallback, Value: true},
			)
		}
		return state, effects
	case 0:
		priorLatch := state.Latch
		agentID := state.AgentID
		state.AgentID = ""
		if priorLatch {
			state.InternalStatus = 0x1a
		} else {
			state.InternalStatus = 0x16
		}
		effects := []PingStatusEffect{
			{Kind: PingStatusClearAgent, AgentID: agentID},
			{Kind: PingStatusWriteStatus, Status: state.InternalStatus},
		}
		if !priorLatch {
			effects = append(effects, PingStatusEffect{Kind: PingStatusCallback, Value: false})
		}
		effects = append(effects,
			PingStatusEffect{Kind: PingStatusQueuePingCancel},
			PingStatusEffect{Kind: PingStatusClearStatusHandler, AgentID: agentID},
		)
		state.HandlerInstalled = false
		return state, effects
	default:
		return state, nil
	}
}
