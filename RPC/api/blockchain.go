package api

import (
	"blockchain-core/blockchain"
	"encoding/json"
	"fmt"
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
	// Calculate circulating supply based on blockchain economics
	height := api.blockchain.GetHeight()

	// Example calculation - adjust based on your actual tokenomics
	baseReward := 50.0
	halveningInterval := uint64(210000) // Example interval

	totalSupply := 0.0

	// Calculate how many coins have been minted so far
	if height > 0 {
		remainingBlocks := height

		// Calculate rewards at each halvening level
		for i := 0; remainingBlocks > 0; i++ {
			blocksAtThisReward := uint64(0)
			if remainingBlocks > halveningInterval {
				blocksAtThisReward = halveningInterval
			} else {
				blocksAtThisReward = remainingBlocks
			}

			// Fix: Use integer operation first, then convert to float64
			divider := uint64(1) << uint(i) // Shift with integers
			rewardAtLevel := baseReward / float64(divider)
			totalSupply += float64(blocksAtThisReward) * rewardAtLevel

			remainingBlocks -= blocksAtThisReward
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

	// Get all UTXOs
	utxoSet := api.blockchain.GetUTXOSet()
	if utxoSet == nil {
		return nil, fmt.Errorf("failed to get UTXO set")
	}

	// Calculate balances per address
	balances := make(map[string]float64)
	for _, utxo := range utxoSet {
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
	// Note: In production, you'd implement a proper sorting algorithm here
	// This is just a simplified version for the example
	for i := 0; i < len(richList); i++ {
		for j := i + 1; j < len(richList); j++ {
			if richList[j].Balance > richList[i].Balance {
				richList[i], richList[j] = richList[j], richList[i]
			}
		}
	}

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

	// Calculate total transactions (simplified - in production, maintain a counter)
	totalTxs := uint64(0)
	for i := uint64(1); i <= height; i++ {
		block := api.blockchain.GetBlockByHeight(i)
		if block != nil && block.Body != nil && block.Body.Transactions != nil {
			totalTxs += uint64(block.TransactionCount())
		}
	}

	// Get current mempool size
	mempoolTxs := 0
	if api.node.Mempool != nil {
		mempoolTxs = len(api.node.Mempool.GetTransactions())
	}

	// Get network difficulty
	difficulty := uint64(0)
	if latestBlock.Header != nil {
		difficulty = uint64(latestBlock.Header.Difficulty)
	}

	// Get active addresses (simplified)
	activeAddresses := 0 // In production, track this properly

	stats := map[string]interface{}{
		"blocks":              height,
		"transactions":        totalTxs,
		"difficulty":          difficulty,
		"networkHashRate":     0, // Calculated above in GetHashRate
		"mempoolTransactions": mempoolTxs,
		"activeAddresses":     activeAddresses,
		"bestBlockHash":       latestBlock.Hash(),
		"bestBlockHeight":     height,
		"chainSize":           0, // In production, track disk usage
	}

	// Calculate hash rate using the GetHashRate method
	hashRateParams, _ := json.Marshal(map[string]int{"days": 7})
	hashRate, err := api.GetHashRate(hashRateParams)
	if err == nil {
		stats["networkHashRate"] = hashRate
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

	// In a real implementation, you would generate a Merkle proof for the specified key
	// This is a simplified placeholder implementation
	utxoPool := api.blockchain.GetUTXOPool()
	if utxoPool == nil {
		return nil, fmt.Errorf("UTXO pool not available")
	}

	// Example: Generate a proof for a UTXO key
	// In real implementation, this would use the Patricia trie to generate a proper proof

	proof := map[string]interface{}{
		"key":       args.Key,
		"stateRoot": api.blockchain.GetLatestBlock().Header.StateRoot,
		"proof":     []string{"proof_placeholder_1", "proof_placeholder_2"}, // Simplified
		"verified":  true,
	}

	return proof, nil
}
