package booking

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type bootstrapFixture struct {
	GetConfRequests []struct {
		Name  string
		Input struct {
			MCCMNC string `json:"MCCMNC"`
			Model  string `json:"model"`
			OS     string `json:"os"`
			UserID int64  `json:"userId"`
		}
		Body json.RawMessage
	} `json:"getconfRequests"`
	CheckinRequests []struct {
		Name  string
		Input struct {
			UserID     int64  `json:"userId"`
			OS         string `json:"os"`
			NType      int32  `json:"ntype"`
			AppVer     string `json:"appVer"`
			MCCMNC     string `json:"MCCMNC"`
			Lang       string `json:"lang"`
			CountryISO string `json:"countryISO"`
			UseSub     bool   `json:"useSub"`
		}
		Body json.RawMessage
	} `json:"checkinRequests"`
	BookingTargets []struct {
		Name  string
		Body  json.RawMessage
		Hosts []string
		Ports []int
		Error bool
	} `json:"bookingTargets"`
	CheckinEndpoints []struct {
		Name  string
		Body  json.RawMessage
		Host  string
		Port  int
		Error bool
	} `json:"checkinEndpoints"`
}

func loadBootstrapFixture(t *testing.T) bootstrapFixture {
	t.Helper()
	data, err := os.ReadFile("../../../research/fixtures/booking/bootstrap.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture bootstrapFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func extJSONToBSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var doc bson.D
	if err := bson.UnmarshalExtJSON(raw, true, &doc); err != nil {
		t.Fatal(err)
	}
	body, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestGetConfRequestMatchesFixture(t *testing.T) {
	fixture := loadBootstrapFixture(t)
	if len(fixture.GetConfRequests) == 0 {
		t.Fatal("fixture has no GETCONF requests")
	}
	for _, tc := range fixture.GetConfRequests {
		got, err := (GetConfRequest{MCCMNC: tc.Input.MCCMNC, Model: tc.Input.Model, OS: tc.Input.OS, UserID: tc.Input.UserID}).MarshalBSON()
		if err != nil {
			t.Fatalf("%s: %v", tc.Name, err)
		}
		if want := extJSONToBSON(t, tc.Body); !bytes.Equal(got, want) {
			t.Errorf("%s: body = %v, want %v", tc.Name, bson.Raw(got), bson.Raw(want))
		}
	}
}

func TestCheckinRequestMatchesFixture(t *testing.T) {
	fixture := loadBootstrapFixture(t)
	if len(fixture.CheckinRequests) == 0 {
		t.Fatal("fixture has no CHECKIN requests")
	}
	for _, tc := range fixture.CheckinRequests {
		got, err := (CheckinRequest{
			UserID: tc.Input.UserID, OS: tc.Input.OS, NType: tc.Input.NType, AppVer: tc.Input.AppVer,
			MCCMNC: tc.Input.MCCMNC, Lang: tc.Input.Lang, CountryISO: tc.Input.CountryISO, UseSub: tc.Input.UseSub,
		}).MarshalBSON()
		if err != nil {
			t.Fatalf("%s: %v", tc.Name, err)
		}
		if want := extJSONToBSON(t, tc.Body); !bytes.Equal(got, want) {
			t.Errorf("%s: body = %v, want %v", tc.Name, bson.Raw(got), bson.Raw(want))
		}
	}
}

func TestDecodeBookingTargetsMatchesFixture(t *testing.T) {
	fixture := loadBootstrapFixture(t)
	if len(fixture.BookingTargets) == 0 {
		t.Fatal("fixture has no GETCONF replies")
	}
	for _, tc := range fixture.BookingTargets {
		hosts, ports, err := DecodeBookingTargets(extJSONToBSON(t, tc.Body))
		if tc.Error {
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("%s: error = %v, want ErrMalformed", tc.Name, err)
			}
			continue
		}
		if err != nil || !slices.Equal(hosts, tc.Hosts) || !slices.Equal(ports, tc.Ports) {
			t.Errorf("%s: hosts=%v ports=%v err=%v, want hosts=%v ports=%v", tc.Name, hosts, ports, err, tc.Hosts, tc.Ports)
		}
	}
}

func TestDecodeCheckinEndpointMatchesFixture(t *testing.T) {
	fixture := loadBootstrapFixture(t)
	if len(fixture.CheckinEndpoints) == 0 {
		t.Fatal("fixture has no CHECKIN replies")
	}
	for _, tc := range fixture.CheckinEndpoints {
		host, port, err := DecodeCheckinEndpoint(extJSONToBSON(t, tc.Body))
		if tc.Error {
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("%s: error = %v, want ErrMalformed", tc.Name, err)
			}
			continue
		}
		if err != nil || host != tc.Host || port != tc.Port {
			t.Errorf("%s: host=%q port=%d err=%v, want host=%q port=%d", tc.Name, host, port, err, tc.Host, tc.Port)
		}
	}
}

func TestDecodeForegroundPingInterval(t *testing.T) {
	getconf := func(wifi bson.D) []byte {
		body, err := bson.Marshal(bson.D{
			{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"ticket.example.test"}}}},
			{Key: "wifi", Value: wifi},
		})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	ports := bson.E{Key: "ports", Value: bson.A{int32(443)}}
	for _, tc := range []struct {
		name string
		body []byte
		want time.Duration
	}{
		// The observed 2026-10-11 GETCONF carries wifi.fgPingItv as int32 seconds.
		{name: "observed shape", body: getconf(bson.D{ports, {Key: "fgPingItv", Value: int32(180)}, {Key: "bgPingItv", Value: int32(600)}}), want: 180 * time.Second},
		{name: "server value", body: getconf(bson.D{ports, {Key: "fgPingItv", Value: int32(90)}}), want: 90 * time.Second},
		// The Mac client substitutes 180 seconds for values below 1.
		{name: "zero", body: getconf(bson.D{ports, {Key: "fgPingItv", Value: int32(0)}}), want: DefaultPingInterval},
		{name: "negative", body: getconf(bson.D{ports, {Key: "fgPingItv", Value: int32(-1)}}), want: DefaultPingInterval},
		// A missing scalar decodes to zero in the Mac model, which also falls back.
		{name: "absent", body: getconf(bson.D{ports}), want: DefaultPingInterval},
		{name: "not int32", body: getconf(bson.D{ports, {Key: "fgPingItv", Value: "180"}}), want: DefaultPingInterval},
		{name: "no wifi", body: mustMarshal(t, bson.D{{Key: "status", Value: int32(0)}}), want: DefaultPingInterval},
	} {
		if got := DecodeForegroundPingInterval(tc.body); got != tc.want {
			t.Errorf("%s: interval = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func mustMarshal(t *testing.T, doc bson.D) []byte {
	t.Helper()
	body, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
