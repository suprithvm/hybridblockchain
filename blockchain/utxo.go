package blockchain

import (
	"sync"
	"fmt"
	"log"
)


// UTXO represents an unspent transaction output
type UTXO struct {
	TransactionID string
	OutputIndex   int
	Receiver      string
	Amount        float64
	Owner         string
}
// UTXOPool manages all UTXOs
type UTXOPool struct {
	utxos map[string]UTXO
	mu    sync.Mutex
}

// NewUTXOPool initializes a new UTXO pool
func NewUTXOPool() *UTXOPool {
	return &UTXOPool{
		utxos: make(map[string]UTXO),
	}
}

// AddUTXO adds a new UTXO to the pool
func (pool *UTXOPool) AddUTXO(txID string, outputIndex int, amount float64, owner string) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	utxo := UTXO{
		TransactionID: txID,
		OutputIndex:   outputIndex,
		Amount:        amount,
		Owner:         owner,
	}
	key := fmt.Sprintf("%s:%d", txID, outputIndex)
	pool.utxos[key] = utxo
}

// RemoveUTXO removes a spent UTXO
func (pool *UTXOPool) RemoveUTXO(txID string, outputIndex int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	key := fmt.Sprintf("%s:%d", txID, outputIndex)
	delete(pool.utxos, key)
}

// ValidateTransaction checks if the sender has enough balance
func (pool *UTXOPool) ValidateTransaction(tx *Transaction) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	totalInput := 0.0
	for _, input := range tx.Inputs {
		key := fmt.Sprintf("%s:%d", input.TransactionID, input.OutputIndex)
		utxo, exists := pool.utxos[key]
		if !exists || utxo.Owner != tx.Sender {
			return false
		}
		totalInput += utxo.Amount
	}
	return totalInput >= tx.Amount
}


func UpdateUTXOSet(tx Transaction, utxoSet map[string]UTXO) {
    // Remove spent UTXOs
    for _, input := range tx.Inputs {
        key := fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)
        delete(utxoSet, key)
        log.Printf("[DEBUG] Removed UTXO: %s", key)
    }

    // Add new UTXOs
    for index, output := range tx.Outputs {
        key := fmt.Sprintf("%s-%d", tx.TransactionID, index)
        utxoSet[key] = UTXO{
            TransactionID: tx.TransactionID,
            OutputIndex:   index,
            Receiver:      output.Receiver,
            Amount:        output.Amount,
        }
        log.Printf("[DEBUG] Added UTXO: %s", key)
    }
}


