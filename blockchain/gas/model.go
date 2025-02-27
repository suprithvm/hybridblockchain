package gas

import (
	"math"
	"sync"
)

const (
	// Base gas costs
	BaseTxGas     = 21000   // Base cost for any transaction
	TxDataByteGas = 16      // Cost per byte of transaction data
	TxSigGas      = 2000    // Cost for signature verification
	BaseGasLimit  = 1000000 // Base gas limit for blocks

	// Priority levels
	PriorityLow    = 0
	PriorityNormal = 1
	PriorityHigh   = 2

	// Gas price adjustments
	MinGasPrice    = 1000   // Minimum gas price in smallest unit
	MaxGasPrice    = 100000 // Maximum gas price to prevent spam
	TargetBlockGas = 0.8    // Target gas usage per block (80%)

	// Adjustment factors
	GasPriceAdjustmentFactor = 0.2 // 20% adjustment up/down for more noticeable changes
)

// GasModel manages gas pricing and calculations
type GasModel struct {
	mu              sync.RWMutex
	currentGasPrice uint64
	blockGasUsed    uint64
	blockGasLimit   uint64
}

// NewGasModel creates a new gas model instance
func NewGasModel(initialGasPrice uint64, blockGasLimit uint64) *GasModel {
	return &GasModel{
		currentGasPrice: initialGasPrice,
		blockGasLimit:   blockGasLimit,
	}
}

// CalculateBaseFee calculates the minimum required gas fee
func (g *GasModel) CalculateBaseFee(dataSize int) uint64 {
	return BaseTxGas + (uint64(dataSize) * TxDataByteGas) + TxSigGas
}

// CalculatePriorityFee calculates additional fee based on priority
func (g *GasModel) CalculatePriorityFee(priority int) uint64 {
	switch priority {
	case PriorityHigh:
		return g.currentGasPrice * 2
	case PriorityNormal:
		return g.currentGasPrice
	default:
		return g.currentGasPrice / 2
	}
}

// UpdateGasPrice adjusts the gas price based on block utilization
func (g *GasModel) UpdateGasPrice() {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Calculate block utilization
	utilization := float64(g.blockGasUsed) / float64(g.blockGasLimit)

	// Adjust gas price based on utilization
	if utilization > TargetBlockGas {
		// Increase gas price if block is too full
		adjustment := float64(g.currentGasPrice) * GasPriceAdjustmentFactor
		g.currentGasPrice = uint64(math.Min(float64(g.currentGasPrice)+adjustment, float64(MaxGasPrice)))
	} else if utilization < TargetBlockGas/2 {
		// Decrease gas price if block is too empty
		adjustment := float64(g.currentGasPrice) * GasPriceAdjustmentFactor
		g.currentGasPrice = uint64(math.Max(float64(g.currentGasPrice)-adjustment, float64(MinGasPrice)))
	}
}

// GetCurrentGasPrice returns the current gas price
func (g *GasModel) GetCurrentGasPrice() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.currentGasPrice
}
