package sessionlogin

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pingIntentVectors struct {
	Evidence []string `json:"evidence"`
	Status   string   `json:"status"`
	Cases    []struct {
		Name            string   `json:"name"`
		Kind            string   `json:"kind"`
		Accepted        bool     `json:"accepted"`
		CallbackPresent bool     `json:"callback_present"`
		Want            []string `json:"want"`
	} `json:"cases"`
}

func TestPingIntentVectors(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q4-q5-lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var vectors pingIntentVectors
	if err := dec.Decode(&vectors); err != nil {
		t.Fatal(err)
	}
	if vectors.Status != "executable-intents-only" {
		t.Fatalf("lifecycle vectors status=%q want executable-intents-only", vectors.Status)
	}
	if len(vectors.Evidence) == 0 || vectors.Evidence[0] != "RC-BIN-003" || len(vectors.Cases) == 0 {
		t.Fatal("lifecycle vectors missing evidence or cases")
	}
	for _, tc := range vectors.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var got []PingIntent
			switch tc.Kind {
			case "request-entered":
				got = PlanPingRequestEntered(tc.Accepted, tc.CallbackPresent, nil, nil)
			case "completion":
				got = PlanPingCompletion(tc.CallbackPresent, nil, nil)
			case "without-completion":
				got = PlanPingWithoutCompletion()
			default:
				t.Fatalf("unsupported lifecycle kind %q", tc.Kind)
			}
			kinds := make([]string, len(got))
			for i, intent := range got {
				kinds[i] = string(intent.Kind)
			}
			if !reflect.DeepEqual(kinds, tc.Want) {
				t.Fatalf("intent kinds=%v want=%v", kinds, tc.Want)
			}
		})
	}
}

func TestPingCompletionPreservesOpaqueResponseAndError(t *testing.T) {
	response := struct{ Marker string }{Marker: "response"}
	errValue := errors.New("synthetic transport error")
	got := PlanPingCompletion(true, response, errValue)
	if len(got) != 2 || got[1].Kind != PingIntentForwardCompletion || !reflect.DeepEqual(got[1].Response, response) || got[1].Err != errValue {
		t.Fatalf("completion intent=%#v", got)
	}
}

func TestPingForwardingPreservesOpaqueValuesAcrossCallbackGates(t *testing.T) {
	response := struct{ Marker string }{Marker: "response"}
	errValue := errors.New("synthetic transport error")
	cases := []struct {
		name         string
		plan         func() []PingIntent
		wantKinds    []PingIntentKind
		wantResponse any
		wantErr      error
		forwardIndex int
	}{
		{
			name:         "completion response only",
			plan:         func() []PingIntent { return PlanPingCompletion(true, response, nil) },
			wantKinds:    []PingIntentKind{PingIntentQueueSchedule, PingIntentForwardCompletion},
			wantResponse: response,
			forwardIndex: 1,
		},
		{
			name:         "completion error only",
			plan:         func() []PingIntent { return PlanPingCompletion(true, nil, errValue) },
			wantKinds:    []PingIntentKind{PingIntentQueueSchedule, PingIntentForwardCompletion},
			wantErr:      errValue,
			forwardIndex: 1,
		},
		{
			name:         "rejection forwards opaque error without rearm",
			plan:         func() []PingIntent { return PlanPingRequestEntered(false, true, nil, errValue) },
			wantKinds:    []PingIntentKind{PingIntentQueueCancel, PingIntentForwardCompletion},
			wantErr:      errValue,
			forwardIndex: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.plan()
			if len(got) != len(tc.wantKinds) {
				t.Fatalf("intent count=%d want=%d: %#v", len(got), len(tc.wantKinds), got)
			}
			for i, want := range tc.wantKinds {
				if got[i].Kind != want {
					t.Fatalf("intent[%d]=%q want %q", i, got[i].Kind, want)
				}
			}
			forwarded := got[tc.forwardIndex]
			if !reflect.DeepEqual(forwarded.Response, tc.wantResponse) || forwarded.Err != tc.wantErr {
				t.Fatalf("forwarded intent=%#v want response=%#v error=%v", forwarded, tc.wantResponse, tc.wantErr)
			}
		})
	}
}
