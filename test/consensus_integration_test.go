package test

import (
	"blockchain-core/blockchain"
	"testing"
)

func TestConsensusIntegration(t *testing.T) {
	// Setup test environment
	bc := blockchain.InitialiseBlockchain()
	stakePool := blockchain.NewStakePool()
	config := blockchain.DefaultConsensusParameters()
	consensusManager := blockchain.NewConsensusManager(&bc, stakePool, &config)

	// Create test wallets
	validator1, _ := blockchain.NewWallet()
	validator2, _ := blockchain.NewWallet()
	validator3, _ := blockchain.NewWallet()

	// Add initial stakes
	stakePool.AddStake(validator1.Address, 1000.0)
	stakePool.AddStake(validator2.Address, 800.0)
	stakePool.AddStake(validator3.Address, 500.0)

	t.Run("Block Validation and Consensus", func(t *testing.T) {
		// Create and process blocks
		mempool := blockchain.NewMempool()
		prevBlock := bc.GetLatestBlock()

		for i := 0; i < int(config.EpochLength); i++ {
			newBlock, err := blockchain.NewBlock(prevBlock, mempool, prevBlock.Difficulty)
			if err != nil {
				t.Fatalf("Failed to create block: %v", err)
			}

			// Set validator
			newBlock.Validator = validator1.Address
			newBlock.ValidatorPublicKey = validator1.PublicKey

			// Process block through consensus
			if err := consensusManager.handleBlockFinalization(newBlock); err != nil {
				t.Errorf("Block finalization failed: %v", err)
			}

			prevBlock = newBlock
		}

		// Verify epoch transition occurred
		stats := consensusManager.GetConsensusStats()
		if stats["currentEpoch"].(int64) != 1 {
			t.Errorf("Expected epoch transition to epoch 1")
		}
	})

	t.Run("Validator Set Updates", func(t *testing.T) {
		// Add new validator with higher stake
		stakePool.AddStake(validator3.Address, 2000.0)

		// Process blocks until validator set update
		mempool := blockchain.NewMempool()
		prevBlock := bc.GetLatestBlock()

		newBlock, _ := blockchain.NewBlock(prevBlock, mempool, prevBlock.Difficulty)
		newBlock.Validator = validator3.Address
		newBlock.ValidatorPublicKey = validator3.PublicKey

		if err := consensusManager.handleBlockFinalization(newBlock); err != nil {
			t.Errorf("Failed to process block with new validator: %v", err)
		}

		// Verify validator set changed
		validators := consensusManager.consensusState.GetCurrentValidators()
		if _, exists := validators[validator3.Address]; !exists {
			t.Error("New validator not added to active set")
		}
	})

	t.Run("Slashing Integration", func(t *testing.T) {
		// Create double signing scenario
		mempool := blockchain.NewMempool()
		prevBlock := bc.GetLatestBlock()

		block1, _ := blockchain.NewBlock(prevBlock, mempool, prevBlock.Difficulty)
		block1.Validator = validator1.Address
		block1.ValidatorPublicKey = validator1.PublicKey

		block2 := block1 // Create conflicting block
		block2.BlockHash = "different_hash"

		// Process both blocks
		consensusManager.handleBlockFinalization(block1)
		consensusManager.handleBlockFinalization(block2)

		// Verify validator was slashed
		validatorInfo, _ := stakePool.GetValidatorInfo(validator1.Address)
		if !validatorInfo.IsSlashed {
			t.Error("Validator should be slashed for double signing")
		}
	})

	t.Run("Reward Distribution", func(t *testing.T) {
		// Record initial balance
		initialStake := stakePool.Stakes[validator2.Address]

		// Create and process block
		mempool := blockchain.NewMempool()
		prevBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(prevBlock, mempool, prevBlock.Difficulty)
		newBlock.Validator = validator2.Address
		newBlock.ValidatorPublicKey = validator2.PublicKey

		consensusManager.handleBlockFinalization(newBlock)

		// Verify rewards were distributed
		finalStake := stakePool.Stakes[validator2.Address]
		if finalStake <= initialStake {
			t.Error("Validator did not receive rewards")
		}
	})
}
