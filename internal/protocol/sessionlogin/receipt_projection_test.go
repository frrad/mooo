package sessionlogin

import (
	"errors"
	"math"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
)

func TestProjectIncomingReceiptBody(t *testing.T) {
	cases := []struct {
		name    string
		input   IncomingReceiptInput
		want    ReceiptBody
		wantErr error
	}{
		{
			name: "hint allocated empty dictionary",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: math.MaxUint32, Method: "HINT"},
				Method: "HINT",
				Body:   map[string]any{},
			},
			want: ReceiptBody{Kind: ReceiptBodyHint, PacketID: math.MaxUint32},
		},
		{
			name: "block sync missing revisions default zero",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 7, Method: "BLOCKSYNC"},
				Method: "BLOCKSYNC",
				Body:   map[string]any{},
			},
			want: ReceiptBody{Kind: ReceiptBodyBlockSync, PacketID: 7},
		},
		{
			name: "block sync signed revisions",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 8, Method: "BLOCKSYNC"},
				Method: "BLOCKSYNC",
				Body: map[string]any{
					"r":  int32(math.MinInt32),
					"pr": int32(math.MaxInt32),
				},
			},
			want: ReceiptBody{Kind: ReceiptBodyBlockSync, PacketID: 8, Revision: math.MinInt32, PlusRevision: math.MaxInt32},
		},
		{
			name: "null source preserves existing destination",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 9, Method: "BLOCKSYNC"},
				Method: "BLOCKSYNC",
				Body: map[string]any{
					"revision":     int32(11),
					"plusRevision": int32(12),
					"r":            SGJSONNull{},
					"pr":           SGJSONNull{},
				},
			},
			want: ReceiptBody{Kind: ReceiptBodyBlockSync, PacketID: 9, Revision: 11, PlusRevision: 12},
		},
		{
			name: "source overrides existing destination",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 10, Method: "BLOCKSYNC"},
				Method: "BLOCKSYNC",
				Body: map[string]any{
					"revision": int32(11),
					"r":        int32(13),
				},
			},
			want: ReceiptBody{Kind: ReceiptBodyBlockSync, PacketID: 10, Revision: 13},
		},
		{
			name: "nil body is distinct from empty dictionary",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 11, Method: "HINT"},
				Method: "HINT",
			},
			wantErr: ErrReceiptNilBody,
		},
		{
			name: "unknown method is not eligible",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 12, Method: "MSG"},
				Method: "MSG",
				Body:   map[string]any{},
			},
			wantErr: ErrReceiptNotEligible,
		},
		{
			name: "header method mismatch",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 13, Method: "HINT"},
				Method: "BLOCKSYNC",
				Body:   map[string]any{},
			},
			wantErr: ErrReceiptMethodMismatch,
		},
		{
			name: "int64 low word wraps to signed int32",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 14, Method: "BLOCKSYNC"},
				Method: "BLOCKSYNC",
				Body:   map[string]any{"r": int64(2147483648)},
			},
			want: ReceiptBody{Kind: ReceiptBodyBlockSync, PacketID: 14, Revision: math.MinInt32},
		},
		{
			name: "unsupported scalar remains explicit",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 15, Method: "BLOCKSYNC"},
				Method: "BLOCKSYNC",
				Body:   map[string]any{"r": float64(14)},
			},
			wantErr: ErrReceiptFieldType,
		},
		{
			name: "source replaces invalid stale destination",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 16, Method: "BLOCKSYNC"},
				Method: "BLOCKSYNC",
				Body: map[string]any{
					"revision": []byte{1},
					"r":        int32(17),
				},
			},
			want: ReceiptBody{Kind: ReceiptBodyBlockSync, PacketID: 16, Revision: 17},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProjectIncomingReceiptBody(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error=%v, want errors.Is(..., %v)", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ProjectIncomingReceiptBody() error=%v", err)
			}
			if got != tc.want {
				t.Fatalf("body=%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestProjectIncomingReceiptBodyRejectsNilHeaderMethodWithoutTransportEffects(t *testing.T) {
	_, err := ProjectIncomingReceiptBody(IncomingReceiptInput{
		Header: loco.Header{PacketID: 15},
		Method: "HINT",
		Body:   map[string]any{},
	})
	if !errors.Is(err, ErrReceiptMethodMismatch) {
		t.Fatalf("error=%v, want header mismatch", err)
	}
}

func TestProjectIncomingReceiptBodyPreservesHeaderIdentityThroughBuilder(t *testing.T) {
	input := IncomingReceiptInput{
		Header: loco.Header{PacketID: ^uint32(0), Method: "HINT"},
		Method: "HINT",
		Body:   map[string]any{},
	}
	body, err := ProjectIncomingReceiptBody(input)
	if err != nil {
		t.Fatalf("projection error=%v", err)
	}
	wire, err := BuildReceiptPacket(ReceiptPacket{
		PacketID: input.Header.PacketID,
		Method:   input.Method,
		Body:     body,
	}, 64)
	if err != nil {
		t.Fatalf("packet build error=%v", err)
	}
	header, err := loco.ParseHeader(wire, 64)
	if err != nil {
		t.Fatalf("header parse error=%v", err)
	}
	if header.PacketID != input.Header.PacketID || header.Method != input.Method {
		t.Fatalf("header=%+v, want packetID=%d method=%q", header, input.Header.PacketID, input.Method)
	}
	if len(wire) != loco.HeaderSize+5 {
		t.Fatalf("wire length=%d, want empty BSON frame length %d", len(wire), loco.HeaderSize+5)
	}
}

func TestReceiptInt32AcceptsApprovedInt64LowWordDomain(t *testing.T) {
	cases := []struct {
		name string
		in   int64
		want int32
	}{
		{name: "int32 minimum", in: -2147483648, want: -2147483648},
		{name: "int32 maximum", in: 2147483647, want: 2147483647},
		{name: "one above int32 maximum", in: 2147483648, want: -2147483648},
		{name: "one below int32 minimum", in: -2147483649, want: 2147483647},
		{name: "two to the thirty second", in: 4294967296, want: 0},
		{name: "int64 maximum", in: 9223372036854775807, want: -1},
		{name: "int64 minimum", in: -9223372036854775808, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := receiptInt32(tc.in, "revision")
			if err != nil {
				t.Fatalf("receiptInt32() error=%v", err)
			}
			if got != tc.want {
				t.Fatalf("receiptInt32(%d)=%d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
