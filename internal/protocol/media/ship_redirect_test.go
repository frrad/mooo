package media

import (
	"encoding/json"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeObservedShipRedirect(t *testing.T) {
	body, err := os.ReadFile("../../../research/fixtures/photo/observed-ship-redirect.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Response struct {
			Key      string `json:"k"`
			Host     string `json:"vh"`
			Port     int32  `json:"p"`
			Redirect bool   `json:"rd"`
			Status   int32  `json:"status"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	r := fixture.Response
	wire, err := bson.Marshal(bson.D{{Key: "k", Value: r.Key}, {Key: "vh", Value: r.Host}, {Key: "p", Value: r.Port}, {Key: "rd", Value: r.Redirect}, {Key: "status", Value: r.Status}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeShipResponse(wire)
	if err != nil || !decoded.Redirect || decoded.Key != "" || decoded.Host != "" || decoded.Port != 0 {
		t.Fatalf("observed redirect decoded as %+v: %v", decoded, err)
	}
}

func TestShipRedirectRequiresBooleanAndPreservesLegacyEndpointChecks(t *testing.T) {
	for _, value := range []any{int32(1), "true", nil} {
		body, err := bson.Marshal(bson.D{{Key: "rd", Value: value}, {Key: "k", Value: "token"}, {Key: "vh", Value: "media.invalid"}, {Key: "p", Value: int32(443)}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeShipResponse(body); err == nil {
			t.Fatalf("accepted rd of type %T", value)
		}
	}
	for _, rd := range []bson.D{nil, {{Key: "rd", Value: false}}} {
		body, err := bson.Marshal(append(rd, bson.E{Key: "k", Value: "token"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(443)}))
		if err != nil {
			t.Fatal(err)
		}
		r, err := DecodeShipResponse(body)
		if err != nil || r.Redirect || r.Key != "token" || r.Host != "media.invalid" || r.Port != 443 {
			t.Fatalf("legacy response %+v: %v", r, err)
		}
	}
	body, _ := bson.Marshal(bson.D{{Key: "rd", Value: false}, {Key: "k", Value: ""}, {Key: "vh", Value: ""}, {Key: "p", Value: int32(0)}})
	if _, err := DecodeShipResponse(body); err == nil {
		t.Fatal("accepted empty endpoint without redirect")
	}
}
