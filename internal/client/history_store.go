package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var (
	ErrHistoryStoreCorrupt     = errors.New("client: acquired history store invalid")
	ErrHistoryStoreCapacity    = errors.New("client: acquired history record exceeds storage limit")
	ErrHistoryTargetUnresolved = errors.New("client: acquired history edit target needs reconciliation")
)

type historyStoreHeader struct {
	Version uint32
	Binding string
}
type acquiredPage struct {
	Version  uint32
	Binding  string
	Request  syncmsg.Request
	Response syncmsg.Response
}
type acquiredPageRef struct {
	request syncmsg.Request
	last    int64
	name    string
}
type historyTarget struct{ chatID, logID int64 }
type historyMutation struct {
	Version     uint32
	Binding     string
	ChatID      int64
	TargetID    int64
	Sequence    int64
	Revision    int64
	Deleted     bool
	Acquired    bool
	Replacement bson.Raw
}

// Pages are immutable source snapshots. Target overlays are written before a
// page is published, so a crash during acquisition cannot resurrect known
// deleted/edited content from an older page. Neither is a delivery checkpoint.
type historyStore struct {
	mu           sync.Mutex
	dir, binding string
	pages        map[string]acquiredPageRef
	mutations    map[historyTarget]historyMutation
	failed       error
	targets      map[historyTarget]bool
	bulkCursors  map[int64]int64
	unavailable  map[syncmsg.Request]historyUnavailableRecord
}

func historyDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func historyPageName(req syncmsg.Request) string {
	return "page-" + historyDigest(fmt.Sprintf("%d/%d/%d", req.ChatID, req.Cur, req.Max)) + ".json"
}
func historyMutationName(target historyTarget) string {
	return "target-" + historyDigest(fmt.Sprintf("%d/%d", target.chatID, target.logID)) + ".json"
}

func openHistoryStore(dir string, accountID int64, deviceID string) (*historyStore, error) {
	if accountID <= 0 || deviceID == "" {
		return nil, ErrProtocol
	}
	if err := privatejson.ValidatePrivateDir(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, ErrHistoryStoreCorrupt
	}
	if err := privatejson.ValidatePrivateDir(dir); err != nil {
		return nil, err
	}
	s := &historyStore{dir: dir, binding: historyDigest(fmt.Sprintf("%d/%s", accountID, deviceID)), pages: make(map[string]acquiredPageRef), mutations: make(map[historyTarget]historyMutation), targets: make(map[historyTarget]bool), bulkCursors: make(map[int64]int64)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ErrHistoryStoreCorrupt
	}
	header, found, err := privatejson.Read[historyStoreHeader](filepath.Join(dir, "header.json"))
	if err != nil {
		return nil, err
	}
	if !found {
		if len(entries) != 0 {
			return nil, ErrHistoryStoreCorrupt
		}
		header = historyStoreHeader{Version: 1, Binding: s.binding}
		if err = privatejson.WriteInitial(filepath.Join(dir, "header.json"), header); err != nil {
			return nil, err
		}
	} else if header.Version != 1 || header.Binding != s.binding {
		return nil, ErrHistoryStoreCorrupt
	}
	pageCursors := map[int64]int64{}
	for _, entry := range entries {
		name := entry.Name()
		if name == "header.json" || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		switch {
		case strings.HasPrefix(name, "unavailable-"):
			r, found, err := privatejson.Read[historyUnavailableRecord](path)
			if err != nil || !found || !s.validUnavailable(r) || name != unavailableName(r.Request) {
				return nil, ErrHistoryStoreCorrupt
			}
			if !time.Now().Before(r.Observation.RetryAfter) {
				if err = os.Remove(path); err != nil {
					return nil, err
				}
				continue
			}
			if s.unavailable == nil {
				s.unavailable = make(map[syncmsg.Request]historyUnavailableRecord)
			}
			if len(s.unavailable) >= historyUnavailableMaximum {
				return nil, ErrHistoryStoreCapacity
			}
			s.unavailable[r.Request] = r
		case name == "bulk.json":
			progress, found, err := privatejson.Read[historyBulkProgress](path)
			if err != nil || !found || progress.Version != 1 || progress.Binding != s.binding {
				return nil, ErrHistoryStoreCorrupt
			}
			for id, cursor := range progress.Cursors {
				if id <= 0 || cursor < 0 {
					return nil, ErrHistoryStoreCorrupt
				}
			}
			for id, cursor := range progress.Cursors {
				s.bulkCursors[id] = max(s.bulkCursors[id], cursor)
			}
		case strings.HasPrefix(name, "page-"):
			page, found, err := privatejson.Read[acquiredPage](path)
			if err != nil || !found || page.Version != 1 || page.Binding != s.binding || name != historyPageName(page.Request) {
				return nil, ErrHistoryStoreCorrupt
			}
			last, err := validateAcquiredPage(page.Request, page.Response)
			if err != nil {
				return nil, ErrHistoryStoreCorrupt
			}
			s.pages[name] = acquiredPageRef{request: page.Request, last: last, name: name}
			pageCursors[page.Request.ChatID] = max(pageCursors[page.Request.ChatID], last)
			s.bulkCursors[page.Request.ChatID] = max(s.bulkCursors[page.Request.ChatID], last)
			for _, raw := range page.Response.ChatLogs {
				id, _ := syncmsg.LogID(raw)
				s.targets[historyTarget{page.Request.ChatID, id}] = true
			}
		case strings.HasPrefix(name, "target-"):
			mutation, found, err := privatejson.Read[historyMutation](path)
			if err != nil || !found || s.validateMutation(mutation) != nil || name != historyMutationName(historyTarget{mutation.ChatID, mutation.TargetID}) {
				return nil, ErrHistoryStoreCorrupt
			}
			s.mutations[historyTarget{mutation.ChatID, mutation.TargetID}] = mutation
			if mutation.Acquired || len(mutation.Replacement) > 0 {
				s.targets[historyTarget{mutation.ChatID, mutation.TargetID}] = true
			}
		default:
			return nil, ErrHistoryStoreCorrupt
		}
	}
	for id, cursor := range s.bulkCursors {
		if cursor > pageCursors[id] {
			return nil, ErrHistoryStoreCorrupt
		}
	}
	return s, nil
}
func validateAcquiredPage(req syncmsg.Request, response syncmsg.Response) (int64, error) {
	if req.Count != 0 {
		return 0, ErrProtocol
	}
	if _, err := req.MarshalBSON(); err != nil {
		return 0, err
	}
	var previous int64
	for _, raw := range response.ChatLogs {
		if err := raw.Validate(); err != nil {
			return 0, ErrHistoryStoreCorrupt
		}
		id, err := syncmsg.LogID(raw)
		if err != nil || (previous > 0 && id <= previous) {
			return 0, ErrHistoryStoreCorrupt
		}
		previous = id
	}
	page, err := decodeHistoryPage(req, response, max(1, len(response.ChatLogs)), nil)
	return page.Next, err
}
func writeHistoryObject(path string, value any) error {
	// Read has a fixed bound. Never write a record that cannot be reopened.
	raw, err := json.Marshal(value)
	if err != nil {
		return ErrHistoryStoreCorrupt
	}
	if len(raw)+1 > privatejson.MaxSize {
		return ErrHistoryStoreCapacity
	}
	return privatejson.WriteAtomic(path, value)
}
func (s *historyStore) validateMutation(m historyMutation) error {
	if m.Version != 1 || m.Binding != s.binding || m.ChatID <= 0 || m.TargetID <= 0 || m.Sequence < 0 || m.Revision < 0 || (!m.Deleted && m.Revision == 0 && len(m.Replacement) == 0) {
		return ErrHistoryStoreCorrupt
	}
	if len(m.Replacement) > 0 {
		event, err := events.DecodeChatLog(m.ChatID, m.Replacement)
		chat, id, ok := events.MessagePosition(event)
		if err != nil || !ok || chat != m.ChatID || id != m.TargetID || bsonfield.Int64OrZero(m.Replacement, "revision") < m.Revision {
			return ErrHistoryStoreCorrupt
		}
	}
	return nil
}
func (s *historyStore) writeMutation(m historyMutation) error {
	if len(m.Replacement) > 0 {
		m.Acquired = true
	}
	if err := s.validateMutation(m); err != nil {
		return err
	}
	if err := writeHistoryObject(filepath.Join(s.dir, historyMutationName(historyTarget{m.ChatID, m.TargetID})), m); err != nil {
		s.failed = err
		return err
	}
	s.mutations[historyTarget{m.ChatID, m.TargetID}] = m
	if m.Acquired {
		s.targets[historyTarget{m.ChatID, m.TargetID}] = true
	}
	return nil
}
func (s *historyStore) observe(event events.Event, replacement bson.Raw) error {
	var target historyTarget
	switch e := event.(type) {
	case events.MessageDeleted:
		target = historyTarget{e.ChatID, e.TargetLogID}
	case events.DeletedMessage:
		target = historyTarget{e.ChatID, e.LogID}
	case events.MessageEdited:
		target = historyTarget{e.ChatID, e.TargetLogID}
	default:
		return nil
	}
	old := s.mutations[target]
	if old.Deleted {
		return nil
	}
	m := historyMutation{Version: 1, Binding: s.binding, ChatID: target.chatID, TargetID: target.logID, Acquired: old.Acquired}
	switch e := event.(type) {
	case events.MessageDeleted:
		m.Replacement = old.Replacement
		m.Revision = old.Revision
		m.Deleted = true
		m.Sequence = e.LogID
	case events.DeletedMessage:
		m.Replacement = old.Replacement
		m.Revision = old.Revision
		m.Deleted = true
		m.Sequence = e.LogID
	case events.MessageEdited:
		if e.LogID < old.Sequence || e.TargetRevision < old.Revision {
			return nil
		}
		m.Sequence = e.LogID
		m.Revision = e.TargetRevision
		if old.Revision == m.Revision {
			m.Replacement = old.Replacement
		}
		if len(replacement) > 0 {
			m.Replacement = append(bson.Raw(nil), replacement...)
		}
		if m.Sequence == old.Sequence && m.Revision == old.Revision && len(replacement) == 0 {
			return nil
		}
	}
	return s.writeMutation(m)
}
func (s *historyStore) observeRecords(chatID int64, logs []bson.Raw) error {
	for _, raw := range logs {
		event, err := events.DecodeChatLog(chatID, raw)
		if err != nil {
			return err
		}
		if err = s.observe(event, nil); err != nil {
			return err
		}
		id, err := syncmsg.LogID(raw)
		if err != nil {
			return err
		}
		revision := bsonfield.Int64OrZero(raw, "revision")
		target := historyTarget{chatID, id}
		old := s.mutations[target]
		if old.Deleted && len(old.Replacement) == 0 {
			old.Replacement = append(bson.Raw(nil), raw...)
			if err = s.writeMutation(old); err != nil {
				return err
			}
		}
		if !old.Deleted && revision >= old.Revision {
			old.Version = 1
			old.Binding = s.binding
			old.ChatID = chatID
			old.TargetID = id
			old.Revision = revision
			old.Replacement = append(bson.Raw(nil), raw...)
			if err = s.writeMutation(old); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *historyStore) save(req syncmsg.Request, response syncmsg.Response) error {
	if len(response.ChatLogs) == 0 && response.MinLogID > req.Max {
		return s.saveUnavailable(req, response)
	}
	last, err := validateAcquiredPage(req, response)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	// Only records in the explicitly selected interval enter this store.
	selected := syncmsg.Response{MinLogID: response.MinLogID}
	for _, raw := range response.ChatLogs {
		id, _ := syncmsg.LogID(raw)
		if id > req.Cur && id <= req.Max {
			selected.ChatLogs = append(selected.ChatLogs, raw)
		}
	}
	if err = s.observeRecords(req.ChatID, selected.ChatLogs); err != nil {
		s.failed = err
		return err
	}
	name := historyPageName(req)
	page := acquiredPage{Version: 1, Binding: s.binding, Request: req, Response: selected}
	if err = writeHistoryObject(filepath.Join(s.dir, name), page); err != nil {
		s.failed = err
		return err
	}
	s.pages[name] = acquiredPageRef{request: req, last: last, name: name}
	s.bulkCursors[req.ChatID] = max(s.bulkCursors[req.ChatID], last)
	for _, raw := range selected.ChatLogs {
		id, _ := syncmsg.LogID(raw)
		s.targets[historyTarget{req.ChatID, id}] = true
	}
	return nil
}
func (s *historyStore) apply(raw bson.Raw, chatID int64) (bson.Raw, error) {
	id, err := syncmsg.LogID(raw)
	if err != nil {
		return nil, err
	}
	mutation, found := s.mutations[historyTarget{chatID, id}]
	if !found {
		return raw, nil
	}
	if mutation.Deleted {
		var doc bson.D
		if err = bson.Unmarshal(raw, &doc); err != nil {
			return nil, ErrHistoryStoreCorrupt
		}
		foundType := false
		for i := range doc {
			if doc[i].Key == "type" {
				foundType = true
				doc[i].Value = int32(bsonfield.Int64OrZero(raw, "type")) | messagetype.DeletedAllChatTypeFlag
			}
		}
		if !foundType {
			doc = append(doc, bson.E{Key: "type", Value: messagetype.DeletedAllChatTypeFlag})
		}
		return bson.Marshal(doc)
	}
	if len(mutation.Replacement) == 0 {
		return nil, ErrHistoryTargetUnresolved
	}
	return mutation.Replacement, nil
}
func (s *historyStore) lookup(req syncmsg.Request) (syncmsg.Response, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return syncmsg.Response{}, false, s.failed
	}
	// Cover only a connected acquired prefix. Sparse IDs are identities, not counts.
	end := req.Cur
	for {
		next := end
		for _, ref := range s.pages {
			if ref.request.ChatID == req.ChatID && ref.request.Cur <= end && ref.last > end {
				next = max(next, min(ref.last, req.Max))
			}
		}
		if next == end {
			break
		}
		end = next
	}
	if end == req.Cur {
		observation, err := s.unavailableLocked(req)
		if err != nil {
			return syncmsg.Response{}, false, err
		}
		if observation != nil {
			return syncmsg.Response{}, true, &HistoryUnavailableError{Observation: *observation}
		}
		return syncmsg.Response{}, false, nil
	}
	rows := make(map[int64]bson.Raw)
	floor := int64(0)
	for _, ref := range s.pages {
		if ref.request.ChatID != req.ChatID || ref.request.Cur >= end || ref.last <= req.Cur {
			continue
		}
		page, found, err := privatejson.Read[acquiredPage](filepath.Join(s.dir, ref.name))
		if err != nil || !found || page.Version != 1 || page.Binding != s.binding || historyPageName(page.Request) != ref.name {
			s.failed = ErrHistoryStoreCorrupt
			return syncmsg.Response{}, false, s.failed
		}
		if _, err = validateAcquiredPage(page.Request, page.Response); err != nil {
			s.failed = ErrHistoryStoreCorrupt
			return syncmsg.Response{}, false, s.failed
		}
		floor = max(floor, page.Response.MinLogID)
		for _, raw := range page.Response.ChatLogs {
			id, _ := syncmsg.LogID(raw)
			if id > req.Cur && id <= end {
				rows[id] = raw
			}
		}
	}
	barrier := int64(0)
	for target, mutation := range s.mutations {
		if target.chatID != req.ChatID || target.logID <= req.Cur || target.logID > end {
			continue
		}
		if !mutation.Deleted && len(mutation.Replacement) == 0 && s.targets[target] && (barrier == 0 || target.logID < barrier) {
			barrier = target.logID
		}
		if len(mutation.Replacement) > 0 {
			rows[target.logID] = mutation.Replacement
		}
	}
	ids := make([]int64, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := syncmsg.Response{MinLogID: floor}
	for _, id := range ids {
		if barrier > 0 && id >= barrier {
			break
		}
		projected, err := s.apply(rows[id], req.ChatID)
		if errors.Is(err, ErrHistoryTargetUnresolved) {
			break
		}
		if err != nil {
			return syncmsg.Response{}, false, err
		}
		out.ChatLogs = append(out.ChatLogs, append(bson.Raw(nil), projected...))
	}
	if len(out.ChatLogs) == 0 {
		return syncmsg.Response{}, false, nil
	}
	return out, true, nil
}

func (s *historyStore) observePacket(packet loco.Packet, event events.Event) error {
	var replacement bson.Raw
	if _, ok := event.(events.MessageEdited); ok {
		value, err := bson.Raw(packet.Body).LookupErr("modifiedChatLog")
		if err == nil && value.Type == bson.TypeEmbeddedDocument {
			replacement = value.Document()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	return s.observe(event, replacement)
}

func (c *Client) observeHistoryPacket(packet loco.Packet, event events.Event) error {
	if c.historyStore == nil {
		return nil
	}
	return c.historyStore.observePacket(packet, event)
}

func (s *historyStore) observeCurrentRecords(chatID int64, logs []bson.Raw) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	if err := s.observeRecords(chatID, logs); err != nil {
		s.failed = err
		return err
	}
	for _, raw := range logs {
		id, _ := syncmsg.LogID(raw)
		s.targets[historyTarget{chatID, id}] = true
	}
	return nil
}

func (s *historyStore) project(event events.Event) (events.Event, error) {
	chat, id, ok := events.MessagePosition(event)
	if !ok {
		return event, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return nil, s.failed
	}
	mutation, known := s.mutations[historyTarget{chat, id}]
	if !known {
		return event, nil
	}
	if len(mutation.Replacement) == 0 {
		if mutation.Deleted {
			switch e := event.(type) {
			case events.TextMessage:
				return events.DeletedMessage{ChatID: chat, LogID: id, AuthorID: e.AuthorID, SentAt: e.SentAt, BaseType: 1}, nil
			default:
				return nil, ErrHistoryTargetUnresolved
			}
		}
		return nil, ErrHistoryTargetUnresolved
	}
	raw, err := s.apply(mutation.Replacement, chat)
	if err != nil {
		return nil, err
	}
	return events.DecodeChatLog(chat, raw)
}
func (c *Client) projectAcquiredEvents(input []events.Event) ([]events.Event, error) {
	if c.historyStore == nil {
		return input, nil
	}
	output := make([]events.Event, len(input))
	for i, event := range input {
		current, err := c.historyStore.project(event)
		if err != nil {
			return nil, err
		}
		output[i] = current
	}
	return output, nil
}

type historyBulkProgress struct {
	Version uint32
	Binding string
	Cursors map[int64]int64
}

func (s *historyStore) bulkSnapshot() map[int64]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.bulkCursors)
}
func (s *historyStore) commitBulk(cursors map[int64]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	next := maps.Clone(s.bulkCursors)
	for id, cursor := range cursors {
		if id <= 0 || cursor < 0 {
			return ErrProtocol
		}
		next[id] = max(next[id], cursor)
	}
	if err := writeHistoryObject(filepath.Join(s.dir, "bulk.json"), historyBulkProgress{Version: 1, Binding: s.binding, Cursors: next}); err != nil {
		s.failed = err
		return err
	}
	s.bulkCursors = next
	return nil
}
