package client

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// This is a local freshness policy, not a server retention duration.
const historyUnavailableFreshness = 5 * time.Minute
const historyUnavailableMaximum = 1024

// HistoryUnavailable is one observed empty response for an exact interval.
// It does not establish that other intervals or rooms are unavailable.
type HistoryUnavailable struct {
	MinLogID   int64
	ObservedAt time.Time
	RetryAfter time.Time
}

type HistoryUnavailableError struct{ Observation HistoryUnavailable }

func (e *HistoryUnavailableError) Error() string {
	return "client: selected history is below an observed server floor; cached observation expires at " + e.Observation.RetryAfter.UTC().Format(time.RFC3339)
}
func (*HistoryUnavailableError) Unwrap() []error {
	return []error{ErrHistoryBelowFloor, ErrGapUnresolved}
}

type historyUnavailableRecord struct {
	Version     uint32
	Binding     string
	Request     syncmsg.Request
	Observation HistoryUnavailable
}

func unavailableName(req syncmsg.Request) string { return "unavailable-" + historyPageName(req)[5:] }
func (s *historyStore) validUnavailable(r historyUnavailableRecord) bool {
	o := r.Observation
	_, err := r.Request.MarshalBSON()
	return err == nil && r.Request.Count == 0 && r.Version == 1 && r.Binding == s.binding && o.MinLogID > r.Request.Max && !o.ObservedAt.IsZero() && !o.ObservedAt.After(time.Now().Add(time.Minute)) && o.RetryAfter.Equal(o.ObservedAt.Add(historyUnavailableFreshness))
}
func (s *historyStore) saveUnavailable(req syncmsg.Request, response syncmsg.Response) error {
	if _, err := req.MarshalBSON(); err != nil {
		return err
	}
	if req.Count != 0 || len(response.ChatLogs) != 0 || response.MinLogID <= req.Max {
		return ErrProtocol
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	now := time.Now().UTC()
	for old, r := range s.unavailable {
		if !now.Before(r.Observation.RetryAfter) {
			if err := os.Remove(filepath.Join(s.dir, unavailableName(old))); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.failed = err
				return err
			}
			delete(s.unavailable, old)
		}
	}
	if _, found := s.unavailable[req]; !found && len(s.unavailable) >= historyUnavailableMaximum {
		return ErrHistoryStoreCapacity
	}
	o := HistoryUnavailable{MinLogID: response.MinLogID, ObservedAt: now, RetryAfter: now.Add(historyUnavailableFreshness)}
	r := historyUnavailableRecord{Version: 1, Binding: s.binding, Request: req, Observation: o}
	if err := writeHistoryObject(filepath.Join(s.dir, unavailableName(req)), r); err != nil {
		s.failed = err
		return err
	}
	if s.unavailable == nil {
		s.unavailable = make(map[syncmsg.Request]historyUnavailableRecord)
	}
	s.unavailable[req] = r
	return &HistoryUnavailableError{Observation: o}
}

// The caller holds the store lock. Positive acquired rows take precedence.
func (s *historyStore) unavailableLocked(req syncmsg.Request) (*HistoryUnavailable, error) {
	if _, found := s.unavailable[req]; !found {
		return nil, nil
	}
	r, found, err := privatejson.Read[historyUnavailableRecord](filepath.Join(s.dir, unavailableName(req)))
	if err != nil || !found || r.Request != req || !s.validUnavailable(r) {
		s.failed = ErrHistoryStoreCorrupt
		return nil, s.failed
	}
	now := time.Now()
	if now.Before(r.Observation.ObservedAt) {
		return nil, ErrHistoryStoreCorrupt
	}
	if !now.Before(r.Observation.RetryAfter) {
		return nil, nil
	}
	o := r.Observation
	return &o, nil
}
