package client

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestShipRedirectStopsPhotoBeforeTrailerOrWrite(t *testing.T) {
	release := make(chan struct{})
	calls := 0
	backend := newScriptedBackend(t, true, func(peer *testloco.Peer) error {
		req, err := peer.Read()
		if err != nil {
			return err
		}
		calls++
		if req.Header.Method != media.ShipCommand {
			return fmt.Errorf("method %s", req.Header.Method)
		}
		if err := peer.WriteBSON(req.Header.PacketID, media.ShipCommand, statusDocument(bson.E{Key: "k", Value: ""}, bson.E{Key: "vh", Value: ""}, bson.E{Key: "p", Value: int32(0)}, bson.E{Key: "rd", Value: true})); err != nil {
			return err
		}
		<-release
		return nil
	})
	s := &session{wire: clientWire(backend), nextID: 1, pushes: make(chan loco.Packet, 4), pending: make(map[uint32]pendingRequest), userID: 7, appVersion: "26.8.0", mediaDial: func(context.Context, string, int) (*wireConn, error) {
		t.Error("redirect opened a trailer connection")
		return nil, errors.New("unexpected trailer")
	}}
	done := make(chan struct{})
	go func() { defer close(done); s.readLoop() }()
	t.Cleanup(func() { _ = backend.Endpoint.Client.Close(); <-done })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.SendImage(ctx, 42, syntheticClientJPEG(t), "")
	close(release)
	waitBackends(t, backend)
	if !errors.Is(err, media.ErrUploadRedirect) || errors.Is(err, media.ErrInvalidResponse) {
		t.Fatalf("redirect failure %v", err)
	}
	if calls != 1 {
		t.Fatalf("SHIP calls %d", calls)
	}
}
