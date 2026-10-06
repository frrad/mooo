package testpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Provenance records where a parity fixture's expected outputs came from.
// See research/parity-fixtures.md.
type Provenance struct {
	Kind   string `json:"kind"`
	Client string `json:"client"`
	Date   string `json:"date"`
	Method string `json:"method"`
	Doc    string `json:"doc"`
}

var provenanceKinds = map[string]bool{"static": true, "executed": true, "observed": true}

// CheckFixtureProvenance validates the provenance object of every JSON file
// under root/research/fixtures and returns one error per invalid fixture.
func CheckFixtureProvenance(root string) ([]error, error) {
	paths, err := filepath.Glob(filepath.Join(root, "research", "fixtures", "*", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var problems []error
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		if err := checkFixture(root, path); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", rel, err))
		}
	}
	return problems, nil
}

func checkFixture(root, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fixture struct {
		Provenance *Provenance `json:"provenance"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		return err
	}
	p := fixture.Provenance
	switch {
	case p == nil:
		return fmt.Errorf("missing provenance")
	case !provenanceKinds[p.Kind]:
		return fmt.Errorf("provenance kind %q is not static, executed, or observed", p.Kind)
	case p.Client == "":
		return fmt.Errorf("provenance client is empty")
	case p.Method == "":
		return fmt.Errorf("provenance method is empty")
	}
	if _, err := time.Parse(time.DateOnly, p.Date); err != nil {
		return fmt.Errorf("provenance date %q is not YYYY-MM-DD", p.Date)
	}
	if p.Doc == "" {
		return fmt.Errorf("provenance doc is empty")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p.Doc))); err != nil {
		return fmt.Errorf("provenance doc %q does not exist", p.Doc)
	}
	return nil
}
