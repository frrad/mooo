package sessionlogin

import (
	"bytes"
	"encoding/json"
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
		} `json:"cases"`
		PingIntervalDefault  *int64   `json:"ping_interval_default_seconds"`
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
	if len(vectors.Cases) == 0 {
		t.Fatal("pure policy vector file contains no cases")
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
				defaults := DefaultSocketTimeouts()
				if defaults.Connect != duration(*tc.ConnectTimeout) || defaults.ReceiveHeader != duration(*tc.ReceiveHeaderTimeout) || defaults.InSegment != duration(*tc.InSegmentTimeout) || defaults.OutSegment != duration(*tc.OutSegmentTimeout) {
					t.Fatalf("defaults=%#v", defaults)
				}
				if PingInterval(int32(*tc.PingIntervalDefault)-1) != int32(*tc.PingIntervalDefault)-1 || PingInterval(int32(*tc.PingIntervalDefault)+1) != int32(*tc.PingIntervalDefault)+1 {
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
