package blockchain

import (
	"bytes"
	"encoding/gob"
	"time"
)

// Transaction represents a single transaction in the blockchain
type Transaction struct {
	Sender    string
	Receiver  string
	Amount    float64
	Timestamp int64
	Signature string // Ensure this field is properly serialized
}

// NewTransaction creates a new transaction
func NewTransaction(sender, receiver string, amount float64, signature string) Transaction {
	return Transaction{
		Sender:    sender,
		Receiver:  receiver,
		Amount:    amount,
		Timestamp: time.Now().Unix(),
		Signature: signature,
	}
}

// SerializeTransaction serializes a transaction
func SerializeTransaction(tx Transaction) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := gob.NewEncoder(&buffer)
	err := encoder.Encode(tx)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// DeserializeTransaction deserializes a transaction
func DeserializeTransaction(data []byte) (Transaction, error) {
	var tx Transaction
	decoder := gob.NewDecoder(bytes.NewReader(data))
	err := decoder.Decode(&tx)
	return tx, err
}
