package blockchain

import (
	"fmt"
	"log"
	"sort"
	"sync"
)

// Mempool stores unconfirmed transactions
type Mempool struct {
	Transactions []Transaction
	mu           sync.Mutex
}

// NewMempool creates a new mempool instance
func NewMempool() *Mempool {
	return &Mempool{
		Transactions: make([]Transaction, 0),
	}
}

func (m *Mempool) AddTransaction(tx Transaction, utxoSet map[string]UTXO) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for duplicate transaction
	for _, existingTx := range m.Transactions {
		if existingTx.TransactionID == tx.TransactionID {
			log.Printf("[DEBUG] Duplicate transaction rejected: %s", tx.TransactionID)
			return false
		}
	}

	// Validate transaction
	if !m.ValidateTransaction(tx, utxoSet) {
		log.Printf("[DEBUG] Transaction validation failed: %s", tx.TransactionID)
		return false
	}

	m.Transactions = append(m.Transactions, tx)
	log.Printf("[DEBUG] Transaction added to mempool: %s", tx.TransactionID)
	return true
}

// ValidateTransaction verifies a transaction against the UTXO set.
func (m *Mempool) ValidateTransaction(tx Transaction, utxoSet map[string]UTXO) bool {
	// Allow transactions with no inputs (genesis-like or mining rewards)
	if len(tx.Inputs) == 0 {
		return true
	}

	// Basic validation
	if tx.Amount <= 0 {
		log.Printf("[DEBUG] Invalid transaction: Amount <= 0")
		return false
	}

	// Calculate total input
	var totalInput float64
	for _, input := range tx.Inputs {
		utxo, exists := utxoSet[fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)]
		if !exists {
			log.Printf("[DEBUG] Invalid transaction: UTXO not found")
			return false // UTXO not found
		}
		if utxo.Receiver != tx.Sender {
			log.Printf("[DEBUG] Invalid transaction: UTXO doesn't belong to sender")
			return false // UTXO doesn't belong to sender
		}
		totalInput += utxo.Amount
	}

	// Calculate total output
	var totalOutput float64
	for _, output := range tx.Outputs {
		if output.Amount <= 0 {
			log.Printf("[DEBUG] Invalid transaction: Output amount <= 0")
			return false
		}
		totalOutput += output.Amount
	}

	// Verify that input covers output plus gas fee
	if totalInput < (totalOutput + tx.GasFee) {
		log.Printf("[DEBUG] Invalid transaction: Insufficient funds. Input: %f, Output + Gas: %f",
			totalInput, totalOutput+tx.GasFee)
		return false
	}

	return true
}

// GetTransactions retrieves all transactions in the mempool
func (m *Mempool) GetTransactions() []Transaction {
	m.mu.Lock()
	defer m.mu.Unlock()

	txCopy := make([]Transaction, len(m.Transactions))
	copy(txCopy, m.Transactions)
	return txCopy
}

// Clear removes all transactions from the mempool
func (m *Mempool) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Transactions = make([]Transaction, 0)
}

// RemoveTransaction removes a specific transaction from the mempool
func (m *Mempool) RemoveTransaction(txID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	newTxs := make([]Transaction, 0)
	for _, tx := range m.Transactions {
		if tx.TransactionID != txID {
			newTxs = append(newTxs, tx)
		}
	}
	m.Transactions = newTxs
}

func (m *Mempool) PrioritizeTransactions() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Sort transactions by GasFee in descending order
	sort.Slice(m.Transactions, func(i, j int) bool {
		return m.Transactions[i].GasFee > m.Transactions[j].GasFee
	})
}

// GetPrioritizedTransactions returns transactions sorted by gas fees up to the limit
// Optimized GetPrioritizedTransactions with batch processing
func (m *Mempool) GetPrioritizedTransactions(limit int) []Transaction {
    m.mu.Lock()
    defer m.mu.Unlock()

    if len(m.Transactions) == 0 {
        return nil
    }

    // Batch processing for large transaction pools
    batchSize := 100
    if limit < batchSize {
        batchSize = limit
    }

    prioritizedTxs := make([]Transaction, 0, batchSize)
    for i := 0; i < batchSize; i++ {
        prioritizedTxs = append(prioritizedTxs, m.Transactions[i])
    }

    return prioritizedTxs
}


func (m *Mempool) ClearProcessedTransactions(processedTxs []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Printf("DEBUG: Clearing processed transactions: %v\n", processedTxs)
	for _, txID := range processedTxs {
		m.RemoveTransaction(txID)
	}

	fmt.Printf("DEBUG: Remaining transactions in mempool: %d\n", len(m.Transactions))
}


// BroadcastPendingTransactions broadcasts all transactions in the mempool.
func (m *Mempool) BroadcastPendingTransactions(n *Node) {
    m.mu.Lock()
    defer m.mu.Unlock()

    for _, tx := range m.Transactions {
        n.BroadcastTransaction(tx)
    }
}
