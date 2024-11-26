package blockchain

import (
	"log"
	"strconv"
	"testing"

	"github.com/libp2p/go-libp2p"
)

func TestWalletCreationAndTransactions(t *testing.T) {
	// Create two wallets
	wallet1, err := NewWallet()
	log.Print("[INFO] wallet1: ", wallet1.Address)
	if err != nil {
		t.Fatalf("Failed to create wallet1: %v", err)
	}

	wallet2, err := NewWallet()
	log.Print("[INFO] wallet2: ", wallet2.Address)
	if err != nil {
		t.Fatalf("Failed to create wallet2: %v", err)
	}

	// Ensure wallets have valid addresses
	if !ValidateAddress(wallet1.Address) || !ValidateAddress(wallet2.Address) {
		t.Fatalf("Invalid wallet addresses generated")
	}

	// Create a transaction
	tx := NewTransaction(wallet1.Address, wallet2.Address, 100.0, 0.5)
	err = wallet1.SignTransaction(tx)
	if err != nil {
		t.Fatalf("Failed to sign transaction: %v", err)
	}

	// Verify the transaction
	if !wallet1.VerifyTransaction(tx) {
		t.Fatal("Transaction verification failed")
	}

	t.Logf("Transaction verified successfully: %+v", tx)
}

func TestBlockMiningAndAddition(t *testing.T) {
	// Initialize blockchain and supporting components
	blockchain := InitialiseBlockchain()
	mempool := NewMempool()
	utxoSet := make(map[string]UTXO)

	// Generate wallets
	wallet, err := NewWallet()
	if err != nil {
		t.Fatalf("Failed to create wallet: %v", err)
	}

	// Add transactions to mempool
	for i := 0; i < 5; i++ {
		tx := NewTransaction(wallet.Address, "receiver"+strconv.Itoa(i), float64(10*(i+1)), 0.2)
		success := mempool.AddTransaction(*tx, utxoSet)
		if !success {
			t.Fatalf("Failed to add transaction to mempool: %v", err)
		}
	}

	// Validate mempool has transactions
	if len(mempool.GetTransactions()) == 0 {
		t.Fatalf("Mempool is empty, transactions not added properly")
	}

	// Create and add a block
	peerHost, err := libp2p.New()
	if err != nil {
		t.Fatalf("Failed to initialize peer host: %v", err)
	}
	defer peerHost.Close()

	stakePool := NewStakePool()
	err = stakePool.AddStake(wallet.Address, peerHost.ID().String(), 100)
	if err != nil {
		t.Fatalf("Failed to add stake for validator: %v", err)
	}

	blockchain.AddBlock(mempool, stakePool, utxoSet, peerHost)

	// Validate new block addition
	if len(blockchain.Chain) != 2 {
		t.Fatalf("Block not added properly to the blockchain")
	}

	t.Logf("Blockchain updated successfully: %+v", blockchain.Chain)
}

func TestValidatorSelectionAndBroadcast(t *testing.T) {
	// Initialize stake pool and P2P host
	stakePool := NewStakePool()
	peerHost, err := libp2p.New()
	if err != nil {
		t.Fatalf("Failed to initialize peer host: %v", err)
	}
	defer peerHost.Close()

	// Generate wallets for validators
	wallet1, _ := NewWallet()
	wallet2, _ := NewWallet()

	// Add stakes for wallets
	err = stakePool.AddStake(wallet1.Address, peerHost.ID().String(), 200)
	if err != nil {
		t.Fatalf("Failed to add stake for wallet1: %v", err)
	}
	err = stakePool.AddStake(wallet2.Address, "host2", 300)
	if err != nil {
		t.Fatalf("Failed to add stake for wallet2: %v", err)
	}

	// Select a validator
	validatorWallet, validatorHost, err := stakePool.SelectValidator(peerHost)
	if err != nil {
		t.Fatalf("Validator selection failed: %v", err)
	}

	if validatorWallet == "" || validatorHost == "" {
		t.Fatalf("Validator selection returned empty wallet or host")
	}

	t.Logf("Validator selected: Wallet=%s, Host=%s", validatorWallet, validatorHost)
}

func TestPatriciaTrieIntegration(t *testing.T) {
    // Initialize Patricia Trie
    trie := NewPatriciaTrie()

    var targetTx Transaction

    // Generate wallets for dynamic sender and receiver addresses
    wallets := make([]*Wallet, 10)
    for i := 0; i < 10; i++ {
        wallet, err := NewWallet()
        if err != nil {
            t.Fatalf("Failed to create wallet %d: %v", i, err)
        }
        wallets[i] = wallet
    }

    // Create transactions with dynamically generated wallet addresses
    for i := 0; i < 10; i++ {
        sender := wallets[i].Address
        receiver := wallets[(i+1)%10].Address // Ensure a different receiver for each transaction
        tx := NewTransaction(sender, receiver, float64(10*(i+1)), 0.1)

        // Sign the transaction using the sender's private key
        err := wallets[i].SignTransaction(tx)
        if err != nil {
            t.Fatalf("Failed to sign transaction %d: %v", i, err)
        }

        trie.Insert(*tx)

        // Save a transaction for validation
        if i == 5 {
            targetTx = *tx
        }
    }

    // Use the dynamically generated TransactionID for search
    txID := targetTx.TransactionID
    foundTx, exists := trie.Search(txID)
    if !exists || foundTx.TransactionID != txID {
        t.Fatalf("Transaction with ID %s not found in trie", txID)
    }

    t.Logf("Transaction found in trie: %+v", foundTx)
}


func TestDynamicDifficultyAdjustment(t *testing.T) {
	// Create blocks with different timestamps
	block1 := GenesisBlock()
	block2 := Block{
		BlockNumber:  1,
		PreviousHash: block1.Hash,
		Timestamp:    block1.Timestamp + 15, // Simulate a block created after 15 seconds
		Difficulty:   block1.Difficulty,
	}

	// Adjust difficulty
	targetTime := int64(10) // Target block time in seconds
	newDifficulty := AdjustDifficulty(block2, targetTime)

	// Validate difficulty adjustment logic
	if newDifficulty != block1.Difficulty+1 {
		t.Fatalf("Difficulty adjustment failed. Expected %d, got %d", block1.Difficulty+1, newDifficulty)
	}

	t.Logf("Difficulty adjusted successfully: %d", newDifficulty)
}

func TestValidateAddress(t *testing.T) {
    wallet, err := NewWallet()
    if err != nil {
        t.Fatalf("Failed to create wallet: %v", err)
    }

    if !ValidateAddress(wallet.Address) {
        t.Errorf("Address validation failed for address: %s", wallet.Address)
    } else {
        t.Logf("Address validation passed for address: %s", wallet.Address)
    }
}
