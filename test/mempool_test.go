package blockchain

import (
	"fmt"
	"testing"
	"time"
)

func TestMempool(t *testing.T) {
	mempool := NewMempool()
	utxoSet := make(map[string]UTXO)

	// Add valid UTXO
	utxoID := "tx1-0"
	utxoSet[utxoID] = UTXO{
		TransactionID: "tx1",
		OutputIndex:   0,
		Amount:        100.0,
		Receiver:      "Alice",
	}

	// Valid Transaction
	tx1 := Transaction{
		TransactionID: "tx2",
		Sender:        "Alice",
		Receiver:      "Bob",
		Amount:        50.0,
		GasFee:        1.0,
		Inputs: []UTXO{
			{
				TransactionID: "tx1",
				OutputIndex:   0,
				Amount:        100.0,
				Receiver:      "Alice",
			},
		},
		Outputs: []UTXO{
			{
				TransactionID: "tx2",
				OutputIndex:   0,
				Amount:        50.0,
				Receiver:      "Bob",
			},
		},
		Timestamp: time.Now().Unix(),
	}

	if !mempool.AddTransaction(tx1, utxoSet) {
		t.Error("Valid transaction should be added to mempool")
	}

	if len(mempool.GetTransactions()) != 1 {
		t.Error("Transaction was not added to mempool")
	}

	// Invalid Transaction (insufficient funds)
	tx2 := Transaction{
		TransactionID: "tx3",
		Sender:        "Alice",
		Receiver:      "Charlie",
		Amount:        200.0,
		GasFee:        1.0,
		Inputs: []UTXO{
			{
				TransactionID: "tx1",
				OutputIndex:   0,
				Amount:        100.0,
				Receiver:      "Alice",
			},
		},
		Outputs: []UTXO{
			{
				TransactionID: "tx3",
				OutputIndex:   0,
				Amount:        200.0,
				Receiver:      "Charlie",
			},
		},
		Timestamp: time.Now().Unix(),
	}

	if mempool.AddTransaction(tx2, utxoSet) {
		t.Error("Invalid transaction should not be added to mempool")
	}
}

func TestNewBlock(t *testing.T) {
	genesis := GenesisBlock()
	utxoSet := make(map[string]UTXO)
	mempool := NewMempool()

	// Add valid transaction
	tx1 := Transaction{
		Sender:    "Alice",
		Receiver:  "Bob",
		Amount:    50.0,
		Inputs:    []UTXO{},
		Outputs:   []UTXO{{Receiver: "Bob", Amount: 50.0}},
		Timestamp: time.Now().Unix(),
	}
	mempool.AddTransaction(tx1, utxoSet)

	// Create new block with mempool
	block := NewBlock(genesis, mempool, utxoSet, 2, "Validator1")

	// Validate block
	if block.PreviousHash != genesis.Hash {
		t.Errorf("PreviousHash mismatch. Got %s, expected %s", block.PreviousHash, genesis.Hash)
	}

	if block.BlockNumber != 1 {
		t.Errorf("BlockNumber mismatch. Got %d, expected 1", block.BlockNumber)
	}
}

func TestBlockchain(t *testing.T) {
	bc := InitialiseBlockchain()
	utxoSet := make(map[string]UTXO)
	stakePool := NewStakePool()

	// Add stake for validator
	stakePool.AddStake("Validator1", 100.0)

	// Create transaction
	tx := Transaction{
		TransactionID: "test-tx",
		Sender:        "Alice",
		Receiver:      "Bob",
		Amount:        50.0,
		GasFee:        1.0,
		Inputs:        []UTXO{},
		Outputs:       []UTXO{{Receiver: "Bob", Amount: 50.0}},
		Timestamp:     time.Now().Unix(),
	}

	// Create mempool and add transaction
	mempool := NewMempool()
	mempool.AddTransaction(tx, utxoSet)

	// Add block using the correct signature
	bc.AddBlock(mempool, stakePool, utxoSet)

	// Verify chain length (should be 2: genesis block + 1 new block)
	expectedLength := 2 // Genesis block only, since the new block failed validation
	if len(bc.Chain) != expectedLength {
		t.Errorf("Blockchain length mismatch. Got %d, expected %d", len(bc.Chain), expectedLength)
	}
}

func TestBroadcastTransaction(t *testing.T) {
	node, _ := NewNode("/ip4/127.0.0.1/tcp/4001")
	tx := Transaction{
		Sender:    "Alice",
		Receiver:  "Bob",
		Amount:    50.0,
		Inputs:    []UTXO{},
		Outputs:   []UTXO{{Receiver: "Bob", Amount: 50.0}},
		Timestamp: time.Now().Unix(),
	}

	node.BroadcastTransaction(tx)
	// Manually check logs or peer behavior

}

func TestBroadcastBlock(t *testing.T) {
	node, _ := NewNode("/ip4/127.0.0.1/tcp/4001")
	genesis := GenesisBlock()
	utxoSet := make(map[string]UTXO)
	mempool := NewMempool()

	// Add valid transaction
	tx1 := Transaction{
		Sender:    "Alice",
		Receiver:  "Bob",
		Amount:    50.0,
		Inputs:    []UTXO{},
		Outputs:   []UTXO{{Receiver: "Bob", Amount: 50.0}},
		Timestamp: time.Now().Unix(),
	}
	mempool.AddTransaction(tx1, utxoSet)

	// Create new block
	block := NewBlock(genesis, mempool, utxoSet, 2, "Validator1")

	node.BroadcastBlock(block)
	// Manually check logs or peer behavior

}

func TestValidateTransaction(t *testing.T) {
	mempool := NewMempool()
	utxoSet := make(map[string]UTXO)

	// Add valid UTXO
	utxoSet["tx1-0"] = UTXO{
		TransactionID: "tx1",
		OutputIndex:   0,
		Amount:        100.0,
		Receiver:      "Alice",
	}

	// Valid Transaction
	tx1 := Transaction{
		TransactionID: "tx1",
		Sender:        "Alice",
		Receiver:      "Bob",
		Amount:        50.0,
		GasFee:        0.1,
		Inputs:        []UTXO{utxoSet["tx1-0"]},
		Outputs: []UTXO{{
			Receiver: "Bob",
			Amount:   50.0,
		}},
	}

	if !mempool.AddTransaction(tx1, utxoSet) {
		t.Error("Valid transaction should be added to mempool")
	}

	// Verify transaction was added
	if len(mempool.GetTransactions()) != 1 {
		t.Error("Transaction was not added to mempool")
	}
}

func TestBlockRewardDistribution(t *testing.T) {
	bc := InitialiseBlockchain()
	utxoSet := make(map[string]UTXO)
	stakePool := NewStakePool()
	mempool := NewMempool()

	// Add stakes for validator selection
	stakePool.AddStake("Validator1", 100.0)
	stakePool.AddStake("Validator2", 50.0)

	// Add a valid transaction to mempool
	tx := Transaction{
		TransactionID: "test-tx",
		Sender:        "Alice",
		Receiver:      "Bob",
		Amount:        20.0,
		GasFee:        1.0,
		Inputs:        []UTXO{},
		Outputs:       []UTXO{{Receiver: "Bob", Amount: 20.0}},
		Timestamp:     time.Now().Unix(),
	}
	mempool.AddTransaction(tx, utxoSet)

	// Create and add block directly
	previousBlock := bc.GetLatestBlock()
	newBlock := NewBlock(previousBlock, mempool, utxoSet, 1, "Validator1")

	// Mine the block to meet difficulty
	err := MineBlock(&newBlock, previousBlock, stakePool, 10)
	if err != nil {
		t.Fatalf("Failed to mine block: %v", err)
	}

	// Add the mined block
	if ValidateBlock(newBlock, previousBlock, "Validator1", stakePool) {
		bc.Chain = append(bc.Chain, newBlock)
	} else {
		t.Fatal("Block validation failed")
	}

	// Check if block was added
	if len(bc.Chain) != 2 { // Genesis + new block
		t.Fatalf("Block was not added to chain. Chain length: %d", len(bc.Chain))
	}

	// Check for reward transaction in UTXOs
	rewardTxID := fmt.Sprintf("reward-%d", newBlock.BlockNumber)
	rewardUTXOKey := fmt.Sprintf("%s-0", rewardTxID)

	if utxo, exists := utxoSet[rewardUTXOKey]; exists {
		expectedReward := BlockReward + tx.GasFee
		if utxo.Amount != expectedReward {
			t.Errorf("Incorrect reward amount. Got %f, expected %f", utxo.Amount, expectedReward)
		}
		if utxo.Receiver != "Validator1" {
			t.Errorf("Incorrect reward receiver. Got %s, expected Validator1", utxo.Receiver)
		}
	} else {
		t.Error("Reward UTXO not found in UTXO set")
	}
}

func TestDynamicBlockSize(t *testing.T) {
	mempool := NewMempool()
	utxoSet := make(map[string]UTXO)

	expectedTxCount := MaxBlockSizeLimit / AvgTransactionSize
	testTxCount := int(expectedTxCount) + 100 // Add extra transactions to test limiting

	// Populate the mempool with transactions
	for i := 1; i <= testTxCount; i++ {
		tx := Transaction{
			TransactionID: fmt.Sprintf("tx%d", i),
			Sender:        "Alice",
			Receiver:      "Bob",
			Amount:        float64(10),
			GasFee:        float64(i), // Ascending gas fee
			Outputs:       []UTXO{{Receiver: "Bob", Amount: 10}},
		}
		mempool.AddTransaction(tx, utxoSet)
	}

	// Get dynamic block size
	blockSize := CalculateDynamicBlockSize(len(mempool.GetTransactions()))
	prioritizedTxs := mempool.GetPrioritizedTransactions(blockSize)

	// Verify the number of transactions doesn't exceed max block size
	maxPossibleTxs := MaxBlockSizeLimit / AvgTransactionSize
	if len(prioritizedTxs) > int(maxPossibleTxs) {
		t.Errorf("Block exceeds maximum size. Got %d transactions, max allowed %d",
			len(prioritizedTxs), maxPossibleTxs)
	}

	// Verify transactions are ordered by gas fee (highest to lowest)
	for i := 0; i < len(prioritizedTxs)-1; i++ {
		if prioritizedTxs[i].GasFee < prioritizedTxs[i+1].GasFee {
			t.Errorf("Transactions not properly sorted by gas fee at index %d and %d", i, i+1)
		}
	}
}

func TestAddBlockWithDynamicSize(t *testing.T) {
	bc := InitialiseBlockchain()
	mempool := NewMempool()
	utxoSet := make(map[string]UTXO)
	stakePool := NewStakePool()

	// Add stakes for validator selection
	stakePool.AddStake("Validator1", 100.0)

	// Populate the mempool
	for i := 1; i <= 30; i++ {
		tx := Transaction{
			TransactionID: fmt.Sprintf("tx%d", i),
			Sender:        "Alice",
			Receiver:      "Bob",
			Amount:        10.0,
			GasFee:        float64(i),
		}
		mempool.AddTransaction(tx, utxoSet)
	}

	// Add a new block
	bc.AddBlock(mempool, stakePool, utxoSet)

	// Verify blockchain length
	if len(bc.Chain) != 2 { // Genesis + 1 new block
		t.Errorf("Blockchain length mismatch. Expected 2, got %d", len(bc.Chain))
	}

	// Verify transactions were processed
	if len(mempool.GetTransactions()) != 0 {
		t.Errorf("Mempool not cleared. Remaining transactions: %d", len(mempool.GetTransactions()))
	}
}
