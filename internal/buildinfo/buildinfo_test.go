package buildinfo

import "testing"

func TestDevelopmentVersionIsSet(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
