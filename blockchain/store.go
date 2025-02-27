package blockchain

import (
	"encoding/json"
	"fmt"
	"sync"

	"blockchain-core/blockchain/db"
)

// Store represents the blockchain store
type Store struct {
	db     db.Database
	mu     sync.RWMutex
	logger db.Logger
}

// NewStore creates a new blockchain store
func NewStore(database db.Database) (*Store, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	return &Store{
		db: database,
	}, nil
}

// SaveBlock saves a block to the store
func (s *Store) SaveBlock(block *Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Serialize block
	data, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("failed to marshal block: %w", err)
	}

	// Create key with block number
	heightBytes := []byte(fmt.Sprintf("%020d", block.Number()))
	key := db.CreateKey(db.BlockPrefix, heightBytes)

	// Save block
	if err := s.db.Put(key, data); err != nil {
		return fmt.Errorf("failed to save block: %w", err)
	}

	return nil
}

// GetBlock retrieves a block by height
func (s *Store) GetBlock(height uint64) (*Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create key with block height
	heightBytes := []byte(fmt.Sprintf("%020d", height))
	key := db.CreateKey(db.BlockPrefix, heightBytes)

	// Get block data
	data, err := s.db.Get(key)
	if err != nil {
		if err == db.ErrKeyNotFound {
			return nil, fmt.Errorf("block not found at height %d", height)
		}
		return nil, fmt.Errorf("failed to get block: %w", err)
	}

	// Deserialize block
	var block Block
	if err := json.Unmarshal(data, &block); err != nil {
		return nil, fmt.Errorf("failed to unmarshal block: %w", err)
	}

	return &block, nil
}

// SaveTransaction saves a transaction to the store
func (s *Store) SaveTransaction(tx *Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Serialize transaction
	data, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("failed to marshal transaction: %w", err)
	}

	// Get transaction hash
	key := db.CreateKey(db.TxPrefix, []byte(tx.Hash()))

	// Save transaction
	if err := s.db.Put(key, data); err != nil {
		return fmt.Errorf("failed to save transaction: %w", err)
	}

	return nil
}

// GetTransaction retrieves a transaction by hash
func (s *Store) GetTransaction(hashStr string) (*Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create key with transaction hash
	key := db.CreateKey(db.TxPrefix, []byte(hashStr))

	// Get transaction data
	data, err := s.db.Get(key)
	if err != nil {
		if err == db.ErrKeyNotFound {
			return nil, fmt.Errorf("transaction not found with hash %s", hashStr)
		}
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	// Deserialize transaction
	var tx Transaction
	if err := json.Unmarshal(data, &tx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transaction: %w", err)
	}

	return &tx, nil
}

// SaveUTXO saves a UTXO to the store
func (s *Store) SaveUTXO(utxo *UTXO) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Serialize UTXO
	data, err := json.Marshal(utxo)
	if err != nil {
		return fmt.Errorf("failed to marshal UTXO: %w", err)
	}

	// Create composite key: txHash + outputIndex
	txHash := utxo.TransactionID // Using the string transaction ID
	key := db.CreateKey(db.UTXOPrefix, append([]byte(txHash), byte(utxo.OutputIndex)))

	// Save UTXO
	if err := s.db.Put(key, data); err != nil {
		return fmt.Errorf("failed to save UTXO: %w", err)
	}

	return nil
}

// GetUTXO retrieves a UTXO by transaction ID and output index
func (s *Store) GetUTXO(txID string, outputIndex uint32) (*UTXO, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create composite key
	key := db.CreateKey(db.UTXOPrefix, append([]byte(txID), byte(outputIndex)))

	// Get UTXO data
	data, err := s.db.Get(key)
	if err != nil {
		if err == db.ErrKeyNotFound {
			return nil, fmt.Errorf("UTXO not found for tx %s output %d", txID, outputIndex)
		}
		return nil, fmt.Errorf("failed to get UTXO: %w", err)
	}

	// Deserialize UTXO
	var utxo UTXO
	if err := json.Unmarshal(data, &utxo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal UTXO: %w", err)
	}

	return &utxo, nil
}

// SaveState saves blockchain state
func (s *Store) SaveState(state *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Serialize state
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Create key for state
	key := db.CreateKey(db.StatePrefix, []byte("current"))

	// Save state
	if err := s.db.Put(key, data); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	return nil
}

// GetState retrieves the current blockchain state
func (s *Store) GetState() (*State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create key for state
	key := db.CreateKey(db.StatePrefix, []byte("current"))

	// Get state data
	data, err := s.db.Get(key)
	if err != nil {
		if err == db.ErrKeyNotFound {
			return nil, fmt.Errorf("blockchain state not found")
		}
		return nil, fmt.Errorf("failed to get state: %w", err)
	}

	// Deserialize state
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &state, nil
}

// Close closes the store
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Close()
}

// Batch creates a new batch operation
func (s *Store) Batch() db.Batch {
	return s.db.NewBatch()
}

// Iterator creates a new iterator
func (s *Store) Iterator() db.Iterator {
	return s.db.NewIterator()
}
