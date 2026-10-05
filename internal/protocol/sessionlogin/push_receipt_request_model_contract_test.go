package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pushReceiptRequestModelFixture struct {
	Status string                        `json:"status"`
	Cases  []pushReceiptRequestModelCase `json:"cases"`
}
type pushReceiptRequestModelCase struct {
	Name                  string   `json:"name"`
	Handler               string   `json:"handler"`
	HeaderMethodPresent   bool     `json:"header_method_present"`
	HeaderPacketIDPresent bool     `json:"header_packet_id_present"`
	Revision              *int     `json:"revision"`
	PlusRevision          *int     `json:"plus_revision"`
	OwnerStatus           int      `json:"owner_status"`
	PacketID              uint32   `json:"packet_id"`
	ExpectedEffects       []string `json:"expected_effects"`
}

func expectedPushReceiptRequestModel(c pushReceiptRequestModelCase) []string {
	if c.Handler == "send" {
		e := []string{"queue_owner_block", "read_execution_status"}
		if c.OwnerStatus == 3 {
			e = append(e, "read_packet_header", "read_packet_id", "derive_tag_negation", "send_packet_tag")
		}
		return e
	}
	e := []string{"delegate_callback_attempt", "construct_from_header"}
	if c.Handler == "hint" {
		e = append(e, "copy_header_method", "copy_header_packet_id")
	} else {
		e = append(e, "set_revision", "set_plus_revision")
	}
	return append(e, "send_carriage_push_receipt")
}
func TestPushReceiptRequestModelFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-request-model.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f pushReceiptRequestModelFixture
	if err = json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 4 {
		t.Fatalf("fixture header = %#v", f)
	}
	for _, c := range f.Cases {
		if got, want := c.ExpectedEffects, expectedPushReceiptRequestModel(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", c.Name, got, want)
		}
	}
}
