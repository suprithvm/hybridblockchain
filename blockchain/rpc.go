package blockchain

import (
	"blockchain-core/blockchain/gas"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type RPCService struct {
	node        *Node
	blockchain  *Blockchain
	mempool     *Mempool
	peerManager *PeerManager
	mu          sync.RWMutex
}

func NewRPCService(node *Node) *RPCService {
	return &RPCService{
		node:        node,
		blockchain:  node.Blockchain,
		mempool:     node.Mempool,
		peerManager: node.PeerManager,
	}
}

// Account Management Endpoints
func (s *RPCService) GetBalance(address string) (float64, error) {
	// Try cache first
	if balance, ok := s.node.accountManager.cache.GetBalance(address); ok {
		return balance, nil
	}

	// Get account state
	state, err := s.node.accountManager.GetAccountState(address)
	if err != nil {
		return 0, err
	}

	return state.Balance, nil
}

func (s *RPCService) GetTransactionHistory(address string) ([]Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var history []Transaction

	// Get from blockchain
	for _, block := range s.blockchain.Chain {
		txs := block.Body.Transactions.GetAllTransactions()
		for _, tx := range txs {
			if tx.Sender == address || tx.Receiver == address {
				history = append(history, tx)
			}
		}
	}

	// Get pending from mempool
	for _, tx := range s.mempool.GetTransactions() {
		if tx.Sender == address || tx.Receiver == address {
			history = append(history, tx)
		}
	}

	return history, nil
}

func (s *RPCService) SendTransaction(from, to string, amount float64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := &Transaction{
		Sender:    from,
		Receiver:  to,
		Amount:    amount,
		Timestamp: time.Now().Unix(),
	}

	// Calculate gas using the gas package
	gasCalculator := gas.NewGasCalculator(s.node.GetGasModel())
	gasInfo := gasCalculator.CalculateTransactionGas(len(tx.Data), gas.PriorityNormal)

	tx.GasLimit = gasInfo.GasUsed
	tx.GasPrice = gasInfo.GasPrice

	// Validate and add to mempool
	if !s.mempool.AddTransaction(*tx, s.node.UTXOPool.GetUTXOs()) {
		return "", fmt.Errorf("transaction validation failed")
	}

	return tx.Hash(), nil
}

// Node Operations
func (s *RPCService) GetBlockHeight() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return uint64(len(s.blockchain.Chain) - 1)
}

func (s *RPCService) GetPeerCount() int {
	return len(s.peerManager.GetTrustedPeers())
}

func (s *RPCService) GetMempool() []Transaction {
	return s.mempool.GetTransactions()
}

// Network Status
func (s *RPCService) GetSyncStatus() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	latestBlock := s.blockchain.GetLatestBlock()

	return map[string]interface{}{
		"currentHeight": len(s.blockchain.Chain) - 1,
		"latestBlock":   latestBlock.Hash(),
		"isSyncing":     s.node.IsSyncing(),
		"syncedPeers":   len(s.peerManager.GetTrustedPeers()),
	}
}

func (s *RPCService) GetNetworkDifficulty() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.blockchain.GetLatestBlock().Header.Difficulty
}

func (s *RPCService) GetPeerList() []string {
	peers := s.peerManager.GetTrustedPeers()
	peerList := make([]string, 0, len(peers))

	for _, p := range peers {
		peerList = append(peerList, p.String())
	}

	return peerList
}

// HTTP handler for RPC requests
func (s *RPCService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		ID     interface{}     `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var result interface{}
	var err error

	switch request.Method {
	case "getBalance":
		var address string
		if err := json.Unmarshal(request.Params, &address); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err = s.GetBalance(address)

	case "getTransactionHistory":
		var address string
		if err := json.Unmarshal(request.Params, &address); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err = s.GetTransactionHistory(address)
	}

	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
			"id":    request.ID,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": result,
		"id":     request.ID,
	})
}
