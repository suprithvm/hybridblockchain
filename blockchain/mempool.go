package blockchain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// Mempool stores unconfirmed transactions
type Mempool struct {
	Transactions []Transaction
	mu           sync.RWMutex
	maxSize      int
	timeouts     map[string]time.Time
	node         *Node
}

// NewMempool creates a new mempool instance with configuration
func NewMempool(node *Node) *Mempool {
	return &Mempool{
		Transactions: make([]Transaction, 0),
		maxSize:      1000,
		timeouts:     make(map[string]time.Time),
		node:         node,
	}
}

func (m *Mempool) AddTransaction(tx Transaction, utxoSet map[string]UTXO) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf("\n💫 Processing New Transaction")
	log.Printf("   ID: %s", tx.TransactionID)
	log.Printf("   From: %s", tx.Sender)
	log.Printf("   Gas Price: %d", tx.GasPrice)

	// Check for duplicate transaction
	for _, existingTx := range m.Transactions {
		if existingTx.TransactionID == tx.TransactionID {
			log.Printf("❌ Duplicate transaction rejected: %s", tx.TransactionID)
			return false
		}
	}

	// Validate transaction
	if !m.ValidateTransaction(tx, utxoSet) {
		log.Printf("❌ Transaction validation failed: %s", tx.TransactionID)
		return false
	}

	// Check if mempool is at capacity
	if len(m.Transactions) >= m.maxSize {
		// Sort by gas fee (highest to lowest)
		sort.SliceStable(m.Transactions, func(i, j int) bool {
			return m.Transactions[i].GasFee > m.Transactions[j].GasFee
		})

		// If new transaction has higher gas fee than lowest fee transaction
		if tx.GasFee > m.Transactions[len(m.Transactions)-1].GasFee {
			// Remove lowest fee transaction
			m.Transactions = m.Transactions[:len(m.Transactions)-1]
		} else {
			log.Printf("[DEBUG] Mempool full and transaction has low priority: %s", tx.TransactionID)
			return false
		}
	}

	m.Transactions = append(m.Transactions, tx)

	// Sort after adding new transaction
	sort.SliceStable(m.Transactions, func(i, j int) bool {
		return m.Transactions[i].GasFee > m.Transactions[j].GasFee
	})

	log.Printf("��� Transaction successfully added to mempool")
	log.Printf("   • Mempool size: %d/%d", len(m.Transactions), m.maxSize)
	return true
}

// ValidateTransaction checks if transaction inputs are valid
func (m *Mempool) ValidateTransaction(tx Transaction, utxos map[string]UTXO) bool {
	log.Printf("\n🔍 Validating Transaction: %s", tx.TransactionID)

	// Add nonce validation
	if !m.node.accountManager.ValidateNonce(tx.Sender, tx.Nonce) {
		log.Printf("❌ Invalid nonce")
		return false
	}

	// Validate gas parameters
	if err := tx.ValidateGas(); err != nil {
		log.Printf("❌ Gas validation failed: %v", err)
		return false
	}

	// Validate inputs
	inputSum := 0.0
	for _, input := range tx.Inputs {
		utxoKey := fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)
		utxo, exists := utxos[utxoKey]
		if !exists {
			log.Printf("❌ UTXO not found")
			return false
		}
		if utxo.Owner != tx.Sender {
			log.Printf("❌ UTXO doesn't belong to sender")
			return false
		}
		if utxo.Spent {
			log.Printf("❌ UTXO already spent")
			return false
		}
		inputSum += utxo.Amount
	}

	// Calculate total input value needed (amount + max gas fee)
	maxGasFee := tx.GasLimit * tx.MaxFeePerGas
	totalRequired := tx.Amount + float64(maxGasFee)

	if inputSum < totalRequired {
		log.Printf("❌ Insufficient funds for amount + gas: have %.8f, need %.8f",
			inputSum, totalRequired)
		return false
	}

	log.Printf("✅ Transaction validation successful")
	log.Printf("   • Gas Limit: %d", tx.GasLimit)
	log.Printf("   • Gas Price: %d", tx.GasPrice)
	log.Printf("   • Max Gas Fee: %d", maxGasFee)

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

// GetPrioritizedTransactions returns transactions sorted by priority
func (m *Mempool) GetPrioritizedTransactions(limit int) []Transaction {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Create a copy for sorting
	txCopy := make([]Transaction, len(m.Transactions))
	copy(txCopy, m.Transactions)

	// Sort by gas fee and age
	sort.SliceStable(txCopy, func(i, j int) bool {
		ageFactor := float64(time.Since(time.Unix(txCopy[i].Timestamp, 0)).Minutes())
		priorityI := txCopy[i].GasFee * (1 + (ageFactor / 1440)) // Age factor in days

		ageFactor = float64(time.Since(time.Unix(txCopy[j].Timestamp, 0)).Minutes())
		priorityJ := txCopy[j].GasFee * (1 + (ageFactor / 1440))

		return priorityI > priorityJ
	})

	if limit > len(txCopy) {
		limit = len(txCopy)
	}
	return txCopy[:limit]
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
		n.BroadcastTransaction(&tx, nil)
	}
}

// SyncMempool synchronizes mempool state with other nodes
func (m *Mempool) SyncMempool(peerMempool []Transaction) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create map of existing transactions for quick lookup
	existingTxs := make(map[string]bool)
	for _, tx := range m.Transactions {
		existingTxs[tx.TransactionID] = true
	}

	// Add new transactions from peer
	for _, tx := range peerMempool {
		if !existingTxs[tx.TransactionID] {
			if m.validateAndAddTransaction(tx) {
				log.Printf("[DEBUG] Added transaction from peer sync: %s", tx.TransactionID)
			}
		}
	}
}

// validateAndAddTransaction validates and adds a transaction
func (m *Mempool) validateAndAddTransaction(tx Transaction) bool {
	// Check transaction age
	if time.Since(time.Unix(tx.Timestamp, 0)) > 24*time.Hour {
		log.Printf("[DEBUG] Transaction too old: %s", tx.TransactionID)
		return false
	}

	// Check mempool size limit
	if len(m.Transactions) >= m.maxSize {
		m.evictOldTransactions()
	}

	// Set transaction timeout
	m.timeouts[tx.TransactionID] = time.Now().Add(24 * time.Hour)
	m.Transactions = append(m.Transactions, tx)
	return true
}

// evictOldTransactions removes old transactions when mempool is full
func (m *Mempool) evictOldTransactions() {
	// Sort by gas fee and timestamp
	sort.SliceStable(m.Transactions, func(i, j int) bool {
		if m.Transactions[i].GasFee == m.Transactions[j].GasFee {
			return m.Transactions[i].Timestamp > m.Transactions[j].Timestamp
		}
		return m.Transactions[i].GasFee > m.Transactions[j].GasFee
	})

	// Keep only the top transactions
	if len(m.Transactions) > m.maxSize {
		m.Transactions = m.Transactions[:m.maxSize]
	}
}

// MempoolSync represents the sync state of mempool
type MempoolSync struct {
	Transactions []Transaction `json:"transactions"`
	StateRoot    string        `json:"state_root"`
	Timestamp    int64         `json:"timestamp"`
	LastSyncTime int64         `json:"last_sync_time"`
}

// GetMempoolSync creates a sync snapshot of the mempool
func (mp *Mempool) GetMempoolSync() *MempoolSync {
	// Use RLock instead of Lock since we're only reading
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	sync := &MempoolSync{
		Transactions: make([]Transaction, 0, len(mp.Transactions)),
		Timestamp:    time.Now().Unix(),
	}

	// Create a copy of transactions directly instead of calling GetPrioritizedTransactions
	txCopy := make([]Transaction, len(mp.Transactions))
	copy(txCopy, mp.Transactions)

	// Sort transactions outside of lock
	mp.mu.RUnlock()
	sort.SliceStable(txCopy, func(i, j int) bool {
		if txCopy[i].GasFee == txCopy[j].GasFee {
			return txCopy[i].Timestamp > txCopy[j].Timestamp
		}
		return txCopy[i].GasFee > txCopy[j].GasFee
	})
	mp.mu.RLock()

	sync.Transactions = txCopy
	sync.StateRoot = mp.calculateStateRoot()
	return sync
}

// ApplySync applies a mempool sync
func (mp *Mempool) ApplySync(sync *MempoolSync) error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	// Clear existing transactions
	mp.Transactions = make([]Transaction, 0)

	// Apply new transactions with validation
	for _, tx := range sync.Transactions {
		txCopy := tx // Create copy to avoid pointer issues
		if !mp.ValidateTransaction(txCopy, nil) {
			log.Printf("[WARN] Invalid transaction in sync: %s", tx.TransactionID)
			continue
		}
		mp.Transactions = append(mp.Transactions, txCopy)
	}

	// Verify state root
	if mp.calculateStateRoot() != sync.StateRoot {
		return fmt.Errorf("state root mismatch after sync")
	}

	log.Printf("[INFO] Applied mempool sync with %d transactions", len(sync.Transactions))
	return nil
}

// calculateStateRoot calculates a deterministic hash of mempool state
func (mp *Mempool) calculateStateRoot() string {
	if len(mp.Transactions) == 0 {
		return ""
	}

	// Create hash from sorted transactions
	var combined string
	for _, tx := range mp.Transactions {
		combined += tx.Hash()
	}

	hash := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(hash[:])
}

// GetDeltaSync creates a delta sync snapshot of the mempool
func (mp *Mempool) GetDeltaSync(lastSyncTime int64) *MempoolSync {
	mp.mu.RLock()
	defer mp.mu.RUnlock()

	sync := &MempoolSync{
		Transactions: make([]Transaction, 0),
		Timestamp:    time.Now().Unix(),
		LastSyncTime: lastSyncTime,
	}

	// Only include transactions newer than lastSyncTime
	for _, tx := range mp.Transactions {
		if tx.Timestamp > lastSyncTime {
			sync.Transactions = append(sync.Transactions, tx)
		}
	}

	sync.StateRoot = mp.calculateStateRoot()
	return sync
}

// ApplyDeltaSync applies incremental updates
func (mp *Mempool) ApplyDeltaSync(delta *MempoolSync) error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	// Remove expired transactions
	mp.removeExpiredTransactions(delta.LastSyncTime)

	// Apply new transactions
	for _, tx := range delta.Transactions {
		txCopy := tx
		if !mp.ValidateTransaction(txCopy, nil) {
			log.Printf("[WARN] Invalid transaction in delta sync: %s", tx.TransactionID)
			continue
		}
		mp.Transactions = append(mp.Transactions, txCopy)
	}

	// Verify final state
	if mp.calculateStateRoot() != delta.StateRoot {
		return fmt.Errorf("state root mismatch after delta sync")
	}

	log.Printf("[INFO] Applied delta sync with %d transactions", len(delta.Transactions))
	return nil
}

func (mp *Mempool) removeExpiredTransactions(lastSyncTime int64) {
	current := make([]Transaction, 0)
	for _, tx := range mp.Transactions {
		if tx.Timestamp > lastSyncTime {
			current = append(current, tx)
		}
	}
	mp.Transactions = current
}

// Add new method for gas-based sorting
func (m *Mempool) SortByGasPrice() {
	m.mu.Lock()
	defer m.mu.Unlock()

	sort.SliceStable(m.Transactions, func(i, j int) bool {
		return m.Transactions[i].GasPrice > m.Transactions[j].GasPrice
	})
}
