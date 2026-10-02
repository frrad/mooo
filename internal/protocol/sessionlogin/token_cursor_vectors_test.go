package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tokenCursorVector struct {
	Name                   string   `json:"name"`
	CurrentTokenID         *int64   `json:"currentTokenId"`
	IncomingTokenID        *int64   `json:"incomingTokenId"`
	CurrentLBK             *int32   `json:"currentLBK"`
	IncomingLBK            *int32   `json:"incomingLBK"`
	ProfileGeneration      string   `json:"profileGeneration"`
	ImplementationDecision string   `json:"implementationDecision"`
	Expect                 []string `json:"expect"`
}

type tokenCursorVectors struct {
	Schema  string              `json:"schema"`
	Status  string              `json:"status"`
	Vectors []tokenCursorVector `json:"vectors"`
}

func TestTokenCursorUnresolvedVectorsSchema(t *testing.T) {
	path := filepath.Join("testdata", "token-cursor-unresolved.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var got tokenCursorVectors
	if err := decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "session-login-token-cursor-v1" {
		t.Fatalf("schema = %q", got.Schema)
	}
	if got.Status != "RED contract vectors; no production reducer" {
		t.Fatalf("status = %q", got.Status)
	}
	if len(got.Vectors) < 10 {
		t.Fatalf("vector count = %d", len(got.Vectors))
	}
	seen := make(map[string]bool, len(got.Vectors))
	for _, vector := range got.Vectors {
		if vector.Name == "" || len(vector.Expect) == 0 {
			t.Fatalf("incomplete vector: %#v", vector)
		}
		if seen[vector.Name] {
			t.Fatalf("duplicate vector %q", vector.Name)
		}
		seen[vector.Name] = true
	}
	for _, name := range []string{
		"equal int64 token is ignored",
		"lower int64 token is ignored",
		"greater int32 blind cursor advances independently",
		"signed int64 negative domain advances strictly",
		"signed int32 negative blind domain advances strictly",
		"negative current with zero incoming takes observed assertion branch",
	} {
		if !seen[name] {
			t.Fatalf("missing required vector %q", name)
		}
	}
}
