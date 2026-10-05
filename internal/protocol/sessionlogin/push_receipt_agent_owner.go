package sessionlogin

import (
	"context"
	"fmt"
	"sync"
)

// PushReceiptAgentQueue is the injected carriage-agent owner queue.
type PushReceiptAgentQueue interface{ Enqueue(func()) }

// PushReceiptAgentStatus uses a wide signed value so the owner does not narrow
// the source status field before applying the exact status-3 gate.
type PushReceiptAgentStatus interface{ Status() int64 }

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
// inspect the packet ID or invoke the sender. Accessor failures are suppressed
// by this clean-room owner because the reviewed source chain does not specify
// downstream error propagation.
type PushReceiptAgentOwner struct {
	mu          sync.Mutex
	queue       PushReceiptAgentQueue
	status      PushReceiptAgentStatus
	accessor    PushReceiptPacketAccessor
	sender      PushReceiptSender
	closed      bool
	generation  uint64
	activeCount int
	activeDone  chan struct{}
}

func NewPushReceiptAgentOwner(queue PushReceiptAgentQueue, status PushReceiptAgentStatus, accessor PushReceiptPacketAccessor, sender PushReceiptSender) (*PushReceiptAgentOwner, error) {
	if queue == nil || status == nil || accessor == nil || sender == nil {
		return nil, fmt.Errorf("sessionlogin: incomplete push-receipt agent owner")
	}
	done := make(chan struct{})
	close(done)
	return &PushReceiptAgentOwner{queue: queue, status: status, accessor: accessor, sender: sender, activeDone: done}, nil
}

func (o *PushReceiptAgentOwner) Send(packet any) error {
	if o == nil {
		return fmt.Errorf("sessionlogin: nil push-receipt agent owner")
	}
	o.mu.Lock()
	queue, status, accessor, sender := o.queue, o.status, o.accessor, o.sender
	generation, closed := o.generation, o.closed
	o.mu.Unlock()
	if queue == nil || status == nil || accessor == nil || sender == nil {
		return fmt.Errorf("sessionlogin: incomplete push-receipt agent owner")
	}
	if closed {
		return nil
	}
	queue.Enqueue(func() {
		if !o.begin(generation) {
			return
		}
		defer o.end()
		if status.Status() != 3 {
			return
		}
		packetID, err := accessor.PacketID(packet)
		if err != nil {
			return
		}
		if !o.active(generation) {
			return
		}
		sender.SendPushReceipt(packet, TagForPushReceiptPacketID(packetID))
	})
	return nil
}

func (o *PushReceiptAgentOwner) active(generation uint64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return !o.closed && o.generation == generation
}

func (o *PushReceiptAgentOwner) begin(generation uint64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.generation != generation {
		return false
	}
	if o.activeCount == 0 {
		o.activeDone = make(chan struct{})
	}
	o.activeCount++
	return true
}

func (o *PushReceiptAgentOwner) end() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.activeCount--
	if o.activeCount == 0 {
		close(o.activeDone)
	}
}

// Close invalidates queued status checks and future receipt sends.
func (o *PushReceiptAgentOwner) Close() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.closed = true
	o.generation++
	done := o.activeDone
	o.mu.Unlock()
	// Close is called by Session's interrupt-only closer goroutine. Waiting
	// here keeps the downstream callback inside the receipt owner's join
	// boundary while remaining safe for callbacks that reenter Session.Close.
	<-done
}

// Wait joins callbacks admitted before Close. Queued work rejected by Close
// never enters the active set and therefore needs no external drain.
func (o *PushReceiptAgentOwner) Wait(ctx context.Context) error {
	if o == nil || ctx == nil {
		return context.Canceled
	}
	o.mu.Lock()
	done := o.activeDone
	o.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TagForPushReceiptPacketID preserves uint32 identity before signed negation.
func TagForPushReceiptPacketID(packetID uint32) int64 { return -int64(uint64(packetID)) }
