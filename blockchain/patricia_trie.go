package blockchain

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
	"log"
)

// PatriciaNode represents a node in the Patricia Trie with caching.
type PatriciaNode struct {
	Key        []byte
	Children   map[byte]*PatriciaNode
	Value      *Transaction
	Hash       string
	IsLeaf     bool
	hashCache  string
	cacheMutex sync.RWMutex
	mu         sync.Mutex   // Protects Children
	Parent     *PatriciaNode
}

// PatriciaTrie represents the trie structure with caching.
type PatriciaTrie struct {
	Root *PatriciaNode
	mu   sync.RWMutex
}

// NewPatriciaTrie initializes a new Patricia Trie with caching.
func NewPatriciaTrie() *PatriciaTrie {
	return &PatriciaTrie{
		Root: &PatriciaNode{
			Children: make(map[byte]*PatriciaNode),
			IsLeaf:   false,
		},
	}
}


// Insert adds a batch of transactions into the trie.
func (t *PatriciaTrie) InsertBatch(transactions []Transaction) {
	for _, tx := range transactions {
		t.Insert(tx)
	}
}

// Insert adds a transaction into the Patricia Trie.
func (t *PatriciaTrie) Insert(tx Transaction) {
    hash := sha256.Sum256([]byte(tx.TransactionID))
    key := hash[:]
    current := t.Root

    for _, b := range key {
        current.mu.Lock()
        if _, exists := current.Children[b]; !exists {
            current.Children[b] = &PatriciaNode{
                Children: make(map[byte]*PatriciaNode),
                IsLeaf:   false,
                Parent:   current,
            }
        }
        next := current.Children[b]
        current.mu.Unlock() // Unlock the parent before moving to the child
        current = next
    }

    current.Value = &tx
    current.IsLeaf = true
    current.Hash = hex.EncodeToString(hash[:])
    t.MarkDirty(current)
}


// GenerateRootHash lazily computes the hash of the root node.
func (t *PatriciaTrie) GenerateRootHash() string {
	if t.Root == nil || len(t.Root.Children) == 0 {
		return "" // Empty trie
	}
	return t.computeHash(t.Root)
}

// computeHash lazily calculates or retrieves the cached hash of a Patricia Node.
func (t *PatriciaTrie) computeHash(node *PatriciaNode) string {
	if node == nil {
		return ""
	}

	// Check cached hash
	node.cacheMutex.RLock()
	if node.hashCache != "" {
		node.cacheMutex.RUnlock()
		return node.hashCache
	}
	node.cacheMutex.RUnlock()

	// Lock for updating cache
	node.cacheMutex.Lock()
	defer node.cacheMutex.Unlock()

	// If the node is a leaf, return its hash directly.
	if node.IsLeaf {
		node.hashCache = node.Hash
		return node.Hash
	}

	// Compute combined hash of children
	var combinedHash []byte
	for _, child := range node.Children {
		combinedHash = append(combinedHash, []byte(t.computeHash(child))...)
	}

	hash := sha256.Sum256(combinedHash)
	node.hashCache = hex.EncodeToString(hash[:])
	return node.hashCache
}

// GetTransaction retrieves a PatriciaNode for a transaction by its hash.
func (t *PatriciaTrie) GetTransaction(txID string) (*PatriciaNode, bool) {
	hash := sha256.Sum256([]byte(txID))
	current := t.Root

	for _, b := range hash[:] {
		if _, exists := current.Children[b]; !exists {
			return nil, false
		}
		current = current.Children[b]
	}

	return current, current.Value != nil
}

// Search retrieves a transaction by its ID.
func (t *PatriciaTrie) Search(txID string) (*Transaction, bool) {
	hash := sha256.Sum256([]byte(txID))
	current := t.Root

	for _, b := range hash[:] {
		if _, exists := current.Children[b]; !exists {
			return nil, false
		}
		current = current.Children[b]
	}

	return current.Value, current.Value != nil
}

// GetAllTransactions retrieves all transactions in the Patricia Trie.
func (t *PatriciaTrie) GetAllTransactions() []Transaction {
	var transactions []Transaction

	var traverse func(node *PatriciaNode)
	traverse = func(node *PatriciaNode) {
		if node == nil {
			return
		}
		if node.IsLeaf && node.Value != nil {
			transactions = append(transactions, *node.Value)
		}
		for _, child := range node.Children {
			traverse(child)
		}
	}

	traverse(t.Root)
	return transactions
}

// MarkDirty invalidates the hash cache for the node and propagates upwards.
func (t *PatriciaTrie) MarkDirty(node *PatriciaNode) {
	for node != nil {
		node.cacheMutex.Lock()
		node.hashCache = ""
		node.cacheMutex.Unlock()
		node = node.Parent
	}
}


// ProfileOperation profiles the duration of a function execution.
func ProfileOperation(operationName string, fn func()) {
	start := time.Now()
	fn()
	duration := time.Since(start)
	log.Printf("Operation %s took %s", operationName, duration)
}

// Example Usage
func (t *PatriciaTrie) ProfiledInsert(tx Transaction) {
	ProfileOperation("Insert Transaction", func() {
		t.Insert(tx)
	})
}


// Len returns the number of transactions in the Patricia Trie.
func (t *PatriciaTrie) Len() int {
    var count int

    var traverse func(node *PatriciaNode)
    traverse = func(node *PatriciaNode) {
        if node == nil {
            return
        }
        if node.IsLeaf && node.Value != nil {
            count++
        }
        for _, child := range node.Children {
            traverse(child)
        }
    }

    traverse(t.Root)
    return count
}
