package gas

import (
	"fmt"
	"sync"
)

// BlockGasTracker tracks gas usage within a block
type BlockGasTracker struct {
	mu          sync.RWMutex
	gasUsed     uint64
	gasLimit    uint64
	txCount     int
	gasModel    *GasModel
}

// NewBlockGasTracker creates a new block gas tracker
func NewBlockGasTracker(gasLimit uint64, model *GasModel) *BlockGasTracker {
	return &BlockGasTracker{
		gasLimit: gasLimit,
		gasModel: model,
	}
}

// AddTransaction attempts to add a transaction's gas to the block
func (bt *BlockGasTracker) AddTransaction(gasInfo *TxGasInfo) error {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	
	// Check if adding this transaction would exceed block gas limit
	if bt.gasUsed+gasInfo.GasUsed > bt.gasLimit {
		return fmt.Errorf("block gas limit exceeded")
	}
	
	// Add gas used
	bt.gasUsed += gasInfo.GasUsed
	bt.txCount++
	
	return nil
}

// GetUtilization returns current block gas utilization
func (bt *BlockGasTracker) GetUtilization() float64 {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	return float64(bt.gasUsed) / float64(bt.gasLimit)
}

// Reset resets the tracker for a new block
func (bt *BlockGasTracker) Reset() {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	
	bt.gasUsed = 0
	bt.txCount = 0
} 