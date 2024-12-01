package blockchain

import (
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckpointSyncs(t *testing.T) {
	log.Printf("\n%s\n Starting Checkpoint Sync Test\n%s\n", strings.Repeat("=", 80), strings.Repeat("=", 80))

	// Initialize test blockchain with proper components
	bc := InitialiseBlockchain()
	wallet, _ := NewWallet()
	mp := NewMempool()
	sp := NewStakePool()
	utxoSet := make(map[string]UTXO)

	log.Printf("🏦 Initialized blockchain components")
	log.Printf("📝 Wallet Address: %s", wallet.Address)

	// Add stake to the validator (wallet)
	stakeAmount := 1000.0
	sp.AddStake(wallet.Address, "test-node", stakeAmount)
	log.Printf("💰 Added stake: %.2f to validator", stakeAmount)

	// Create blocks with real transactions and mining
	numBlocks := 5 // We'll create 6 blocks total
	lastBlock := bc.GetLatestBlock()

	log.Printf("\n%s\n📦 Starting Block Creation (Target: %d blocks)\n%s\n", 
		strings.Repeat("-", 50), numBlocks, strings.Repeat("-", 50))

	for i := 0; i < numBlocks; i++ {
		log.Printf("\n🔨 Creating Block %d/%d", i+1, numBlocks)
		
		// Create multiple transactions per block
		numTxPerBlock := 5
		for j := 0; j < numTxPerBlock; j++ {
			amount := float64(10 + j)
			gasFee := 0.1 * float64(j+1)
			
			tx := NewTransaction(wallet.Address, fmt.Sprintf("receiver-%d", j), amount, gasFee)
			
			mp.AddTransaction(*tx, utxoSet)
			log.Printf("   💸 Added transaction: Amount=%.2f, Gas=%.2f", amount, gasFee)
		}

		// Create and mine block with proper difficulty
		difficulty := 1
		block := NewBlock(lastBlock, mp, utxoSet, difficulty, wallet.Address)
		block.BlockNumber = i + 1
		block.PreviousHash = lastBlock.Hash
		
		// Initialize Patricia Trie for the block
		block.Transactions = NewPatriciaTrie()
		
		// Add transactions to block's Patricia Trie and calculate root
		for _, tx := range mp.GetTransactions() {
			block.Transactions.Insert(tx)
		}
		
		// Calculate and set Patricia root
		block.PatriciaRoot = block.Transactions.GenerateRootHash()

		log.Printf("   ⛏️  Mining block with difficulty %d", difficulty)
		err := MineBlock(&block, lastBlock, sp, 10, nil)
		if err != nil {
			log.Printf("   ❌ Mining failed: %v", err)
			t.Fatal(err)
		}

		// Create checkpoint if needed
		if (i + 1) % CheckpointInterval == 0 {
			checkpoint := block.CreateCheckpoint()
			log.Printf("   ✅ CHECKPOINT created at block %d", i+1)
			log.Printf("      📍 Height: %d", checkpoint.Height)
			log.Printf("      🔗 Hash: %.8s...", checkpoint.Hash)
			log.Printf("      🌳 State Root: %.8s...", checkpoint.StateRoot)
		}

		// Add block to chain
		bc.mu.Lock()
		bc.Chain = append(bc.Chain, block)
		bc.mu.Unlock()
		lastBlock = block
		
		log.Printf("   ✅ Block %d added successfully - Hash: %.8s...", i+1, block.Hash)
	}

	t.Run("Checkpoint Verification", func(t *testing.T) {
		log.Printf("\n%s\n🔍 Verifying Checkpoints\n%s\n", 
			strings.Repeat("-", 50), strings.Repeat("-", 50))

		checkpoints := bc.GetCheckpoints(0, uint64(numBlocks))
		if len(checkpoints) == 0 {
			t.Fatal("No checkpoints found")
		}
		log.Printf("📊 Found %d checkpoints", len(checkpoints))

		for i, cp := range checkpoints {
			if cp == nil {
				t.Fatalf("Checkpoint %d is nil", i+1)
			}
			
			block := bc.GetBlockByHeight(int(cp.Height))
			if block == nil {
				t.Fatalf("Block at height %d not found", cp.Height)
			}

			log.Printf("\n🔎 Verifying Checkpoint %d:", i+1)
			log.Printf("   📍 Height: %d", cp.Height)
			log.Printf("   🔗 Hash: %.8s...", cp.Hash)
			log.Printf("   🌳 State Root: %.8s...", cp.StateRoot)
			
			assert.Equal(t, block.Hash, cp.Hash, "Block hash mismatch at height %d", cp.Height)
			assert.Equal(t, block.PatriciaRoot, cp.StateRoot, "State root mismatch at height %d", cp.Height)
		}
	})

	log.Printf("\n%s\n✨ Checkpoint Sync Test Completed\n%s\n", 
		strings.Repeat("=", 80), strings.Repeat("=", 80))
} 