package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type agentReceiptQueue struct {
	work   []func()
	events []string
}

func (q *agentReceiptQueue) Enqueue(fn func()) {
	q.events = append(q.events, "enqueue")
	q.work = append(q.work, fn)
}

type agentReceiptStatus struct{ value int8 }

func (s *agentReceiptStatus) Status() int8 { return s.value }

type agentReceiptPacket struct{ ID uint32 }
type agentReceiptAccessor struct{ calls int }

func (a *agentReceiptAccessor) PacketID(packet any) (uint32, error) {
	a.calls++
	return packet.(agentReceiptPacket).ID, nil
}

type agentReceiptSender struct {
	events []string
	tags   []int64
}

func (s *agentReceiptSender) SendPushReceipt(_ any, tag int64) {
	s.events = append(s.events, "send")
	s.tags = append(s.tags, tag)
}

func TestPushReceiptAgentOwnerConsumesApprovedFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			ExecutionStatus int    `json:"execution_status"`
			PacketID        uint32 `json:"packet_id"`
			ExpectedTag     int64  `json:"expected_tag"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		q := &agentReceiptQueue{}
		status := &agentReceiptStatus{value: int8(tc.ExecutionStatus)}
		accessor := &agentReceiptAccessor{}
		sender := &agentReceiptSender{}
		owner, err := NewPushReceiptAgentOwner(q, status, accessor, sender)
		if err != nil {
			t.Fatal(err)
		}
		if err := owner.Send(agentReceiptPacket{ID: tc.PacketID}); err != nil {
			t.Fatal(err)
		}
		if len(q.work) != 1 || len(q.events) != 1 || accessor.calls != 0 || len(sender.tags) != 0 {
			t.Fatalf("before execution status=%d queue=%d events=%v accessor=%d sends=%v", tc.ExecutionStatus, len(q.work), q.events, accessor.calls, sender.tags)
		}
		q.work[0]()
		if tc.ExecutionStatus == 3 {
			if accessor.calls != 1 || len(sender.tags) != 1 || sender.tags[0] != tc.ExpectedTag {
				t.Fatalf("ready status=%d id=%d accessor=%d tags=%v want %d", tc.ExecutionStatus, tc.PacketID, accessor.calls, sender.tags, tc.ExpectedTag)
			}
		} else if accessor.calls != 0 || len(sender.tags) != 0 {
			t.Fatalf("blocked status=%d accessed packet or sent: accessor=%d tags=%v", tc.ExecutionStatus, accessor.calls, sender.tags)
		}
	}
}

func TestPushReceiptAgentOwnerUsesLiteralBoundaryTags(t *testing.T) {
	if got := TagForPushReceiptPacketID(17); got != -17 {
		t.Fatalf("tag(17)=%d", got)
	}
	if got := TagForPushReceiptPacketID(^uint32(0)); got != -4294967295 {
		t.Fatalf("tag(max)=%d", got)
	}
}

func TestPushReceiptAgentOwnerQueuesBeforeStatusOrSend(t *testing.T) {
	q := &agentReceiptQueue{}
	status := &agentReceiptStatus{value: 3}
	accessor := &agentReceiptAccessor{}
	sender := &agentReceiptSender{}
	owner, err := NewPushReceiptAgentOwner(q, status, accessor, sender)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Send(agentReceiptPacket{ID: 17}); err != nil {
		t.Fatal(err)
	}
	if accessor.calls != 0 || len(sender.events) != 0 || len(q.events) != 1 {
		t.Fatalf("work executed before queue: queue=%v accessor=%d sends=%v", q.events, accessor.calls, sender.events)
	}
	q.work[0]()
	if accessor.calls != 1 || len(sender.events) != 1 {
		t.Fatalf("queued work did not send: accessor=%d sends=%v", accessor.calls, sender.events)
	}
}
