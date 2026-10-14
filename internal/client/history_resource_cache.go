package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/privatejson"
)

const historyResourceMaximum = 64 << 20
const historyResourceBudget = 256 << 20
const historyResourceHeaderSize = 104

var errHistoryResourceCache = errors.New("client: invalid private history resource cache")

type historyResourceCache struct {
	mu           sync.Mutex
	dir, binding string
}

func openHistoryResourceCache(dir, binding string) (*historyResourceCache, error) {
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, errHistoryResourceCache
	}
	if err := privatejson.ValidatePrivateDir(dir); err != nil {
		return nil, err
	}
	header, found, err := privatejson.Read[historyStoreHeader](filepath.Join(dir, "header.json"))
	if err != nil {
		return nil, err
	}
	if !found {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			return nil, errHistoryResourceCache
		}
		if err = privatejson.WriteInitial(filepath.Join(dir, "header.json"), historyStoreHeader{Version: 1, Binding: binding}); err != nil {
			return nil, err
		}
	}
	if found && (header.Version != 1 || header.Binding != binding) {
		return nil, errHistoryResourceCache
	}
	return &historyResourceCache{dir: dir, binding: binding}, nil
}
func (c *historyResourceCache) get(key string) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := privatejson.ValidatePrivateDir(c.dir); err != nil {
		return nil, false, err
	}
	path := filepath.Join(c.dir, historyDigest(key)+".resource")
	if err := privatejson.ValidatePrivateFile(path); err != nil {
		if errors.Is(err, privatejson.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, errHistoryResourceCache
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.Size() < historyResourceHeaderSize || info.Size() > historyResourceMaximum+historyResourceHeaderSize {
		return nil, false, errHistoryResourceCache
	}
	header := make([]byte, historyResourceHeaderSize)
	if _, err = io.ReadFull(file, header); err != nil {
		return nil, false, errHistoryResourceCache
	}
	size := binary.LittleEndian.Uint64(header[64:72])
	if string(header[:64]) != c.binding || size != uint64(info.Size()-historyResourceHeaderSize) {
		return nil, false, errHistoryResourceCache
	}
	data, err := io.ReadAll(io.LimitReader(file, historyResourceMaximum+1))
	if err != nil || len(data) > historyResourceMaximum {
		return nil, false, errHistoryResourceCache
	}
	hash := sha256.Sum256(data)
	if !bytes.Equal(header[72:], hash[:]) {
		return nil, false, errHistoryResourceCache
	}
	if err = os.Chtimes(path, time.Now(), time.Now()); err != nil {
		return nil, false, errHistoryResourceCache
	}
	return data, true, nil
}
func (c *historyResourceCache) put(key string, data []byte) error {
	if len(data) > historyResourceMaximum {
		return errHistoryResourceCache
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := privatejson.ValidatePrivateDir(c.dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil || len(entries) > 4096 {
		return errHistoryResourceCache
	}
	type item struct {
		path string
		size int64
		at   time.Time
	}
	target := filepath.Join(c.dir, historyDigest(key)+".resource")
	var resources []item
	var total int64
	for _, entry := range entries {
		if entry.Name() == "header.json" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".resource") {
			return errHistoryResourceCache
		}
		path := filepath.Join(c.dir, entry.Name())
		if err = privatejson.ValidatePrivateFile(path); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return errHistoryResourceCache
		}
		if path == target {
			continue
		}
		total += info.Size()
		resources = append(resources, item{path, info.Size(), info.ModTime()})
	}
	slices.SortFunc(resources, func(a, b item) int { return a.at.Compare(b.at) })
	for _, resource := range resources {
		if total+int64(len(data)+historyResourceHeaderSize) <= historyResourceBudget {
			break
		}
		if err = os.Remove(resource.path); err != nil {
			return errHistoryResourceCache
		}
		total -= resource.size
	}
	tmp, err := os.CreateTemp(c.dir, ".resource-")
	if err != nil {
		return errHistoryResourceCache
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	header := make([]byte, historyResourceHeaderSize)
	copy(header, c.binding)
	binary.LittleEndian.PutUint64(header[64:72], uint64(len(data)))
	hash := sha256.Sum256(data)
	copy(header[72:], hash[:])
	if _, err = tmp.Write(header); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err != nil {
		return errHistoryResourceCache
	}
	if err = os.Rename(name, target); err != nil {
		return errHistoryResourceCache
	}
	dir, err := os.Open(c.dir)
	if err != nil {
		return errHistoryResourceCache
	}
	defer func() { _ = dir.Close() }()
	if err = dir.Sync(); err != nil {
		return errHistoryResourceCache
	}
	return nil
}
