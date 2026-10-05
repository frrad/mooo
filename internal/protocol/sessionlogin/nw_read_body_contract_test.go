package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type nwReadBodyFixture struct {
	Status string           `json:"status"`
	Cases  []nwReadBodyCase `json:"cases"`
}

type nwReadBodyCase struct {
	Name                        string   `json:"name"`
	OwnerPresent                bool     `json:"owner_present"`
	CompletionOwnerPresent      bool     `json:"completion_owner_present"`
	Connection                  bool     `json:"connection_present"`
	CurrentConnectionPresent    bool     `json:"current_connection_present"`
	Length                      uint64   `json:"length"`
	Completion                  string   `json:"completion"` // none, data, or error
	ErrorKind                   string   `json:"error_kind"` // posix or other
	ErrorCode                   int      `json:"error_code"`
	CurrentConnectionIdentity   string   `json:"current_connection_identity"`
	ExpectedReceiveMinimum      *uint64  `json:"expected_receive_minimum"`
	ExpectedReceiveMaximum      *uint64  `json:"expected_receive_maximum"`
	ExpectedCancelledConnection string   `json:"expected_cancelled_connection"`
	Expected                    []string `json:"expected"`
}

type nwReadBodyProjection struct {
	ReceiveMinimum      *uint64
	ReceiveMaximum      *uint64
	CancelledConnection string
	Effects             []string
}

func projectNWReadBody(c nwReadBodyCase) nwReadBodyProjection {
	if !c.OwnerPresent {
		return nwReadBodyProjection{Effects: []string{}}
	}
	minimum := uint64(1)
	effects := []string{}
	if c.Connection {
		effects = append(effects, "toggle_in_segment_timeout_true")
		if c.Length&(uint64(1)<<63) != 0 {
			return nwReadBodyProjection{Effects: effects}
		}
		effects = append(effects, "receive_minimum_length_1", "receive_maximum_length_input")
	}
	if !c.CompletionOwnerPresent {
		return nwReadBodyProjection{ReceiveMinimum: func() *uint64 {
			if c.Connection {
				return &minimum
			}
			return nil
		}(), ReceiveMaximum: func() *uint64 {
			if c.Connection {
				return &c.Length
			}
			return nil
		}(), Effects: effects}
	}
	switch c.Completion {
	case "data":
		return nwReadBodyProjection{ReceiveMinimum: func() *uint64 {
			if c.Connection {
				return &minimum
			}
			return nil
		}(), ReceiveMaximum: func() *uint64 {
			if c.Connection {
				return &c.Length
			}
			return nil
		}(), Effects: append(effects, "toggle_in_segment_timeout_false", "bridge_data_to_nsdata", "did_read_body", "read_header")}
	case "error":
		if c.ErrorKind == "posix" && c.ErrorCode == 89 {
			return nwReadBodyProjection{ReceiveMinimum: &minimum, ReceiveMaximum: &c.Length, Effects: effects}
		}
		effects = append(effects, "log_read_body_error")
		if c.CurrentConnectionPresent {
			effects = append(effects, "cancel_current_connection")
		}
		cancelled := ""
		if c.CurrentConnectionPresent {
			cancelled = c.CurrentConnectionIdentity
		}
		return nwReadBodyProjection{ReceiveMinimum: func() *uint64 {
			if c.Connection {
				return &minimum
			}
			return nil
		}(), ReceiveMaximum: func() *uint64 {
			if c.Connection {
				return &c.Length
			}
			return nil
		}(), CancelledConnection: cancelled, Effects: effects}
	default:
		return nwReadBodyProjection{ReceiveMinimum: func() *uint64 {
			if c.Connection {
				return &minimum
			}
			return nil
		}(), ReceiveMaximum: func() *uint64 {
			if c.Connection {
				return &c.Length
			}
			return nil
		}(), Effects: effects}
	}
}

func TestNWReadBodyContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-nw-read-body.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f nwReadBodyFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 9 {
		t.Fatalf("fixture header=%#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		p := projectNWReadBody(c)
		if !reflect.DeepEqual(p.Effects, c.Expected) || !reflect.DeepEqual(p.ReceiveMinimum, c.ExpectedReceiveMinimum) ||
			!reflect.DeepEqual(p.ReceiveMaximum, c.ExpectedReceiveMaximum) || p.CancelledConnection != c.ExpectedCancelledConnection {
			t.Errorf("%s projection=%#v want effects=%v min=%v max=%v cancelled=%q", c.Name, p, c.Expected, c.ExpectedReceiveMinimum, c.ExpectedReceiveMaximum, c.ExpectedCancelledConnection)
		}
	}
}
