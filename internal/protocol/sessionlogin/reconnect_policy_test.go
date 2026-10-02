package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type reconnectVectors struct {
	Questions []string `json:"questions"`
	Evidence  []string `json:"evidence"`
	Cases     []struct {
		Name              string   `json:"name"`
		Kind              string   `json:"kind"`
		Evidence          []string `json:"evidence"`
		Tag               *int64   `json:"tag"`
		ConfiguredTimeout *int64   `json:"configured_timeout_seconds"`
		Arm               *bool    `json:"arm"`
		Subcases          []struct {
			Tag               int64 `json:"tag"`
			ConfiguredTimeout int64 `json:"configured_timeout_seconds"`
			Arm               bool  `json:"arm"`
			ConfiguredSeconds int32 `json:"configured_seconds"`
			StoredSeconds     int32 `json:"stored_seconds"`
		} `json:"cases"`
		PingIntervalFallback *int64   `json:"ping_interval_fallback_seconds"`
		ConnectTimeout       *int64   `json:"connect_timeout_seconds"`
		ReceiveHeaderTimeout *int64   `json:"receive_header_timeout_seconds"`
		InSegmentTimeout     *int64   `json:"in_segment_timeout_seconds"`
		OutSegmentTimeout    *int64   `json:"out_segment_timeout_seconds"`
		RemainingGaps        []string `json:"remaining_gaps"`
	} `json:"cases"`
}

func TestReconnectPolicyVectors(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q4-q5.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors reconnectVectors
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&vectors); err != nil {
		t.Fatal(err)
	}
	if err := validateReconnectPolicyVectors(vectors); err != nil {
		t.Fatal(err)
	}
	executed := 0
	for _, tc := range vectors.Cases {
		if len(tc.Evidence) == 0 {
			t.Fatalf("case %q has no evidence IDs", tc.Name)
		}

		t.Run(tc.Name, func(t *testing.T) {
			switch tc.Kind {
			case "timeout-predicate":
				for _, sub := range tc.Subcases {
					if ShouldArmReceiveHeaderTimeout(sub.Tag, duration(sub.ConfiguredTimeout)) != sub.Arm {
						t.Fatalf("tag=%d timeout=%d predicate mismatch", sub.Tag, sub.ConfiguredTimeout)
					}
				}
			case "configuration":
				if len(tc.Subcases) > 0 {
					for _, sub := range tc.Subcases {
						if got := PingInterval(sub.ConfiguredSeconds); got != sub.StoredSeconds {
							t.Fatalf("PingInterval(%d) = %d, want %d", sub.ConfiguredSeconds, got, sub.StoredSeconds)
						}
					}
					break
				}
				defaults := RecoveredManagerTimeouts()
				if defaults.Connect != duration(*tc.ConnectTimeout) || defaults.ReceiveHeader != duration(*tc.ReceiveHeaderTimeout) || defaults.InSegment != duration(*tc.InSegmentTimeout) || defaults.OutSegment != duration(*tc.OutSegmentTimeout) {
					t.Fatalf("defaults=%#v", defaults)
				}
				if PingInterval(int32(*tc.PingIntervalFallback)-1) != int32(*tc.PingIntervalFallback)-1 || PingInterval(int32(*tc.PingIntervalFallback)+1) != int32(*tc.PingIntervalFallback)+1 {
					t.Fatal("ping floor mismatch")
				}
			default:
				t.Fatalf("unsupported pure policy kind %q", tc.Kind)
			}
			executed++
		})
	}
	if executed != len(vectors.Cases) {
		t.Fatalf("executed %d of %d pure policy cases", executed, len(vectors.Cases))
	}
}

func validateReconnectPolicyVectors(v reconnectVectors) error {
	if len(v.Cases) == 0 {
		return fmt.Errorf("pure policy vector file contains no cases")
	}
	for _, tc := range v.Cases {
		if len(tc.Evidence) == 0 {
			return fmt.Errorf("case %q has no evidence IDs", tc.Name)
		}
		switch tc.Kind {
		case "timeout-predicate":
			if len(tc.Subcases) == 0 {
				return fmt.Errorf("case %q has no timeout subcases", tc.Name)
			}
		case "configuration":
			if len(tc.Subcases) > 0 {
				continue
			}
			if tc.PingIntervalFallback == nil || tc.ConnectTimeout == nil || tc.ReceiveHeaderTimeout == nil || tc.InSegmentTimeout == nil || tc.OutSegmentTimeout == nil {
				return fmt.Errorf("case %q is missing configuration values", tc.Name)
			}
		default:
			return fmt.Errorf("unsupported pure policy kind %q", tc.Kind)
		}
	}
	return nil
}

func TestReconnectPolicySchemaValidation(t *testing.T) {
	unknown := []byte(`{"questions":[],"evidence":[],"cases":[{"name":"x","kind":"configuration","evidence":["RC-BIN-001"],"unexpected":true}]}`)
	decoder := json.NewDecoder(bytes.NewReader(unknown))
	decoder.DisallowUnknownFields()
	var vectors reconnectVectors
	if err := decoder.Decode(&vectors); err == nil {
		t.Fatal("unknown vector field accepted")
	}
	missing := reconnectVectors{Cases: []struct {
		Name              string   `json:"name"`
		Kind              string   `json:"kind"`
		Evidence          []string `json:"evidence"`
		Tag               *int64   `json:"tag"`
		ConfiguredTimeout *int64   `json:"configured_timeout_seconds"`
		Arm               *bool    `json:"arm"`
		Subcases          []struct {
			Tag               int64 `json:"tag"`
			ConfiguredTimeout int64 `json:"configured_timeout_seconds"`
			Arm               bool  `json:"arm"`
			ConfiguredSeconds int32 `json:"configured_seconds"`
			StoredSeconds     int32 `json:"stored_seconds"`
		} `json:"cases"`
		PingIntervalFallback *int64   `json:"ping_interval_fallback_seconds"`
		ConnectTimeout       *int64   `json:"connect_timeout_seconds"`
		ReceiveHeaderTimeout *int64   `json:"receive_header_timeout_seconds"`
		InSegmentTimeout     *int64   `json:"in_segment_timeout_seconds"`
		OutSegmentTimeout    *int64   `json:"out_segment_timeout_seconds"`
		RemainingGaps        []string `json:"remaining_gaps"`
	}{{Name: "missing", Kind: "configuration", Evidence: []string{"RC-BIN-001"}}}}
	if err := validateReconnectPolicyVectors(missing); err == nil {
		t.Fatal("missing configuration values accepted")
	}
	missing.Cases[0].Kind = "future-kind"
	if err := validateReconnectPolicyVectors(missing); err == nil {
		t.Fatal("unsupported policy kind accepted")
	}
}

func duration(v int64) time.Duration { return time.Duration(v) * time.Second }

func TestPingIntervalFallbackBoundaries(t *testing.T) {
	for _, tc := range []struct{ configured, want int32 }{
		{-1, 180}, {0, 180}, {1, 1}, {179, 179}, {180, 180}, {181, 181},
	} {
		if got := PingInterval(tc.configured); got != tc.want {
			t.Errorf("PingInterval(%d) = %d, want %d", tc.configured, got, tc.want)
		}
	}
}

func TestReceiveHeaderTimeoutDomainBoundaries(t *testing.T) {
	for _, tag := range []int64{0, 1, 1<<63 - 1} {
		if !ShouldArmReceiveHeaderTimeout(tag, time.Nanosecond) {
			t.Errorf("tag %d with positive timeout did not arm", tag)
		}
	}
	for _, tag := range []int64{-1, -1 << 63} {
		if ShouldArmReceiveHeaderTimeout(tag, time.Nanosecond) {
			t.Errorf("tag %d with positive timeout armed", tag)
		}
	}
	if ShouldArmReceiveHeaderTimeout(0, -time.Nanosecond) || ShouldArmReceiveHeaderTimeout(0, 0) {
		t.Fatal("nonpositive timeout armed")
	}
}

func TestPingIntervalSigned32Domain(t *testing.T) {
	if got := PingInterval(-1 << 31); got != 180 {
		t.Fatalf("minimum int32 config = %d, want 180", got)
	}
	if got := PingInterval(1<<31 - 1); got != 1<<31-1 {
		t.Fatalf("maximum int32 config = %d, want unchanged", got)
	}
}
