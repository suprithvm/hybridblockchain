package db

import (
	"context"
	"fmt"
	"path/filepath"
)

// DBType represents the type of database
type DBType string

const (
	// Database types
	LevelDB  DBType = "leveldb"
	BadgerDB DBType = "badger"
)

// Database represents the interface for database operations
type Database interface {
	// Core operations
	Put(key []byte, value []byte) error
	Get(key []byte) ([]byte, error)
	Delete(key []byte) error
	Has(key []byte) (bool, error)

	// Batch operations
	NewBatch() Batch

	// Iterator operations
	NewIterator() Iterator

	// Database management
	Close() error
	Path() string
	Stats() (map[string]interface{}, error)
	Compact(start []byte, limit []byte) error

	// Context operations
	WithContext(ctx context.Context) Database
}

// Batch represents a batch of database operations
type Batch interface {
	Put(key []byte, value []byte) error
	Delete(key []byte) error
	ValueSize() int
	Write() error
	Reset()
}

// Iterator represents a iterator over database contents
type Iterator interface {
	Next() bool
	Error() error
	Key() []byte
	Value() []byte
	Release()
	Seek(key []byte) bool
}

// Logger interface for database logging
type Logger interface {
	Info(v ...interface{})
	Error(v ...interface{})
	Debug(v ...interface{})
}

// Snapshot represents a database snapshot
type Snapshot interface {
	Get(key []byte) ([]byte, error)
	Release()
}

// Config represents database configuration
type Config struct {
	// Database type (leveldb, badger, etc)
	Type DBType

	// Path to database files
	Path string

	// Maximum size of the database cache in MB
	CacheSize int64

	// Maximum number of open files
	MaxOpenFiles int

	// Enable compression
	Compression bool

	// Database name
	Name string

	// Logger instance
	Logger Logger
}

// DefaultConfig returns default database configuration
func DefaultConfig(dbPath string) *Config {
	return &Config{
		Type:         LevelDB,
		Path:         dbPath,
		CacheSize:    512, // 512MB cache
		MaxOpenFiles: 64,  // 64 open files
		Compression:  true,
		Name:         "blockchain",
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Type == "" {
		return fmt.Errorf("database type not specified")
	}

	if c.Path == "" {
		return fmt.Errorf("database path not specified")
	}

	if c.CacheSize <= 0 {
		return fmt.Errorf("invalid cache size: %d", c.CacheSize)
	}

	if c.MaxOpenFiles <= 0 {
		return fmt.Errorf("invalid max open files: %d", c.MaxOpenFiles)
	}

	return nil
}

// GetDBPath returns the full path for a specific database
func (c *Config) GetDBPath(dbName string) string {
	return filepath.Join(c.Path, c.Name, dbName)
}

// Error types
var (
	ErrKeyNotFound    = fmt.Errorf("key not found")
	ErrDatabaseClosed = fmt.Errorf("database closed")
	ErrBatchTooLarge  = fmt.Errorf("batch too large")
	ErrInvalidKey     = fmt.Errorf("invalid key")
	ErrInvalidValue   = fmt.Errorf("invalid value")
	ErrSnapshotClosed = fmt.Errorf("snapshot closed")
	ErrIteratorClosed = fmt.Errorf("iterator closed")
	ErrDatabaseExists = fmt.Errorf("database already exists")
	ErrInvalidOptions = fmt.Errorf("invalid options")
)

// KeyPrefix represents database key prefixes for different data types
type KeyPrefix byte

const (
	// Database prefixes
	BlockPrefix      KeyPrefix = 0x01
	TxPrefix         KeyPrefix = 0x02
	UTXOPrefix       KeyPrefix = 0x03
	StatePrefix      KeyPrefix = 0x04
	MetadataPrefix   KeyPrefix = 0x05
	CheckpointPrefix KeyPrefix = 0x06
	ValidatorPrefix  KeyPrefix = 0x07
	IndexPrefix      KeyPrefix = 0x08
)

// CreateKey creates a prefixed key
func CreateKey(prefix KeyPrefix, key []byte) []byte {
	result := make([]byte, len(key)+1)
	result[0] = byte(prefix)
	copy(result[1:], key)
	return result
}

// SplitKey splits a prefixed key
func SplitKey(key []byte) (KeyPrefix, []byte) {
	if len(key) == 0 {
		return 0, nil
	}
	return KeyPrefix(key[0]), key[1:]
}
