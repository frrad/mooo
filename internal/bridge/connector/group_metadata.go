package connector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var errOutboundRoomMetadata = errors.New("connector: outbound Matrix room name and avatar changes are not supported; change the connected Kakao profile's chat settings in the native client")

var errOutboundAnnouncement = errors.New("connector: outbound Matrix topic changes are not supported; post or remove the announcement in the native client")

var _ bridgev2.RoomNameHandlingNetworkAPI = (*KakaoClient)(nil)
var _ bridgev2.RoomAvatarHandlingNetworkAPI = (*KakaoClient)(nil)
var _ bridgev2.RoomTopicHandlingNetworkAPI = (*KakaoClient)(nil)

// Room metadata is a read projection. Reject changes before any source write or
// portal metadata update instead of presenting a local change as source success.
func (*KakaoClient) HandleMatrixRoomName(context.Context, *bridgev2.MatrixRoomName) (bool, error) {
	return false, unsupportedRoomMetadataStatus()
}

func (*KakaoClient) HandleMatrixRoomAvatar(context.Context, *bridgev2.MatrixRoomAvatar) (bool, error) {
	return false, unsupportedRoomMetadataStatus()
}

// The topic mirrors the group's Boards announcement. The Boards write contract
// is untraced, so a Matrix topic change is rejected before any source request
// and the bridge bot restores the announcement topic in Matrix, which would
// otherwise keep the rejected text until the announcement next changes. The
// rejection is certain and never retried.
func (kc *KakaoClient) HandleMatrixRoomTopic(ctx context.Context, msg *bridgev2.MatrixRoomTopic) (bool, error) {
	status := bridgev2.WrapErrorInStatus(errOutboundAnnouncement).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithSendNotice(true).
		WithMessage("Changing the topic does not change the KakaoTalk announcement; post or remove the announcement in KakaoTalk. The topic was restored.")
	if msg == nil || msg.Portal == nil || msg.Portal.MXID == "" || msg.Portal.Bridge == nil || msg.Portal.Bridge.Bot == nil {
		return false, status
	}
	content := &event.Content{Parsed: &event.TopicEventContent{Topic: msg.Portal.Topic}}
	if _, err := msg.Portal.Bridge.Bot.SendState(ctx, msg.Portal.MXID, event.StateTopic, "", content, time.Time{}); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to restore the announcement topic after a rejected Matrix topic change")
		return false, status.WithMessage("Changing the topic does not change the KakaoTalk announcement; post or remove the announcement in KakaoTalk. The topic could not be restored.")
	}
	return false, status
}

func unsupportedRoomMetadataStatus() error {
	return bridgev2.WrapErrorInStatus(errOutboundRoomMetadata).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithSendNotice(true).
		WithMessage("Outbound Matrix room name and avatar changes are not supported; change the connected Kakao profile's chat settings in the native client.")
}

// A separate KV record avoids rewriting shared portal access/announcement
// metadata. Database methods are used directly because KV.Set logs values on
// failure and suppresses errors. Profile URLs must never enter such logs.
func (kc *KakaoClient) checkpointGroupDisplay(ctx context.Context, p *bridgev2.Portal, data chatmeta.ChatData) (chatmeta.ChatData, error) {
	// Validate the incoming snapshot before the persistent revision gate can
	// discard contradictory fields. Admission rules also apply on a cold start.
	if _, err := chatmeta.ProjectGroupDisplay(data); err != nil {
		return chatmeta.ChatData{}, err
	}
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return data, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	kc.displayGate.Lock()
	defer kc.displayGate.Unlock()
	key := fmt.Sprintf("kakao:group-display:%s:%s", p.Receiver, p.ID)
	db := kc.login.Bridge.DB.KV
	var raw string
	err := db.QueryRow(ctx, "SELECT value FROM kv_store WHERE bridge_id=$1 AND key=$2", db.BridgeID, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return chatmeta.ChatData{}, err
	}
	if len(raw) > 256<<10 {
		return chatmeta.ChatData{}, errors.New("connector: display checkpoint exceeds its bound")
	}
	var stored []chatmeta.ChatMeta
	if raw != "" && json.Unmarshal([]byte(raw), &stored) != nil {
		return chatmeta.ChatData{}, errors.New("connector: display checkpoint is invalid")
	}
	selected := map[int32]chatmeta.ChatMeta{}
	for _, meta := range stored {
		if !isDisplayMeta(meta.Type) || meta.Revision < 0 {
			return chatmeta.ChatData{}, errors.New("connector: display checkpoint has invalid metadata")
		}
		if _, exists := selected[meta.Type]; exists {
			return chatmeta.ChatData{}, errors.New("connector: display checkpoint has duplicate types")
		}
		selected[meta.Type] = meta
	}
	for _, meta := range data.ChatMetas {
		if !isDisplayMeta(meta.Type) {
			continue
		}
		if meta.Revision < 0 {
			return chatmeta.ChatData{}, chatmeta.ErrInvalidResponse
		}
		previous, exists := selected[meta.Type]
		if exists && meta.Revision <= previous.Revision {
			continue
		}
		selected[meta.Type] = chatmeta.ChatMeta{Type: meta.Type, Revision: meta.Revision, Content: meta.Content}
	}
	merged := make([]chatmeta.ChatMeta, 0, len(selected))
	for _, meta := range selected {
		merged = append(merged, meta)
	}
	slices.SortFunc(merged, func(a, b chatmeta.ChatMeta) int {
		if a.Type < b.Type {
			return -1
		}
		if a.Type > b.Type {
			return 1
		}
		return 0
	})
	data.ChatMetas = merged
	if _, err = chatmeta.ProjectGroupDisplay(data); err != nil {
		return chatmeta.ChatData{}, err
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return chatmeta.ChatData{}, err
	}
	if len(encoded) > 256<<10 {
		return chatmeta.ChatData{}, errors.New("connector: display checkpoint exceeds its bound")
	}
	if string(encoded) != raw {
		if _, err = db.Exec(ctx, "INSERT INTO kv_store (bridge_id,key,value) VALUES ($1,$2,$3) ON CONFLICT (bridge_id,key) DO UPDATE SET value=excluded.value", db.BridgeID, key, string(encoded)); err != nil {
			return chatmeta.ChatData{}, err
		}
	}
	var saved string
	if err = db.QueryRow(ctx, "SELECT value FROM kv_store WHERE bridge_id=$1 AND key=$2", db.BridgeID, key).Scan(&saved); err != nil {
		return chatmeta.ChatData{}, err
	}
	if saved != string(encoded) {
		return chatmeta.ChatData{}, errors.New("connector: display checkpoint was not durable")
	}
	return data, nil
}

func isDisplayMeta(typ int32) bool {
	return typ == chatmeta.SharedMetaKakaoGroup || typ == chatmeta.SharedMetaTitle || typ == chatmeta.SharedMetaProfile
}
