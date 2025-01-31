package gas

import (
	"log"
	"testing"
	"github.com/stretchr/testify/assert"
)

func init() {
	log.SetFlags(log.Ltime) // Only show time in logs
}

func TestGasModel(t *testing.T) {
	log.Printf("\n🧪 Starting Gas Model Tests")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	t.Run("Gas Model Initialization", func(t *testing.T) {
		log.Printf("\n📌 Testing Gas Model Initialization")
		model := NewGasModel(MinGasPrice, 15_000_000)
		log.Printf("   • Initial Gas Price: %d", model.GetCurrentGasPrice())
		log.Printf("   • Block Gas Limit: %d", model.blockGasLimit)
		assert.NotNil(t, model)
		assert.Equal(t, uint64(MinGasPrice), model.GetCurrentGasPrice())
	})

	t.Run("Base Fee Calculation", func(t *testing.T) {
		log.Printf("\n📌 Testing Base Fee Calculation")
		model := NewGasModel(MinGasPrice, 15_000_000)
		
		// Test empty transaction
		emptyTxFee := model.CalculateBaseFee(0)
		log.Printf("   • Empty Transaction Fee: %d", emptyTxFee)
		assert.Equal(t, uint64(BaseTxGas+TxSigGas), emptyTxFee)
		
		// Test transaction with data
		dataSize := 100
		withDataFee := model.CalculateBaseFee(dataSize)
		log.Printf("   • Transaction with %d bytes: %d gas", dataSize, withDataFee)
		expectedFee := BaseTxGas + TxSigGas + (uint64(dataSize) * TxDataByteGas)
		assert.Equal(t, expectedFee, withDataFee)
	})

	t.Run("Priority Fee Calculation", func(t *testing.T) {
		log.Printf("\n📌 Testing Priority Fee Calculation")
		model := NewGasModel(MinGasPrice, 15_000_000)
		basePrice := model.GetCurrentGasPrice()
		log.Printf("   • Base Gas Price: %d", basePrice)

		// Test different priority levels
		lowPriorityFee := model.CalculatePriorityFee(PriorityLow)
		log.Printf("   • Low Priority Fee: %d", lowPriorityFee)
		assert.Equal(t, basePrice/2, lowPriorityFee)

		normalPriorityFee := model.CalculatePriorityFee(PriorityNormal)
		log.Printf("   • Normal Priority Fee: %d", normalPriorityFee)
		assert.Equal(t, basePrice, normalPriorityFee)

		highPriorityFee := model.CalculatePriorityFee(PriorityHigh)
		log.Printf("   • High Priority Fee: %d", highPriorityFee)
		assert.Equal(t, basePrice*2, highPriorityFee)
	})

	t.Run("Dynamic Gas Price Adjustment", func(t *testing.T) {
		log.Printf("\n📌 Testing Dynamic Gas Price Adjustment")
		model := NewGasModel(MinGasPrice, 1_000_000)
		initialPrice := model.GetCurrentGasPrice()
		log.Printf("   • Initial Gas Price: %d", initialPrice)

		// Simulate high utilization
		model.blockGasUsed = uint64(float64(model.blockGasLimit) * 0.9)
		log.Printf("   • Block Utilization: 90%%")
		model.UpdateGasPrice()
		highUtilPrice := model.GetCurrentGasPrice()
		log.Printf("   • Updated Gas Price (High Util): %d", highUtilPrice)
		assert.Greater(t, highUtilPrice, initialPrice)

		// Simulate low utilization
		model.blockGasUsed = uint64(float64(model.blockGasLimit) * 0.3)
		log.Printf("   • Block Utilization: 30%%")
		model.UpdateGasPrice()
		lowUtilPrice := model.GetCurrentGasPrice()
		log.Printf("   • Updated Gas Price (Low Util): %d", lowUtilPrice)
		assert.Less(t, lowUtilPrice, highUtilPrice)
	})
}

func TestGasCalculator(t *testing.T) {
	log.Printf("\n🧮 Starting Gas Calculator Tests")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	t.Run("Transaction Gas Calculation", func(t *testing.T) {
		log.Printf("\n📌 Testing Transaction Gas Calculation")
		model := NewGasModel(MinGasPrice, 15_000_000)
		calculator := NewGasCalculator(model)

		txSize := 200
		priority := PriorityNormal
		log.Printf("   • Transaction Size: %d bytes", txSize)
		log.Printf("   • Priority Level: %d", priority)

		gasInfo := calculator.CalculateTransactionGas(txSize, priority)
		log.Printf("   • Gas Used: %d", gasInfo.GasUsed)
		log.Printf("   • Gas Price: %d", gasInfo.GasPrice)
		log.Printf("   • Total Fee: %d", gasInfo.TotalFee)
		
		assert.NotNil(t, gasInfo)
		assert.Equal(t, priority, gasInfo.Priority)
		assert.Greater(t, gasInfo.TotalFee, uint64(0))
	})

	t.Run("Gas Validation", func(t *testing.T) {
		model := NewGasModel(MinGasPrice, 15_000_000)
		calculator := NewGasCalculator(model)

		// Valid gas info
		validGasInfo := &TxGasInfo{
			GasPrice: MinGasPrice + 1000,
			Priority: PriorityNormal,
		}
		assert.True(t, calculator.ValidateGas(validGasInfo))

		// Invalid gas price (too low)
		invalidGasInfo := &TxGasInfo{
			GasPrice: MinGasPrice - 1,
			Priority: PriorityNormal,
		}
		assert.False(t, calculator.ValidateGas(invalidGasInfo))

		// Invalid gas price (too high)
		invalidGasInfo = &TxGasInfo{
			GasPrice: MaxGasPrice + 1,
			Priority: PriorityNormal,
		}
		assert.False(t, calculator.ValidateGas(invalidGasInfo))

		// Invalid priority
		invalidGasInfo = &TxGasInfo{
			GasPrice: MinGasPrice,
			Priority: PriorityHigh + 1,
		}
		assert.False(t, calculator.ValidateGas(invalidGasInfo))
	})
}

func TestBlockGasTracker(t *testing.T) {
	log.Printf("\n📊 Starting Block Gas Tracker Tests")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	t.Run("Block Gas Tracking", func(t *testing.T) {
		log.Printf("\n📌 Testing Block Gas Tracking")
		model := NewGasModel(MinGasPrice, 15_000_000)
		tracker := NewBlockGasTracker(1_000_000, model)
		log.Printf("   • Block Gas Limit: %d", tracker.gasLimit)

		// Test initial state
		utilization := tracker.GetUtilization()
		log.Printf("   • Initial Utilization: %.2f%%", utilization*100)
		assert.Equal(t, float64(0), utilization)

		// Add transaction
		gasInfo := &TxGasInfo{
			GasUsed: 21000,
			GasPrice: MinGasPrice,
		}
		err := tracker.AddTransaction(gasInfo)
		log.Printf("   • Added Transaction Gas: %d", gasInfo.GasUsed)
		log.Printf("   • New Utilization: %.2f%%", tracker.GetUtilization()*100)
		assert.NoError(t, err)
		assert.Greater(t, tracker.GetUtilization(), float64(0))
	})

	t.Run("Concurrent Gas Tracking", func(t *testing.T) {
		model := NewGasModel(MinGasPrice, 15_000_000)
		tracker := NewBlockGasTracker(1_000_000, model)

		// Simulate concurrent transactions
		done := make(chan bool)
		for i := 0; i < 10; i++ {
			go func() {
				gasInfo := &TxGasInfo{
					GasUsed: 21000,
					GasPrice: MinGasPrice,
				}
				_ = tracker.AddTransaction(gasInfo)
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// Verify final state
		assert.Greater(t, tracker.GetUtilization(), float64(0))
		assert.LessOrEqual(t, tracker.GetUtilization(), float64(1))
	})
}

func TestGasModelIntegration(t *testing.T) {
	log.Printf("\n🔄 Starting Gas Model Integration Tests")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	t.Run("Complete Transaction Flow", func(t *testing.T) {
		log.Printf("\n📌 Testing Complete Transaction Flow")
		
		// Initialize components with higher initial gas price
		initialGasPrice := uint64(2000) // Set higher than MinGasPrice
		model := NewGasModel(initialGasPrice, 15_000_000)
		calculator := NewGasCalculator(model)
		tracker := NewBlockGasTracker(1_000_000, model)
		log.Printf("   • Components Initialized")
		log.Printf("   • Initial Gas Price: %d", initialGasPrice)

		// Calculate gas for transaction
		txSize := 200
		gasInfo := calculator.CalculateTransactionGas(txSize, PriorityHigh)
		log.Printf("   • Transaction Size: %d bytes", txSize)
		log.Printf("   • Gas Used: %d", gasInfo.GasUsed)
		log.Printf("   • Gas Price: %d", gasInfo.GasPrice)
		log.Printf("   • Total Fee: %d", gasInfo.TotalFee)
		
		// Validate gas parameters
		if !calculator.ValidateGas(gasInfo) {
			t.Errorf("❌ Gas validation failed: GasPrice=%d, Priority=%d", 
				gasInfo.GasPrice, gasInfo.Priority)
			return
		}
		log.Printf("   • Gas validation successful")

		// Add to block
		err := tracker.AddTransaction(gasInfo)
		if err != nil {
			t.Errorf("❌ Failed to add transaction: %v", err)
			return
		}
		log.Printf("   • Transaction added to block")

		// Verify block utilization
		utilization := tracker.GetUtilization()
		log.Printf("   • Block Utilization: %.2f%%", utilization*100)
		if utilization <= 0 {
			t.Error("❌ Block utilization should be greater than 0")
			return
		}

		// Update gas price based on utilization
		oldPrice := model.GetCurrentGasPrice()
		log.Printf("   • Old Gas Price: %d", oldPrice)
		
		// Force high utilization to ensure price change
		model.blockGasUsed = uint64(float64(model.blockGasLimit) * 0.9) // 90% utilization
		model.UpdateGasPrice()
		
		newPrice := model.GetCurrentGasPrice()
		log.Printf("   • New Gas Price: %d", newPrice)
		log.Printf("   • Price Change: %d", newPrice-oldPrice)

		// Verify price changed
		if newPrice <= oldPrice {
			t.Errorf("❌ Gas price should increase under high utilization. Old: %d, New: %d", 
				oldPrice, newPrice)
			return
		}
		
		log.Printf("   ✅ All checks passed")
	})
	
	log.Printf("\n✅ All Gas Tests Completed Successfully!")
} 