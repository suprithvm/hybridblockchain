package blockchain

import (
	"fmt"
	"time"
)

// State represents the current state of the blockchain
type State struct {
	// Block-related state
	Height        uint64    `json:"height"`          // Current block height
	LastBlockHash string    `json:"last_block_hash"` // Hash of the last block
	LastBlockTime time.Time `json:"last_block_time"` // Timestamp of the last block

	// Chain-related state
	TotalTransactions uint64 `json:"total_transactions"` // Total number of transactions
	TotalBlocks       uint64 `json:"total_blocks"`       // Total number of blocks
	TotalSupply       uint64 `json:"total_supply"`       // Total coin supply

	// UTXO-related state
	UTXOSetRoot string `json:"utxo_set_root"` // Merkle root of UTXO set
	UTXOCount   uint64 `json:"utxo_count"`    // Number of unspent outputs

	// Consensus-related state
	StateRoot     string `json:"state_root"`     // Root hash of state trie
	ValidatorRoot string `json:"validator_root"` // Root hash of validator set
	Difficulty    uint64 `json:"difficulty"`     // Current mining difficulty
	GasPrice      uint64 `json:"gas_price"`      // Current minimum gas price

	// Network-related state
	NetworkVersion uint32    `json:"network_version"` // Current network protocol version
	LastUpdate     time.Time `json:"last_update"`     // Last state update time
}

// NewState creates a new blockchain state
func NewState() *State {
	return &State{
		Height:         0,
		LastBlockTime:  time.Now(),
		LastUpdate:     time.Now(),
		NetworkVersion: 1,
		Difficulty:     1,
		GasPrice:       1000, // Default gas price
	}
}

// UpdateWithBlock updates the state with a new block
func (s *State) UpdateWithBlock(block *Block) {
	s.Height = block.Header.BlockNumber
	s.LastBlockHash = block.Hash()
	s.LastBlockTime = time.Unix(block.Header.Timestamp, 0)
	s.TotalBlocks++
	s.TotalTransactions += uint64(len(block.Body.Transactions.GetAllTransactions()))
	s.LastUpdate = time.Now()

	// Update difficulty and gas price if needed
	s.Difficulty = uint64(block.Header.Difficulty)
	if block.Header.GasUsed > 0 {
		// Simple gas price adjustment based on block usage
		if float64(block.Header.GasUsed)/float64(block.Header.GasLimit) > 0.8 {
			s.GasPrice = s.GasPrice * 110 / 100 // Increase by 10%
		} else if float64(block.Header.GasUsed)/float64(block.Header.GasLimit) < 0.3 {
			s.GasPrice = s.GasPrice * 90 / 100 // Decrease by 10%
		}
	}
}

// Copy creates a deep copy of the state
func (s *State) Copy() *State {
	newState := *s
	return &newState
}

// Validate checks if the state is valid
func (s *State) Validate() error {
	if s.Height < 0 {
		return fmt.Errorf("invalid height: %d", s.Height)
	}
	if s.TotalBlocks < s.Height {
		return fmt.Errorf("total blocks less than height")
	}
	if s.Difficulty < 1 {
		return fmt.Errorf("invalid difficulty: %d", s.Difficulty)
	}
	if s.GasPrice < 1 {
		return fmt.Errorf("invalid gas price: %d", s.GasPrice)
	}
	return nil
}
