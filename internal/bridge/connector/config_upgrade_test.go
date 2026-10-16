package connector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	up "go.mau.fi/util/configupgrade"
	"gopkg.in/yaml.v3"
)

// The bridge runs on the example config plus only the keys upgradeConfig
// copies from the operator's file, so a key missing there is silently reset.
// Change every leaf of the example config and require each change to survive.
func TestUpgradeConfigKeepsEveryExampleConfigKey(t *testing.T) {
	var example yaml.Node
	if err := yaml.Unmarshal([]byte(exampleConfig), &example); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	var changeLeaves func(node *yaml.Node, path []string)
	changeLeaves = func(node *yaml.Node, path []string) {
		switch node.Kind {
		case yaml.DocumentNode:
			changeLeaves(node.Content[0], path)
		case yaml.MappingNode:
			for i := 0; i < len(node.Content); i += 2 {
				changeLeaves(node.Content[i+1], append(append([]string{}, path...), node.Content[i].Value))
			}
		case yaml.ScalarNode:
			key := strings.Join(path, ".")
			switch node.Tag {
			case "!!bool":
				if node.Value == "true" {
					node.Value = "false"
				} else {
					node.Value = "true"
				}
			case "!!int":
				node.Value = "7"
			default:
				node.Value = "operator-" + strings.ReplaceAll(key, ".", "-")
			}
			want[key] = node.Value
		default:
			t.Fatalf("example config %s has unsupported YAML kind %d", strings.Join(path, "."), node.Kind)
		}
	}
	changeLeaves(&example, nil)
	if _, ok := want["device_name"]; !ok {
		t.Fatal("example config has no device_name")
	}
	operator, err := yaml.Marshal(&example)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, operator, 0o600); err != nil {
		t.Fatal(err)
	}

	upgraded, _, err := up.Do(path, false, up.MergeUpgraders(exampleConfig, up.SimpleUpgrader(upgradeConfig)))
	if err != nil {
		t.Fatal(err)
	}
	var got yaml.Node
	if err := yaml.Unmarshal(upgraded, &got); err != nil {
		t.Fatal(err)
	}
	for key, value := range want {
		node := got.Content[0]
		for _, part := range strings.Split(key, ".") {
			var next *yaml.Node
			for i := 0; i < len(node.Content); i += 2 {
				if node.Content[i].Value == part {
					next = node.Content[i+1]
				}
			}
			if next == nil {
				t.Fatalf("upgraded config lost %s", key)
			}
			node = next
		}
		if node.Value != value {
			t.Errorf("upgraded config %s = %q, want the operator's %q", key, node.Value, value)
		}
	}
}
