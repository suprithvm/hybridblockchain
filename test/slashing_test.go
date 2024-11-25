package test

import (
	"blockchain-core/blockchain"
	"testing"
	"time"
)

func setupSlashingTest(t *testing.T) (*blockchain.SlashingManager, *blockchain.StakePool, *blockchain.Blockchain) {
	bc := blockchain.InitialiseBlockchain()
	stakePool := blockchain.NewStakePool()
	config := blockchain.DefaultConsensusParameters()
	slashingManager := blockchain.NewSlashingManager(stakePool, &bc, &config)
	return slashingManager, stakePool, &bc
}

func TestSlashing(t *testing.T) {
	slashingManager, stakePool, _ := setupSlashingTest(t)
	wallet, _ := blockchain.NewWallet()

	t.Run("Double Signing Detection", func(t *testing.T) {
		// Add stake for the validator
		err := stakePool.AddStake(wallet.Address, 1000.0)
		if err != nil {
			t.Fatalf("Failed to add stake: %v", err)
		}

		// First valid signature
		err = slashingManager.CheckAndSlashDoubleSign(
			wallet.Address,
			1,
			"block_hash_1",
			"signature_1",
		)
		if err != nil {
			t.Errorf("First signature should be accepted: %v", err)
		}

		// Second signature for same block (double signing)
		err = slashingManager.CheckAndSlashDoubleSign(
			wallet.Address,
			1,
			"block_hash_2", // Different hash
			"signature_2",
		)
		if err == nil {
			t.Error("Double signing not detected")
		}

		// Verify validator was slashed
		validatorInfo, _ := stakePool.GetValidatorInfo(wallet.Address)
		if !validatorInfo.IsSlashed {
			t.Error("Validator should be slashed for double signing")
		}

		// Verify slash amount (100% for double signing)
		if validatorInfo.Stake >= 1000.0 {
			t.Error("Validator stake should be slashed completely")
		}
	})

	t.Run("Inactivity Slashing", func(t *testing.T) {
		wallet2, _ := blockchain.NewWallet()
		stakePool.AddStake(wallet2.Address, 1000.0)

		// Update last activity
		slashingManager.UpdateValidatorActivity(wallet2.Address)

		// Wait for inactivity period
		time.Sleep(time.Second)

		// Check inactivity
		err := slashingManager.CheckAndSlashInactivity(wallet2.Address)
		if err != nil {
			t.Errorf("Unexpected error checking inactivity: %v", err)
		}

		// Verify validator status
		validatorInfo, _ := stakePool.GetValidatorInfo(wallet2.Address)
		initialStake := validatorInfo.Stake

		// Simulate long inactivity
		slashingManager.lastActivity[wallet2.Address] = time.Now().Add(-24 * time.Hour)
		err = slashingManager.CheckAndSlashInactivity(wallet2.Address)

		validatorInfo, _ = stakePool.GetValidatorInfo(wallet2.Address)
		if validatorInfo.Stake >= initialStake {
			t.Error("Validator should be slashed for inactivity")
		}
	})

	t.Run("Block Withholding", func(t *testing.T) {
		wallet3, _ := blockchain.NewWallet()
		stakePool.AddStake(wallet3.Address, 1000.0)

		// Simulate missed blocks
		for i := 0; i < 10; i++ {
			err := slashingManager.CheckAndSlashBlockWithholding(wallet3.Address, 5)
			if err != nil && i < 4 {
				t.Errorf("Should not slash before threshold: %v", err)
			}
			if err == nil && i >= 5 {
				t.Error("Should slash after threshold")
			}
		}

		// Verify validator was slashed
		validatorInfo, _ := stakePool.GetValidatorInfo(wallet3.Address)
		if !validatorInfo.IsSlashed {
			t.Error("Validator should be slashed for block withholding")
		}
	})

	t.Run("Low Uptime Slashing", func(t *testing.T) {
		wallet4, _ := blockchain.NewWallet()
		stakePool.AddStake(wallet4.Address, 1000.0)

		// Test with good uptime
		err := slashingManager.CheckAndSlashLowUptime(wallet4.Address, 0.96)
		if err != nil {
			t.Errorf("Should not slash for good uptime: %v", err)
		}

		// Test with low uptime
		err = slashingManager.CheckAndSlashLowUptime(wallet4.Address, 0.2)
		if err == nil {
			t.Error("Should slash for low uptime")
		}

		// Verify validator was slashed
		validatorInfo, _ := stakePool.GetValidatorInfo(wallet4.Address)
		if !validatorInfo.IsSlashed {
			t.Error("Validator should be slashed for low uptime")
		}
	})

	t.Run("Violation History", func(t *testing.T) {
		wallet5, _ := blockchain.NewWallet()
		stakePool.AddStake(wallet5.Address, 1000.0)

		// Create some violations
		slashingManager.CheckAndSlashLowUptime(wallet5.Address, 0.2)
		slashingManager.CheckAndSlashBlockWithholding(wallet5.Address, 1)

		// Check violation history
		violations := slashingManager.GetValidatorViolations(wallet5.Address)
		if len(violations) != 2 {
			t.Errorf("Expected 2 violations, got %d", len(violations))
		}

		// Verify violation details
		for _, violation := range violations {
			if violation.Validator != wallet5.Address {
				t.Error("Violation record has incorrect validator address")
			}
			if violation.Amount <= 0 {
				t.Error("Violation should have non-zero slash amount")
			}
		}
	})
}
