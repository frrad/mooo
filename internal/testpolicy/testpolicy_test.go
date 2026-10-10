package testpolicy

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRepositoryHasNoNewModelOnlyTests(t *testing.T) {
	offenders, err := ModelOnlyTests(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range offenders {
		t.Errorf("%s references no production code; exercise mooo instead of a model (see research/parity-fixtures.md)", path)
	}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModelOnlyTestsClassifiesReferences(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"internal/codec/codec.go": `package codec

func Decode(b []byte) string { return string(b) }
`,
		"internal/codec/calls_production_test.go": `package codec

import "testing"

func TestDecode(t *testing.T) {
	if Decode([]byte("a")) != "a" {
		t.Fatal("decode")
	}
}
`,
		"internal/codec/model_only_test.go": `package codec

import "testing"

type modelCase struct{ In, Want string }

func modelDecode(in string) string { return in }

func TestModel(t *testing.T) {
	for _, c := range []modelCase{{"a", "a"}} {
		if modelDecode(c.In) != c.Want {
			t.Fatal("model")
		}
	}
}
`,
		"internal/codec/shadowed_name_test.go": `package codec

import "testing"

func Decode2() {}

var Decode = func(b []byte) string { return "" }

func TestShadow(t *testing.T) { _ = Decode(nil) }
`,
		"internal/codec/external_test.go": `package codec_test

import (
	"testing"

	"github.com/frrad/mooo/internal/codec"
)

func TestExternal(t *testing.T) { _ = codec.Decode(nil) }
`,
		"internal/codec/testsupport_only_test.go": `package codec_test

import (
	"testing"

	"github.com/frrad/mooo/internal/testsupport/loco"
)

func TestSupportOnly(t *testing.T) { _ = loco.Script{} }
`,
		"internal/codec/testdata/ignored_test.go": `package ignored
`,
	})

	got, err := ModelOnlyTests(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"internal/codec/model_only_test.go",
		"internal/codec/shadowed_name_test.go",
		"internal/codec/testsupport_only_test.go",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ModelOnlyTests() = %q, want %q", got, want)
	}
}
