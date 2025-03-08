package syncprotocol

import (
	"fmt"
	"time"
)

// SyncRequest represents a request to sync blocks
type SyncRequest struct {
	StartHeight uint64
	EndHeight   uint64
}

// SyncResponse represents a response to a sync request
type SyncResponse struct {
	Success bool
	Error   string
	Height  uint64
}

// BlockRequest represents a request for a batch of blocks
type BlockRequest struct {
	StartHeight uint64
	EndHeight   uint64
}

// BlockResponse represents a response containing a batch of blocks
type BlockResponse struct {
	Success bool
	Error   string
	Blocks  []Block
}

// Block represents a blockchain block in the sync protocol
type Block struct {
	Hash       string
	PrevHash   string
	Height     uint64
	Timestamp  time.Time
	Data       []byte
	Signature  []byte
	MerkleRoot string
	StateRoot  string
}

// ValidateBlock validates the block structure
func ValidateBlock(block *Block) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	if block.Hash == "" {
		return fmt.Errorf("block hash is empty")
	}

	if block.Height > 0 && block.PrevHash == "" {
		return fmt.Errorf("previous hash is empty for non-genesis block")
	}

	if block.MerkleRoot == "" {
		return fmt.Errorf("merkle root is empty")
	}

	if block.StateRoot == "" {
		return fmt.Errorf("state root is empty")
	}

	if block.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is zero")
	}

	return nil
}
