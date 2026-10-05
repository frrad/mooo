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
					"revision":     int32(math.MinInt32),
					"plusRevision": int32(math.MaxInt32),
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
					"r":            int32(11),
					"pr":           int32(12),
					"revision":     SGJSONNull{},
					"plusRevision": SGJSONNull{},
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
					"r":        int32(11),
					"revision": int32(13),
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
			name: "unsupported numeric coercion remains dependency",
			input: IncomingReceiptInput{
				Header: loco.Header{PacketID: 14, Method: "BLOCKSYNC"},
				Method: "BLOCKSYNC",
				Body:   map[string]any{"revision": int64(14)},
			},
			wantErr: ErrReceiptFieldType,
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
