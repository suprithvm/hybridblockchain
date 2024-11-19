package blockchain

import "sync"

//Mempool stores unconfirmed transactions

type Mempool struct{
	Transactions []Transaction
	mu 			 sync.Mutex
}

//Add Transaction adds a transaction to the mempool
func (m *Mempool) AddTransaction(tx Transaction){
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Transactions = append(m.Transactions,tx)
}