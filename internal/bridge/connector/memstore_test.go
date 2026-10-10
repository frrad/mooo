package connector

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// memStore is an in-memory store for unit tests that do not need the
// framework. It follows the bridge database queries bridgeStore issues and
// hands out copies, so a caller's change is visible only after it is saved.
// Tests that also need Portal.Save, UserLogin.Save or Matrix room state use a
// sqlite framework bridge with bridgeStore instead.
type memStore struct {
	mu        sync.Mutex
	portals   map[networkid.PortalKey]*database.Portal
	messages  []*database.Message
	reactions []*database.Reaction
	kv        map[string]string
}

var _ store = (*memStore)(nil)

func newMemStore() *memStore {
	return &memStore{
		portals: map[networkid.PortalKey]*database.Portal{},
		kv:      map[string]string{},
	}
}

func copyMetadata(meta any) any {
	switch v := meta.(type) {
	case *KakaoPortalMetadata:
		if v != nil {
			c := *v
			c.MembershipRoster = slices.Clone(v.MembershipRoster)
			return &c
		}
	case *KakaoMessageMetadata:
		if v != nil {
			c := *v
			return &c
		}
	}
	return meta
}

func copyPortal(p *database.Portal) *database.Portal {
	c := *p
	c.Metadata = copyMetadata(p.Metadata)
	return &c
}

func copyMessage(m *database.Message) *database.Message {
	c := *m
	c.Metadata = copyMetadata(m.Metadata)
	return &c
}

func copyReaction(r *database.Reaction) *database.Reaction {
	c := *r
	return &c
}

// putMessage stores a copy of m, replacing any part with the same identity.
func (s *memStore) putMessage(m *database.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, old := range s.messages {
		if old.Room.Receiver == m.Room.Receiver && old.ID == m.ID && old.PartID == m.PartID {
			s.messages[i] = copyMessage(m)
			return
		}
	}
	s.messages = append(s.messages, copyMessage(m))
}

func sameReaction(a, b *database.Reaction) bool {
	return a.Room.Receiver == b.Room.Receiver && a.MessageID == b.MessageID && a.MessagePartID == b.MessagePartID && a.SenderID == b.SenderID && a.EmojiID == b.EmojiID
}

func (s *memStore) portalByKey(_ context.Context, key networkid.PortalKey) (*database.Portal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.portals[key]; p != nil {
		return copyPortal(p), nil
	}
	return nil, nil
}

func (s *memStore) portalsWithMXID(context.Context) ([]*database.Portal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*database.Portal
	for _, p := range s.portals {
		if p.MXID != "" {
			out = append(out, copyPortal(p))
		}
	}
	return out, nil
}

// userPortals treats every stored portal received by the login as one the
// login's user is in.
func (s *memStore) userPortals(_ context.Context, login *database.UserLogin) ([]*database.UserPortal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*database.UserPortal
	for key := range s.portals {
		if key.Receiver == login.ID {
			out = append(out, &database.UserPortal{UserMXID: login.UserMXID, LoginID: login.ID, Portal: key})
		}
	}
	return out, nil
}

// userLoginByID finds nothing: logins are saved through the framework, so
// tests that read one back use a framework bridge.
func (s *memStore) userLoginByID(context.Context, networkid.UserLoginID) (*database.UserLogin, error) {
	return nil, nil
}

// receiverMatches mirrors the framework's (room_receiver=$2 OR room_receiver=”).
func receiverMatches(m *database.Message, receiver networkid.UserLoginID) bool {
	return m.Room.Receiver == receiver || m.Room.Receiver == ""
}

func (s *memStore) partsByID(receiver networkid.UserLoginID, id networkid.MessageID) []*database.Message {
	var out []*database.Message
	for _, m := range s.messages {
		if receiverMatches(m, receiver) && m.ID == id {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b *database.Message) int { return cmp.Compare(a.PartID, b.PartID) })
	return out
}

func (s *memStore) messageParts(_ context.Context, receiver networkid.UserLoginID, id networkid.MessageID) ([]*database.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*database.Message
	for _, m := range s.partsByID(receiver, id) {
		out = append(out, copyMessage(m))
	}
	return out, nil
}

func (s *memStore) firstMessagePart(_ context.Context, receiver networkid.UserLoginID, id networkid.MessageID) (*database.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if parts := s.partsByID(receiver, id); len(parts) > 0 {
		return copyMessage(parts[0]), nil
	}
	return nil, nil
}

func (s *memStore) lastMessagePart(_ context.Context, receiver networkid.UserLoginID, id networkid.MessageID) (*database.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if parts := s.partsByID(receiver, id); len(parts) > 0 {
		return copyMessage(parts[len(parts)-1]), nil
	}
	return nil, nil
}

func (s *memStore) firstOrSpecificMessagePart(ctx context.Context, receiver networkid.UserLoginID, id networkid.MessageOptionalPartID) (*database.Message, error) {
	if id.PartID == nil {
		return s.firstMessagePart(ctx, receiver, id.MessageID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.partsByID(receiver, id.MessageID) {
		if m.PartID == *id.PartID {
			return copyMessage(m), nil
		}
	}
	return nil, nil
}

// portalMessages returns key's messages newest first, in the framework's
// timestamp, id, part_id order.
func (s *memStore) portalMessages(key networkid.PortalKey) []*database.Message {
	var out []*database.Message
	for _, m := range s.messages {
		if m.Room == key {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b *database.Message) int {
		return cmp.Or(b.Timestamp.Compare(a.Timestamp), cmp.Compare(b.ID, a.ID), cmp.Compare(b.PartID, a.PartID))
	})
	return out
}

func (s *memStore) firstMessageInPortal(_ context.Context, key networkid.PortalKey) (*database.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if all := s.portalMessages(key); len(all) > 0 {
		return copyMessage(all[len(all)-1]), nil
	}
	return nil, nil
}

func (s *memStore) lastNMessagesInPortal(_ context.Context, key networkid.PortalKey, n int) ([]*database.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*database.Message
	for _, m := range s.portalMessages(key) {
		if len(out) == n {
			break
		}
		out = append(out, copyMessage(m))
	}
	return out, nil
}

func (s *memStore) lastRealMessageAtOrBefore(_ context.Context, key networkid.PortalKey, at time.Time) (*database.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.portalMessages(key) {
		if !m.Timestamp.After(at) && !strings.HasPrefix(m.MXID.String(), "~fake:") {
			return copyMessage(m), nil
		}
	}
	return nil, nil
}

func (s *memStore) updateMessage(_ context.Context, msg *database.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, old := range s.messages {
		if old.Room.Receiver == msg.Room.Receiver && old.ID == msg.ID && old.PartID == msg.PartID {
			s.messages[i] = copyMessage(msg)
		}
	}
	return nil
}

func (s *memStore) reactionsToMessage(_ context.Context, receiver networkid.UserLoginID, id networkid.MessageID) ([]*database.Reaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*database.Reaction
	for _, r := range s.reactions {
		if r.Room.Receiver == receiver && r.MessageID == id {
			out = append(out, copyReaction(r))
		}
	}
	return out, nil
}

func (s *memStore) reactionsBySender(_ context.Context, receiver networkid.UserLoginID, id networkid.MessageID, sender networkid.UserID) ([]*database.Reaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*database.Reaction
	for _, r := range s.reactions {
		if r.Room.Receiver == receiver && r.MessageID == id && r.SenderID == sender {
			out = append(out, copyReaction(r))
		}
	}
	slices.SortFunc(out, func(a, b *database.Reaction) int { return b.Timestamp.Compare(a.Timestamp) })
	return out, nil
}

func (s *memStore) reaction(_ context.Context, receiver networkid.UserLoginID, id networkid.MessageID, sender networkid.UserID, emoji networkid.EmojiID) (*database.Reaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found *database.Reaction
	for _, r := range s.reactions {
		if r.Room.Receiver == receiver && r.MessageID == id && r.SenderID == sender && r.EmojiID == emoji && (found == nil || r.MessagePartID < found.MessagePartID) {
			found = r
		}
	}
	if found == nil {
		return nil, nil
	}
	return copyReaction(found), nil
}

func (s *memStore) deleteReaction(_ context.Context, reaction *database.Reaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reactions = slices.DeleteFunc(s.reactions, func(r *database.Reaction) bool { return sameReaction(r, reaction) })
	return nil
}

func (s *memStore) get(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, found := s.kv[key]
	return value, found, nil
}

func (s *memStore) put(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kv[key] = value
	return nil
}

func (s *memStore) putIfAbsent(_ context.Context, key, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.kv[key]; found {
		return false, nil
	}
	s.kv[key] = value
	return true, nil
}

func (s *memStore) deleteOlderThan(_ context.Context, prefix, stamp string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range s.kv {
		if strings.HasPrefix(key, prefix) && value < stamp {
			delete(s.kv, key)
		}
	}
	return nil
}
