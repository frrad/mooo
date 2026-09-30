package notiread

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestRequestWireShape(t *testing.T) {
	body, err := (Request{
		ChatID: 42, LinkID: 7, Watermark: 99, NotiRead: true, ServiceID: 3,
	}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	want := map[string]bson.Type{
		"chatId":    bson.TypeInt64,
		"li":        bson.TypeInt64,
		"watermark": bson.TypeInt64,
		"notiRead":  bson.TypeBoolean,
		"serviceId": bson.TypeInt32,
	}
	for key, typ := range want {
		if got := raw.Lookup(key).Type; got != typ {
			t.Fatalf("%s type = %v, want %v", key, got, typ)
		}
	}
	if got := raw.Lookup("chatId").Int64(); got != 42 {
		t.Fatalf("chatId = %d, want 42", got)
	}
	if got := raw.Lookup("li").Int64(); got != 7 {
		t.Fatalf("li = %d, want 7", got)
	}
	if got := raw.Lookup("watermark").Int64(); got != 99 {
		t.Fatalf("watermark = %d, want 99", got)
	}
	if !raw.Lookup("notiRead").Boolean() || raw.Lookup("serviceId").Int32() != 3 {
		t.Fatal("NOTIREAD values were not preserved")
	}
	if elements, err := raw.Elements(); err != nil {
		t.Fatal(err)
	} else {
		keys := make([]string, 0, len(elements))
		for _, element := range elements {
			keys = append(keys, element.Key())
		}
		wantKeys := []string{"chatId", "li", "watermark", "notiRead", "serviceId"}
		if len(keys) != len(wantKeys) {
			t.Fatalf("keys = %v, want %v", keys, wantKeys)
		}
		for i := range wantKeys {
			if keys[i] != wantKeys[i] {
				t.Fatalf("key %d = %q, want %q", i, keys[i], wantKeys[i])
			}
		}
	}
}

func TestSendDoesNotRetryAmbiguousFailureOrReportReadSuccess(t *testing.T) {
	transport := &failingTransport{err: errors.New("synthetic disconnect")}
	request := Request{ChatID: 42, LinkID: 7, Watermark: 99, NotiRead: true, ServiceID: 3}
	_, err := Send(context.Background(), transport, request)
	if err == nil {
		t.Fatal("Send unexpectedly succeeded")
	}
	if transport.calls != 1 {
		t.Fatalf("transport calls = %d, want one", transport.calls)
	}
}

type failingTransport struct {
	err   error
	calls int
}

func (t *failingTransport) Request(context.Context, string, []byte) ([]byte, error) {
	t.calls++
	return nil, t.err
}
