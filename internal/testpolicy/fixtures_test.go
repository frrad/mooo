package testpolicy

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryFixturesCarryProvenance(t *testing.T) {
	problems, err := CheckFixtureProvenance(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

func TestCheckFixtureProvenanceReportsEachDefect(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"research/doc.md": "# doc\n",
		"research/fixtures/area/good.json": `{"provenance": {"kind": "executed", "client": "KakaoTalk for macOS 26.8.0",
			"date": "2026-10-05", "method": "lab harness", "doc": "research/doc.md"}, "cases": []}`,
		"research/fixtures/area/missing.json":   `{"cases": []}`,
		"research/fixtures/area/bad-kind.json":  `{"provenance": {"kind": "guess", "client": "c", "date": "2026-10-05", "method": "m", "doc": "research/doc.md"}}`,
		"research/fixtures/area/bad-date.json":  `{"provenance": {"kind": "static", "client": "c", "date": "Oct 5", "method": "m", "doc": "research/doc.md"}}`,
		"research/fixtures/area/no-doc.json":    `{"provenance": {"kind": "static", "client": "c", "date": "2026-10-05", "method": "m", "doc": "research/gone.md"}}`,
		"research/fixtures/area/no-client.json": `{"provenance": {"kind": "observed", "client": "", "date": "2026-10-05", "method": "m", "doc": "research/doc.md"}}`,
	})

	problems, err := CheckFixtureProvenance(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, problem := range problems {
		got = append(got, problem.Error())
	}
	want := []string{
		`research/fixtures/area/bad-date.json: provenance date "Oct 5" is not YYYY-MM-DD`,
		`research/fixtures/area/bad-kind.json: provenance kind "guess" is not static, executed, or observed`,
		`research/fixtures/area/missing.json: missing provenance`,
		`research/fixtures/area/no-client.json: provenance client is empty`,
		`research/fixtures/area/no-doc.json: provenance doc "research/gone.md" does not exist`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
