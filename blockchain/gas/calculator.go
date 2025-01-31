package gas



// TxGasInfo holds gas-related information for a transaction
type TxGasInfo struct {
	GasUsed    uint64 `json:"gas_used"`
	GasPrice   uint64 `json:"gas_price"`
	TotalFee   uint64 `json:"total_fee"`
	Priority   int    `json:"priority"`
}

// GasCalculator handles transaction-specific gas calculations
type GasCalculator struct {
	model *GasModel
}

// NewGasCalculator creates a new gas calculator instance
func NewGasCalculator(model *GasModel) *GasCalculator {
	return &GasCalculator{
		model: model,
	}
}

// CalculateTransactionGas calculates the total gas needed for a transaction
func (gc *GasCalculator) CalculateTransactionGas(txSize int, priority int) *TxGasInfo {
	// Calculate base gas cost
	baseFee := gc.model.CalculateBaseFee(txSize)
	
	// Get priority fee
	priorityFee := gc.model.CalculatePriorityFee(priority)
	
	// Calculate total gas price
	gasPrice := gc.model.GetCurrentGasPrice()
	
	// Calculate total fee
	totalFee := baseFee * gasPrice + priorityFee
	
	return &TxGasInfo{
		GasUsed:  baseFee,
		GasPrice: gasPrice,
		TotalFee: totalFee,
		Priority: priority,
	}
}

// ValidateGas checks if a transaction's gas parameters are valid
func (gc *GasCalculator) ValidateGas(gasInfo *TxGasInfo) bool {
	if gasInfo.GasPrice < MinGasPrice || gasInfo.GasPrice > MaxGasPrice {
		return false
	}
	
	if gasInfo.Priority < PriorityLow || gasInfo.Priority > PriorityHigh {
		return false
	}
	
	return true
} 