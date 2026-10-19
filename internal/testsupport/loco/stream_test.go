package loco

import (
	"bytes"
	"crypto/rand"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	protocol "github.com/frrad/mooo/internal/protocol/loco"
)

// writeChunks encrypts each chunk as its own secure frame and writes it, the
// way the official client streams media in 4096-byte chunks after POST.
func writeChunks(t *testing.T, conn net.Conn, secure *protocol.SecureV3, data []byte, chunk int) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		for rest := data; len(rest) > 0; {
			n := min(len(rest), chunk)
			frame, err := secure.Encrypt(rest[:n])
			if err != nil {
				done <- err
				return
			}
			if err := writeAll(conn, frame); err != nil {
				done <- err
				return
			}
			rest = rest[n:]
		}
		done <- nil
	}()
	return done
}

// A media peer reassembles an upload stream that is larger than one secure
// frame may carry, and reports where the sender split it.
func TestReadStreamReassemblesStreamLargerThanOneFrame(t *testing.T) {
	size := protocol.DefaultMaxBody + 4096 + 7
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	var got []byte
	var frames []int
	backend, err := NewBackend(true, func(peer *Peer) error {
		var err error
		got, frames, err = peer.ReadStream(size)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	written := writeChunks(t, backend.Endpoint.Client, backend.Endpoint.ClientSecure, data, 4096)
	if err := backend.Wait(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("reassembled stream differs from the bytes sent")
	}
	wantFrames := make([]int, size/4096)
	for i := range wantFrames {
		wantFrames[i] = 4096
	}
	wantFrames = append(wantFrames, 7)
	if !slices.Equal(frames, wantFrames) {
		t.Fatalf("got %d frames, want %d frames of 4096 bytes and one of 7", len(frames), len(wantFrames))
	}
}

func TestReadStreamRejectsFrameRunningPastStream(t *testing.T) {
	data := []byte("synthetic upload bytes")
	backend, err := NewBackend(true, func(peer *Peer) error {
		_, _, err := peer.ReadStream(len(data) - 1)
		if err == nil || !strings.Contains(err.Error(), "runs past") {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	written := writeChunks(t, backend.Endpoint.Client, backend.Endpoint.ClientSecure, data, len(data))
	if err := backend.Wait(3 * time.Second); err != nil {
		t.Fatalf("ReadStream accepted an overlong frame: %v", err)
	}
	<-written
}
