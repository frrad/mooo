package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

type nwJSONObjectFixture struct {
	Status string             `json:"status"`
	Cases  []nwJSONObjectCase `json:"cases"`
}

type nwJSONObjectCase struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	MethodPresent bool              `json:"method_present"`
	Method        string            `json:"method,omitempty"`
	PacketID      uint32            `json:"packet_id"`
	Revision      int32             `json:"revision,omitempty"`
	PlusRevision  int32             `json:"plus_revision,omitempty"`
	Expected      map[string]string `json:"expected"`
}

func expectedJSONObject(c nwJSONObjectCase) map[string]string {
	got := map[string]string{"packetId": strconv.FormatUint(uint64(c.PacketID), 10)}
	if c.MethodPresent {
		got["method"] = c.Method
	}
	if c.Kind == "blocksync" {
		got["revision"] = strconv.FormatInt(int64(c.Revision), 10)
		got["plusRevision"] = strconv.FormatInt(int64(c.PlusRevision), 10)
	}
	return got
}

func TestNWReceiptJSONObjectFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-jsonobject.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f nwJSONObjectFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 4 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty %q", c.Name)
		}
		seen[c.Name] = true
		if c.Kind != "hint" && c.Kind != "blocksync" {
			t.Fatalf("%s kind=%q", c.Name, c.Kind)
		}
		if got, want := c.Expected, expectedJSONObject(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s dictionary=%v want %v", c.Name, got, want)
		}
	}
}
