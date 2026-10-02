package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tokenHelperVector struct {
	Name                         string   `json:"name"`
	Kind                         string   `json:"kind"`
	Current                      *int64   `json:"current"`
	Incoming                     *int64   `json:"incoming"`
	ExistingLossCheckPositive    bool     `json:"existingLossCheckPositive"`
	ExistingTokenEqualsLossCheck bool     `json:"existingTokenEqualsLossCheck"`
	NestedContext                string   `json:"nestedContext"`
	Expect                       []string `json:"expect"`
}

type tokenHelperVectors struct {
	Schema  string              `json:"schema"`
	Status  string              `json:"status"`
	Vectors []tokenHelperVector `json:"vectors"`
}

func TestTokenHelperUnresolvedVectorsSchema(t *testing.T) {
	path := filepath.Join("testdata", "token-helper-unresolved.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var got tokenHelperVectors
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "session-login-token-helper-v1" {
		t.Fatalf("schema = %q", got.Schema)
	}
	if got.Status != "RED contract vectors; no production reducer" {
		t.Fatalf("status = %q", got.Status)
	}
	if len(got.Vectors) < 8 {
		t.Fatalf("vector count = %d", len(got.Vectors))
	}
	seen := map[string]bool{}
	for _, v := range got.Vectors {
		if v.Name == "" || (v.Kind != "token" && v.Kind != "blind") || len(v.Expect) == 0 {
			t.Fatalf("incomplete vector: %#v", v)
		}
		if seen[v.Name] {
			t.Fatalf("duplicate vector %q", v.Name)
		}
		seen[v.Name] = true
	}
	for _, name := range []string{
		"strict token advance dispatches token write",
		"negative token to zero is typed assertion error",
		"strict blind advance dispatches blind write",
		"missing nested context exposes storage gap",
		"commit and restart durability remain unresolved",
	} {
		if !seen[name] {
			t.Fatalf("missing required vector %q", name)
		}
	}
}
