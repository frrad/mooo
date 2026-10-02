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
		Name              string `json:"name"`
		Kind              string `json:"kind"`
		Tag               *int64 `json:"tag"`
		ConfiguredTimeout *int64 `json:"configured_timeout_seconds"`
		Arm               *bool  `json:"arm"`
		Subcases          []struct {
			Tag               int64 `json:"tag"`
			ConfiguredTimeout int64 `json:"configured_timeout_seconds"`
			Arm               bool  `json:"arm"`
		} `json:"cases"`
		PingIntervalMin      *int64   `json:"ping_interval_min_seconds"`
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
	if len(vectors.Cases) != 2 {
		t.Fatalf("pure policy case count = %d, want 2", len(vectors.Cases))
	}
	for _, tc := range vectors.Cases {
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
				if PingInterval(duration(*tc.PingIntervalMin)-time.Second) != duration(*tc.PingIntervalMin) || PingInterval(duration(*tc.PingIntervalMin)+time.Second) != duration(*tc.PingIntervalMin)+time.Second {
					t.Fatal("ping floor mismatch")
				}
			default:
				t.Fatalf("unsupported pure policy kind %q", tc.Kind)
			}
		})
	}
}

func duration(v int64) time.Duration { return time.Duration(v) * time.Second }

func TestPingIntervalFloorBoundaries(t *testing.T) {
	for _, tc := range []struct{ configured, want time.Duration }{
		{-time.Second, 180 * time.Second},
		{0, 180 * time.Second},
		{180 * time.Second, 180 * time.Second},
		{181 * time.Second, 181 * time.Second},
	} {
		if got := PingInterval(tc.configured); got != tc.want {
			t.Errorf("PingInterval(%s) = %s, want %s", tc.configured, got, tc.want)
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

func TestPingIntervalPreservesFractionalPositiveValues(t *testing.T) {
	configured := 180*time.Second + 500*time.Millisecond
	if got := PingInterval(configured); got != configured {
		t.Fatalf("PingInterval(%s) = %s, want exact configured duration", configured, got)
	}
}
