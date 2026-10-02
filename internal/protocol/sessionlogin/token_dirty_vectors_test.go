package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type tokenDirtyVector struct {
	Name           string   `json:"name"`
	Initialization string   `json:"initialization"`
	Field          string   `json:"field"`
	Old            *int64   `json:"old"`
	Incoming       *int64   `json:"incoming"`
	Expect         []string `json:"expect"`
	Execution      string   `json:"execution"`
}

type tokenDirtyVectors struct {
	Schema  string             `json:"schema"`
	Status  string             `json:"status"`
	Vectors []tokenDirtyVector `json:"vectors"`
}

// This is deliberately a schema and evidence test. It does not claim that the
// unresolved durable effects are implemented by the current protocol package.
func TestTokenDirtyVectorsSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "token-dirty-unresolved.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got tokenDirtyVectors
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "session-login-token-dirty-v1" {
		t.Fatalf("schema = %q", got.Schema)
	}
	if got.Status != "observed model dirty path; durable consumer unresolved" {
		t.Fatalf("status = %q", got.Status)
	}
	if len(got.Vectors) != 6 {
		t.Fatalf("vector count = %d", len(got.Vectors))
	}
	seen := make(map[string]bool, len(got.Vectors))
	for _, vector := range got.Vectors {
		if vector.Name == "" || vector.Field == "" || len(vector.Expect) == 0 {
			t.Fatalf("incomplete vector: %#v", vector)
		}
		if vector.Initialization != "non_database" && vector.Initialization != "database" {
			t.Fatalf("%q: initialization = %q", vector.Name, vector.Initialization)
		}
		if vector.Execution != "observed" && vector.Execution != "observed_generic" && vector.Execution != "unresolved" {
			t.Fatalf("%q: execution = %q", vector.Name, vector.Execution)
		}
		if seen[vector.Name] {
			t.Fatalf("duplicate vector %q", vector.Name)
		}
		seen[vector.Name] = true
	}
	for _, name := range []string{
		"new model initialization registers changed object",
		"observer event with changed blind token records delta",
		"observer event with equal token has no changed field delta",
		"database-loaded initializer registration boundary",
		"dirty transaction save and commit result",
		"exception rollback and rethrow",
	} {
		if !seen[name] {
			t.Fatalf("missing required vector %q", name)
		}
	}
}
