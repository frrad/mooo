package loco

import "errors"

var ErrParserFailed = errors.New("loco: parser is in failed state")

// Parser accepts arbitrary plaintext transport chunks and emits complete packets.
// A malformed header permanently fails it rather than resuming at an untrusted
// byte boundary.
type Parser struct {
	maxBody uint32
	buffer  []byte
	failed  bool
}

func NewParser(maxBody uint32) *Parser {
	if maxBody == 0 {
		maxBody = DefaultMaxBody
	}
	return &Parser{maxBody: maxBody}
}

func (p *Parser) Feed(chunk []byte) ([]Packet, error) {
	if p == nil || p.failed {
		return nil, ErrParserFailed
	}
	if uint64(len(p.buffer))+uint64(len(chunk)) > uint64(HeaderSize)+uint64(p.maxBody) {
		p.failed = true
		return nil, ErrBodyTooLarge
	}
	p.buffer = append(p.buffer, chunk...)
	var packets []Packet
	for len(p.buffer) >= HeaderSize {
		header, err := ParseHeader(p.buffer[:HeaderSize], p.maxBody)
		if err != nil {
			p.failed = true
			return nil, err
		}
		frameLen := HeaderSize + int(header.BodyLen)
		if len(p.buffer) < frameLen {
			break
		}
		body := make([]byte, int(header.BodyLen))
		copy(body, p.buffer[HeaderSize:frameLen])
		packets = append(packets, Packet{Header: header, Body: body})
		p.buffer = p.buffer[frameLen:]
	}
	if len(p.buffer) == 0 {
		p.buffer = nil
	}
	return packets, nil
}

func (p *Parser) Buffered() int {
	if p == nil {
		return 0
	}
	return len(p.buffer)
}
