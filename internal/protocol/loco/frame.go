// Package loco implements the bounded, account-independent LOCO wire codec.
package loco

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	HeaderSize      = 22
	MethodFieldSize = 11
	DefaultMaxBody  = 16 << 20
	BodyTypeBSON    = uint8(0)
)

var (
	ErrInvalidMethod = errors.New("loco: invalid method")
	ErrBodyTooLarge  = errors.New("loco: body too large")
	ErrInvalidFrame  = errors.New("loco: invalid frame")
)

type Header struct {
	PacketID uint32
	Status   uint16
	Method   string
	BodyType uint8
	BodyLen  uint32
}

type Packet struct {
	Header Header
	Body   []byte
}

func (h Header) MarshalBinary() ([]byte, error) {
	method := []byte(h.Method)
	if len(method) == 0 || len(method) > MethodFieldSize || !utf8.Valid(method) {
		return nil, ErrInvalidMethod
	}
	for _, b := range method {
		if b == 0 || b > 0x7f {
			return nil, ErrInvalidMethod
		}
	}
	out := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(out[0:4], h.PacketID)
	binary.LittleEndian.PutUint16(out[4:6], h.Status)
	copy(out[6:17], method)
	out[17] = h.BodyType
	binary.LittleEndian.PutUint32(out[18:22], h.BodyLen)
	return out, nil
}

func ParseHeader(src []byte, maxBody uint32) (Header, error) {
	if len(src) < HeaderSize {
		return Header{}, ErrInvalidFrame
	}
	if maxBody == 0 {
		maxBody = DefaultMaxBody
	}
	methodEnd := 6
	for methodEnd < 17 && src[methodEnd] != 0 {
		if src[methodEnd] > 0x7f {
			return Header{}, ErrInvalidMethod
		}
		methodEnd++
	}
	if methodEnd == 6 {
		return Header{}, ErrInvalidMethod
	}
	for i := methodEnd; i < 17; i++ {
		if src[i] != 0 {
			return Header{}, ErrInvalidMethod
		}
	}
	bodyLen := binary.LittleEndian.Uint32(src[18:22])
	if bodyLen > maxBody {
		return Header{}, ErrBodyTooLarge
	}
	return Header{
		PacketID: binary.LittleEndian.Uint32(src[0:4]),
		Status:   binary.LittleEndian.Uint16(src[4:6]),
		Method:   string(src[6:methodEnd]),
		BodyType: src[17],
		BodyLen:  bodyLen,
	}, nil
}

func (p Packet) MarshalBinary(maxBody uint32) ([]byte, error) {
	if maxBody == 0 {
		maxBody = DefaultMaxBody
	}
	if uint64(len(p.Body)) > uint64(maxBody) {
		return nil, ErrBodyTooLarge
	}
	p.Header.BodyLen = uint32(len(p.Body))
	header, err := p.Header.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal header: %w", err)
	}
	out := make([]byte, 0, len(header)+len(p.Body))
	out = append(out, header...)
	out = append(out, p.Body...)
	return out, nil
}
