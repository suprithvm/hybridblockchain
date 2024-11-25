package test

import (
	"blockchain-core/blockchain"
	"testing"
	"time"
)

func TestEpochConsensusIntegration(t *testing.T) {
	// Setup test environment
	bc := blockchain.InitialiseBlockchain()
	stakePool := blockchain.NewStakePool()
	config := blockchain.DefaultConsensusParameters()
	consensusEngine := blockchain.NewHybridConsensus(&bc, stakePool)
	epochManager := blockchain.NewEpochManager(&bc, stakePool, &config)
	epochFinalizer := blockchain.NewEpochFinalizer(&bc, stakePool, epochManager, &config)
	consensusState := blockchain.NewConsensusState(&bc, stakePool, epochManager, epochFinalizer, &config)

	// Create test wallets
	validator1, _ := blockchain.NewWallet()
	validator2, _ := blockchain.NewWallet()
	validator3, _ := blockchain.NewWallet()

	// Add initial stakes
	stakePool.AddStake(validator1.Address, 1000.0)
	stakePool.AddStake(validator2.Address, 800.0)
	stakePool.AddStake(validator3.Address, 500.0)

	t.Run("Epoch Transition", func(t *testing.T) {
		// Create blocks until epoch transition
		mempool := blockchain.NewMempool()
		prevBlock := bc.GetLatestBlock()

		for i := 0; i < int(config.EpochLength); i++ {
			newBlock, err := blockchain.NewBlock(prevBlock, mempool, prevBlock.Difficulty)
			if err != nil {
				t.Fatalf("Failed to create block: %v", err)
			}

			// Mine and add block
			err = consensusEngine.MineBlock(&newBlock, validator1.Address)
			if err != nil {
				t.Fatalf("Failed to mine block: %v", err)
			}

			err = bc.AddBlock(newBlock, validator1.Address, stakePool)
			if err != nil {
				t.Fatalf("Failed to add block: %v", err)
			}

			prevBlock = newBlock

			// Process block in consensus state
			consensusState.handleBlockFinalization(newBlock)
		}

		// Verify epoch transition occurred
		if epochManager.currentEpoch != 1 {
			t.Errorf("Expected epoch transition to epoch 1, got %d", epochManager.currentEpoch)
		}
	})

	t.Run("Validator Set Updates", func(t *testing.T) {
		// Get initial validator set
		initialValidators := consensusState.GetCurrentValidators()
		initialCount := len(initialValidators)

		// Add new validator
		stakePool.AddStake(validator3.Address, 2000.0) // Higher stake than others

		// Trigger validator set update
		update := blockchain.ValidatorUpdate{
			Added:   []string{validator3.Address},
			Removed: []string{},
			Epoch:   epochManager.currentEpoch,
		}

		consensusState.validatorUpdate <- update

		// Wait for update to process
		time.Sleep(time.Second)

		// Verify validator set changed
		newValidators := consensusState.GetCurrentValidators()
		if len(newValidators) != initialCount+1 {
			t.Errorf("Expected validator count to increase by 1, got %d", len(newValidators))
		}

		if _, exists := newValidators[validator3.Address]; !exists {
			t.Error("New validator not added to active set")
		}
	})

	t.Run("Slashing Integration", func(t *testing.T) {
		// Create double signing scenario
		block1 := bc.GetLatestBlock()
		block2 := block1 // Create conflicting block

		// Sign both blocks with same validator
		block1.Validator = validator1.Address
		block2.Validator = validator1.Address

		// Process blocks in consensus state
		consensusState.handleBlockFinalization(block1)
		consensusState.handleBlockFinalization(block2)

		// Verify validator was slashed
		validatorInfo, _ := stakePool.GetValidatorInfo(validator1.Address)
		if !validatorInfo.IsSlashed {
			t.Error("Validator should be slashed for double signing")
		}
	})

	t.Run("Reward Distribution", func(t *testing.T) {
		// Record initial balances
		initialStake := stakePool.Stakes[validator2.Address]

		// Create and process blocks
		mempool := blockchain.NewMempool()
		prevBlock := bc.GetLatestBlock()
		newBlock, _ := blockchain.NewBlock(prevBlock, mempool, prevBlock.Difficulty)

		consensusEngine.MineBlock(&newBlock, validator2.Address)
		bc.AddBlock(newBlock, validator2.Address, stakePool)
		consensusState.handleBlockFinalization(newBlock)

		// Verify rewards were distributed
		finalStake := stakePool.Stakes[validator2.Address]
		if finalStake <= initialStake {
			t.Error("Validator did not receive rewards")
		}
	})
}
