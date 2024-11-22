package blockchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"time"
)

// Transaction represents a single transaction in the blockchain
type Transaction struct {
	Sender    string
	Receiver  string
	Amount    float64
	Timestamp int64
	Signature string // Digital signature for verification
}

// NewTransaction creates a new transaction
func NewTransaction(sender, receiver string, amount float64) *Transaction {
	return &Transaction{
		Sender:    sender,
		Receiver:  receiver,
		Amount:    amount,
		Timestamp: time.Now().Unix(),
	}
}

// Hash computes a hash of the transaction data
func (tx *Transaction) Hash() string {
	data := fmt.Sprintf("%s%s%f%d", tx.Sender, tx.Receiver, tx.Amount, tx.Timestamp)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// SerializeTransaction serializes a transaction
func SerializeTransaction(tx *Transaction) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := gob.NewEncoder(&buffer)
	err := encoder.Encode(tx)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// DeserializeTransaction deserializes a transaction
func DeserializeTransaction(data []byte) (*Transaction, error) {
	var tx Transaction
	decoder := gob.NewDecoder(bytes.NewReader(data))
	err := decoder.Decode(&tx)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}


