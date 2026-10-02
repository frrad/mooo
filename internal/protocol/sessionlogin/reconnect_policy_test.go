package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type reconnectVectors struct {
	Cases []struct {
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
		PingIntervalMin      *int64 `json:"ping_interval_min_seconds"`
		ConnectTimeout       *int64 `json:"connect_timeout_seconds"`
		ReceiveHeaderTimeout *int64 `json:"receive_header_timeout_seconds"`
		InSegmentTimeout     *int64 `json:"in_segment_timeout_seconds"`
		OutSegmentTimeout    *int64 `json:"out_segment_timeout_seconds"`
	} `json:"cases"`
}

func TestReconnectPolicyVectors(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q4-q5.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors reconnectVectors
	if err := json.Unmarshal(body, &vectors); err != nil {
		t.Fatal(err)
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
