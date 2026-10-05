package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type downstreamTerminalContract struct {
	Status string                   `json:"status"`
	Cases  []downstreamTerminalCase `json:"cases"`
}

type downstreamTerminalCase struct {
	Name                   string   `json:"name"`
	Kind                   string   `json:"kind"`
	LoggedIn               bool     `json:"logged_in"`
	LoggingOut             bool     `json:"logging_out"`
	ReasonCode             int      `json:"reason_code"`
	ErrorMessagePresent    bool     `json:"error_message_present"`
	ErrorURLPresent        bool     `json:"error_url_present"`
	ErrorURLLabelPresent   bool     `json:"error_url_label_present"`
	MainWindowPresent      bool     `json:"main_window_present"`
	HasMoreTicketAddresses bool     `json:"has_more_ticket_addresses"`
	ExpectedEffects        []string `json:"expected_effects"`
}

func expectedDownstreamEffects(tc downstreamTerminalCase) []string {
	if tc.Kind == "changesvr" {
		effects := []string{"clear_carriage_address", "check_more_ticket_addresses"}
		if tc.HasMoreTicketAddresses {
			effects = append(effects, "move_ticket_address_cursor")
		}
		return append(effects, "send_logout", "disconnect_ticket_agent", "disconnect_carriage_agent")
	}
	if !tc.LoggedIn || tc.LoggingOut {
		return []string{}
	}
	effects := []string{"extract_reason", "extract_error_message", "extract_error_url", "extract_error_url_label"}
	if tc.ReasonCode == 1 || tc.ReasonCode == 10 {
		effects = append(effects, "derive_reset_true")
	} else {
		effects = append(effects, "derive_reset_false")
	}
	effects = append(effects, "logout_with_reset_database", "dispatch_main_queue")
	if !tc.ErrorMessagePresent {
		effects = append(effects, "localize_default_error_message")
	}
	if !tc.ErrorURLPresent {
		effects = append(effects, "localize_default_error_url")
	}
	if tc.ErrorURLPresent && tc.ErrorURLLabelPresent {
		effects = append(effects, "include_error_url_label")
	}
	effects = append(effects, "create_alert")
	if tc.MainWindowPresent {
		effects = append(effects, "begin_alert_sheet")
	} else {
		effects = append(effects, "run_alert_modal")
	}
	return effects
}

func TestDownstreamTerminalLifecycleFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-downstream-terminal-lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture downstreamTerminalContract
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-unexecuted-runtime" || len(fixture.Cases) < 10 {
		t.Fatalf("fixture header = %#v", fixture)
	}
	seen := map[string]bool{}
	for _, tc := range fixture.Cases {
		if tc.Name == "" || seen[tc.Name] || (tc.Kind != "kickout" && tc.Kind != "changesvr") {
			t.Fatalf("invalid fixture case %#v", tc)
		}
		seen[tc.Name] = true
		want := expectedDownstreamEffects(tc)
		if !reflect.DeepEqual(tc.ExpectedEffects, want) {
			t.Errorf("%s effects = %v, want input-derived %v", tc.Name, tc.ExpectedEffects, want)
		}
	}
}
