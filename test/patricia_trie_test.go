package blockchain

import (
	"testing"
	"crypto/sha256"
	"fmt"
)

func TestPatriciaTrie_InsertAndSearch(t *testing.T) {
	trie := NewPatriciaTrie()
	tx := NewTransaction("Alice", "Bob", 10, "Signature1")
	trie.Insert(tx)

	txHash := sha256.Sum256([]byte(fmt.Sprintf("%v", tx)))
	foundTx, exists := trie.Search(txHash[:])
	if !exists || foundTx.Sender != "Alice" {
		t.Errorf("Failed to retrieve the transaction from the Patricia Trie")
	}
}

func TestPatriciaTrie_GenerateRootHash(t *testing.T) {
	trie := NewPatriciaTrie()
	tx1 := NewTransaction("Alice", "Bob", 10, "Signature1")
	tx2 := NewTransaction("Charlie", "Dave", 5, "Signature2")
	trie.Insert(tx1)
	trie.Insert(tx2)

	rootHash := trie.GenerateRootHash()
	if rootHash == "" {
		t.Errorf("Failed to generate Patricia Root Hash")
	}
}
