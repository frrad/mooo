package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type foundationStringKVCFixture struct {
	Status   string                    `json:"status"`
	Platform string                    `json:"platform"`
	Cases    []foundationStringKVCCase `json:"cases"`
}

type foundationStringKVCCase struct {
	Name          string `json:"name"`
	Input         string `json:"input"`
	LiteralClass  string `json:"literal_class"`
	RuntimeClass  string `json:"runtime_class"`
	ExpectedValue int32  `json:"expected_value"`
	Capture       string `json:"capture"`
}

// foundationStringKVC is an executable model for the explicitly captured
// input domain. The domain guard keeps this from becoming a portable Unicode
// or numeric-string grammar.
func foundationStringKVC(input string) (int32, bool) {
	allowed := map[string]bool{
		"":                                       true,
		"0":                                      true,
		"+0":                                     true,
		"-0":                                     true,
		"42":                                     true,
		" 42":                                    true,
		"\t-42":                                  true,
		"\n+42":                                  true,
		"42 ":                                    true,
		"42tail":                                 true,
		"tail42":                                 true,
		"+":                                      true,
		"-":                                      true,
		"++42":                                   true,
		"--42":                                   true,
		"+-42":                                   true,
		"0x10":                                   true,
		"010":                                    true,
		"3.75":                                   true,
		"-3.75":                                  true,
		"1e3":                                    true,
		"2147483647":                             true,
		"2147483648":                             true,
		"-2147483648":                            true,
		"-2147483649":                            true,
		"4294967296":                             true,
		"9223372036854775807":                    true,
		"9223372036854775808":                    true,
		"-9223372036854775809":                   true,
		"99999999999999999999999999999999999999": true,
		"-99999999999999999999999999999999999999": true,
		"\uff11\uff12":     true,
		"\u0661\u0662":     true,
		"\u00a042":         true,
		"\u200342":         true,
		"\u221242":         true,
		"true":             true,
		"NaN":              true,
		"Infinity":         true,
		"42\n17":           true,
		"+42":              true,
		"-42":              true,
		"\n42":             true,
		"\r42":             true,
		"\f42":             true,
		"\u000b42":         true,
		"\t42":             true,
		"  +42":            true,
		"  -42":            true,
		"\u202842":         true,
		"\u202942":         true,
		"\u300042":         true,
		"\uff11\uff12tail": true,
		"\u0661\u0662tail": true,
		"\u0967\u0662":     true,
		"-\uff11\uff12":    true,
		"+\uff11\uff12":    true,
		"\u0664\u0662":     true,
		"\u200b42":         true,
		"42\u0000tail":     true,
		"-00042":           true,
		"--0":              true,
		"- 42":             true,
		" 4 2":             true,
	}
	if !allowed[input] {
		return 0, false
	}

	runes := []rune(input)
	pos := 0
	for pos < len(runes) {
		switch runes[pos] {
		case ' ', '\t', '\u00a0', '\u2003', '\u200b', '\u3000':
			pos++
		default:
			goto sign
		}
	}
sign:
	sign := int64(1)
	if pos < len(runes) && (runes[pos] == '+' || runes[pos] == '-') {
		if runes[pos] == '-' {
			sign = -1
		}
		pos++
		for pos < len(runes) {
			switch runes[pos] {
			case ' ', '\t', '\u00a0', '\u2003', '\u200b', '\u3000':
				pos++
			default:
				goto digits
			}
		}
	}
digits:
	value := int64(0)
	digits := 0
	for ; pos < len(runes); pos++ {
		digit, ok := capturedDecimalDigit(runes[pos])
		if !ok {
			break
		}
		digits++
		if value > 214748364 || (value == 214748364 && digit > 7) {
			if sign < 0 {
				return -2147483648, true
			}
			return 2147483647, true
		}
		value = value*10 + int64(digit)
	}
	if digits == 0 {
		return 0, true
	}
	if sign < 0 {
		return int32(-value), true
	}
	return int32(value), true
}

func capturedDecimalDigit(r rune) (int, bool) {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0'), true
	case r >= '\u0660' && r <= '\u0669':
		return int(r - '\u0660'), true
	case r >= '\u0966' && r <= '\u096f':
		return int(r - '\u0966'), true
	case r >= '\uff10' && r <= '\uff19':
		return int(r - '\uff10'), true
	default:
		return 0, false
	}
}

func TestFoundationStringKVCFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-foundation-string-kvc.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture foundationStringKVCFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-platform-bounded-synthetic" || fixture.Platform == "" || len(fixture.Cases) != 64 {
		t.Fatalf("fixture header=%#v", fixture)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		if c.LiteralClass == "" || c.RuntimeClass == "" || c.Capture == "" {
			t.Fatalf("%s missing NSString class provenance", c.Name)
		}
		got, ok := foundationStringKVC(c.Input)
		if !ok || got != c.ExpectedValue {
			t.Errorf("%s got=%d/%t want=%d", c.Name, got, ok, c.ExpectedValue)
		}
	}
	if _, ok := foundationStringKVC("captured-but-not-supported"); ok {
		t.Fatal("uncaptured input unexpectedly accepted")
	}
}
