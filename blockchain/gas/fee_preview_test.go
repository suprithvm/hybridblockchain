package gas

import (
	"testing"
	"time"
	"log"
	"github.com/stretchr/testify/assert"
)

func TestFeePreview(t *testing.T) {
	log.Printf("\n🧪 Starting Fee Preview Tests")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	t.Run("Real-world Transaction Fee Preview", func(t *testing.T) {
		log.Printf("\n📌 Simulating Real Transaction Fee Preview")
		
		// Initialize components with realistic values
		model := NewGasModel(2000, 15_000_000) // 2000 wei base price
		estimator := NewGasEstimator(model)
		preview := NewFeePreview(estimator)
		
		// Simulate different network conditions
		testCases := []struct {
			utilization float64
			txSize     int
			desc       string
		}{
			{0.3, 250, "Low Network Usage"},
			{0.6, 250, "Medium Network Usage"},
			{0.85, 250, "High Network Usage"},
		}

		for _, tc := range testCases {
			log.Printf("\n🌐 Testing %s", tc.desc)
			log.Printf("   • Network Utilization: %.1f%%", tc.utilization*100)
			
			// Set network utilization
			model.blockGasUsed = uint64(float64(model.blockGasLimit) * tc.utilization)
			
			// Get fee preview
			fees := preview.GetTransactionFeePreview(tc.txSize)
			
			// Verify all priority levels are present
			assert.Contains(t, fees, "Economic")
			assert.Contains(t, fees, "Standard")
			assert.Contains(t, fees, "Fast")
			
			// Log detailed fee information
			log.Printf("\n💰 Fee Options:")
			for priority, details := range fees {
				log.Printf("\n   %s:", priority)
				log.Printf("   • Base Fee: %d", details.BaseFee)
				log.Printf("   • Priority Fee: %d", details.PriorityFee)
				log.Printf("   • Total Fee: %d", details.TotalFee)
				log.Printf("   • Max Fee: %d", details.MaxFee)
				log.Printf("   • Estimated Time: %s", details.EstimatedTime)
				
				// Verify fee relationships
				assert.Greater(t, details.TotalFee, uint64(0))
				assert.Greater(t, details.MaxFee, details.TotalFee)
			}
			
			// Test recommended fee based on network conditions
			recommended := preview.GetRecommendedFee(tc.txSize)
			log.Printf("\n💡 Recommended Configuration:")
			log.Printf("   • Priority Level: %s", recommended.PriorityLevel)
			log.Printf("   • Total Fee: %d", recommended.TotalFee)
			log.Printf("   • Network Status: %s", recommended.NetworkCongestion)
			
			// Verify recommended fee logic
			switch {
			case tc.utilization > 0.8:
				assert.Equal(t, "High Priority (Faster, More Expensive)", recommended.PriorityLevel)
			case tc.utilization > 0.5:
				assert.Equal(t, "Normal Priority (Regular Speed)", recommended.PriorityLevel)
			default:
				assert.Equal(t, "Low Priority (Slower, Cheaper)", recommended.PriorityLevel)
			}
		}
	})

	t.Run("Fee Update Period", func(t *testing.T) {
		log.Printf("\n📌 Testing Fee Update Mechanism")
		
		model := NewGasModel(2000, 15_000_000)
		estimator := NewGasEstimator(model)
		preview := NewFeePreview(estimator)
		
		// Get initial fees
		initialFees := preview.GetTransactionFeePreview(250)
		time.Sleep(2 * time.Second)
		
		// Simulate network congestion change
		model.blockGasUsed = uint64(float64(model.blockGasLimit) * 0.9)
		model.UpdateGasPrice()
		
		// Get updated fees
		updatedFees := preview.GetTransactionFeePreview(250)
		
		// Verify fee adjustment
		assert.NotEqual(t, 
			initialFees["Standard"].TotalFee, 
			updatedFees["Standard"].TotalFee,
			"Fees should adjust based on network conditions")
		
		log.Printf("\n📊 Fee Adjustment:")
		log.Printf("   • Initial Standard Fee: %d", initialFees["Standard"].TotalFee)
		log.Printf("   • Updated Standard Fee: %d", updatedFees["Standard"].TotalFee)
	})
	
	log.Printf("\n✅ All Fee Preview Tests Completed Successfully!")
} 