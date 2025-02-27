package db

import (
	"context"
	"fmt"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/errors"
	"github.com/syndtr/goleveldb/leveldb/filter"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// levelDB represents a LevelDB database instance
type levelDB struct {
	db      *leveldb.DB
	path    string
	ctx     context.Context
	cancel  context.CancelFunc
	logger  Logger
	closed  bool
	mu      sync.RWMutex
	options *opt.Options
}

// NewLevelDB creates a new LevelDB instance
func NewLevelDB(config *Config) (Database, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create database options
	options := &opt.Options{
		BlockCacheCapacity:     int(config.CacheSize) * opt.MiB,
		OpenFilesCacheCapacity: config.MaxOpenFiles,
		CompactionTableSize:    32 * opt.MiB,
		WriteBuffer:            64 * opt.MiB,
		Filter:                 filter.NewBloomFilter(10),
	}

	if config.Compression {
		options.Compression = opt.SnappyCompression
	}

	// Open database
	db, err := leveldb.OpenFile(config.GetDBPath("leveldb"), options)
	if err != nil {
		if errors.IsCorrupted(err) {
			// Try to recover corrupted database
			db, err = leveldb.RecoverFile(config.GetDBPath("leveldb"), options)
			if err != nil {
				return nil, fmt.Errorf("failed to recover database: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to open database: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &levelDB{
		db:      db,
		path:    config.GetDBPath("leveldb"),
		ctx:     ctx,
		cancel:  cancel,
		logger:  config.Logger,
		options: options,
	}, nil
}

// Put stores a key-value pair
func (ldb *levelDB) Put(key []byte, value []byte) error {
	ldb.mu.RLock()
	defer ldb.mu.RUnlock()

	if ldb.closed {
		return ErrDatabaseClosed
	}

	if len(key) == 0 {
		return ErrInvalidKey
	}

	if len(value) == 0 {
		return ErrInvalidValue
	}

	return ldb.db.Put(key, value, nil)
}

// Get retrieves a value by key
func (ldb *levelDB) Get(key []byte) ([]byte, error) {
	ldb.mu.RLock()
	defer ldb.mu.RUnlock()

	if ldb.closed {
		return nil, ErrDatabaseClosed
	}

	if len(key) == 0 {
		return nil, ErrInvalidKey
	}

	value, err := ldb.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return nil, ErrKeyNotFound
	}
	return value, err
}

// Delete removes a key-value pair
func (ldb *levelDB) Delete(key []byte) error {
	ldb.mu.RLock()
	defer ldb.mu.RUnlock()

	if ldb.closed {
		return ErrDatabaseClosed
	}

	if len(key) == 0 {
		return ErrInvalidKey
	}

	return ldb.db.Delete(key, nil)
}

// Has checks if a key exists
func (ldb *levelDB) Has(key []byte) (bool, error) {
	ldb.mu.RLock()
	defer ldb.mu.RUnlock()

	if ldb.closed {
		return false, ErrDatabaseClosed
	}

	if len(key) == 0 {
		return false, ErrInvalidKey
	}

	return ldb.db.Has(key, nil)
}

// NewBatch creates a new batch operation
func (ldb *levelDB) NewBatch() Batch {
	return &levelDBBatch{
		db:    ldb.db,
		batch: new(leveldb.Batch),
	}
}

// NewIterator creates a new iterator
func (ldb *levelDB) NewIterator() Iterator {
	return &levelDBIterator{
		iter: ldb.db.NewIterator(nil, nil),
	}
}

// Close closes the database
func (ldb *levelDB) Close() error {
	ldb.mu.Lock()
	defer ldb.mu.Unlock()

	if ldb.closed {
		return nil
	}

	ldb.cancel()
	ldb.closed = true
	return ldb.db.Close()
}

// Path returns the database path
func (ldb *levelDB) Path() string {
	return ldb.path
}

// Stats returns database statistics
func (ldb *levelDB) Stats() (map[string]interface{}, error) {
	ldb.mu.RLock()
	defer ldb.mu.RUnlock()

	if ldb.closed {
		return nil, ErrDatabaseClosed
	}

	stats := make(map[string]interface{})
	dbStats := &leveldb.DBStats{}
	if err := ldb.db.Stats(dbStats); err != nil {
		return nil, err
	}
	stats["leveldb.stats"] = dbStats
	return stats, nil
}

// Compact compacts the database
func (ldb *levelDB) Compact(start []byte, limit []byte) error {
	ldb.mu.RLock()
	defer ldb.mu.RUnlock()

	if ldb.closed {
		return ErrDatabaseClosed
	}

	ldb.db.CompactRange(util.Range{Start: start, Limit: limit})
	return nil
}

// WithContext returns a new database instance with the given context
func (ldb *levelDB) WithContext(ctx context.Context) Database {
	ldb.mu.Lock()
	defer ldb.mu.Unlock()

	ldb.ctx = ctx
	return ldb
}

// levelDBBatch represents a LevelDB batch operation
type levelDBBatch struct {
	db    *leveldb.DB
	batch *leveldb.Batch
}

func (b *levelDBBatch) Put(key []byte, value []byte) error {
	if len(key) == 0 {
		return ErrInvalidKey
	}
	if len(value) == 0 {
		return ErrInvalidValue
	}
	b.batch.Put(key, value)
	return nil
}

func (b *levelDBBatch) Delete(key []byte) error {
	if len(key) == 0 {
		return ErrInvalidKey
	}
	b.batch.Delete(key)
	return nil
}

func (b *levelDBBatch) ValueSize() int {
	return b.batch.Len()
}

func (b *levelDBBatch) Write() error {
	return b.db.Write(b.batch, nil)
}

func (b *levelDBBatch) Reset() {
	b.batch.Reset()
}

// levelDBIterator represents a LevelDB iterator
type levelDBIterator struct {
	iter iterator.Iterator
}

func (it *levelDBIterator) Next() bool {
	return it.iter.Next()
}

func (it *levelDBIterator) Error() error {
	return it.iter.Error()
}

func (it *levelDBIterator) Key() []byte {
	return it.iter.Key()
}

func (it *levelDBIterator) Value() []byte {
	return it.iter.Value()
}

func (it *levelDBIterator) Release() {
	it.iter.Release()
}

func (it *levelDBIterator) Seek(key []byte) bool {
	return it.iter.Seek(key)
}

// NewDatabase creates a new database instance based on the given configuration
func NewDatabase(config *Config) (Database, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	switch config.Type {
	case LevelDB:
		return NewLevelDB(config)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", config.Type)
	}
}
