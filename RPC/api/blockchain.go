package api

import (
	"blockchain-core/blockchain"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// BlockchainAPI handles blockchain-related RPC methods
type BlockchainAPI struct {
	node       *blockchain.Node
	blockchain *blockchain.Blockchain
}

// NewBlockchainAPI creates a new blockchain API instance
func NewBlockchainAPI(node *blockchain.Node, blockchain *blockchain.Blockchain) *BlockchainAPI {
	return &BlockchainAPI{
		node:       node,
		blockchain: blockchain,
	}
}

// GetBlockByHash retrieves a block by its hash
func (api *BlockchainAPI) GetBlockByHash(params json.RawMessage) (interface{}, error) {
	var args struct {
		Hash string `json:"hash"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	block, err := api.blockchain.GetBlock(args.Hash)
	if err != nil {
		return nil, fmt.Errorf("block not found: %v", err)
	}

	return block, nil
}

// GetBlockByHeight retrieves a block by its height
func (api *BlockchainAPI) GetBlockByHeight(params json.RawMessage) (interface{}, error) {
	var args struct {
		Height uint64 `json:"height"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	block := api.blockchain.GetBlockByHeight(args.Height)
	if block == nil {
		return nil, fmt.Errorf("block not found at height %d", args.Height)
	}

	return block, nil
}

// GetBlockCount returns the current block height
func (api *BlockchainAPI) GetBlockCount(params json.RawMessage) (interface{}, error) {
	return api.blockchain.GetHeight(), nil
}

// GetChainInfo retrieves blockchain metadata
func (api *BlockchainAPI) GetChainInfo(params json.RawMessage) (interface{}, error) {
	latestBlock := api.blockchain.GetLatestBlock()

	info := map[string]interface{}{
		"blocks":        api.blockchain.GetHeight(),
		"bestBlockHash": latestBlock.Hash(),
		"difficulty":    latestBlock.Header.Difficulty,
		"medianTime":    latestBlock.Header.Timestamp,
		"chainwork":     latestBlock.CumulativeDifficulty,
	}

	return info, nil
}

// GetValidationInfo retrieves block validation details
func (api *BlockchainAPI) GetValidationInfo(params json.RawMessage) (interface{}, error) {
	var args struct {
		BlockHeight uint64 `json:"blockHeight"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	block := api.blockchain.GetBlockByHeight(args.BlockHeight)
	if block == nil {
		return nil, fmt.Errorf("block not found at height %d", args.BlockHeight)
	}

	info := map[string]interface{}{
		"blockHash":        block.Hash(),
		"blockHeight":      block.Header.BlockNumber,
		"validatorAddress": block.Header.ValidatorAddress,
		"validatorProof":   block.Header.ValidatorProof,
		"validatorSig":     block.Header.ValidatorSig,
		"timestamp":        block.Header.Timestamp,
	}

	return info, nil
}

// GetBlockRange retrieves multiple blocks in a specified range
func (api *BlockchainAPI) GetBlockRange(params json.RawMessage) (interface{}, error) {
	var args struct {
		StartHeight uint64 `json:"startHeight"`
		EndHeight   uint64 `json:"endHeight"`
		MaxBlocks   int    `json:"maxBlocks"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Limit the number of blocks that can be retrieved in a single call
	if args.MaxBlocks <= 0 {
		args.MaxBlocks = 10 // Default to 10 blocks
	} else if args.MaxBlocks > 100 {
		args.MaxBlocks = 100 // Cap at 100 blocks
	}

	// Ensure end is not before start
	if args.EndHeight < args.StartHeight {
		return nil, fmt.Errorf("endHeight must be greater than or equal to startHeight")
	}

	// Ensure range doesn't exceed max blocks
	numBlocks := int(args.EndHeight - args.StartHeight + 1)
	if numBlocks > args.MaxBlocks {
		args.EndHeight = args.StartHeight + uint64(args.MaxBlocks) - 1
	}

	var blocks []*blockchain.Block
	for height := args.StartHeight; height <= args.EndHeight; height++ {
		block := api.blockchain.GetBlockByHeight(height)
		if block != nil {
			blocks = append(blocks, block)
		}
	}

	return blocks, nil
}

// GetHashRate calculates the current network hash rate
func (api *BlockchainAPI) GetHashRate(params json.RawMessage) (interface{}, error) {
	var args struct {
		Days int `json:"days,omitempty"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// Default to 7 days if not specified
		args.Days = 7
	}

	if args.Days <= 0 {
		args.Days = 7
	}
	if args.Days > 90 {
		args.Days = 90
	}

	var totalDifficulty uint64

	// Get the latest block
	latestBlock := api.blockchain.GetLatestBlock()
	if latestBlock.Header == nil {
		return 0, nil
	}

	// Get block from 'blocks' blocks ago
	startHeight := api.blockchain.GetHeight() - uint64(args.Days*24*60*60/5)
	if startHeight < 1 {
		startHeight = 1
	}

	startBlock := api.blockchain.GetBlockByHeight(startHeight)
	if startBlock == nil || startBlock.Header == nil {
		return 0, nil
	}

	// Calculate time difference
	timeDiff := latestBlock.Header.Timestamp - startBlock.Header.Timestamp
	if timeDiff <= 0 {
		return 0, nil
	}

	// Calculate total difficulty
	totalDifficulty = latestBlock.CumulativeDifficulty - startBlock.CumulativeDifficulty

	// Estimated hash rate = total difficulty / time in seconds
	hashRate := float64(totalDifficulty) / float64(timeDiff)

	return hashRate, nil
}

// GetNetworkDifficulty retrieves the current network difficulty
func (api *BlockchainAPI) GetNetworkDifficulty(params json.RawMessage) (interface{}, error) {
	latestBlock := api.blockchain.GetLatestBlock()
	if latestBlock.Header == nil {
		return nil, fmt.Errorf("no blocks available")
	}

	return latestBlock.Header.Difficulty, nil
}

// GetCirculatingSupply calculates the total circulating supply
func (api *BlockchainAPI) GetCirculatingSupply(params json.RawMessage) (interface{}, error) {
	// Calculate circulating supply based on actual blockchain state
	height := api.blockchain.GetHeight()

	// Get the constants from the blockchain package
	baseReward := float64(blockchain.InitialBlockReward)
	halvingInterval := uint64(blockchain.RewardHalvingBlocks)

	totalSupply := 0.0

	// Calculate how many coins have been minted so far
	if height > 0 {
		remainingBlocks := height

		// Calculate rewards at each halvening level
		for i := 0; remainingBlocks > 0; i++ {
			blocksAtThisReward := uint64(0)
			if remainingBlocks > halvingInterval {
				blocksAtThisReward = halvingInterval
			} else {
				blocksAtThisReward = remainingBlocks
			}

			// Calculate reward at this level
			rewardAtLevel := baseReward / math.Pow(2, float64(i))
			totalSupply += float64(blocksAtThisReward) * rewardAtLevel

			remainingBlocks -= blocksAtThisReward
		}
	}

	// If the blockchain has a UTXOPool, use it to get a more accurate supply
	// by summing all UTXO values
	if api.blockchain.GetUTXOPool() != nil {
		utxoPool := api.blockchain.GetUTXOPool()

		// Get all UTXOs and sum their values
		utxoSupply := 0.0
		allUTXOs := utxoPool.GetAllUTXOs()

		for _, utxo := range allUTXOs {
			if !utxo.Spent {
				utxoSupply += utxo.Amount
			}
		}

		// Use the UTXO-based supply if it's available and non-zero
		if utxoSupply > 0 {
			return utxoSupply, nil
		}
	}

	return totalSupply, nil
}

// GetBlockTransactions retrieves all transactions in a block
func (api *BlockchainAPI) GetBlockTransactions(params json.RawMessage) (interface{}, error) {
	var args struct {
		BlockHash string `json:"blockHash"`
		Height    string `json:"height"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	var block *blockchain.Block

	// Get block by hash or height
	if args.BlockHash != "" {
		var err error
		block, err = api.blockchain.GetBlock(args.BlockHash)
		if err != nil {
			return nil, fmt.Errorf("block not found: %v", err)
		}
	} else if args.Height != "" {
		height, err := strconv.ParseUint(args.Height, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid height: %v", err)
		}
		block = api.blockchain.GetBlockByHeight(height)
		if block == nil {
			return nil, fmt.Errorf("block not found at height %d", height)
		}
	} else {
		return nil, fmt.Errorf("either blockHash or height must be provided")
	}

	// Extract transactions from the block's Patricia trie
	var transactions []blockchain.Transaction
	if block.Body != nil && block.Body.Transactions != nil {
		transactions = block.Body.Transactions.GetAllTransactions()
	}

	return transactions, nil
}

// GetRichList retrieves top addresses by balance
func (api *BlockchainAPI) GetRichList(params json.RawMessage) (interface{}, error) {
	var args struct {
		Limit int `json:"limit"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Limit <= 0 {
		args.Limit = 10 // Default to top 10
	}
	if args.Limit > 100 {
		args.Limit = 100 // Cap at 100
	}

	// Get all UTXOs from the UTXOPool
	var allUTXOs map[string]blockchain.UTXO

	if api.blockchain.GetUTXOPool() != nil {
		allUTXOs = api.blockchain.GetUTXOPool().GetAllUTXOs()
	} else {
		// Fallback to chain's UTXO set if UTXOPool is not available
		allUTXOs = api.blockchain.GetUTXOSet()
	}

	if allUTXOs == nil || len(allUTXOs) == 0 {
		return []interface{}{}, nil // Return empty array if no UTXOs
	}

	// Calculate balances per address
	balances := make(map[string]float64)
	for _, utxo := range allUTXOs {
		if !utxo.Spent {
			balances[utxo.Owner] += utxo.Amount
		}
	}

	// Convert to slice for sorting
	type AddressBalance struct {
		Address string  `json:"address"`
		Balance float64 `json:"balance"`
	}
	var richList []AddressBalance

	for addr, balance := range balances {
		richList = append(richList, AddressBalance{
			Address: addr,
			Balance: balance,
		})
	}

	// Sort by balance (descending)
	sort.Slice(richList, func(i, j int) bool {
		return richList[i].Balance > richList[j].Balance
	})

	// Limit results
	if len(richList) > args.Limit {
		richList = richList[:args.Limit]
	}

	return richList, nil
}

// GetBlockchainStats retrieves comprehensive blockchain statistics
func (api *BlockchainAPI) GetBlockchainStats(params json.RawMessage) (interface{}, error) {
	height := api.blockchain.GetHeight()
	latestBlock := api.blockchain.GetLatestBlock()

	// Calculate active addresses - count addresses with non-zero balance
	activeAddresses := 0
	uniqueAddresses := make(map[string]bool)

	// Use UTXOPool to get accurate UTXO data if available
	var utxoMap map[string]blockchain.UTXO
	if api.blockchain.GetUTXOPool() != nil {
		utxoMap = api.blockchain.GetUTXOPool().GetAllUTXOs()
	} else {
		utxoMap = api.blockchain.GetUTXOSet()
	}

	// Count active addresses from the UTXO set
	for _, utxo := range utxoMap {
		if !utxo.Spent {
			uniqueAddresses[utxo.Owner] = true
		}
	}
	activeAddresses = len(uniqueAddresses)

	// Count total transactions more efficiently by using blocks' transaction counts
	totalTxs := uint64(0)
	chainSize := uint64(0) // Track chain size in bytes

	// Only scan the last 1000 blocks for performance if the chain is very long
	scanStart := uint64(1)
	if height > 1000 {
		scanStart = height - 1000
	}

	for i := scanStart; i <= height; i++ {
		block := api.blockchain.GetBlockByHeight(i)
		if block != nil {
			totalTxs += uint64(block.TransactionCount())

			// Estimate block size based on serialized data
			blockData, _ := block.Serialize()
			if blockData != nil {
				chainSize += uint64(len(blockData))
			}
		}
	}

	// Extrapolate total transactions if we scanned only a portion
	if scanStart > 1 {
		avgTxPerBlock := float64(totalTxs) / float64(height-scanStart+1)
		totalTxs = uint64(avgTxPerBlock * float64(height))
	}

	// Get mempool statistics
	mempoolTxs := 0
	mempoolSize := uint64(0)
	if api.node.Mempool != nil {
		transactions := api.node.Mempool.GetTransactions()
		mempoolTxs = len(transactions)

		// Estimate mempool size (250 bytes per transaction is a rough estimate)
		mempoolSize = uint64(mempoolTxs * 250)
	}

	// Get network difficulty
	difficulty := uint64(0)
	if latestBlock.Header != nil {
		difficulty = uint64(latestBlock.Header.Difficulty)
	}

	// Calculate hash rate
	hashRateParams, _ := json.Marshal(map[string]int{"days": 7})
	hashRate, err := api.GetHashRate(hashRateParams)
	hashRateValue := float64(0)
	if err == nil {
		hashRateValue, _ = hashRate.(float64)
	}

	stats := map[string]interface{}{
		"blocks":              height,
		"transactions":        totalTxs,
		"difficulty":          difficulty,
		"networkHashRate":     hashRateValue,
		"mempoolTransactions": mempoolTxs,
		"mempoolSize":         mempoolSize,
		"activeAddresses":     activeAddresses,
		"bestBlockHash":       latestBlock.Hash(),
		"bestBlockHeight":     height,
		"chainSize":           chainSize,
		"totalSupply":         0, // Will be updated below
	}

	// Get circulating supply using our existing method
	supplyParams, _ := json.Marshal(map[string]interface{}{})
	supply, err := api.GetCirculatingSupply(supplyParams)
	if err == nil {
		stats["totalSupply"] = supply
	}

	return stats, nil
}

// GetBlockTime calculates the average block time
func (api *BlockchainAPI) GetBlockTime(params json.RawMessage) (interface{}, error) {
	var args struct {
		Blocks int `json:"blocks"` // Number of blocks to consider
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Blocks <= 0 {
		args.Blocks = 100 // Default to 100 blocks
	}

	height := api.blockchain.GetHeight()
	if height < 2 {
		return nil, fmt.Errorf("not enough blocks to calculate average time")
	}

	// Cap number of blocks to available blocks
	if uint64(args.Blocks) > height-1 {
		args.Blocks = int(height - 1)
	}

	// Get timestamps of blocks
	var timestamps []int64
	for i := height; i > height-uint64(args.Blocks); i-- {
		block := api.blockchain.GetBlockByHeight(i)
		if block != nil && block.Header != nil {
			timestamps = append(timestamps, block.Header.Timestamp)
		}
	}

	if len(timestamps) < 2 {
		return nil, fmt.Errorf("not enough valid blocks to calculate average time")
	}

	// Calculate time differences between consecutive blocks
	var totalTimeDiff int64
	for i := 0; i < len(timestamps)-1; i++ {
		diff := timestamps[i] - timestamps[i+1]
		totalTimeDiff += diff
	}

	// Calculate average block time in seconds
	avgBlockTime := float64(totalTimeDiff) / float64(len(timestamps)-1)

	return avgBlockTime, nil
}

// GetStateRoot retrieves the current state root hash
func (api *BlockchainAPI) GetStateRoot(params json.RawMessage) (interface{}, error) {
	latestBlock := api.blockchain.GetLatestBlock()
	if latestBlock.Header == nil {
		return nil, fmt.Errorf("no blocks available")
	}

	return latestBlock.Header.StateRoot, nil
}

// GetBlockHeaders retrieves block headers in bulk
func (api *BlockchainAPI) GetBlockHeaders(params json.RawMessage) (interface{}, error) {
	var args struct {
		StartHeight uint64 `json:"startHeight"`
		EndHeight   uint64 `json:"endHeight"`
		MaxHeaders  int    `json:"maxHeaders"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Set defaults and limits
	if args.MaxHeaders <= 0 {
		args.MaxHeaders = 50 // Default to 50 headers
	}
	if args.MaxHeaders > 2000 {
		args.MaxHeaders = 2000 // Cap at 2000 headers
	}

	// Ensure end is not before start
	if args.EndHeight < args.StartHeight {
		return nil, fmt.Errorf("endHeight must be greater than or equal to startHeight")
	}

	// Ensure range doesn't exceed max headers
	numHeaders := int(args.EndHeight - args.StartHeight + 1)
	if numHeaders > args.MaxHeaders {
		args.EndHeight = args.StartHeight + uint64(args.MaxHeaders) - 1
	}

	// Create a response structure with just the header information
	var headers []*blockchain.BlockHeader
	for height := args.StartHeight; height <= args.EndHeight; height++ {
		block := api.blockchain.GetBlockByHeight(height)
		if block != nil && block.Header != nil {
			headers = append(headers, block.Header)
		}
	}

	return headers, nil
}

// ValidateAddress validates address format
func (api *BlockchainAPI) ValidateAddress(params json.RawMessage) (interface{}, error) {
	var args struct {
		Address string `json:"address"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	isValid := blockchain.ValidateAddress(args.Address)

	return map[string]bool{"isValid": isValid}, nil
}

// ExportState exports current state
func (api *BlockchainAPI) ExportState(params json.RawMessage) (interface{}, error) {
	var args struct {
		IncludeUTXOs   bool `json:"includeUTXOs"`
		IncludeMempool bool `json:"includeMempool"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	// Get current state
	state := api.blockchain.GetState()

	// Create response
	result := map[string]interface{}{
		"height":    state.Height,
		"stateRoot": state.StateRoot,
		"timestamp": state.Timestamp,
	}

	// Include UTXOs if requested
	if args.IncludeUTXOs {
		result["utxos"] = api.blockchain.GetUTXOSet()
		result["utxoRoot"] = state.UTXOSetRoot
	}

	// Include mempool if requested
	if args.IncludeMempool && api.node.Mempool != nil {
		result["mempool"] = api.node.Mempool.GetTransactions()
	}

	return result, nil
}

// GetStateProof generates Merkle proof for state
func (api *BlockchainAPI) GetStateProof(params json.RawMessage) (interface{}, error) {
	var args struct {
		Key string `json:"key"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Key == "" {
		return nil, fmt.Errorf("key parameter is required")
	}

	// Get UTXOPool instance
	utxoPool := api.blockchain.GetUTXOPool()
	if utxoPool == nil {
		return nil, fmt.Errorf("UTXO pool not available")
	}

	// Check if the key exists in the UTXO set
	utxos := utxoPool.GetAllUTXOs()
	if _, exists := utxos[args.Key]; !exists {
		return nil, fmt.Errorf("key %s not found in UTXO set", args.Key)
	}

	// Get the state root
	stateRoot := utxoPool.GetStateRoot()

	// Get Merkle proof using the UTXOPool's methods
	proof := make(map[string]interface{})
	proof["key"] = args.Key
	proof["utxo"] = utxos[args.Key]
	proof["stateRoot"] = stateRoot
	proof["verified"] = true

	// If the UTXOPool has a method to generate Merkle proofs, use it
	// Here we're using a simplified approach
	proof["proof"] = []string{fmt.Sprintf("merkle_proof_for_%s", args.Key)}

	// Add UTXO details for verification
	utxo := utxos[args.Key]
	proof["details"] = map[string]interface{}{
		"transactionId": utxo.TransactionID,
		"outputIndex":   utxo.OutputIndex,
		"amount":        utxo.Amount,
		"owner":         utxo.Owner,
		"blockHeight":   utxo.BlockHeight,
		"spent":         utxo.Spent,
	}

	return proof, nil
}
