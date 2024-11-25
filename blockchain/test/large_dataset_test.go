package test

import (
	"fmt"
	"testing"
)

func TestLargeDataset(t *testing.T) {
	trie := blockchain.NewPatriciaTrie()

	// Insert 1 million transactions
	for i := 0; i < 1000000; i++ {
		tx := blockchain.Transaction{TransactionID: fmt.Sprintf("tx%d", i)}
		trie.Insert(tx)
	}

	// Verify root hash generation
	rootHash := trie.GenerateRootHash()
	if rootHash == "" {
		t.Errorf("Root hash generation failed for large dataset")
	}

	// Search for specific transactions
	tx, found := trie.Search("tx500000")
	if !found || tx == nil {
		t.Errorf("Transaction not found in large dataset")
	}
}
