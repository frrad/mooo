package sessionlogin

import (
	"reflect"
	"testing"
	"time"
)

type compositionQueue struct {
	label  string
	work   []func()
	events *[]string
}

func (q *compositionQueue) Enqueue(fn func()) {
	*q.events = append(*q.events, q.label+".enqueue")
	q.work = append(q.work, fn)
}

type compositionScheduler struct {
	cancels   []string
	schedules []compositionSchedule
}

type compositionSchedule struct {
	target, selector string
	object           any
	delay            time.Duration
}

func (s *compositionScheduler) Cancel(target, selector string, object any) {
	s.cancels = append(s.cancels, target+"|"+selector+"|"+formatNil(object))
}

func (s *compositionScheduler) Schedule(target, selector string, object any, delay time.Duration) {
	s.schedules = append(s.schedules, compositionSchedule{target: target, selector: selector, object: object, delay: delay})
}

func formatNil(value any) string {
	if value == nil {
		return "nil"
	}
	return "value"
}

type compositionStatus struct{ value int64 }

func (s *compositionStatus) Status() int64 { return s.value }

type compositionPacket struct{ id uint32 }

type compositionAccessor struct{ calls int }

func (a *compositionAccessor) PacketID(packet any) (uint32, error) {
	a.calls++
	return packet.(compositionPacket).id, nil
}

type compositionSender struct {
	events *[]string
	tags   []int64
}

func (s *compositionSender) SendPushReceipt(_ any, tag int64) {
	*s.events = append(*s.events, "agent.send")
	s.tags = append(s.tags, tag)
}

func TestPushReceiptOwnersComposeWithIndependentQueuesAndExecutionGates(t *testing.T) {
	events := []string{}
	managerQueue := &compositionQueue{label: "manager", events: &events}
	agentQueue := &compositionQueue{label: "agent", events: &events}
	managerScheduler := &compositionScheduler{}
	managerConfig := &pushReceiptOwnerConfig{interval: 12 * time.Second}
	agentStatus := &compositionStatus{value: 2}
	agentAccessor := &compositionAccessor{}
	agentSender := &compositionSender{events: &events}
	agentOwner, err := NewPushReceiptAgentOwner(agentQueue, agentStatus, agentAccessor, agentSender)
	if err != nil {
		t.Fatal(err)
	}
	agent := "agent-1"
	managerOwner, err := NewPushReceiptOwner("manager-1", func() string { return agent }, managerQueue, managerScheduler, managerConfig, func(target string, packet any) {
		if target != agent {
			t.Fatalf("inline agent target=%q, want %q", target, agent)
		}
		events = append(events, "manager.inline")
		if err := agentOwner.Send(packet); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := managerOwner.Send(compositionPacket{id: 17}); err != nil {
		t.Fatal(err)
	}
	wantAdmission := []string{"manager.enqueue", "manager.inline", "agent.enqueue", "manager.enqueue"}
	if !reflect.DeepEqual(events, wantAdmission) {
		t.Fatalf("admission events=%v, want %v", events, wantAdmission)
	}
	if agentStatus.value != 2 || agentAccessor.calls != 0 || len(agentSender.tags) != 0 {
		t.Fatalf("agent work ran before queue execution: status=%d accessor=%d tags=%v", agentStatus.value, agentAccessor.calls, agentSender.tags)
	}
	managerQueue.work[0]()
	managerQueue.work = managerQueue.work[1:]
	managerConfig.interval = -500 * time.Millisecond
	managerQueue.work[0]()
	managerQueue.work = managerQueue.work[1:]
	if len(managerScheduler.cancels) != 1 || managerScheduler.cancels[0] != "manager-1|sendPingRequest:|nil" {
		t.Fatalf("manager cancel=%v", managerScheduler.cancels)
	}
	if len(managerScheduler.schedules) != 1 || managerScheduler.schedules[0].delay != -500*time.Millisecond {
		t.Fatalf("manager schedule=%v", managerScheduler.schedules)
	}
	agentQueue.work[0]()
	agentQueue.work = agentQueue.work[1:]
	if agentAccessor.calls != 0 || len(agentSender.tags) != 0 {
		t.Fatalf("status-2 agent work sent: accessor=%d tags=%v", agentAccessor.calls, agentSender.tags)
	}

	agent = "agent-2"
	agentStatus.value = 3
	if err := managerOwner.Send(compositionPacket{id: ^uint32(0)}); err != nil {
		t.Fatal(err)
	}
	managerQueue.work[0]()
	managerQueue.work = managerQueue.work[1:]
	managerQueue.work[0]()
	managerQueue.work = managerQueue.work[1:]
	agentQueue.work[0]()
	if !reflect.DeepEqual(agentSender.tags, []int64{-4294967295}) {
		t.Fatalf("agent tags=%v", agentSender.tags)
	}
	if len(managerScheduler.cancels) != 2 || len(managerScheduler.schedules) != 2 || managerScheduler.cancels[1] != "manager-1|sendPingRequest:|nil" {
		t.Fatalf("manager identity drifted: cancels=%v schedules=%v", managerScheduler.cancels, managerScheduler.schedules)
	}
}
