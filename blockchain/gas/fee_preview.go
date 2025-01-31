package gas

import (
	"fmt"
	"log"
	"time"
)

// FeePreview provides real-time gas fee estimates for users
type FeePreview struct {
	estimator    *GasEstimator
	lastUpdate   time.Time
	updatePeriod time.Duration
}

// FeeDetails contains detailed fee information for user display
type FeeDetails struct {
	BaseFee         uint64
	PriorityFee     uint64
	TotalFee        uint64
	MaxFee          uint64
	EstimatedTime   string
	PriorityLevel   string
	NetworkCongestion string
	USDEstimate     float64  // Optional: if we want to add fiat conversion
}

// NewFeePreview creates a new fee preview instance
func NewFeePreview(estimator *GasEstimator) *FeePreview {
	return &FeePreview{
		estimator:    estimator,
		updatePeriod: 10 * time.Second, // Update estimates every 10 seconds
	}
}

// GetTransactionFeePreview provides a user-friendly fee preview
func (fp *FeePreview) GetTransactionFeePreview(txSize int) map[string]*FeeDetails {
	log.Printf("\n💰 Calculating Transaction Fee Preview")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	
	// Get current network conditions
	congestion := fp.GetNetworkCongestion()
	
	// Calculate fees for all priority levels
	previews := make(map[string]*FeeDetails)
	
	priorities := []struct {
		level    int
		name     string
	}{
		{PriorityLow, "Economic"},
		{PriorityNormal, "Standard"},
		{PriorityHigh, "Fast"},
	}
	
	for _, p := range priorities {
		estimate := fp.estimator.EstimateGas(txSize, p.level)
		
		details := &FeeDetails{
			BaseFee:          estimate.BaseFee,
			PriorityFee:      estimate.PriorityFee,
			TotalFee:         estimate.TotalFee,
			MaxFee:           estimate.TotalFee + (estimate.TotalFee / 10), // Add 10% buffer
			EstimatedTime:    estimate.EstimatedTime,
			PriorityLevel:    p.name,
			NetworkCongestion: congestion,
		}
		
		previews[p.name] = details
		
		log.Printf("\n📊 %s Option", p.name)
		log.Printf("   • Base Fee: %d", details.BaseFee)
		log.Printf("   • Priority Fee: %d", details.PriorityFee)
		log.Printf("   • Total Fee: %d", details.TotalFee)
		log.Printf("   • Max Fee: %d", details.MaxFee)
		log.Printf("   • Estimated Time: %s", details.EstimatedTime)
	}
	
	log.Printf("\n🌐 Network Status: %s", congestion)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	
	return previews
}

// GetQuickFeePreview provides a simplified fee preview for basic transactions
func (fp *FeePreview) GetQuickFeePreview() string {
	standard := fp.estimator.EstimateGas(250, PriorityNormal) // Assume average transaction size
	
	return fmt.Sprintf(`
💰 Current Transaction Fees
━━━━━━━━━━━━━━━━━━━━━━━━━
   🐢 Economic:  %d (5-10 min)
   🚶 Standard:  %d (2-5 min)
   🏃 Fast:      %d (< 2 min)

Network Status: %s
`, 
		standard.TotalFee/2,
		standard.TotalFee,
		standard.TotalFee*2,
		fp.GetNetworkCongestion(),
	)
}

// GetRecommendedFee suggests the optimal fee based on network conditions
func (fp *FeePreview) GetRecommendedFee(txSize int) *FeeDetails {
	congestion := fp.GetNetworkCongestion()
	var priority int
	
	// Adjust priority based on network congestion
	switch congestion {
	case "High Congestion":
		priority = PriorityHigh
	case "Medium Congestion":
		priority = PriorityNormal
	default:
		priority = PriorityLow
	}
	
	estimate := fp.estimator.EstimateGas(txSize, priority)
	
	details := &FeeDetails{
		BaseFee:          estimate.BaseFee,
		PriorityFee:      estimate.PriorityFee,
		TotalFee:         estimate.TotalFee,
		MaxFee:           estimate.TotalFee + (estimate.TotalFee / 10),
		EstimatedTime:    estimate.EstimatedTime,
		PriorityLevel:    getPriorityString(priority),
		NetworkCongestion: congestion,
	}
	
	log.Printf("\n💡 Recommended Fee Configuration")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("   • Priority Level: %s", details.PriorityLevel)
	log.Printf("   • Total Fee: %d", details.TotalFee)
	log.Printf("   • Estimated Time: %s", details.EstimatedTime)
	log.Printf("   • Network Status: %s", details.NetworkCongestion)
	
	return details
}

// GetNetworkCongestion determines current network congestion level
func (fp *FeePreview) GetNetworkCongestion() string {
	utilization := float64(fp.estimator.model.blockGasUsed) / float64(fp.estimator.model.blockGasLimit)
	
	switch {
	case utilization > 0.8:
		return "High Congestion"
	case utilization > 0.5:
		return "Medium Congestion"
	default:
		return "Low Congestion"
	}
} 