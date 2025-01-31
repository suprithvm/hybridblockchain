package gas

import (
    "math"
)

// BlockGasCalculator handles block-specific gas calculations
type BlockGasCalculator struct {
    baseGasLimit uint64
}

// NewBlockGasCalculator creates a new block gas calculator
func NewBlockGasCalculator(baseLimit uint64) *BlockGasCalculator {
    return &BlockGasCalculator{
        baseGasLimit: baseLimit,
    }
}

// CalculateDynamicGasLimit adjusts gas limit based on previous block usage
func (bgc *BlockGasCalculator) CalculateDynamicGasLimit(prevGasUsed, prevGasLimit uint64) uint64 {
    if prevGasLimit == 0 {
        return bgc.baseGasLimit
    }

    utilization := float64(prevGasUsed) / float64(prevGasLimit)
    
    // Adjust based on previous block utilization
    if utilization > 0.8 { // Above target
        newLimit := prevGasLimit + (prevGasLimit / 20) // +5%
        return uint64(math.Min(float64(newLimit), float64(bgc.baseGasLimit*2)))
    } else if utilization < 0.4 { // Below target
        newLimit := prevGasLimit - (prevGasLimit / 20) // -5%
        return uint64(math.Max(float64(newLimit), float64(bgc.baseGasLimit)))
    }
    
    return prevGasLimit
}

// CalculateBlockSize determines block size based on network conditions
func (bgc *BlockGasCalculator) CalculateBlockSize(pendingTxCount int) uint64 {
    // Base size for empty block
    baseSize := uint64(10 * 1024) // 10KB base size
    
    // Add space for transactions
    txSpace := uint64(pendingTxCount * 250) // Assume 250 bytes per tx
    
    // Cap at max block size
    maxSize := uint64(1 * 1024 * 1024) // 1MB max
    return uint64(math.Min(float64(baseSize+txSpace), float64(maxSize)))
} 