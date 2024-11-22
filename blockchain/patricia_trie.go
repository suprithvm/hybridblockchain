package blockchain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Node represents a node in the Patricia Trie.
type PatriciaNode struct {
	Children map[byte]*PatriciaNode // Children nodes
	Value    *Transaction           // Stores the transaction at the leaf
	Hash     string                 // Hash of the current node
}

// PatriciaTrie represents the trie structure.
type PatriciaTrie struct {
	Root *PatriciaNode
}

// NewPatriciaTrie initializes a new trie.
func NewPatriciaTrie() *PatriciaTrie {
	return &PatriciaTrie{
		Root: &PatriciaNode{
			Children: make(map[byte]*PatriciaNode),
		},
	}
}

// Insert adds a transaction to the trie.
func (t *PatriciaTrie) Insert(tx Transaction) {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%v", tx)))
	key := hash[:]
	current := t.Root

	for _, b := range key {
		if _, exists := current.Children[b]; !exists {
			current.Children[b] = &PatriciaNode{
				Children: make(map[byte]*PatriciaNode),
			}
		}
		current = current.Children[b]
	}
	current.Value = &tx
	current.Hash = hex.EncodeToString(hash[:])
}

// Search finds a transaction in the trie by its ID (hash).
func (t *PatriciaTrie) Search(txHash []byte) (*Transaction, bool) {
	current := t.Root
	for _, b := range txHash {
		if _, exists := current.Children[b]; !exists {
			return nil, false
		}
		current = current.Children[b]
	}
	return current.Value, current.Value != nil
}

// GenerateRootHash computes the root hash of the trie.
func (t *PatriciaTrie) GenerateRootHash() string {
	return t.computeHash(t.Root)
}

func (t *PatriciaTrie) computeHash(node *PatriciaNode) string {
	if node == nil {
		return ""
	}

	if node.Value != nil {
		return node.Hash
	}

	var combinedHash []byte
	for _, child := range node.Children {
		combinedHash = append(combinedHash, []byte(t.computeHash(child))...)
	}

	hash := sha256.Sum256(combinedHash)
	node.Hash = hex.EncodeToString(hash[:])
	return node.Hash
}

// Add this new method to list all transactions in the trie
func (t *PatriciaTrie) ListAll() []Transaction {
	transactions := make([]Transaction, 0)
	if t.Root == nil {
		return transactions
	}

	// Helper function to traverse the trie
	var traverse func(*PatriciaNode)
	traverse = func(node *PatriciaNode) {
		if node == nil {
			return
		}

		// If this is a leaf node with a transaction
		if node.Value != nil {
			transactions = append(transactions, *node.Value)
		}

		// Recursively traverse all children
		for _, child := range node.Children {
			traverse(child)
		}
	}

	traverse(t.Root)
	return transactions
}
