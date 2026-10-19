package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

type streamChunksFixture struct {
	Provenance struct {
		Kind string `json:"kind"`
	} `json:"provenance"`
	ChunkSize         int `json:"chunk_size"`
	MaxFramePlaintext int `json:"max_frame_plaintext"`
	StreamCases       []struct {
		Name       string `json:"name"`
		FileSize   int    `json:"file_size"`
		PostOffset int64  `json:"post_offset"`
		Expected   struct {
			Streams    bool `json:"streams"`
			FullFrames int  `json:"full_frames"`
			TailFrame  int  `json:"tail_frame"`
		} `json:"expected"`
		MoooExpected *struct {
			Streams bool `json:"streams"`
			Fails   bool `json:"fails"`
		} `json:"mooo_expected"`
	} `json:"stream_cases"`
}

func loadStreamChunksFixture(t *testing.T) streamChunksFixture {
	t.Helper()
	data, err := os.ReadFile("../../research/fixtures/media-upload/stream-chunks.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture streamChunksFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Provenance.Kind != "static" || fixture.ChunkSize != 4096 || len(fixture.StreamCases) != 7 {
		t.Fatalf("unexpected fixture shape: kind=%q chunk=%d cases=%d", fixture.Provenance.Kind, fixture.ChunkSize, len(fixture.StreamCases))
	}
	return fixture
}

// uploadSession is a bare session whose SHIP is answered by mainBackend and
// whose media connection is mediaBackend.
func uploadSession(mainBackend, mediaBackend *testloco.Backend) *session {
	session := &session{
		wire: clientWire(mainBackend), nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]pendingRequest), userID: 7, appVersion: "26.8.0",
		mediaDial: func(context.Context, string, int) (*wireConn, error) { return clientWire(mediaBackend), nil },
	}
	go session.readLoop()
	return session
}

func shipAnswered(t *testing.T) *testloco.Backend {
	t.Helper()
	return newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "SHIP" {
			return fmt.Errorf("method = %q, want SHIP", request.Header.Method)
		}
		return server.WriteBSON(request.Header.PacketID, "SHIP", statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		))
	})
}

func syntheticUploadBytes(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 31)
	}
	return data
}

// Each fixture case runs through SendUpload against the scripted media peer.
// The static fixture gives the Mac client's 4096-byte frame plan, counted
// from the POST offset. The 16,777,239-byte and 22,276,883-byte cases are
// the frrad/mooo#428 regression: one frame used to carry the whole remainder
// and failed with "ciphertext too large" after SHIP and POST.
func TestSendUploadStreamsFixtureFramePlans(t *testing.T) {
	fixture := loadStreamChunksFixture(t)
	for _, tc := range fixture.StreamCases {
		t.Run(tc.Name, func(t *testing.T) {
			fileData := syntheticUploadBytes(tc.FileSize)
			upload, err := media.PrepareUpload("synthetic.dat", fileData, "")
			if err != nil {
				t.Fatal(err)
			}
			wantFails := tc.MoooExpected != nil && tc.MoooExpected.Fails
			wantStreams := tc.Expected.Streams
			if tc.MoooExpected != nil {
				wantStreams = tc.MoooExpected.Streams
			}
			var frames []int
			completeLog := mustBSON(bson.D{{Key: "logId", Value: int64(428)}, {Key: "type", Value: int32(18)}, {Key: "sendAt", Value: int64(1)}})
			mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
				request, err := server.Read()
				if err != nil {
					return err
				}
				if request.Header.Method != "POST" {
					return fmt.Errorf("method = %q, want POST", request.Header.Method)
				}
				if err := server.WriteBSON(request.Header.PacketID, "POST", statusDocument(bson.E{Key: "o", Value: tc.PostOffset})); err != nil {
					return err
				}
				if wantFails {
					// The client must close the connection without streaming.
					if _, err := server.ReadPayload(); err == nil {
						return errors.New("client streamed after an out-of-range offset")
					}
					return nil
				}
				if wantStreams {
					received, sizes, err := server.ReadStream(tc.FileSize - int(tc.PostOffset))
					if err != nil {
						return err
					}
					frames = sizes
					if !bytes.Equal(received, fileData[tc.PostOffset:]) {
						return errors.New("uploaded bytes mismatch")
					}
				}
				return server.WriteBSON(0, "COMPLETE", statusDocument(bson.E{Key: "chatLog", Value: bson.Raw(completeLog)}))
			})
			mainBackend := shipAnswered(t)
			session := uploadSession(mainBackend, mediaBackend)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			result, err := session.SendUpload(ctx, 42, upload)
			waitBackends(t, mediaBackend)
			waitBackends(t, mainBackend)
			if wantFails {
				if !errors.Is(err, media.ErrInvalidOffset) || !errors.Is(err, ErrUploadNotSent) {
					t.Fatalf("SendUpload err = %v, want a not-sent offset failure", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SendUpload err = %v", err)
			}
			if got := result.ChatLog.Lookup("logId").Int64(); got != 428 {
				t.Fatalf("logId = %d, want 428", got)
			}
			full, tail := 0, 0
			for i, size := range frames {
				if size > fixture.MaxFramePlaintext {
					t.Fatalf("frame %d has %d plaintext bytes, over %d", i, size, fixture.MaxFramePlaintext)
				}
				switch {
				case size == fixture.ChunkSize:
					full++
				case i == len(frames)-1:
					tail = size
				default:
					t.Fatalf("frame %d of %d has %d bytes, want %d", i, len(frames), size, fixture.ChunkSize)
				}
			}
			if full != tc.Expected.FullFrames || tail != tc.Expected.TailFrame {
				t.Fatalf("frames: %d full + tail %d, want %d full + tail %d", full, tail, tc.Expected.FullFrames, tc.Expected.TailFrame)
			}
		})
	}
}

// The server creates the message only after the whole stream, so a failure
// before the first byte is certainly not sent: here the media server drops
// the connection after reading POST.
func TestSendUploadFailureBeforeStreamIsNotSent(t *testing.T) {
	upload, err := media.PrepareUpload("synthetic.dat", syntheticUploadBytes(10000), "")
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	mediaBackend := newScriptedBackend(t, true, testloco.DropAfter("POST", &posts))
	mainBackend := shipAnswered(t)
	session := uploadSession(mainBackend, mediaBackend)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = session.SendUpload(ctx, 42, upload)
	waitBackends(t, mediaBackend)
	waitBackends(t, mainBackend)
	if !errors.Is(err, ErrUploadNotSent) || posts != 1 {
		t.Fatalf("SendUpload err = %v posts = %d, want ErrUploadNotSent after one POST", err, posts)
	}
}

// Once bytes have been streamed, a drop before COMPLETE is ambiguous: the
// server may have completed the upload.
func TestSendUploadDropAfterStreamingIsAmbiguous(t *testing.T) {
	upload, err := media.PrepareUpload("synthetic.dat", syntheticUploadBytes(10000), "")
	if err != nil {
		t.Fatal(err)
	}
	mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if err := server.WriteBSON(request.Header.PacketID, "POST", statusDocument()); err != nil {
			return err
		}
		if _, err := server.ReadPayload(); err != nil {
			return err
		}
		return server.Conn.Close()
	})
	mainBackend := shipAnswered(t)
	session := uploadSession(mainBackend, mediaBackend)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = session.SendUpload(ctx, 42, upload)
	waitBackends(t, mediaBackend)
	waitBackends(t, mainBackend)
	if err == nil || errors.Is(err, ErrUploadNotSent) {
		t.Fatalf("SendUpload err = %v, want an ambiguous failure", err)
	}
}

// A stream that stops making progress fails after mediaStallTimeout even
// when the caller's context allows much longer. The server read one frame,
// so the failure is ambiguous.
func TestSendUploadStallFailsAfterStallTimeout(t *testing.T) {
	oldStall := mediaStallTimeout
	mediaStallTimeout = 100 * time.Millisecond
	t.Cleanup(func() { mediaStallTimeout = oldStall })
	upload, err := media.PrepareUpload("synthetic.dat", syntheticUploadBytes(3*4096), "")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if err := server.WriteBSON(request.Header.PacketID, "POST", statusDocument()); err != nil {
			return err
		}
		if _, err := server.ReadPayload(); err != nil {
			return err
		}
		<-release
		return nil
	})
	mainBackend := shipAnswered(t)
	session := uploadSession(mainBackend, mediaBackend)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	_, err = session.SendUpload(ctx, 42, upload)
	elapsed := time.Since(started)
	close(release)
	waitBackends(t, mediaBackend)
	waitBackends(t, mainBackend)
	if err == nil || errors.Is(err, ErrUploadNotSent) {
		t.Fatalf("SendUpload err = %v, want an ambiguous stall failure", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("stall failed after %v, want about %v", elapsed, mediaStallTimeout)
	}
}
