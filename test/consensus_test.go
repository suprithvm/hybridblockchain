package test

import (
	"blockchain-core/blockchain"
	"testing"
	"time"
)

func setupTestEnvironment(t *testing.T) (*blockchain.HybridConsensusEngine, *blockchain.Blockchain, *blockchain.StakePool) {
	bc := blockchain.InitialiseBlockchain()
	stakePool := blockchain.NewStakePool()
	consensus := blockchain.NewHybridConsensus(&bc, stakePool)
	hybridConsensus, ok := consensus.(*blockchain.HybridConsensusEngine)
	if !ok {
		t.Fatal("Failed to cast consensus to HybridConsensusEngine")
	}
	return hybridConsensus, &bc, stakePool
}

func createTestWallet(t *testing.T) *blockchain.Wallet {
	wallet, err := blockchain.NewWallet()
	if err != nil {
		t.Fatalf("Failed to create test wallet: %v", err)
	}
	return wallet
}

func TestHybridConsensus(t *testing.T) {
	consensus, bc, stakePool := setupTestEnvironment(t)
	wallet := createTestWallet(t)

	t.Run("Block Timing Validation", func(t *testing.T) {
		previousBlock := bc.GetLatestBlock()
		mempool := blockchain.NewMempool()
		newBlock, err := blockchain.NewBlock(previousBlock, mempool, 1)
		if err != nil {
			t.Fatalf("Failed to create block: %v", err)
		}

		// Test valid timing
		if err := consensus.ValidateBlock(newBlock, previousBlock); err != nil {
			t.Errorf("Valid block timing failed validation: %v", err)
		}

		// Test future block
		futureBlock := newBlock
		futureBlock.Timestamp = time.Now().Add(time.Hour * 24).Unix()
		if err := consensus.ValidateBlock(futureBlock, previousBlock); err == nil {
			t.Error("Future block should fail validation")
		}

		// Test past block
		pastBlock := newBlock
		pastBlock.Timestamp = previousBlock.Timestamp - 1
		if err := consensus.ValidateBlock(pastBlock, previousBlock); err == nil {
			t.Error("Past block should fail validation")
		}
	})

	t.Run("Mining with Stake", func(t *testing.T) {
		// Add stake for the miner
		err := stakePool.AddStake(wallet.Address, 100.0)
		if err != nil {
			t.Fatalf("Failed to add stake: %v", err)
		}

		previousBlock := bc.GetLatestBlock()
		mempool := blockchain.NewMempool()
		newBlock, err := blockchain.NewBlock(previousBlock, mempool, 1)
		if err != nil {
			t.Fatalf("Failed to create block: %v", err)
		}

		// Test mining
		err = consensus.MineBlock(&newBlock, wallet.Address)
		if err != nil {
			t.Errorf("Mining failed: %v", err)
		}

		// Verify the mined block
		if err := consensus.ValidateBlock(newBlock, previousBlock); err != nil {
			t.Errorf("Mined block failed validation: %v", err)
		}

		// Verify PoW
		if !blockchain.IsHashValid(newBlock.BlockHash, newBlock.Difficulty) {
			t.Error("Block hash doesn't meet difficulty requirement")
		}
	})

	t.Run("Mining without Sufficient Stake", func(t *testing.T) {
		lowStakeWallet := createTestWallet(t)
		err := stakePool.AddStake(lowStakeWallet.Address, 1.0) // Add insufficient stake

		previousBlock := bc.GetLatestBlock()
		mempool := blockchain.NewMempool()
		newBlock, err := blockchain.NewBlock(previousBlock, mempool, 1)
		if err != nil {
			t.Fatalf("Failed to create block: %v", err)
		}

		// Mining should fail due to insufficient stake
		err = consensus.MineBlock(&newBlock, lowStakeWallet.Address)
		if err == nil {
			t.Error("Mining should fail with insufficient stake")
		}
	})

	t.Run("Difficulty Adjustment", func(t *testing.T) {
		// Mine several blocks to trigger difficulty adjustment
		for i := 0; i < blockchain.DifficultyAdjustPeriod+1; i++ {
			previousBlock := bc.GetLatestBlock()
			mempool := blockchain.NewMempool()
			newBlock, err := blockchain.NewBlock(previousBlock, mempool, 1)
			if err != nil {
				t.Fatalf("Failed to create block: %v", err)
			}

			err = consensus.MineBlock(&newBlock, wallet.Address)
			if err != nil {
				t.Fatalf("Mining failed: %v", err)
			}

			err = bc.AddBlock(newBlock, wallet.Address, stakePool)
			if err != nil {
				t.Fatalf("Failed to add block: %v", err)
			}
		}

		// Get consensus stats
		stats := consensus.GetConsensusStats()
		if stats["averageBlockTime"] == 0 {
			t.Error("Average block time should be calculated")
		}
	})

	t.Run("Consensus Parameters Update", func(t *testing.T) {
		params := map[string]interface{}{
			"targetTime": int64(20),
			"minStake":   float64(200),
			"powWeight":  float64(0.4),
		}

		err := consensus.UpdateParameters(params)
		if err != nil {
			t.Errorf("Failed to update consensus parameters: %v", err)
		}

		stats := consensus.GetConsensusStats()
		if stats["powWeight"] != 0.4 {
			t.Errorf("PoW weight not updated correctly, got %v, want 0.4", stats["powWeight"])
		}
	})

	t.Run("Block Validation with Transactions", func(t *testing.T) {
		mempool := blockchain.NewMempool()

		// Add a valid transaction
		tx, err := blockchain.NewTransaction(
			wallet.Address,
			"receiver",
			1.0,
			0.1,
			wallet,
		)
		if err != nil {
			t.Fatalf("Failed to create transaction: %v", err)
		}

		err = mempool.AddTransaction(tx, bc)
		if err != nil {
			t.Fatalf("Failed to add transaction to mempool: %v", err)
		}

		previousBlock := bc.GetLatestBlock()
		newBlock, err := blockchain.NewBlock(previousBlock, mempool, 1)
		if err != nil {
			t.Fatalf("Failed to create block: %v", err)
		}

		err = consensus.MineBlock(&newBlock, wallet.Address)
		if err != nil {
			t.Fatalf("Mining failed: %v", err)
		}

		// Validate the block with transactions
		err = consensus.ValidateBlock(newBlock, previousBlock)
		if err != nil {
			t.Errorf("Block with valid transactions failed validation: %v", err)
		}
	})
}
