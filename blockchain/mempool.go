package blockchain

import (
	"sort"
	"sync"
)

// Mempool stores unconfirmed transactions
type Mempool struct {
	Transactions []Transaction
	mu           sync.Mutex
}

// AddTransaction adds a transaction to the mempool
func (m *Mempool) AddTransaction(tx Transaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Transactions = append(m.Transactions, tx)
}

// GetTransactions retrieves all transactions in the mempool
func (m *Mempool) GetTransactions() []Transaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Transactions
}

// Clear clears all transactions from the mempool
func (m *Mempool) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Transactions = nil
}

func (m *Mempool) ValidateTransaction(tx Transaction) bool {
	// Basic validation: Check for duplicate transactions
	for _, existingTx := range m.GetTransactions() {
		if existingTx == tx {
			return false // Duplicate transaction
		}
	}
	// Additional validation logic can be added here
	return true
}

func (m *Mempool) RemoveTransaction(tx Transaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existingTx := range m.Transactions {
		if existingTx == tx {
			m.Transactions = append(m.Transactions[:i], m.Transactions[i+1:]...)
			break
		}
	}
}


func (m *Mempool) PrioritizeTransactions(stakePool *StakePool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Sort transactions based on sender's stake.
	sort.Slice(m.Transactions, func(i, j int) bool {
		return stakePool.Stakes[m.Transactions[i].Sender] > stakePool.Stakes[m.Transactions[j].Sender]
	})
}


