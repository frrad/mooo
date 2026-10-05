package sessionlogin

import (
	"fmt"
	"sync"
)

// PushReceiptAgentQueue is the injected carriage-agent owner queue.
type PushReceiptAgentQueue interface{ Enqueue(func()) }

type PushReceiptAgentStatus interface{ Status() int8 }

// PushReceiptPacketAccessor exposes only the packet-ID identity needed after
// the execution-time status gate. Implementations may inspect packet headers;
// callers must not access packet data before the queued gate runs.
type PushReceiptPacketAccessor interface {
	PacketID(packet any) (uint32, error)
}

type PushReceiptSender interface {
	SendPushReceipt(packet any, tag int64)
}

// PushReceiptAgentOwner models sendPushReceipt:. It queues one block, rereads
// the carriage-agent status when that block executes, and only status 3 may
// inspect the packet ID or invoke the sender.
type PushReceiptAgentOwner struct {
	mu       sync.Mutex
	queue    PushReceiptAgentQueue
	status   PushReceiptAgentStatus
	accessor PushReceiptPacketAccessor
	sender   PushReceiptSender
}

func NewPushReceiptAgentOwner(queue PushReceiptAgentQueue, status PushReceiptAgentStatus, accessor PushReceiptPacketAccessor, sender PushReceiptSender) (*PushReceiptAgentOwner, error) {
	if queue == nil || status == nil || accessor == nil || sender == nil {
		return nil, fmt.Errorf("sessionlogin: incomplete push-receipt agent owner")
	}
	return &PushReceiptAgentOwner{queue: queue, status: status, accessor: accessor, sender: sender}, nil
}

func (o *PushReceiptAgentOwner) Send(packet any) error {
	if o == nil {
		return fmt.Errorf("sessionlogin: nil push-receipt agent owner")
	}
	o.mu.Lock()
	queue, status, accessor, sender := o.queue, o.status, o.accessor, o.sender
	o.mu.Unlock()
	if queue == nil || status == nil || accessor == nil || sender == nil {
		return fmt.Errorf("sessionlogin: incomplete push-receipt agent owner")
	}
	queue.Enqueue(func() {
		if status.Status() != 3 {
			return
		}
		packetID, err := accessor.PacketID(packet)
		if err != nil {
			return
		}
		sender.SendPushReceipt(packet, TagForPushReceiptPacketID(packetID))
	})
	return nil
}

// TagForPushReceiptPacketID preserves uint32 identity before signed negation.
func TagForPushReceiptPacketID(packetID uint32) int64 { return -int64(uint64(packetID)) }
