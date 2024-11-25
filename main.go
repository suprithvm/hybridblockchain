package main

import (
	
	"fmt"
	"time"
	"blockchain-core/blockchain"
)

func simulateTransactions() {
	utxoSet := make(map[string]blockchain.UTXO)

	// Create initial UTXO
	utxoSet["genesis-0"] = blockchain.UTXO{
		TransactionID: "genesis",
		OutputIndex:   0,
		Receiver:      "Alice",
		Amount:        100.0,
	}

	tx1 := blockchain.Transaction{
		TransactionID: "tx1",
		Inputs: []TransactionInput{
			{TransactionID: "genesis", OutputIndex: 0},
		},
		Outputs: []TransactionOutput{
			{Receiver: "Bob", Amount: 50.0},
			{Receiver: "Alice", Amount: 50.0},
		},
		Timestamp: time.Now().Unix(),
	}

	if blockchain.ValidateTransaction(tx1, utxoSet) {
		UpdateUTXOSet(tx1, utxoSet)
		fmt.Println("Transaction validated and UTXO updated")
	} else {
		fmt.Println("Transaction validation failed")
	}
}
