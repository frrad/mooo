package loco

import "errors"

var (
	ErrProducerFailed = errors.New("loco: producer is in failed state")
	ErrBufferTooLarge = errors.New("loco: producer buffer too large")
)

// DefaultMaxBuffer bounds plaintext retained between producer supplies. It is
// deliberately separate from DefaultMaxBody: one authenticated supply may
// contain more than one complete frame, while an incomplete frame must still
// be bounded independently.
const DefaultMaxBuffer = 2 * (HeaderSize + DefaultMaxBody)

// Producer retains authenticated plaintext between supplies and delivers
// header and complete-packet callbacks in wire order. Callers must only pass
// plaintext after the transport's authentication/decryption step succeeds.
// A callback is never repeated for a header while its body is incomplete.
type Producer struct {
	maxBody   uint32
	maxBuffer uint32
	buffer    []byte
	header    *Header
	failed    bool
}

func NewProducer(maxBody, maxBuffer uint32) *Producer {
	if maxBody == 0 {
		maxBody = DefaultMaxBody
	}
	if maxBuffer == 0 {
		maxBuffer = DefaultMaxBuffer
	}
	return &Producer{maxBody: maxBody, maxBuffer: maxBuffer}
}

// Feed appends one authenticated plaintext supply. Header callbacks precede
// their corresponding packet callbacks, including when a supply contains
// several frames. Callback failures are intentionally outside this codec
// boundary; malformed framing permanently fails the producer.
func (p *Producer) Feed(plaintext []byte, onHeader func(Header), onPacket func(Packet)) error {
	if p == nil || p.failed {
		return ErrProducerFailed
	}
	if err := p.Append(plaintext); err != nil {
		return err
	}
	for {
		packet, ok, err := p.Next(onHeader)
		if err != nil || !ok {
			return err
		}
		if onPacket != nil {
			onPacket(packet)
		}
		if len(p.buffer) == 0 {
			return nil
		}
	}
}

// Append retains an authenticated plaintext supply without delivering a
// callback. Next can then expose exactly one frame at a time to a dispatcher.
func (p *Producer) Append(plaintext []byte) error {
	if p == nil || p.failed {
		return ErrProducerFailed
	}
	if uint64(len(p.buffer))+uint64(len(plaintext)) > uint64(p.maxBuffer) {
		p.failed = true
		return ErrBufferTooLarge
	}
	p.buffer = append(p.buffer, plaintext...)
	return nil
}

// Next delivers at most one complete packet. A header callback is delivered
// once when its header becomes available, even if the body arrives later.
func (p *Producer) Next(onHeader func(Header)) (Packet, bool, error) {
	if p == nil || p.failed {
		return Packet{}, false, ErrProducerFailed
	}
	if p.header == nil {
		if len(p.buffer) < HeaderSize {
			return Packet{}, false, nil
		}
		header, err := ParseHeader(p.buffer[:HeaderSize], p.maxBody)
		if err != nil {
			p.failed = true
			return Packet{}, false, err
		}
		p.header = &header
		if onHeader != nil {
			onHeader(header)
		}
	}
	frameLen := HeaderSize + int(p.header.BodyLen)
	if len(p.buffer) < frameLen {
		return Packet{}, false, nil
	}
	body := make([]byte, int(p.header.BodyLen))
	copy(body, p.buffer[HeaderSize:frameLen])
	packet := Packet{Header: *p.header, Body: body}
	p.buffer = p.buffer[frameLen:]
	p.header = nil
	if len(p.buffer) == 0 {
		p.buffer = nil
	}
	return packet, true, nil
}

func (p *Producer) Buffered() int {
	if p == nil {
		return 0
	}
	return len(p.buffer)
}
