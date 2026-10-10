package connector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// kvStore reads and writes the framework's kv_store table with returned
// errors. database.KVQuery's Get and Set are not used for connector state that
// must be durable: Set logs the value on failure and both suppress errors, and
// values such as display checkpoints carry profile URLs that must never enter
// logs. The read-receipt and reaction-resync watermarks still use KVQuery
// directly: they are best-effort integers whose loss only repeats work, so a
// logged and suppressed failure is acceptable there.
type kvStore struct {
	db       *dbutil.Database
	bridgeID networkid.BridgeID
}

var errNotDurable = errors.New("connector: kv_store value was not durable")

func newKVStore(q *database.KVQuery) kvStore {
	return kvStore{db: q.Database, bridgeID: q.BridgeID}
}

func (s kvStore) get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(ctx, "SELECT value FROM kv_store WHERE bridge_id=$1 AND key=$2", s.bridgeID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// put upserts value and reads it back, returning errNotDurable unless the
// stored value matches.
func (s kvStore) put(ctx context.Context, key, value string) error {
	if _, err := s.db.Exec(ctx, "INSERT INTO kv_store (bridge_id,key,value) VALUES ($1,$2,$3) ON CONFLICT (bridge_id,key) DO UPDATE SET value=excluded.value", s.bridgeID, key, value); err != nil {
		return err
	}
	saved, found, err := s.get(ctx, key)
	if err != nil {
		return err
	}
	if !found || saved != value {
		return errNotDurable
	}
	return nil
}

// putIfAbsent inserts value only when key is unset and reports whether it did.
func (s kvStore) putIfAbsent(ctx context.Context, key, value string) (bool, error) {
	result, err := s.db.Exec(ctx, "INSERT INTO kv_store (bridge_id,key,value) VALUES ($1,$2,$3) ON CONFLICT (bridge_id,key) DO NOTHING", s.bridgeID, key, value)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return inserted == 1, nil
}

// kvTimeStamp formats t as fixed-width seconds so stored stamps sort
// lexically in time order.
func kvTimeStamp(t time.Time) string {
	return fmt.Sprintf("%020d", t.Unix())
}

// deleteOlderThan removes keys starting with prefix whose value sorts before
// stamp. Callers store fixed-width stamps so lexical order is time order.
func (s kvStore) deleteOlderThan(ctx context.Context, prefix, stamp string) error {
	_, err := s.db.Exec(ctx, "DELETE FROM kv_store WHERE bridge_id=$1 AND key LIKE $2 AND value < $3", s.bridgeID, prefix+"%", stamp)
	return err
}
