package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func loadPingStatusVectors() (pingStatusVectors, error) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-bin-003-status.json"))
	if err != nil {
		return pingStatusVectors{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var vectors pingStatusVectors
	if err := dec.Decode(&vectors); err != nil {
		return pingStatusVectors{}, err
	}
	return vectors, nil
}

func TestReducePingStatusMatchesApprovedVectors(t *testing.T) {
	body, err := loadPingStatusVectors()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range body.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			state := PingStatusState{
				AgentID:          tc.Input.CurrentAgent,
				InternalStatus:   tc.Input.InternalStatus,
				HandlerInstalled: tc.Input.HandlerInstalled,
				Latch:            tc.Input.Latch,
			}
			got, effects := ReducePingStatus(state, PingStatusNotice{
				CallbackAgent: tc.Input.CallbackAgent,
				Status:        tc.Input.Status,
			})
			gotEffects := make([]string, len(effects))
			for i, effect := range effects {
				gotEffects[i] = formatPingStatusEffect(effect)
			}
			if !reflect.DeepEqual(gotEffects, tc.WantEffects) {
				t.Fatalf("effects=%v want=%v", gotEffects, tc.WantEffects)
			}
			wantAgent := ""
			if tc.WantState.Agent != nil {
				wantAgent = *tc.WantState.Agent
			}
			want := PingStatusState{
				AgentID:          wantAgent,
				InternalStatus:   tc.WantState.InternalStatus,
				HandlerInstalled: tc.WantState.HandlerInstalled,
				Latch:            tc.WantState.Latch,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("state=%#v want=%#v", got, want)
			}
		})
	}
}

func formatPingStatusEffect(effect PingStatusEffect) string {
	switch effect.Kind {
	case PingStatusWriteStatus:
		return "write_status:" + strconv.FormatInt(int64(effect.Status), 10)
	case PingStatusSetLatch:
		return "set_latch:" + strconv.FormatBool(effect.Value)
	case PingStatusCallback:
		return "callback:" + strconv.FormatBool(effect.Value)
	case PingStatusClearAgent:
		return "clear_agent:" + effect.AgentID
	case PingStatusQueuePingCancel:
		return "queue_ping_cancel"
	case PingStatusClearStatusHandler:
		return "clear_status_handler:" + effect.AgentID
	default:
		return fmt.Sprintf("unknown:%d", effect.Kind)
	}
}
