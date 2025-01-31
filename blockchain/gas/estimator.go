package gas

import (
	"fmt"
	"log"
)

// GasEstimator provides user-friendly gas estimation
type GasEstimator struct {
	model *GasModel
}

// EstimationResult holds user-friendly gas estimates
type EstimationResult struct {
	BaseFee       uint64  // Base transaction fee
	PriorityFee   uint64  // Additional fee for priority
	TotalFee      uint64  // Total fee to be paid
	EstimatedTime string  // Estimated processing time
	Priority      string  // Human readable priority level
}

// NewGasEstimator creates a new gas estimator
func NewGasEstimator(model *GasModel) *GasEstimator {
	return &GasEstimator{
		model: model,
	}
}

// EstimateGas provides user-friendly gas estimation
func (ge *GasEstimator) EstimateGas(txSize int, priority int) *EstimationResult {
	// Calculate base costs
	baseFee := ge.model.CalculateBaseFee(txSize)
	priorityFee := ge.model.CalculatePriorityFee(priority)
	totalFee := baseFee + priorityFee

	// Get human-readable priority
	priorityStr := getPriorityString(priority)
	
	// Estimate processing time
	estimatedTime := getEstimatedTime(priority)

	result := &EstimationResult{
		BaseFee:       baseFee,
		PriorityFee:   priorityFee,
		TotalFee:      totalFee,
		EstimatedTime: estimatedTime,
		Priority:      priorityStr,
	}

	// Log user-friendly information
	log.Printf("\n💰 Gas Fee Estimation")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("   • Transaction Size: %d bytes", txSize)
	log.Printf("   • Priority Level: %s", result.Priority)
	log.Printf("   • Base Fee: %d", result.BaseFee)
	log.Printf("   • Priority Fee: %d", result.PriorityFee)
	log.Printf("   • Total Fee: %d", result.TotalFee)
	log.Printf("   • Estimated Time: %s", result.EstimatedTime)

	return result
}

// Helper functions for user-friendly output
func getPriorityString(priority int) string {
	switch priority {
	case PriorityLow:
		return "Low Priority (Slower, Cheaper)"
	case PriorityNormal:
		return "Normal Priority (Regular Speed)"
	case PriorityHigh:
		return "High Priority (Faster, More Expensive)"
	default:
		return "Unknown Priority"
	}
}

func getEstimatedTime(priority int) string {
	switch priority {
	case PriorityLow:
		return "5-10 minutes"
	case PriorityNormal:
		return "2-5 minutes"
	case PriorityHigh:
		return "< 2 minutes"
	default:
		return "unknown"
	}
}

// GetCurrentGasInfo returns current gas prices in human readable format
func (ge *GasEstimator) GetCurrentGasInfo() string {
	currentPrice := ge.model.GetCurrentGasPrice()
	
	return fmt.Sprintf(`
📊 Current Gas Prices
━━━━━━━━━━━━━━━━━━━━━━
   • Low Priority: %d (Slower)
   • Normal Priority: %d (Regular)
   • High Priority: %d (Faster)
`, 
		currentPrice/2,    // Low priority price
		currentPrice,      // Normal priority price
		currentPrice*2,    // High priority price
	)
}