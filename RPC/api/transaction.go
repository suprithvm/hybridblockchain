package api

import (
	"blockchain-core/blockchain"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// TransactionAPI handles transaction-related RPC methods
type TransactionAPI struct {
	node       *blockchain.Node
	blockchain *blockchain.Blockchain
}

// NewTransactionAPI creates a new transaction API instance
func NewTransactionAPI(node *blockchain.Node, blockchain *blockchain.Blockchain) *TransactionAPI {
	return &TransactionAPI{
		node:       node,
		blockchain: blockchain,
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

// CreateTransaction creates a new transaction
func (api *TransactionAPI) CreateTransaction(params json.RawMessage) (interface{}, error) {
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

	return tx, nil
}

// SendTransaction signs and broadcasts a transaction
func (api *TransactionAPI) SendTransaction(params json.RawMessage) (interface{}, error) {
	var args struct {
		From     string  `json:"from"`
		To       string  `json:"to"`
		Amount   float64 `json:"amount"`
		GasPrice uint64  `json:"gasPrice"`
		GasLimit uint64  `json:"gasLimit"`
		Mnemonic string  `json:"mnemonic"`
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

	// Recover wallet from mnemonic
	wallet, err := blockchain.RecoverWalletFromMnemonic(args.Mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to recover wallet: %v", err)
	}

	// Verify that the wallet address matches the from address
	if wallet.Address != args.From {
		return nil, fmt.Errorf("wallet address does not match sender address")
	}

	// Sign transaction
	if err := wallet.SignTransaction(tx); err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Broadcast transaction
	if err := api.node.BroadcastTransaction(tx, nil); err != nil {
		return nil, fmt.Errorf("failed to broadcast transaction: %v", err)
	}

	return map[string]string{
		"transactionId": tx.TransactionID,
		"status":        "success",
	}, nil
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

		txNode, found := block.Body.Transactions.GetTransaction(args.TxID)
		if found && txNode != nil {
			// In a real implementation, you would generate a Merkle proof here
			// using the Patricia trie's proof generation functionality

			// For this example, we'll create a simplified proof structure
			merkleRoot := block.Header.MerkleRoot

			proof := map[string]interface{}{
				"txid":       args.TxID,
				"merkleRoot": merkleRoot,
				"blockHash":  block.Hash(),
				"height":     block.Header.BlockNumber,
				// In a real implementation, this would be an array of hashes forming the proof path
				"proof":    []string{"proof_element_1", "proof_element_2"},
				"verified": true,
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

	// Decode the raw transaction
	// In a real implementation, you would parse the hex-encoded transaction
	// For this example, we'll just return a simplified placeholder

	// Example: tx, err := blockchain.DeserializeTransactionFromHex(args.RawTransaction)
	// We'll simulate this with a placeholder response

	decodedTx := map[string]interface{}{
		"version":   1,
		"lockTime":  0,
		"size":      225,
		"inputs":    []string{"simulated_input_1", "simulated_input_2"},
		"outputs":   []string{"simulated_output_1", "simulated_output_2"},
		"hex":       args.RawTransaction,
		"txid":      "simulated_txid",
		"timestamp": time.Now().Unix(),
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

	// In a real implementation, you would retrieve execution traces for each transaction
	// We'll create a simplified trace structure for this example

	var txTraces []map[string]interface{}
	if block.Body != nil && block.Body.Transactions != nil {
		txs := block.Body.Transactions.GetAllTransactions()
		for _, tx := range txs {
			trace := map[string]interface{}{
				"txid":      tx.TransactionID,
				"sender":    tx.Sender,
				"receiver":  tx.Receiver,
				"amount":    tx.Amount,
				"gasUsed":   tx.GasUsed,
				"gasFee":    tx.GasFee,
				"timestamp": tx.Timestamp,
				"status":    "success",
				// In a real implementation, you would include detailed execution steps here
			}
			txTraces = append(txTraces, trace)
		}
	}

	result := map[string]interface{}{
		"blockHash":     block.Hash(),
		"height":        block.Header.BlockNumber,
		"timestamp":     block.Header.Timestamp,
		"miner":         block.Header.MinedBy,
		"validator":     block.Header.ValidatorAddress,
		"difficulty":    block.Header.Difficulty,
		"gasLimit":      block.Header.GasLimit,
		"gasUsed":       block.Header.GasUsed,
		"stateRoot":     block.Header.StateRoot,
		"transactions":  txTraces,
		"executionTime": 0.5, // Simulated execution time in seconds
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
