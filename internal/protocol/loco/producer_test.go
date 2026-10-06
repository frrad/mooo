package loco

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProducerOrdersHeaderAndPacketCallbacksAcrossSupplies(t *testing.T) {
	first, _ := (Packet{Header: Header{PacketID: 1, Method: "ONE"}, Body: []byte("abc")}).MarshalBinary(64)
	second, _ := (Packet{Header: Header{PacketID: 2, Method: "TWO"}, Body: []byte("defg")}).MarshalBinary(64)
	p := NewProducer(64, 256)
	var events []string
	var packets []Packet
	for _, part := range [][]byte{first[:7], first[7:HeaderSize], first[HeaderSize:], second[:HeaderSize-1], second[HeaderSize-1:]} {
		err := p.Feed(part, func(h Header) { events = append(events, "H"+h.Method) }, func(packet Packet) {
			events = append(events, "P"+packet.Header.Method)
			packets = append(packets, packet)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	wantEvents := []string{"HONE", "PONE", "HTWO", "PTWO"}
	if len(events) != len(wantEvents) {
		t.Fatalf("events=%v want=%v", events, wantEvents)
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Fatalf("events=%v want=%v", events, wantEvents)
		}
	}
	if len(packets) != 2 || string(packets[0].Body) != "abc" || string(packets[1].Body) != "defg" {
		t.Fatalf("packets=%#v", packets)
	}
}

func TestProducerRejectsAggregateBufferAndFailsClosed(t *testing.T) {
	frame, _ := (Packet{Header: Header{PacketID: 1, Method: "ONE"}, Body: []byte("abc")}).MarshalBinary(64)
	p := NewProducer(64, HeaderSize+2)
	if err := p.Feed(frame[:HeaderSize-1], nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Feed(frame[HeaderSize-1:], nil, nil); !errors.Is(err, ErrBufferTooLarge) {
		t.Fatalf("err=%v want buffer limit", err)
	}
	if err := p.Feed(nil, nil, nil); !errors.Is(err, ErrProducerFailed) {
		t.Fatalf("failed producer resumed: %v", err)
	}
}

func TestProducerOnlyAuthenticatedPlaintextReachesCallbacks(t *testing.T) {
	key := bytes.Repeat([]byte{0x41}, V3KeySize)
	server, err := NewSecureV3WithKey(key, bytes.NewReader(bytes.Repeat([]byte{0x22}, 1024)), 0)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := NewSecureV3WithKey(key, bytes.NewReader(bytes.Repeat([]byte{0x33}, 1024)), 0)
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := (Packet{Header: Header{PacketID: 9, Method: "SECURE"}, Body: []byte("ok")}).MarshalBinary(64)
	envelope, err := server.Encrypt(frame)
	if err != nil {
		t.Fatal(err)
	}
	producer := NewProducer(64, 256)
	var headers, packets int
	plain, err := receiver.Decrypt(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.Feed(plain, func(Header) { headers++ }, func(Packet) { packets++ }); err != nil {
		t.Fatal(err)
	}
	if headers != 1 || packets != 1 {
		t.Fatalf("callbacks headers=%d packets=%d", headers, packets)
	}
	// Authentication failure must not expose any plaintext to the producer.
	envelope[len(envelope)-1] ^= 1
	if _, err := receiver.Decrypt(envelope); !errors.Is(err, ErrInvalidSecureEnvelope) {
		t.Fatalf("tampered envelope err=%v", err)
	}
}

func TestProducerRejectsMalformedHeaderBeforeHeaderCallback(t *testing.T) {
	p := NewProducer(64, 128)
	bad := bytes.Repeat([]byte{0}, HeaderSize)
	headers := 0
	if err := p.Feed(bad, func(Header) { headers++ }, nil); !errors.Is(err, ErrInvalidMethod) {
		t.Fatalf("err=%v want invalid method", err)
	}
	if headers != 0 {
		t.Fatalf("headers=%d after malformed input", headers)
	}
	if err := p.Feed(nil, nil, nil); !errors.Is(err, ErrProducerFailed) {
		t.Fatalf("failed producer resumed: %v", err)
	}
}

func TestProducerRejectsBodyLimitBeforeHeaderCallback(t *testing.T) {
	header, err := (Header{PacketID: 4, Method: "LIMIT", BodyLen: 65}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	p := NewProducer(64, 128)
	headers := 0
	if err := p.Feed(header, func(Header) { headers++ }, nil); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err=%v want body limit", err)
	}
	if headers != 0 {
		t.Fatalf("headers=%d after oversized body", headers)
	}
}

func TestProducerHandlesEmptyBodyAndRetainsPartialNextFrame(t *testing.T) {
	first, _ := (Packet{Header: Header{PacketID: 6, Method: "EMPTY"}}).MarshalBinary(64)
	second, _ := (Packet{Header: Header{PacketID: 7, Method: "NEXT"}, Body: []byte("x")}).MarshalBinary(64)
	p := NewProducer(64, 256)
	var events []string
	if err := p.Feed(append(first, second[:HeaderSize]...), func(h Header) { events = append(events, "H"+h.Method) }, func(packet Packet) { events = append(events, "P"+packet.Header.Method) }); err != nil {
		t.Fatal(err)
	}
	if want := []string{"HEMPTY", "PEMPTY", "HNEXT"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	var got Packet
	if err := p.Feed(second[HeaderSize:], nil, func(packet Packet) { got = packet }); err != nil {
		t.Fatal(err)
	}
	if got.Header.PacketID != 7 || string(got.Body) != "x" {
		t.Fatalf("packet=%#v", got)
	}
}

func TestProducerExecutesReviewedSecureFramingProducerVectors(t *testing.T) {
	type vectorCase struct {
		Name            string   `json:"name"`
		CryptoPresent   bool     `json:"crypto_present"`
		ReadTag         int      `json:"read_tag"`
		PrefixValue     *uint32  `json:"prefix_value"`
		Accumulated     int      `json:"accumulated_bytes"`
		BodyLength      int      `json:"body_length"`
		CurrentHeader   bool     `json:"current_header_present"`
		HeaderCapable   bool     `json:"delegate_supports_header"`
		PacketCapable   bool     `json:"delegate_supports_packet"`
		IdentityMatch   bool     `json:"timeout_identity_matches"`
		DelegateIsAgent bool     `json:"delegate_is_agent"`
		ExpectedBuffer  *int     `json:"expected_buffered_bytes"`
		ExpectedCurrent *bool    `json:"expected_current_header_present"`
		ExpectedNeeded  *int     `json:"expected_consumer_bytes_needed"`
		BodyLengths     []int    `json:"body_lengths"`
		Expected        []string `json:"expected"`
	}
	var fixture struct {
		Provenance json.RawMessage `json:"provenance"`
		Status     string          `json:"status"`
		Question   string          `json:"question"`
		Cases      []vectorCase    `json:"cases"`
	}
	file, err := os.Open(filepath.Join("..", "..", "..", "research", "fixtures", "reconnect", "rc-q5-secure-framing.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-unexecuted-runtime" || fixture.Question != "RC-Q5" || len(fixture.Cases) != 16 {
		t.Fatalf("fixture=%q question=%q cases=%d", fixture.Status, fixture.Question, len(fixture.Cases))
	}
	executed := 0
	for _, vector := range fixture.Cases {
		if vector.ReadTag != 1 || vector.CryptoPresent || (vector.Accumulated == 0 && vector.BodyLength == 0 && len(vector.BodyLengths) == 0) {
			continue
		}
		executed++
		lengths := vector.BodyLengths
		if len(lengths) == 0 {
			lengths = []int{vector.BodyLength}
		}
		frames := make([][]byte, len(lengths))
		stream := []byte(nil)
		for i, bodyLength := range lengths {
			frame, marshalErr := (Packet{Header: Header{PacketID: uint32(i + 1), Method: "VECTOR"}, Body: bytes.Repeat([]byte{'x'}, bodyLength)}).MarshalBinary(64)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			frames[i] = frame
			stream = append(stream, frame...)
		}
		if vector.Accumulated > len(stream) {
			t.Fatalf("vector=%q accumulated=%d stream=%d", vector.Name, vector.Accumulated, len(stream))
		}
		p := NewProducer(64, 256)
		if vector.CurrentHeader {
			if len(frames) == 0 || len(frames[0]) < HeaderSize {
				t.Fatalf("vector=%q has no seed header", vector.Name)
			}
			if err := p.Append(frames[0][:HeaderSize]); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := p.Next(nil); err != nil || ok {
				t.Fatalf("vector=%q seed ok=%v err=%v", vector.Name, ok, err)
			}
			if err := p.Append(stream[HeaderSize:vector.Accumulated]); err != nil {
				t.Fatal(err)
			}
		} else if err := p.Append(stream[:vector.Accumulated]); err != nil {
			t.Fatal(err)
		}
		events := []string{}
		for {
			onHeader := func(Header) { events = append(events, "header") }
			if !vector.HeaderCapable {
				onHeader = nil
			}
			packet, ok, nextErr := p.Next(onHeader)
			if nextErr != nil {
				t.Fatal(nextErr)
			}
			if !ok {
				break
			}
			if vector.PacketCapable {
				events = append(events, "packet")
			}
			_ = packet
		}
		want := []string{}
		for _, effect := range vector.Expected {
			switch effect {
			case "header_callback_if_supported":
				if vector.HeaderCapable {
					want = append(want, "header")
				}
			case "complete_packet_callback_after_body":
				if vector.PacketCapable {
					want = append(want, "packet")
				}
			}
		}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("vector=%q events=%v want=%v", vector.Name, events, want)
		}
		if vector.ExpectedBuffer != nil && p.Buffered() != *vector.ExpectedBuffer {
			t.Fatalf("vector=%q buffered=%d want=%d", vector.Name, p.Buffered(), *vector.ExpectedBuffer)
		}
		if vector.ExpectedCurrent != nil && (p.header != nil) != *vector.ExpectedCurrent {
			t.Fatalf("vector=%q current_header=%v want=%v", vector.Name, p.header != nil, *vector.ExpectedCurrent)
		}
	}
	if executed != 10 {
		t.Fatalf("executed producer cases=%d want=10", executed)
	}
}
