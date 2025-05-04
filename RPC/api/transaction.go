package api

import (
	"blockchain-core/blockchain"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// TransactionAPI handles transaction-related RPC methods
type TransactionAPI struct {
	node               *blockchain.Node
	blockchain         *blockchain.Blockchain
	pendingTxCallbacks map[string]string // txID -> callbackURL
}

// NewTransactionAPI creates a new transaction API instance
func NewTransactionAPI(node *blockchain.Node, blockchain *blockchain.Blockchain) *TransactionAPI {
	return &TransactionAPI{
		node:               node,
		blockchain:         blockchain,
		pendingTxCallbacks: make(map[string]string),
	}
}

// GetBalance retrieves the balance for an address
func (api *TransactionAPI) GetBalance(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if !blockchain.ValidateAddress(args.Address) {
		return nil, fmt.Errorf("invalid address format")
	}

	// Use UTXOPool directly to get balance
	balance := api.node.UTXOPool.GetBalance(args.Address)
	return balance, nil
}

// GetUTXOs retrieves unspent transactions for an address
func (api *TransactionAPI) GetUTXOs(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if !blockchain.ValidateAddress(args.Address) {
		return nil, fmt.Errorf("invalid address format")
	}

	utxos := api.node.UTXOPool.GetUTXOsForAddress(args.Address)
	return utxos, nil
}

// GetAccountState retrieves full account state
func (api *TransactionAPI) GetAccountState(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if !blockchain.ValidateAddress(args.Address) {
		return nil, fmt.Errorf("invalid address format")
	}

	// Get UTXOs directly from UTXOPool
	utxos := api.node.UTXOPool.GetUTXOsForAddress(args.Address)

	// Calculate balance from UTXOs
	balance := api.node.UTXOPool.GetBalance(args.Address)

	// In UTXO model, nonce can be derived from transaction count or UTXO count
	// Using the length of UTXOs as an approximation for nonce
	nonce := uint64(len(utxos))

	// Create account state
	state := map[string]interface{}{
		"address":     args.Address,
		"balance":     balance,
		"nonce":       nonce,
		"utxoCount":   len(utxos),
		"lastUpdated": time.Now().Unix(),
	}

	return state, nil
}

// CreateUnsignedTransaction creates an unsigned transaction structure
func (api *TransactionAPI) CreateUnsignedTransaction(params json.RawMessage) (interface{}, error) {
	var args struct {
		From      string  `json:"from"`
		To        string  `json:"to"`
		Amount    float64 `json:"amount"`
		GasPrice  uint64  `json:"gasPrice"`
		GasLimit  uint64  `json:"gasLimit"`
		PublicKey string  `json:"publicKey"` // Sender's public key in hex format
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Validate addresses
	if !blockchain.ValidateAddress(args.From) {
		return nil, fmt.Errorf("invalid sender address")
	}
	if !blockchain.ValidateAddress(args.To) {
		return nil, fmt.Errorf("invalid receiver address")
	}

	// Create transaction
	tx, err := blockchain.NewTransaction(args.From, args.To, args.Amount, args.GasPrice, args.GasLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction: %v", err)
	}

	// Set public key if provided
	if args.PublicKey != "" {
		// Decode the public key from hex
		publicKeyBytes, err := hex.DecodeString(args.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("invalid public key format: %v", err)
		}
		tx.SenderPubKey = publicKeyBytes
	}

	// Return the transaction object with a note about it being unsigned
	return map[string]interface{}{
		"transaction": tx,
		"status":      "unsigned",
		"note":        "This transaction must be signed with the sender's private key before broadcasting",
	}, nil
}

// SendTransaction signs and broadcasts a transaction
func (api *TransactionAPI) SendTransaction(params json.RawMessage) (interface{}, error) {
	var args struct {
		From           string  `json:"from"`
		To             string  `json:"to"`
		Amount         float64 `json:"amount"`
		GasPrice       uint64  `json:"gasPrice"`
		GasLimit       uint64  `json:"gasLimit"`
		Signature      string  `json:"signature"`      // Signed transaction data
		PublicKey      string  `json:"publicKey"`      // Sender's public key in hex format
		RawTransaction string  `json:"rawTransaction"` // Optional: Complete serialized transaction
		CallbackURL    string  `json:"callbackUrl"`    // Optional: URL to notify when transaction status changes

		Timestamp     int64  `json:"timestamp"`
		Nonce         int64  `json:"nonce"`
		TransactionID string `json:"transactionId"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	var tx *blockchain.Transaction

	// Handle pre-signed transaction
	if args.RawTransaction != "" {
		// Deserialize the complete transaction
		var err error
		txBytes, err := hex.DecodeString(args.RawTransaction)
		if err != nil {
			return nil, fmt.Errorf("invalid transaction encoding: %v", err)
		}

		tx = &blockchain.Transaction{}
		if err := json.Unmarshal(txBytes, tx); err != nil {
			return nil, fmt.Errorf("failed to deserialize transaction: %v", err)
		}

		// Verify the deserialized transaction
		if !tx.VerifySignature() {
			return nil, fmt.Errorf("transaction signature verification failed")
		}
	} else {
		// Validate addresses
		if !blockchain.ValidateAddress(args.From) {
			return nil, fmt.Errorf("invalid sender address")
		}
		if !blockchain.ValidateAddress(args.To) {
			return nil, fmt.Errorf("invalid receiver address")
		}
		if args.Signature == "" {
			return nil, fmt.Errorf("transaction signature is required")
		}
		if args.PublicKey == "" {
			return nil, fmt.Errorf("sender's public key is required")
		}

		// Decode the public key from hex
		publicKeyBytes, err := hex.DecodeString(args.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("invalid public key format: %v", err)
		}

		if args.TransactionID != "" {
			tx = &blockchain.Transaction{
				TransactionID: args.TransactionID,
				Sender:        args.From,
				Receiver:      args.To,
				Amount:        args.Amount,
				GasPrice:      args.GasPrice,
				GasLimit:      args.GasLimit,
				SenderPubKey:  publicKeyBytes,
				Signature:     args.Signature,
			}
			if args.Timestamp > 0 {
				tx.Timestamp = args.Timestamp
			} else {
				tx.Timestamp = time.Now().Unix()
			}
			if args.Nonce > 0 {
				tx.Nonce = uint64(args.Nonce)
			} else {
				tx.Nonce = 0
			}

			tx.GasFee = blockchain.ConvertGasToTokens(args.GasPrice * args.GasLimit)

			if args.TransactionID == "" {
				tx.TransactionID = tx.Hash()
			}

		} else {
			tx, err = blockchain.NewTransaction(args.From, args.To, args.Amount, args.GasPrice, args.GasLimit)
			if err != nil {
				return nil, fmt.Errorf("failed to create transaction: %v", err)
			}

			if args.Timestamp > 0 {
				tx.Timestamp = args.Timestamp
				tx.TransactionID = tx.Hash()
			} 

			if args.Nonce > 0 {
				tx.Nonce = uint64(args.Nonce)

				tx.TransactionID = tx.Hash()
			}
			
			

		}

		// Set the sender's public key
		tx.SenderPubKey = publicKeyBytes

		// Apply the provided signature - verify it's valid hex first
		_, hexErr := hex.DecodeString(args.Signature)
		if hexErr != nil {
			return nil, fmt.Errorf("invalid signature format: %v", hexErr)
		}
		tx.Signature = args.Signature

		// Verify the signature matches the transaction and sender
		if !tx.VerifySignature() {
			return nil, fmt.Errorf("transaction signature verification failed")
		}
	}

	// Broadcast the validated and signed transaction
	if err := api.node.BroadcastTransaction(tx, nil); err != nil {
		return nil, fmt.Errorf("failed to broadcast transaction: %v", err)
	}

	// If a callback URL is provided, store it and start monitoring the transaction
	if args.CallbackURL != "" {
		api.pendingTxCallbacks[tx.TransactionID] = args.CallbackURL

		// Start monitoring in a goroutine
		go api.monitorTransaction(tx.TransactionID)
	}

	// Return comprehensive transaction data with pending status
	return map[string]interface{}{
		"transactionId": tx.TransactionID,
		"status":        "pending",
		"from":          tx.Sender,
		"to":            tx.Receiver,
		"amount":        tx.Amount,
		"timestamp":     tx.Timestamp,
		"gasPrice":      tx.GasPrice,
		"gasLimit":      tx.GasLimit,
		"gasFee":        tx.GasFee,
		"inMempool":     true,
		"confirmations": 0,
	}, nil
}

// monitorTransaction monitors a transaction until it's confirmed or fails
func (api *TransactionAPI) monitorTransaction(txID string) {
	// Get the callback URL
	callbackURL, exists := api.pendingTxCallbacks[txID]
	if !exists {
		return
	}

	// Maximum number of attempts (approximately 30 minutes assuming 10-second intervals)
	maxAttempts := 180
	attempts := 0

	for attempts < maxAttempts {
		// Check if transaction is in a block
		confirmed := false
		var result map[string]interface{}

		// Search for transaction in blocks
		height := api.blockchain.GetHeight()
		for i := uint64(1); i <= height; i++ {
			block := api.blockchain.GetBlockByHeight(i)
			if block == nil || block.Body == nil || block.Body.Transactions == nil {
				continue
			}

			tx, found := block.Body.Transactions.Search(txID)
			if found && tx != nil {
				// Transaction found in a block
				confirmed = true
				result = map[string]interface{}{
					"transactionId":    txID,
					"status":           "confirmed",
					"blockHash":        block.Hash(),
					"blockHeight":      block.Header.BlockNumber,
					"confirmations":    height - block.Header.BlockNumber + 1,
					"timestamp":        block.Header.Timestamp,
					"confirmationTime": time.Unix(block.Header.Timestamp, 0).Format(time.RFC3339),
				}
				break
			}
		}

		// If confirmed, notify via callback and stop monitoring
		if confirmed {
			api.notifyCallback(callbackURL, result)
			delete(api.pendingTxCallbacks, txID)
			return
		}

		// Check if transaction is still in mempool
		inMempool := false
		if api.node.Mempool != nil {
			for _, tx := range api.node.Mempool.GetTransactions() {
				if tx.TransactionID == txID {
					inMempool = true
					break
				}
			}
		}

		// If not in mempool and not in a block after a certain number of attempts,
		// assume it failed (could have been rejected by validators)
		if !inMempool && attempts > 30 { // After 5 minutes (30 attempts * 10 seconds)
			result = map[string]interface{}{
				"transactionId": txID,
				"status":        "failed",
				"reason":        "Transaction dropped from mempool without being included in a block",
			}
			api.notifyCallback(callbackURL, result)
			delete(api.pendingTxCallbacks, txID)
			return
		}

		// Wait before next check
		time.Sleep(10 * time.Second)
		attempts++
	}

	// If we reach max attempts, assume it's still pending but stop monitoring
	result := map[string]interface{}{
		"transactionId": txID,
		"status":        "unknown",
		"reason":        "Monitoring timeout reached",
	}
	api.notifyCallback(callbackURL, result)
	delete(api.pendingTxCallbacks, txID)
}

// notifyCallback sends a POST request to the callback URL with transaction status
func (api *TransactionAPI) notifyCallback(callbackURL string, data map[string]interface{}) {
	// Convert data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		// Log error and return
		fmt.Printf("Error marshaling callback data: %v\n", err)
		return
	}

	// Send POST request to callback URL
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Post(callbackURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error sending callback notification: %v\n", err)
		return
	}
	defer resp.Body.Close()

	// Log response status
	fmt.Printf("Callback notification sent to %s, status: %d\n", callbackURL, resp.StatusCode)
}

// GetTransaction retrieves transaction details by ID
func (api *TransactionAPI) GetTransaction(params json.RawMessage) (interface{}, error) {
	var args struct {
		TxID string `json:"txid"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Search for transaction in blocks
	height := api.blockchain.GetHeight()
	for i := uint64(1); i <= height; i++ {
		block := api.blockchain.GetBlockByHeight(i)
		if block == nil || block.Body == nil || block.Body.Transactions == nil {
			continue
		}

		tx, found := block.Body.Transactions.Search(args.TxID)
		if found && tx != nil {
			// Add block information to transaction response
			result := map[string]interface{}{
				"transaction":      tx,
				"blockHash":        block.Hash(),
				"blockHeight":      block.Header.BlockNumber,
				"confirmations":    height - block.Header.BlockNumber + 1,
				"timestamp":        block.Header.Timestamp,
				"confirmationTime": time.Unix(block.Header.Timestamp, 0).Format(time.RFC3339),
			}
			return result, nil
		}
	}

	// Check if the transaction is in the mempool
	if api.node.Mempool != nil {
		for _, tx := range api.node.Mempool.GetTransactions() {
			if tx.TransactionID == args.TxID {
				return map[string]interface{}{
					"transaction":   tx,
					"confirmations": 0,
					"inMempool":     true,
					"timestamp":     tx.Timestamp,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("transaction not found")
}

// GetPendingTransactions retrieves transactions in mempool
func (api *TransactionAPI) GetPendingTransactions(params json.RawMessage) (interface{}, error) {
	if api.node.Mempool == nil {
		return []blockchain.Transaction{}, nil
	}

	var args struct {
		Limit  int    `json:"limit"`
		Filter string `json:"filter"` // Optional: filter by address
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// If no parameters provided, return all transactions
		return api.node.Mempool.GetTransactions(), nil
	}

	// Apply limit if specified
	transactions := api.node.Mempool.GetTransactions()

	// Apply filter if specified
	if args.Filter != "" {
		var filtered []blockchain.Transaction
		for _, tx := range transactions {
			if tx.Sender == args.Filter || tx.Receiver == args.Filter {
				filtered = append(filtered, tx)
			}
		}
		transactions = filtered
	}

	// Apply limit
	if args.Limit > 0 && args.Limit < len(transactions) {
		transactions = transactions[:args.Limit]
	}

	return transactions, nil
}

// EstimateFee estimates fee for a transaction
func (api *TransactionAPI) EstimateFee(params json.RawMessage) (interface{}, error) {
	var args struct {
		From     string  `json:"from"`
		To       string  `json:"to"`
		Amount   float64 `json:"amount"`
		GasPrice uint64  `json:"gasPrice,omitempty"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.From == "" {
		return nil, fmt.Errorf("sender address is required")
	}
	if args.To == "" {
		return nil, fmt.Errorf("receiver address is required")
	}
	if args.Amount <= 0 {
		return nil, fmt.Errorf("amount must be greater than 0")
	}

	// Set default gas price if not provided
	gasPrice := args.GasPrice
	if gasPrice == 0 {
		gasPrice = blockchain.DefaultGasPrice
	}

	// Default gas limit for a simple transfer
	gasLimit := uint64(blockchain.DefaultGasLimit)

	// Calculate fee using gas price and limit
	// Convert gas units to tokens
	fee := blockchain.ConvertGasToTokens(gasLimit * gasPrice)

	// If gas model is available in the node, use it for more accurate estimates
	if api.node.GetGasModel() != nil {
		// Since gas.GasModel.EstimateGas doesn't exist, use a simplified approach
		// instead of trying to call the undefined method

		// For a simple transfer, use the default gas limit
		gasLimit = uint64(blockchain.DefaultGasLimit)

		// For more complex transactions (with data), we'd estimate higher
		if len(args.To) > 40 || len(args.From) > 40 { // Longer addresses might indicate contracts
			gasLimit = gasLimit * 2 // Double the gas for more complex operations
		}

		fee = blockchain.ConvertGasToTokens(gasLimit * gasPrice)
	}

	result := map[string]interface{}{
		"gasLimit": gasLimit,
		"gasPrice": gasPrice,
		"fee":      fee,
		"currency": "tokens",
	}

	return result, nil
}

// GetTransactionHistory retrieves transaction history for an address
func (api *TransactionAPI) GetTransactionHistory(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
		Limit   int    `json:"limit"`
		Offset  int    `json:"offset"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if !blockchain.ValidateAddress(args.Address) {
		return nil, fmt.Errorf("invalid address format")
	}

	// Set defaults
	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	var history []map[string]interface{}
	txCount := 0

	// Scan blocks for transactions involving the address
	height := api.blockchain.GetHeight()
	for i := height; i >= 1 && txCount < args.Limit+args.Offset; i-- {
		block := api.blockchain.GetBlockByHeight(i)
		if block == nil || block.Body == nil || block.Body.Transactions == nil {
			continue
		}

		txs := block.Body.Transactions.GetAllTransactions()
		for _, tx := range txs {
			if tx.Sender == args.Address || tx.Receiver == args.Address {
				txCount++
				if txCount <= args.Offset {
					continue
				}

				// Add to history if past offset
				history = append(history, map[string]interface{}{
					"txid":          tx.TransactionID,
					"sender":        tx.Sender,
					"receiver":      tx.Receiver,
					"amount":        tx.Amount,
					"timestamp":     tx.Timestamp,
					"confirmations": height - i + 1,
					"blockHeight":   block.Header.BlockNumber,
					"blockHash":     block.Hash(),
					"type":          tx.IsCoinbase(),
					"fee":           tx.GasFee,
				})

				if len(history) >= args.Limit {
					break
				}
			}
		}
	}

	// Get any pending transactions in mempool
	if api.node.Mempool != nil && args.Offset == 0 {
		for _, tx := range api.node.Mempool.GetTransactions() {
			if tx.Sender == args.Address || tx.Receiver == args.Address {
				// Add to history with 0 confirmations
				history = append([]map[string]interface{}{
					{
						"txid":          tx.TransactionID,
						"sender":        tx.Sender,
						"receiver":      tx.Receiver,
						"amount":        tx.Amount,
						"timestamp":     tx.Timestamp,
						"confirmations": 0,
						"pending":       true,
						"fee":           tx.GasFee,
					},
				}, history...)

				if len(history) >= args.Limit {
					history = history[:args.Limit]
					break
				}
			}
		}
	}

	result := map[string]interface{}{
		"address":      args.Address,
		"transactions": history,
		"total":        txCount,
	}

	return result, nil
}

// GetTransactionProof generates Merkle proof for a transaction
func (api *TransactionAPI) GetTransactionProof(params json.RawMessage) (interface{}, error) {
	var args struct {
		TxID string `json:"txid"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Find the block containing the transaction
	height := api.blockchain.GetHeight()
	for i := uint64(1); i <= height; i++ {
		block := api.blockchain.GetBlockByHeight(i)
		if block == nil || block.Body == nil || block.Body.Transactions == nil {
			continue
		}

		_, found := block.Body.Transactions.GetTransaction(args.TxID)
		if found {
			// Use the block's method to generate a Merkle proof
			proof, err := block.GenerateMerkleProof(args.TxID)
			if err != nil {
				return nil, fmt.Errorf("failed to generate Merkle proof: %v", err)
			}

			return proof, nil
		}
	}

	return nil, fmt.Errorf("transaction not found")
}

// DecodeTransaction decodes raw transaction
func (api *TransactionAPI) DecodeTransaction(params json.RawMessage) (interface{}, error) {
	var args struct {
		RawTransaction string `json:"rawTransaction"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Use the blockchain's function to deserialize the transaction
	tx, err := blockchain.DeserializeTransactionFromHex(args.RawTransaction)
	if err != nil {
		return nil, fmt.Errorf("failed to decode transaction: %v", err)
	}

	// Create a detailed response with transaction details
	decodedTx := map[string]interface{}{
		"txid":         tx.TransactionID,
		"sender":       tx.Sender,
		"receiver":     tx.Receiver,
		"amount":       tx.Amount,
		"timestamp":    tx.Timestamp,
		"nonce":        tx.Nonce,
		"gasPrice":     tx.GasPrice,
		"gasLimit":     tx.GasLimit,
		"gasUsed":      tx.GasUsed,
		"gasFee":       tx.GasFee,
		"txType":       tx.TxType,
		"signature":    tx.Signature,
		"rawHex":       args.RawTransaction,
		"inputs":       tx.Inputs,
		"outputs":      tx.Outputs,
		"priority":     tx.Priority,
		"maxFeePerGas": tx.MaxFeePerGas,
	}

	return decodedTx, nil
}

// DebugTransaction gets detailed transaction execution information
func (api *TransactionAPI) DebugTransaction(params json.RawMessage) (interface{}, error) {
	var args struct {
		TxID string `json:"txid"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// In a real implementation, you would retrieve detailed execution info
	// For this example, we'll return a placeholder with simulated debug info

	debugInfo := map[string]interface{}{
		"txid":        args.TxID,
		"executed":    true,
		"gasUsed":     21000,
		"blockHeight": 100,
		"status":      "success",
		"traces": []map[string]string{
			{"op": "PUSH", "value": "100"},
			{"op": "LOAD", "value": "address_1"},
			{"op": "STORE", "value": "address_2"},
		},
		"logs": []string{
			"Transaction execution started",
			"UTXO inputs validated",
			"Transaction execution completed",
		},
	}

	return debugInfo, nil
}

// CallReadOnly executes a read-only transaction simulation
func (api *TransactionAPI) CallReadOnly(params json.RawMessage) (interface{}, error) {
	var args struct {
		From     string  `json:"from"`
		To       string  `json:"to"`
		Amount   float64 `json:"amount"`
		GasPrice uint64  `json:"gasPrice"`
		GasLimit uint64  `json:"gasLimit"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Create a simulated transaction
	tx, err := blockchain.NewTransaction(args.From, args.To, args.Amount, args.GasPrice, args.GasLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction: %v", err)
	}

	// Validate the transaction
	err = api.blockchain.ValidateTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("transaction validation failed: %v", err)
	}

	// Simulate execution (in a real implementation, this would execute without state changes)
	result := map[string]interface{}{
		"valid":    true,
		"gasUsed":  21000, // Simulated value
		"errors":   []string{},
		"warnings": []string{},
		"balanceChanges": map[string]float64{
			args.From: -args.Amount - blockchain.ConvertGasToTokens(21000*args.GasPrice),
			args.To:   args.Amount,
		},
	}

	return result, nil
}

// GetRecentTransactions retrieves most recent transactions
func (api *TransactionAPI) GetRecentTransactions(params json.RawMessage) (interface{}, error) {
	var args struct {
		Limit int `json:"limit"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// If no parameters provided, use default limit
		args.Limit = 10
	}

	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	var recentTxs []map[string]interface{}
	count := 0
	height := api.blockchain.GetHeight()

	// Scan recent blocks to find transactions
	for i := height; i >= 1 && count < args.Limit; i-- {
		block := api.blockchain.GetBlockByHeight(i)
		if block == nil || block.Body == nil || block.Body.Transactions == nil {
			continue
		}

		txs := block.Body.Transactions.GetAllTransactions()
		for _, tx := range txs {
			recentTxs = append(recentTxs, map[string]interface{}{
				"txid":          tx.TransactionID,
				"sender":        tx.Sender,
				"receiver":      tx.Receiver,
				"amount":        tx.Amount,
				"timestamp":     tx.Timestamp,
				"confirmations": height - i + 1,
				"blockHeight":   block.Header.BlockNumber,
				"blockHash":     block.Hash(),
			})

			count++
			if count >= args.Limit {
				break
			}
		}
	}

	return recentTxs, nil
}

// GetMempoolInfo retrieves detailed mempool statistics
func (api *TransactionAPI) GetMempoolInfo(params json.RawMessage) (interface{}, error) {
	// Get all transactions in the mempool
	mempool := api.node.Mempool.GetTransactions()

	// Calculate statistics
	var totalValue float64
	var totalFees float64
	totalSize := uint64(0)
	minFee := float64(100.0)
	maxFee := float64(0.0)

	if len(mempool) > 0 {
		minFee = mempool[0].GasFee
	}

	for _, tx := range mempool {
		totalValue += tx.Amount
		totalFees += tx.GasFee

		// Use an estimated size since GetSize() is not defined
		// Typical transaction might be around 250 bytes
		estimatedSize := uint64(250)
		totalSize += estimatedSize

		if tx.GasFee < minFee {
			minFee = tx.GasFee
		}
		if tx.GasFee > maxFee {
			maxFee = tx.GasFee
		}
	}

	// In case there are no transactions
	if len(mempool) == 0 {
		minFee = 0
	}

	// Build the response
	info := map[string]interface{}{
		"size":             len(mempool),
		"bytes":            totalSize,
		"totalValue":       totalValue,
		"totalFees":        totalFees,
		"minFee":           minFee,
		"maxFee":           maxFee,
		"oldestTimestamp":  0,
		"newestTimestamp":  0,
		"averageFeeRate":   0.0,
		"medianFeeRate":    0.0,
		"memoryUsageBytes": totalSize + uint64(1024), // Add some overhead
	}

	// Calculate fee rates and timestamps if we have transactions
	if len(mempool) > 0 {
		var feeRates []float64
		var timestamps []int64

		for _, tx := range mempool {
			// Use an estimated size since GetSize() is not defined
			estimatedSize := uint64(250)
			if estimatedSize > 0 {
				feeRate := tx.GasFee / float64(estimatedSize)
				feeRates = append(feeRates, feeRate)
			}
			timestamps = append(timestamps, tx.Timestamp)
		}

		// Sort timestamps to find oldest and newest
		sort.Slice(timestamps, func(i, j int) bool {
			return timestamps[i] < timestamps[j]
		})

		if len(timestamps) > 0 {
			info["oldestTimestamp"] = timestamps[0]
			info["newestTimestamp"] = timestamps[len(timestamps)-1]
		}

		// Calculate average fee rate
		if len(feeRates) > 0 {
			sum := 0.0
			for _, rate := range feeRates {
				sum += rate
			}
			info["averageFeeRate"] = sum / float64(len(feeRates))

			// Calculate median fee rate
			sort.Float64s(feeRates)
			middle := len(feeRates) / 2
			if len(feeRates)%2 == 0 {
				info["medianFeeRate"] = (feeRates[middle-1] + feeRates[middle]) / 2
			} else {
				info["medianFeeRate"] = feeRates[middle]
			}
		}
	}

	return info, nil
}

// GetAverageFees retrieves average transaction fees over time
func (api *TransactionAPI) GetAverageFees(params json.RawMessage) (interface{}, error) {
	var args struct {
		Blocks   int  `json:"blocks,omitempty"`
		Detailed bool `json:"detailed,omitempty"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// Default to 10 blocks if not specified
		args.Blocks = 10
	}

	if args.Blocks <= 0 {
		args.Blocks = 10
	}
	if args.Blocks > 1000 {
		args.Blocks = 1000
	}

	// Get the current blockchain height
	currentHeight := api.blockchain.GetHeight()

	// Calculate starting block height
	startHeight := uint64(0)
	if currentHeight > uint64(args.Blocks) {
		startHeight = currentHeight - uint64(args.Blocks)
	}

	// Track fee data
	var totalFees float64
	var totalTxs int
	var feeRates []float64
	var blockFees []map[string]interface{}

	// Process blocks
	for height := startHeight; height <= currentHeight; height++ {
		block := api.blockchain.GetBlockByHeight(height)
		if block == nil {
			continue
		}

		// Get transactions from the block
		var txs []blockchain.Transaction
		if block.Body != nil && block.Body.Transactions != nil {
			txs = block.Body.Transactions.GetAllTransactions()
		}

		blockTotalFees := 0.0
		blockTxCount := len(txs)
		blockFeeRates := make([]float64, 0, blockTxCount)
		highestFeeRate := 0.0
		lowestFeeRate := float64(1000.0)

		for _, tx := range txs {
			totalFees += tx.GasFee

			// Use an estimated size for rate calculations
			estimatedSize := uint64(250)
			if estimatedSize > 0 {
				feeRate := tx.GasFee / float64(estimatedSize)
				feeRates = append(feeRates, feeRate)
				blockFeeRates = append(blockFeeRates, feeRate)

				if feeRate > highestFeeRate {
					highestFeeRate = feeRate
				}
				if feeRate < lowestFeeRate {
					lowestFeeRate = feeRate
				}
			}
		}

		totalTxs += blockTxCount

		// Add detailed block info if requested
		if args.Detailed && blockTxCount > 0 {
			// Sort fee rates for median calculation
			sort.Float64s(blockFeeRates)
			medianFeeRate := 0.0
			if len(blockFeeRates) > 0 {
				middle := len(blockFeeRates) / 2
				if len(blockFeeRates)%2 == 0 && len(blockFeeRates) > 1 {
					medianFeeRate = (blockFeeRates[middle-1] + blockFeeRates[middle]) / 2
				} else if len(blockFeeRates) > 0 {
					medianFeeRate = blockFeeRates[middle]
				}
			}

			// If we have no transactions, adjust the lowest fee rate
			if len(blockFeeRates) == 0 {
				lowestFeeRate = 0
			}

			blockFee := map[string]interface{}{
				"height":         height,
				"timestamp":      block.Header.Timestamp,
				"txCount":        blockTxCount,
				"totalFees":      blockTotalFees,
				"averageFee":     blockTotalFees / float64(blockTxCount),
				"medianFeeRate":  medianFeeRate,
				"highestFeeRate": highestFeeRate,
				"lowestFeeRate":  lowestFeeRate,
			}
			blockFees = append(blockFees, blockFee)
		}
	}

	// Calculate overall average fee
	averageFee := 0.0
	if totalTxs > 0 {
		averageFee = totalFees / float64(totalTxs)
	}

	// Calculate overall average fee rate
	averageFeeRate := 0.0
	if len(feeRates) > 0 {
		sum := 0.0
		for _, rate := range feeRates {
			sum += rate
		}
		averageFeeRate = sum / float64(len(feeRates))
	}

	// Calculate overall median fee rate
	medianFeeRate := 0.0
	if len(feeRates) > 0 {
		sort.Float64s(feeRates)
		middle := len(feeRates) / 2
		if len(feeRates)%2 == 0 && len(feeRates) > 1 {
			medianFeeRate = (feeRates[middle-1] + feeRates[middle]) / 2
		} else {
			medianFeeRate = feeRates[middle]
		}
	}

	result := map[string]interface{}{
		"blocks":         args.Blocks,
		"averageFee":     averageFee,
		"averageFeeRate": averageFeeRate,
		"medianFeeRate":  medianFeeRate,
		"totalTxs":       totalTxs,
	}

	if args.Detailed {
		result["blockFees"] = blockFees
	}

	return result, nil
}

// TraceBlock gets detailed block processing information
func (api *TransactionAPI) TraceBlock(params json.RawMessage) (interface{}, error) {
	var args struct {
		BlockHash string `json:"blockHash"`
		Height    string `json:"height"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	var block *blockchain.Block

	if args.BlockHash != "" {
		var err error
		block, err = api.blockchain.GetBlock(args.BlockHash)
		if err != nil {
			return nil, fmt.Errorf("block not found: %v", err)
		}
	} else {
		var height uint64
		var err error

		if args.Height != "" {
			// Parse string to uint64 directly instead of using json.Number
			height, err = strconv.ParseUint(args.Height, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid height: %v", err)
			}
		} else {
			// Use latest block if neither hash nor height specified
			height = api.blockchain.GetHeight()
		}

		block = api.blockchain.GetBlockByHeight(height)
		if block == nil {
			return nil, fmt.Errorf("block not found at height %d", height)
		}
	}

	// Generate the block trace
	trace := block.GenerateBlockTrace()

	// Format the response
	result := map[string]interface{}{
		"blockHash":        trace.BlockHash,
		"blockHeight":      trace.BlockNumber,
		"timestamp":        trace.Timestamp,
		"previousHash":     trace.PreviousBlockHash,
		"merkleRoot":       trace.MerkleRoot,
		"stateRoot":        trace.StateRoot,
		"miner":            trace.MinerAddress,
		"validator":        trace.ValidatorAddress,
		"gasUsed":          trace.TotalGasUsed,
		"transactionCount": len(trace.TransactionTraces),
		"transactions":     trace.TransactionTraces,
		"executionTimeMs":  trace.ExecutionTimeMs,
	}

	return result, nil
}

// SearchByAddress searches transactions by address
func (api *TransactionAPI) SearchByAddress(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address  string `json:"address"`
		Limit    int    `json:"limit,omitempty"`
		Offset   int    `json:"offset,omitempty"`
		SortBy   string `json:"sortBy,omitempty"` // "time", "amount", etc.
		SortDesc bool   `json:"sortDesc,omitempty"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Validate address
	if args.Address == "" {
		return nil, fmt.Errorf("address is required")
	}

	if !blockchain.ValidateAddress(args.Address) {
		return nil, fmt.Errorf("invalid address format")
	}

	// Set defaults for pagination
	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 100 {
		args.Limit = 100 // Cap at 100 transactions
	}
	if args.SortBy == "" {
		args.SortBy = "time" // Default sort by time
	}

	// Get transactions related to this address
	// First, get the blocks containing transactions with this address
	// This is a simplified approach - in a production environment, you'd have an indexed database
	latestBlock := api.blockchain.GetLatestBlock()
	heightStart := uint64(0)
	if latestBlock.Header.BlockNumber > uint64(5000) { // Arbitrary limit to not scan too many blocks
		heightStart = latestBlock.Header.BlockNumber - 5000
	}

	var transactions []map[string]interface{}
	for height := latestBlock.Header.BlockNumber; height >= heightStart; height-- {
		block := api.blockchain.GetBlockByHeight(height)
		if block == nil {
			continue
		}

		// Get transactions from the block
		var txs []blockchain.Transaction
		if block.Body != nil && block.Body.Transactions != nil {
			txs = block.Body.Transactions.GetAllTransactions()
		}

		for _, tx := range txs {
			// Check if this transaction involves the requested address
			if tx.Sender == args.Address || tx.Receiver == args.Address {
				txInfo := map[string]interface{}{
					"txid":        tx.TransactionID,
					"blockHeight": block.Header.BlockNumber,
					"blockHash":   block.Hash(),
					"timestamp":   block.Header.Timestamp,
					"from":        tx.Sender,
					"to":          tx.Receiver,
					"amount":      tx.Amount,
					"fee":         tx.GasFee,
					"confirmed":   true,
				}

				// Set transaction type
				if args.Address == tx.Sender {
					txInfo["type"] = "send"
				} else {
					txInfo["type"] = "receive"
				}

				transactions = append(transactions, txInfo)
			}
		}

		// If we've found enough transactions, stop searching
		if len(transactions) >= args.Limit+args.Offset {
			break
		}
	}

	// Also check mempool for pending transactions
	mempool := api.node.Mempool.GetTransactions()
	for _, tx := range mempool {
		if tx.Sender == args.Address || tx.Receiver == args.Address {
			txInfo := map[string]interface{}{
				"txid":      tx.TransactionID,
				"timestamp": time.Now().Unix(), // Current time as they're pending
				"from":      tx.Sender,
				"to":        tx.Receiver,
				"amount":    tx.Amount,
				"fee":       tx.GasFee,
				"confirmed": false,
				"pending":   true,
			}

			// Set transaction type
			if args.Address == tx.Sender {
				txInfo["type"] = "send"
			} else {
				txInfo["type"] = "receive"
			}

			transactions = append(transactions, txInfo)
		}
	}

	// Sort transactions based on sort criteria
	// This is a simplified sorting logic
	if args.SortBy == "time" {
		sort.Slice(transactions, func(i, j int) bool {
			timeI, _ := transactions[i]["timestamp"].(int64)
			timeJ, _ := transactions[j]["timestamp"].(int64)
			if args.SortDesc {
				return timeI > timeJ
			}
			return timeI < timeJ
		})
	} else if args.SortBy == "amount" {
		sort.Slice(transactions, func(i, j int) bool {
			amtI, _ := transactions[i]["amount"].(float64)
			amtJ, _ := transactions[j]["amount"].(float64)
			if args.SortDesc {
				return amtI > amtJ
			}
			return amtI < amtJ
		})
	}

	// Apply pagination
	totalCount := len(transactions)
	if args.Offset < totalCount {
		end := args.Offset + args.Limit
		if end > totalCount {
			end = totalCount
		}
		transactions = transactions[args.Offset:end]
	} else {
		transactions = []map[string]interface{}{}
	}

	result := map[string]interface{}{
		"address":      args.Address,
		"transactions": transactions,
		"total":        totalCount,
	}

	return result, nil
}
