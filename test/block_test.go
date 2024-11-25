package test

import (
	"blockchain-core/blockchain"
	"testing"
	"time"
)

func TestBlockCreation(t *testing.T) {
	// Setup test environment
	bc := blockchain.InitialiseBlockchain()
	mempool := blockchain.NewMempool()
	stakePool := blockchain.NewStakePool()
	wallet, _ := blockchain.NewWallet()

	t.Run("Genesis Block Creation", func(t *testing.T) {
		genesis := blockchain.GenesisBlock()
		if genesis.BlockNumber != 0 {
			t.Error("Genesis block number should be 0")
		}
		if genesis.PreviousHash != "0x00000000000000000000000000000000" {
			t.Error("Genesis block should have zero previous hash")
		}
	})

	t.Run("New Block Creation", func(t *testing.T) {
		// Add some transactions to mempool
		for i := 0; i < 5; i++ {
			tx, _ := blockchain.NewTransaction(
				wallet.Address,
				"receiver",
				float64(i+1),
				0.1,
				wallet,
			)
			mempool.AddTransaction(tx, &bc)
		}

		previousBlock := bc.GetLatestBlock()
		newBlock, err := blockchain.NewBlock(previousBlock, mempool, 1)
		if err != nil {
			t.Fatalf("Failed to create new block: %v", err)
		}

		// Verify block properties
		if newBlock.BlockNumber != previousBlock.BlockNumber+1 {
			t.Error("Block number not incremented correctly")
		}
		if newBlock.PreviousHash != previousBlock.BlockHash {
			t.Error("Previous hash not set correctly")
		}
		if newBlock.Timestamp <= previousBlock.Timestamp {
			t.Error("Block timestamp not set correctly")
		}
	})

	t.Run("Block Size Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		// Test normal block size
		if !blockchain.ValidateBlockSize(newBlock) {
			t.Error("Valid block size failed validation")
		}

		// Create an oversized block by adding many transactions
		mempool.Clear()             // Clear existing transactions
		for i := 0; i < 1000; i++ { // Add many transactions
			tx, _ := blockchain.NewTransaction(
				wallet.Address,
				"receiver",
				float64(i),
				0.1,
				wallet,
			)
			mempool.AddTransaction(tx, &bc)
		}

		oversizedBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		// Serialize the block to calculate its actual size
		serializedBlock, _ := blockchain.SerializeBlock(oversizedBlock)
		oversizedBlock.Size = int64(len(serializedBlock))

		// Set size beyond the maximum allowed size
		oversizedBlock.Size = blockchain.MaxBlockSize + 1

		if blockchain.ValidateBlockSize(oversizedBlock) {
			t.Error("Oversized block passed validation")
		}
	})

	t.Run("Block Timestamp Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		// Valid timestamp
		if !blockchain.ValidateBlockTimestamp(newBlock) {
			t.Error("Valid timestamp failed validation")
		}

		// Future timestamp
		newBlock.Timestamp = time.Now().Add(time.Hour * 24).Unix()
		if blockchain.ValidateBlockTimestamp(newBlock) {
			t.Error("Future timestamp passed validation")
		}

		// Past timestamp
		newBlock.Timestamp = previousBlock.Timestamp - 1
		if blockchain.ValidateBlockTimestamp(newBlock) {
			t.Error("Past timestamp passed validation")
		}
	})

	t.Run("Block Mining", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 2)

		// Add stake for mining
		stakePool.AddStake(wallet.Address, 100)

		err := blockchain.MineBlock(&newBlock, previousBlock, stakePool, 10)
		if err != nil {
			t.Fatalf("Block mining failed: %v", err)
		}

		// Verify mining result
		if !blockchain.ValidateBlock(newBlock, previousBlock, wallet.Address, stakePool) {
			t.Error("Mined block failed validation")
		}

		// Verify difficulty
		if !blockchain.IsHashValid(newBlock.BlockHash, newBlock.Difficulty) {
			t.Error("Block hash doesn't meet difficulty requirement")
		}
	})

	t.Run("Block Transaction Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		if !blockchain.ValidateBlockTransactions(newBlock, &bc) {
			t.Error("Valid transactions failed validation")
		}

		// Test double-spending within block
		tx1, _ := blockchain.NewTransaction(wallet.Address, "receiver1", 100.0, 0.1, wallet)
		tx2, _ := blockchain.NewTransaction(wallet.Address, "receiver2", 100.0, 0.1, wallet)

		mempool.Clear()
		mempool.AddTransaction(tx1, &bc)
		mempool.AddTransaction(tx2, &bc)

		doubleSpendBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)
		if blockchain.ValidateBlockTransactions(doubleSpendBlock, &bc) {
			t.Error("Double-spending block passed validation")
		}
	})

	t.Run("Block State Root", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		if newBlock.StateRoot == "" {
			t.Error("State root not calculated")
		}

		// Verify state root changes with different transactions
		tx, _ := blockchain.NewTransaction(wallet.Address, "receiver", 1.0, 0.1, wallet)
		mempool.AddTransaction(tx, &bc)

		anotherBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)
		if anotherBlock.StateRoot == newBlock.StateRoot {
			t.Error("State root should be different for different transactions")
		}
	})

	t.Run("Block Serialization", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		originalBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		// Serialize
		serialized, err := blockchain.SerializeBlock(originalBlock)
		if err != nil {
			t.Fatalf("Block serialization failed: %v", err)
		}

		// Deserialize
		deserialized, err := blockchain.DeserializeBlock(serialized)
		if err != nil {
			t.Fatalf("Block deserialization failed: %v", err)
		}

		// Compare
		if originalBlock.BlockHash != deserialized.BlockHash {
			t.Error("Deserialized block hash doesn't match original")
		}
		if originalBlock.BlockNumber != deserialized.BlockNumber {
			t.Error("Deserialized block number doesn't match original")
		}
	})
}

func TestBlockValidation(t *testing.T) {
	// Setup
	bc := blockchain.InitialiseBlockchain()
	mempool := blockchain.NewMempool()
	stakePool := blockchain.NewStakePool()
	wallet, _ := blockchain.NewWallet()

	t.Run("Block Size Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()

		// Create a normal block
		normalBlock, err := blockchain.NewBlock(previousBlock, mempool, 1)
		if err != nil {
			t.Fatalf("Failed to create normal block: %v", err)
		}

		if !blockchain.ValidateBlockSize(normalBlock) {
			t.Error("Valid block size failed validation")
		}

		// Create an oversized block by adding many transactions
		for i := 0; i < 10000; i++ {
			tx, _ := blockchain.NewTransaction(
				wallet.Address,
				"receiver",
				float64(i),
				0.1,
				wallet,
			)
			mempool.AddTransaction(tx, &bc)
		}

		oversizedBlock, err := blockchain.NewBlock(previousBlock, mempool, 1)
		if err != nil {
			t.Fatalf("Failed to create oversized block: %v", err)
		}

		// Calculate and set a large size
		serializedBlock, _ := blockchain.SerializeBlock(oversizedBlock)
		oversizedBlock.Size = int64(len(serializedBlock) * 1000) // Make it artificially large

		if blockchain.ValidateBlockSize(oversizedBlock) {
			t.Error("Oversized block passed validation")
		}
	})

	t.Run("Block Hash Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		// Validate correct hash
		originalHash := newBlock.BlockHash
		if !blockchain.ValidateBlock(newBlock, previousBlock, "", stakePool) {
			t.Error("Valid block failed validation")
		}

		// Test with incorrect hash
		newBlock.BlockHash = "invalid_hash"
		if blockchain.ValidateBlock(newBlock, previousBlock, "", stakePool) {
			t.Error("Block with invalid hash passed validation")
		}

		// Restore original hash for further tests
		newBlock.BlockHash = originalHash
	})

	t.Run("Block Number Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		// Test correct block number
		if newBlock.BlockNumber != previousBlock.BlockNumber+1 {
			t.Error("Block number not incremented correctly")
		}

		// Test invalid block number
		newBlock.BlockNumber += 1
		if blockchain.ValidateBlock(newBlock, previousBlock, "", stakePool) {
			t.Error("Block with invalid number passed validation")
		}
	})

	t.Run("Block Timestamp Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		// Test future timestamp
		newBlock.Timestamp = time.Now().Add(time.Hour * 24).Unix()
		if blockchain.ValidateBlockTimestamp(newBlock) {
			t.Error("Block with future timestamp passed validation")
		}

		// Test valid timestamp
		newBlock.Timestamp = time.Now().Unix()
		if !blockchain.ValidateBlockTimestamp(newBlock) {
			t.Error("Block with valid timestamp failed validation")
		}
	})

	t.Run("Block Transaction Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()

		// Add valid transaction
		tx, _ := blockchain.NewTransaction(wallet.Address, "receiver", 1.0, 0.1, wallet)
		mempool.AddTransaction(tx, &bc)

		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		if !blockchain.ValidateBlockTransactions(newBlock, &bc) {
			t.Error("Valid block transactions failed validation")
		}

		// Test with invalid transaction (double spend)
		mempool.Clear()
		tx1, _ := blockchain.NewTransaction(wallet.Address, "receiver1", 1000.0, 0.1, wallet)
		tx2, _ := blockchain.NewTransaction(wallet.Address, "receiver2", 1000.0, 0.1, wallet)
		mempool.AddTransaction(tx1, &bc)
		mempool.AddTransaction(tx2, &bc)

		invalidBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)
		if blockchain.ValidateBlockTransactions(invalidBlock, &bc) {
			t.Error("Block with invalid transactions passed validation")
		}
	})

	t.Run("Block Difficulty Adjustment", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(previousBlock, mempool, 1)

		// Test difficulty adjustment
		targetTime := int64(10) // 10 seconds
		adjustedDifficulty := blockchain.AdjustDifficulty(previousBlock, targetTime)

		if adjustedDifficulty < 1 {
			t.Error("Difficulty adjustment resulted in invalid difficulty")
		}

		// Test mining with adjusted difficulty
		newBlock.Difficulty = adjustedDifficulty
		err := blockchain.MineBlock(&newBlock, previousBlock, stakePool, targetTime)
		if err != nil {
			t.Errorf("Failed to mine block with adjusted difficulty: %v", err)
		}

		if !blockchain.IsHashValid(newBlock.BlockHash, adjustedDifficulty) {
			t.Error("Mined block hash doesn't meet difficulty requirement")
		}
	})
}

func TestBlockDifficulty(t *testing.T) {
	config := blockchain.DefaultConsensusParameters()

	t.Run("Difficulty Bounds", func(t *testing.T) {
		if !blockchain.IsValidDifficulty(config.InitialDifficulty, config) {
			t.Error("Initial difficulty outside valid range")
		}

		// Test upper bound
		difficulty := blockchain.AdjustDifficultyForNextBlock(
			config.MaxDifficulty-1,
			float64(config.TargetBlockTime)*0.5,
			config,
		)
		if difficulty > config.MaxDifficulty {
			t.Error("Difficulty exceeded maximum")
		}

		// Test lower bound
		difficulty = blockchain.AdjustDifficultyForNextBlock(
			config.MinDifficulty+1,
			float64(config.TargetBlockTime)*2.0,
			config,
		)
		if difficulty < config.MinDifficulty {
			t.Error("Difficulty below minimum")
		}
	})
}
