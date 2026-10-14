package connector

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// store is the part of the bridge database the connector reads and writes
// directly. Framework operations that also touch the database, such as
// Portal.Save or UserLogin.Save, still go through bridgev2.
type store interface {
	portalByKey(ctx context.Context, key networkid.PortalKey) (*database.Portal, error)
	portalsWithMXID(ctx context.Context) ([]*database.Portal, error)
	userPortals(ctx context.Context, login *database.UserLogin) ([]*database.UserPortal, error)
	userLoginByID(ctx context.Context, id networkid.UserLoginID) (*database.UserLogin, error)

	messageParts(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID) ([]*database.Message, error)
	firstMessagePart(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID) (*database.Message, error)
	lastMessagePart(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID) (*database.Message, error)
	firstOrSpecificMessagePart(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageOptionalPartID) (*database.Message, error)
	firstMessageInPortal(ctx context.Context, key networkid.PortalKey) (*database.Message, error)
	lastNMessagesInPortal(ctx context.Context, key networkid.PortalKey, n int) ([]*database.Message, error)
	lastRealMessageAtOrBefore(ctx context.Context, key networkid.PortalKey, at time.Time) (*database.Message, error)
	updateMessage(ctx context.Context, msg *database.Message) error

	reactionsToMessage(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID) ([]*database.Reaction, error)
	reactionsBySender(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID, sender networkid.UserID) ([]*database.Reaction, error)
	reaction(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID, sender networkid.UserID, emoji networkid.EmojiID) (*database.Reaction, error)
	deleteReaction(ctx context.Context, reaction *database.Reaction) error

	// The key-value methods are kvStore's; see its comment.
	get(ctx context.Context, key string) (string, bool, error)
	put(ctx context.Context, key, value string) error
	putIfAbsent(ctx context.Context, key, value string) (bool, error)
	compareAndSwap(ctx context.Context, key, previous, value string) (bool, error)
	deleteOlderThan(ctx context.Context, prefix, stamp string) error
}

// bridgeStore is the production store over the framework database.
type bridgeStore struct {
	db *database.Database
	kvStore
}

var _ store = bridgeStore{}

func newBridgeStore(db *database.Database) bridgeStore {
	return bridgeStore{db: db, kvStore: newKVStore(db.KV)}
}

func (s bridgeStore) portalByKey(ctx context.Context, key networkid.PortalKey) (*database.Portal, error) {
	return s.db.Portal.GetByKey(ctx, key)
}

func (s bridgeStore) portalsWithMXID(ctx context.Context) ([]*database.Portal, error) {
	return s.db.Portal.GetAllWithMXID(ctx)
}

func (s bridgeStore) userPortals(ctx context.Context, login *database.UserLogin) ([]*database.UserPortal, error) {
	return s.db.UserPortal.GetAllForLogin(ctx, login)
}

func (s bridgeStore) userLoginByID(ctx context.Context, id networkid.UserLoginID) (*database.UserLogin, error) {
	return s.db.UserLogin.GetByID(ctx, id)
}

func (s bridgeStore) messageParts(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID) ([]*database.Message, error) {
	return s.db.Message.GetAllPartsByID(ctx, receiver, id)
}

func (s bridgeStore) firstMessagePart(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID) (*database.Message, error) {
	return s.db.Message.GetFirstPartByID(ctx, receiver, id)
}

func (s bridgeStore) lastMessagePart(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID) (*database.Message, error) {
	return s.db.Message.GetLastPartByID(ctx, receiver, id)
}

func (s bridgeStore) firstOrSpecificMessagePart(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageOptionalPartID) (*database.Message, error) {
	return s.db.Message.GetFirstOrSpecificPartByID(ctx, receiver, id)
}

func (s bridgeStore) firstMessageInPortal(ctx context.Context, key networkid.PortalKey) (*database.Message, error) {
	return s.db.Message.GetFirstPortalMessage(ctx, key)
}

func (s bridgeStore) lastNMessagesInPortal(ctx context.Context, key networkid.PortalKey, n int) ([]*database.Message, error) {
	return s.db.Message.GetLastNInPortal(ctx, key, n)
}

func (s bridgeStore) lastRealMessageAtOrBefore(ctx context.Context, key networkid.PortalKey, at time.Time) (*database.Message, error) {
	return s.db.Message.GetLastNonFakePartAtOrBeforeTime(ctx, key, at)
}

func (s bridgeStore) updateMessage(ctx context.Context, msg *database.Message) error {
	return s.db.Message.Update(ctx, msg)
}

func (s bridgeStore) reactionsToMessage(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID) ([]*database.Reaction, error) {
	return s.db.Reaction.GetAllToMessage(ctx, receiver, id)
}

func (s bridgeStore) reactionsBySender(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID, sender networkid.UserID) ([]*database.Reaction, error) {
	return s.db.Reaction.GetAllToMessageBySender(ctx, receiver, id, sender)
}

func (s bridgeStore) reaction(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageID, sender networkid.UserID, emoji networkid.EmojiID) (*database.Reaction, error) {
	return s.db.Reaction.GetByIDWithoutMessagePart(ctx, receiver, id, sender, emoji)
}

func (s bridgeStore) deleteReaction(ctx context.Context, reaction *database.Reaction) error {
	return s.db.Reaction.Delete(ctx, reaction)
}
