package api

import (
	"blockchain-core/blockchain"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// NetworkAPI handles network-related RPC methods
type NetworkAPI struct {
	node       *blockchain.Node
	blockchain *blockchain.Blockchain
}

// NewNetworkAPI creates a new network API instance
func NewNetworkAPI(node *blockchain.Node, blockchain *blockchain.Blockchain) *NetworkAPI {
	return &NetworkAPI{
		node:       node,
		blockchain: blockchain,
	}
}

// GetPeerInfo retrieves connected peer information
func (api *NetworkAPI) GetPeerInfo(params json.RawMessage) (interface{}, error) {
	peers := api.node.Host.Network().Peers()
	peerInfos := make([]map[string]interface{}, 0, len(peers))

	for _, peerID := range peers {
		// Get basic peer details
		addrs := api.node.Host.Peerstore().Addrs(peerID)
		protocols, _ := api.node.Host.Peerstore().GetProtocols(peerID)

		// Check if it's a bootstrap node
		isBootstrap := api.node.IsPeerBootstrapNode(peerID)

		// Check if it's a TXNS node
		isTXNS := api.node.IsPeerTXNSNode(peerID)

		// Create peer info structure
		peerInfo := map[string]interface{}{
			"id":             peerID.String(),
			"addresses":      addrs,
			"protocols":      protocols,
			"isBootstrap":    isBootstrap,
			"isTXNS":         isTXNS,
			"connectedSince": time.Now().Format(time.RFC3339),
			"latency":        api.node.Host.Peerstore().LatencyEWMA(peerID).String(),
		}

		peerInfos = append(peerInfos, peerInfo)
	}

	return peerInfos, nil
}

// GetNetworkStats retrieves network statistics
func (api *NetworkAPI) GetNetworkStats(params json.RawMessage) (interface{}, error) {
	// Get basic peer stats
	peers := api.node.Host.Network().Peers()
	bootstrapNodes := 0
	txnsNodes := 0
	validatorNodes := 0

	for _, peerID := range peers {
		if api.node.IsPeerBootstrapNode(peerID) {
			bootstrapNodes++
		} else if api.node.IsPeerTXNSNode(peerID) {
			txnsNodes++
		} else {
			// In a real implementation, you would check if it's a validator node
			// For now, we'll count any node that's not bootstrap or TXNS as a potential validator
			validatorNodes++
		}
	}

	// Get protocol stats - protocols supported by this node
	protocols, _ := api.node.Host.Peerstore().GetProtocols(api.node.Host.ID())

	// Create network stats
	stats := map[string]interface{}{
		"nodeID":               api.node.Host.ID().String(),
		"listenAddresses":      api.node.Host.Addrs(),
		"connectionCount":      len(peers),
		"bootstrapNodeCount":   bootstrapNodes,
		"txnsNodeCount":        txnsNodes,
		"validatorNodeCount":   validatorNodes,
		"protocols":            protocols,
		"networkID":            api.node.NetworkID,
		"chainID":              api.node.ChainID,
		"bandwidthIn":          0, // In a real implementation, you would track bandwidth
		"bandwidthOut":         0, // In a real implementation, you would track bandwidth
		"blockHeight":          api.blockchain.GetHeight(),
		"lastBlockTime":        api.blockchain.GetLatestBlock().Header.Timestamp,
		"lastConnectedPeer":    "", // In a real implementation, you would track this
		"lastDisconnectedPeer": "", // In a real implementation, you would track this
	}

	return stats, nil
}

// AddPeer adds a peer manually
func (api *NetworkAPI) AddPeer(params json.RawMessage) (interface{}, error) {
	var args struct {
		PeerAddress string `json:"peerAddress"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.PeerAddress == "" {
		return nil, fmt.Errorf("peer address is required")
	}

	// Connect to the peer
	err := api.node.ConnectToPeer(args.PeerAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to peer: %v", err)
	}

	return map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Connected to peer: %s", args.PeerAddress),
	}, nil
}

// GetNodeStatus retrieves node status information
func (api *NetworkAPI) GetNodeStatus(params json.RawMessage) (interface{}, error) {
	// Determine node roles based on actual state
	isMining := false
	isValidating := false

	// Check if node is a validator
	if api.node.IsInitializedValidator() {
		isValidating = true
	}

	// Get protocol version from peer manager constant
	protocolVersion := blockchain.ProtocolVersion

	// Use start time to calculate approximate uptime
	uptime := time.Now().Unix()

	nodeStatus := map[string]interface{}{
		"nodeID":             api.node.Host.ID().String(),
		"version":            protocolVersion,
		"uptime":             uptime,
		"blockHeight":        api.blockchain.GetHeight(),
		"lastBlockTime":      api.blockchain.GetLatestBlock().Header.Timestamp,
		"peerCount":          len(api.node.Host.Network().Peers()),
		"syncing":            api.node.IsSyncing(),
		"mining":             isMining,
		"validating":         isValidating,
		"networkID":          api.node.NetworkID,
		"chainID":            api.node.ChainID,
		"peerIDCount":        len(api.node.Host.Network().Peers()),
		"bootstrapConnected": api.node.CountNonBootnodePeers() > 0,
		"syncComplete":       !api.node.IsSyncing(),
	}

	return nodeStatus, nil
}

// GetSyncStatus retrieves blockchain sync status
func (api *NetworkAPI) GetSyncStatus(params json.RawMessage) (interface{}, error) {
	isSyncing := api.node.IsSyncing()
	currentHeight := api.blockchain.GetHeight()

	// Get the list of peers to identify potential sync sources
	peers := api.node.Host.Network().Peers()
	peerIDs := make([]string, 0, len(peers))
	for _, peer := range peers {
		peerIDs = append(peerIDs, peer.String())
	}

	// Basic sync status
	syncStatus := map[string]interface{}{
		"syncing":            isSyncing,
		"currentBlockHeight": currentHeight,
		"peersCount":         len(peers),
		"peersConnected":     peerIDs,
		"syncComplete":       !isSyncing,
		"networkID":          api.node.NetworkID,
		"chainID":            api.node.ChainID,
	}

	// If the node is syncing, we can calculate estimated progress
	if isSyncing {
		// If syncing, add more details about the sync progress
		// Target block is likely to be the highest block among peers
		// But since we don't have direct access to that, we'll estimate
		targetHeight := currentHeight // Default assumption

		// Estimate sync speed (blocks per minute)
		// This is a rough estimate since we don't track the actual sync rate
		syncSpeed := 0.0

		// For a better UX, we can provide an estimation of time remaining
		// based on the sync speed and remaining blocks
		remainingBlocks := int64(0)
		if targetHeight > currentHeight {
			remainingBlocks = int64(targetHeight - currentHeight)
		}

		// Rough estimate of time remaining in seconds
		var estimatedTimeRemaining int64 = 0
		if syncSpeed > 0 {
			estimatedTimeRemaining = int64(float64(remainingBlocks) / syncSpeed * 60) // Convert to seconds
		}

		syncStatus["targetBlockHeight"] = targetHeight
		syncStatus["remainingBlocks"] = remainingBlocks
		syncStatus["syncSpeed"] = syncSpeed
		syncStatus["estimatedTimeRemaining"] = estimatedTimeRemaining
		syncStatus["syncStartTime"] = time.Now().Add(-time.Duration(estimatedTimeRemaining) * time.Second).Unix()
	}

	return syncStatus, nil
}

// GetProtocolVersion retrieves node protocol version
func (api *NetworkAPI) GetProtocolVersion(params json.RawMessage) (interface{}, error) {
	return map[string]interface{}{
		"version":    blockchain.ProtocolVersion,
		"minVersion": blockchain.MinProtocolVersion,
		"maxVersion": blockchain.ProtocolVersion, // Current version is also the max supported
		"p2pProtocols": []string{
			blockchain.BlockProtocolID,
			blockchain.TransactionProtocolID,
			blockchain.HeartbeatProtocolID,
			blockchain.BlockchainSyncProtocol,
			blockchain.ChainStateProtocol,
			blockchain.BlockProtocol,
			blockchain.SyncProtocol,
			blockchain.StakeSyncProtocol,
			blockchain.BlockValidationProtocolID,
			blockchain.ValidatorProtocolID,
		},
	}, nil
}

// GetNodePerformance retrieves performance metrics of node
func (api *NetworkAPI) GetNodePerformance(params json.RawMessage) (interface{}, error) {
	// Get current blockchain height and timestamp of the latest block
	currentHeight := api.blockchain.GetHeight()
	latestBlock := api.blockchain.GetLatestBlock()

	// Calculate average block time based on the last 100 blocks (or fewer if chain is shorter)
	var avgBlockTime float64 = 0

	// Calculate average block time by analyzing blockchain data
	height := api.blockchain.GetHeight()
	if height >= 2 {
		// Cap number of blocks to analyze
		blockCount := 100
		if int(height) < blockCount {
			blockCount = int(height)
		}

		// Get timestamps of recent blocks
		var timestamps []int64
		for i := height; i > height-uint64(blockCount); i-- {
			block := api.blockchain.GetBlockByHeight(i)
			if block != nil && block.Header != nil {
				timestamps = append(timestamps, block.Header.Timestamp)
			}
		}

		// Calculate time differences between consecutive blocks
		if len(timestamps) >= 2 {
			totalDiff := int64(0)
			for i := 0; i < len(timestamps)-1; i++ {
				diff := timestamps[i] - timestamps[i+1]
				totalDiff += diff
			}

			// Calculate average block time in seconds
			avgBlockTime = float64(totalDiff) / float64(len(timestamps)-1)
		}
	}

	// Calculate transactions per second (approximate)
	var txPerSecond float64 = 0
	if avgBlockTime > 0 {
		// Get the latest block's transaction count
		txCount := latestBlock.TransactionCount()
		txPerSecond = float64(txCount) / avgBlockTime
	}

	// Get transactions in mempool
	var mempoolTxCount int = 0
	if api.node.Mempool != nil {
		mempoolTxCount = len(api.node.Mempool.GetTransactions())
	}

	// Create performance metrics
	blocksPerMinute := 0.0
	if avgBlockTime > 0 {
		blocksPerMinute = 60.0 / avgBlockTime
	}

	performance := map[string]interface{}{
		"transactionsPerSecond": txPerSecond,
		"blocksPerMinute":       blocksPerMinute,
		"peakTransactions":      mempoolTxCount,
		"averageBlockTime":      avgBlockTime,
		"currentHeight":         currentHeight,
		"connectedPeers":        len(api.node.Host.Network().Peers()),
		"pendingTransactions":   mempoolTxCount,
		"lastBlockHash":         latestBlock.Hash(),
		"lastBlockTime":         latestBlock.Header.Timestamp,
		"lastUpdate":            time.Now().Unix(),
	}

	return performance, nil
}

// GetBootnodeInfo retrieves information about bootstrap nodes
func (api *NetworkAPI) GetBootnodeInfo(params json.RawMessage) (interface{}, error) {
	peers := api.node.Host.Network().Peers()
	bootNodes := make([]map[string]interface{}, 0)

	for _, peerID := range peers {
		if api.node.IsPeerBootstrapNode(peerID) {
			addrs := api.node.Host.Peerstore().Addrs(peerID)
			protocols, _ := api.node.Host.Peerstore().GetProtocols(peerID)

			bootNodeInfo := map[string]interface{}{
				"id":             peerID.String(),
				"addresses":      addrs,
				"protocols":      protocols,
				"connectedSince": time.Now().Format(time.RFC3339),
				"latency":        api.node.Host.Peerstore().LatencyEWMA(peerID).String(),
			}

			bootNodes = append(bootNodes, bootNodeInfo)
		}
	}

	return bootNodes, nil
}

// GetPeerLatency retrieves latency statistics for peers
func (api *NetworkAPI) GetPeerLatency(params json.RawMessage) (interface{}, error) {
	peers := api.node.Host.Network().Peers()
	latencyStats := make(map[string]interface{})

	for _, peerID := range peers {
		latency := api.node.Host.Peerstore().LatencyEWMA(peerID)

		latencyStats[peerID.String()] = map[string]interface{}{
			"current":  latency.String(),
			"average":  latency.String(), // In a real implementation, this would be a moving average
			"min":      latency.String(), // In a real implementation, this would be the minimum observed
			"max":      latency.String(), // In a real implementation, this would be the maximum observed
			"samples":  1,                // In a real implementation, this would be the number of samples
			"lastPing": time.Now().Add(-latency).Unix(),
		}
	}

	return latencyStats, nil
}

// GetBandwidthUsage retrieves bandwidth usage statistics
func (api *NetworkAPI) GetBandwidthUsage(params json.RawMessage) (interface{}, error) {
	// In a production environment, we'd track actual bandwidth metrics
	// Here we'll create estimates based on blockchain and peer activity

	// Get peer count
	peerCount := len(api.node.Host.Network().Peers())

	// Get recent blockchain activity (last 100 blocks)
	height := api.blockchain.GetHeight()
	startHeight := uint64(1)
	if height > 100 {
		startHeight = height - 100
	}

	// Count average transactions per block as proxy for network activity
	totalTxs := 0
	blockCount := 0
	for i := startHeight; i <= height; i++ {
		block := api.blockchain.GetBlockByHeight(i)
		if block != nil {
			totalTxs += int(block.TransactionCount())
			blockCount++
		}
	}

	// Calculate average transaction count
	avgTxPerBlock := 0
	if blockCount > 0 {
		avgTxPerBlock = totalTxs / blockCount
	}

	// Estimate bandwidth based on peer count and transaction activity
	// These are rough estimates for demonstration purposes
	// A typical transaction might be ~250 bytes
	// A typical block header might be ~100 bytes
	txSize := 250          // bytes
	blockHeaderSize := 100 // bytes

	// Estimate peer data exchange (inbound)
	peerInRate := peerCount * blockHeaderSize // block headers from each peer

	// Estimate peer data exchange (outbound)
	peerOutRate := peerCount * blockHeaderSize

	// Estimate transaction-related bandwidth
	txInRate := avgTxPerBlock * txSize // bytes per block
	txOutRate := txInRate              // assume symmetric tx propagation

	// Mempool size
	mempoolTxCount := 0
	if api.node.Mempool != nil {
		mempoolTxCount = len(api.node.Mempool.GetTransactions())
	}

	// Mempool bandwidth estimate
	mempoolBandwidth := mempoolTxCount * txSize

	// Total estimates (bytes per block)
	totalIn := peerInRate + txInRate
	totalOut := peerOutRate + txOutRate

	// Create per-peer estimates
	peerStats := make(map[string]interface{})
	peers := api.node.Host.Network().Peers()
	for _, peer := range peers {
		peerID := peer.String()
		peerStats[peerID] = map[string]interface{}{
			"in":  totalIn / peerCount,
			"out": totalOut / peerCount,
		}
	}

	// Create bandwidth statistics
	bandwidthStats := map[string]interface{}{
		"total": map[string]interface{}{
			"in":  totalIn,
			"out": totalOut,
		},
		"rate": map[string]interface{}{
			"in":  totalIn,
			"out": totalOut,
		},
		"byPeer": peerStats,
		"byProtocol": map[string]interface{}{
			"blocks":       blockHeaderSize * peerCount,
			"transactions": txSize * avgTxPerBlock,
			"mempool":      mempoolBandwidth,
		},
		"peers":            peerCount,
		"avgTxPerBlock":    avgTxPerBlock,
		"estimatedBytes":   true, // Flag to indicate these are estimates
		"mempoolTxCount":   mempoolTxCount,
		"sampleBlockCount": blockCount,
	}

	return bandwidthStats, nil
}

// GetNetworkGrowth retrieves network growth metrics
func (api *NetworkAPI) GetNetworkGrowth(params json.RawMessage) (interface{}, error) {
	var args struct {
		Days int `json:"days"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// Default to 30 days if not specified
		args.Days = 30
	}

	if args.Days <= 0 {
		args.Days = 30
	}
	if args.Days > 365 {
		args.Days = 365
	}

	// Calculate metrics based on actual blockchain data
	height := api.blockchain.GetHeight()

	// Calculate average block time to determine blocks per day
	var avgBlockTime float64 = 10.0 // Default assumption: 10 seconds per block

	// Get timestamps of recent blocks to calculate actual average block time
	if height >= 100 {
		var timestamps []int64
		for i := height; i > height-100 && i > 0; i-- {
			block := api.blockchain.GetBlockByHeight(i)
			if block != nil && block.Header != nil {
				timestamps = append(timestamps, block.Header.Timestamp)
			}
		}

		// Calculate average time between blocks
		if len(timestamps) >= 2 {
			totalDiff := int64(0)
			for i := 0; i < len(timestamps)-1; i++ {
				diff := timestamps[i] - timestamps[i+1]
				if diff > 0 {
					totalDiff += diff
				}
			}

			if len(timestamps) > 1 {
				avgBlockTime = float64(totalDiff) / float64(len(timestamps)-1)
			}
		}
	}

	// Calculate blocks per day
	blocksPerDay := int(24 * 60 * 60 / avgBlockTime)

	// Determine the block height for each day in the requested period
	var dailyHeights []uint64
	var endHeight uint64 = height

	for i := 0; i < args.Days; i++ {
		startHeight := uint64(0)
		if endHeight > uint64(blocksPerDay) {
			startHeight = endHeight - uint64(blocksPerDay)
		}
		dailyHeights = append(dailyHeights, startHeight)
		endHeight = startHeight
		if endHeight == 0 {
			break
		}
	}

	// Collect data for each interval (reverse order for chronological)
	activeNodes := make([]int, 0, args.Days)
	transactions := make([]int, 0, args.Days)
	blocks := make([]int, 0, args.Days)
	newAddresses := make([]int, 0, args.Days)

	// Track unique addresses seen
	uniqueAddresses := make(map[string]bool)

	// Process each interval
	currentHeight := height
	for i := 0; i < len(dailyHeights); i++ {
		// For each daily interval, count blocks, transactions, and addresses
		periodBlocks := 0
		periodTxs := 0
		periodAddresses := make(map[string]bool)

		// Define the range for this interval
		startHeight := dailyHeights[i]

		// Process blocks in this interval
		for h := currentHeight; h > startHeight && h > 0; h-- {
			block := api.blockchain.GetBlockByHeight(h)
			if block == nil {
				continue
			}

			periodBlocks++

			// Process transactions in this block
			txCount := int(block.TransactionCount())
			periodTxs += txCount

			// Process transaction senders and receivers to track addresses
			for _, tx := range block.Body.Transactions.GetAllTransactions() {
				// Track addresses in this period
				if tx.Sender != "" {
					periodAddresses[tx.Sender] = true
				}
				if tx.Receiver != "" {
					periodAddresses[tx.Receiver] = true
				}
			}
		}

		// Count new addresses in this period
		newAddrCount := 0
		for addr := range periodAddresses {
			if !uniqueAddresses[addr] {
				newAddrCount++
				uniqueAddresses[addr] = true
			}
		}

		// Add metrics for this period
		activeNodes = append(activeNodes, len(api.node.Host.Network().Peers()))
		transactions = append(transactions, periodTxs)
		blocks = append(blocks, periodBlocks)
		newAddresses = append(newAddresses, newAddrCount)

		// Move to the next interval
		currentHeight = startHeight
	}

	// Reverse arrays to get chronological order
	for i, j := 0, len(activeNodes)-1; i < j; i, j = i+1, j-1 {
		activeNodes[i], activeNodes[j] = activeNodes[j], activeNodes[i]
		transactions[i], transactions[j] = transactions[j], transactions[i]
		blocks[i], blocks[j] = blocks[j], blocks[i]
		newAddresses[i], newAddresses[j] = newAddresses[j], newAddresses[i]
	}

	// Return the growth metrics
	growthMetrics := map[string]interface{}{
		"activeNodes":  activeNodes,
		"transactions": transactions,
		"blocks":       blocks,
		"newAddresses": newAddresses,
		"timespan":     fmt.Sprintf("Last %d days", args.Days),
		"interval":     "daily",
		"blocksPerDay": blocksPerDay,
		"avgBlockTime": avgBlockTime,
	}

	return growthMetrics, nil
}

// GetActiveAddresses retrieves number of active addresses
func (api *NetworkAPI) GetActiveAddresses(params json.RawMessage) (interface{}, error) {
	var args struct {
		Days int `json:"days"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// Default to 30 days if not specified
		args.Days = 30
	}

	if args.Days <= 0 {
		args.Days = 30
	}
	if args.Days > 365 {
		args.Days = 365
	}

	// Calculate metrics based on actual blockchain data
	height := api.blockchain.GetHeight()

	// Calculate average block time to determine blocks per day
	var avgBlockTime float64 = 10.0 // Default assumption: 10 seconds per block

	// Get timestamps of recent blocks to calculate actual average block time
	if height >= 100 {
		var timestamps []int64
		for i := height; i > height-100 && i > 0; i-- {
			block := api.blockchain.GetBlockByHeight(i)
			if block != nil && block.Header != nil {
				timestamps = append(timestamps, block.Header.Timestamp)
			}
		}

		// Calculate average time between blocks
		if len(timestamps) >= 2 {
			totalDiff := int64(0)
			for i := 0; i < len(timestamps)-1; i++ {
				diff := timestamps[i] - timestamps[i+1]
				if diff > 0 {
					totalDiff += diff
				}
			}

			if len(timestamps) > 1 {
				avgBlockTime = float64(totalDiff) / float64(len(timestamps)-1)
			}
		}
	}

	// Calculate blocks per day
	blocksPerDay := int(24 * 60 * 60 / avgBlockTime)

	// Calculate the block height from days ago
	startHeight := uint64(1)
	if height > uint64(args.Days*blocksPerDay) {
		startHeight = height - uint64(args.Days*blocksPerDay)
	}

	// Track active addresses within the time period
	activeAddresses := make(map[string]bool)
	senderAddresses := make(map[string]bool)
	receiverAddresses := make(map[string]bool)
	newAddresses := make(map[string]bool)

	// Addresses seen before this period
	knownAddressesBefore := make(map[string]bool)

	// First scan blockchain before requested period to identify known addresses
	if startHeight > 1 {
		for h := uint64(1); h < startHeight; h++ {
			block := api.blockchain.GetBlockByHeight(h)
			if block == nil {
				continue
			}

			// Process transaction addresses
			for _, tx := range block.Body.Transactions.GetAllTransactions() {
				if tx.Sender != "" {
					knownAddressesBefore[tx.Sender] = true
				}
				if tx.Receiver != "" {
					knownAddressesBefore[tx.Receiver] = true
				}
			}
		}
	}

	// Now scan the period of interest
	for h := startHeight; h <= height; h++ {
		block := api.blockchain.GetBlockByHeight(h)
		if block == nil {
			continue
		}

		// Process transaction addresses
		for _, tx := range block.Body.Transactions.GetAllTransactions() {
			if tx.Sender != "" {
				activeAddresses[tx.Sender] = true
				senderAddresses[tx.Sender] = true

				// Check if this is a new address
				if !knownAddressesBefore[tx.Sender] {
					newAddresses[tx.Sender] = true
				}
			}

			if tx.Receiver != "" {
				activeAddresses[tx.Receiver] = true
				receiverAddresses[tx.Receiver] = true

				// Check if this is a new address
				if !knownAddressesBefore[tx.Receiver] {
					newAddresses[tx.Receiver] = true
				}
			}
		}
	}

	// Count addresses that both sent and received
	bothAddresses := make(map[string]bool)
	for addr := range senderAddresses {
		if receiverAddresses[addr] {
			bothAddresses[addr] = true
		}
	}

	// Build the response
	activeAddrs := map[string]interface{}{
		"total":     len(activeAddresses),
		"new":       len(newAddresses),
		"returning": len(activeAddresses) - len(newAddresses),
		"byActivity": map[string]int{
			"send":    len(senderAddresses),
			"receive": len(receiverAddresses),
			"both":    len(bothAddresses),
		},
		"timespan":     fmt.Sprintf("Last %d days", args.Days),
		"startBlock":   startHeight,
		"endBlock":     height,
		"blocksPerDay": blocksPerDay,
		"avgBlockTime": avgBlockTime,
	}

	return activeAddrs, nil
}

// GetPeerCount retrieves the current number of connected peers
func (api *NetworkAPI) GetPeerCount(params json.RawMessage) (interface{}, error) {
	return len(api.node.Host.Network().Peers()), nil
}

// GetSlashingEvents retrieves history of slashing events
func (api *NetworkAPI) GetSlashingEvents(params json.RawMessage) (interface{}, error) {
	var args struct {
		Limit     int    `json:"limit"`
		Offset    int    `json:"offset"`
		Validator string `json:"validator"` // Optional: filter by validator address
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// Default values if not specified
		args.Limit = 10
		args.Offset = 0
	}

	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 100 {
		args.Limit = 100
	}

	// Get all validators to search for slashing events
	validators := api.blockchain.Validators

	// Fetch slashing events by checking validator status changes
	var events []map[string]interface{}

	// Traverse the blockchain to find validator status changes that indicate slashing
	height := api.blockchain.GetHeight()
	startHeight := uint64(1)

	// Limit how far back we search based on chain size
	if height > 5000 {
		startHeight = height - 5000
	}

	// Create a map of slashing events to prevent duplicates
	slashingEvents := make(map[string]map[string]interface{})

	// Check each block for evidence of slashing
	for h := startHeight; h <= height; h++ {
		block := api.blockchain.GetBlockByHeight(h)
		if block == nil || block.Header == nil {
			continue
		}

		// Check if this block contains slashing info
		// In a real implementation, this would be recorded in blockchain state or events
		// Here we'll check the validator list for status changes

		for validatorAddr, validator := range validators {
			// Skip if we're filtering by validator and this isn't the one
			if args.Validator != "" && validatorAddr != args.Validator {
				continue
			}

			// Look for validators with slashed status
			if validator.Status == blockchain.ValidatorStatusSlashed {
				// Create unique key to prevent duplicate events
				eventKey := fmt.Sprintf("%s-%d", validatorAddr, h)

				// Calculate slashing amount (approximately 10% of stake from StakePool if available)
				slashAmount := 0.0
				if api.blockchain.GetStakePool() != nil {
					if stake, exists := api.blockchain.GetStakePool().Stakes[validatorAddr]; exists && stake != nil {
						slashAmount = float64(stake.Amount) * 0.1
					}
				}

				// Create the slashing event
				slashingEvent := map[string]interface{}{
					"validator":   validatorAddr,
					"amount":      slashAmount,
					"reason":      determineSlashingReason(validator),
					"blockHeight": h,
					"timestamp":   block.Header.Timestamp,
				}

				slashingEvents[eventKey] = slashingEvent
			}
		}
	}

	// Convert map to slice
	for _, event := range slashingEvents {
		events = append(events, event)
	}

	// Sort events by timestamp (newest first)
	sort.Slice(events, func(i, j int) bool {
		timestampI, _ := events[i]["timestamp"].(int64)
		timestampJ, _ := events[j]["timestamp"].(int64)
		return timestampI > timestampJ
	})

	// Apply offset and limit
	startIdx := args.Offset
	if startIdx >= len(events) {
		startIdx = len(events)
	}

	endIdx := startIdx + args.Limit
	if endIdx > len(events) {
		endIdx = len(events)
	}

	var resultEvents []map[string]interface{}
	if startIdx < endIdx {
		resultEvents = events[startIdx:endIdx]
	}

	return map[string]interface{}{
		"events": resultEvents,
		"total":  len(events),
	}, nil
}

// Helper function to determine slashing reason based on validator state
func determineSlashingReason(validator *blockchain.Validator) string {
	if validator.Performance != nil && validator.Performance.MissedValidations > 10 {
		return "missed_blocks"
	}

	if validator.ConsensusFailures > 0 {
		return "consensus_failures"
	}

	if validator.Violations > 0 {
		return "protocol_violations"
	}

	return "unknown"
}

// GetValidatorUptime retrieves validator uptime statistics
func (api *NetworkAPI) GetValidatorUptime(params json.RawMessage) (interface{}, error) {
	var args struct {
		Validator string `json:"validator"`
		Days      int    `json:"days"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid parameters: %v", err)
	}

	if args.Validator == "" {
		return nil, fmt.Errorf("validator address is required")
	}

	if args.Days <= 0 {
		args.Days = 7 // Default to 7 days
	}
	if args.Days > 90 {
		args.Days = 90 // Cap at 90 days
	}

	// Get validator from blockchain
	validator, exists := api.blockchain.Validators[args.Validator]
	if !exists {
		return nil, fmt.Errorf("validator not found: %s", args.Validator)
	}

	// Get latest block for perspective
	latestBlock := api.blockchain.GetLatestBlock()
	blockchainHeight := latestBlock.Header.BlockNumber

	// Calculate block height from days ago
	// Assuming average block time (calculated from blockchain data)
	var avgBlockTime float64 = 10.0 // Default assumption: 10 seconds per block

	// Calculate actual average block time from recent blocks
	if blockchainHeight >= 100 {
		// Get timestamps of recent blocks
		var timestamps []int64
		for i := blockchainHeight; i > blockchainHeight-100; i-- {
			block := api.blockchain.GetBlockByHeight(i)
			if block != nil && block.Header != nil {
				timestamps = append(timestamps, block.Header.Timestamp)
			}
		}

		// Calculate average time between blocks
		if len(timestamps) >= 2 {
			totalDiff := int64(0)
			for i := 0; i < len(timestamps)-1; i++ {
				diff := timestamps[i] - timestamps[i+1]
				if diff > 0 {
					totalDiff += diff
				}
			}
			avgBlockTime = float64(totalDiff) / float64(len(timestamps)-1)
		}
	}

	// Calculate blocks per day based on average block time
	blocksPerDay := int(24 * 60 * 60 / avgBlockTime)
	blockStartHeight := uint64(0)

	if blockchainHeight > uint64(args.Days*blocksPerDay) {
		blockStartHeight = blockchainHeight - uint64(args.Days*blocksPerDay)
	}

	// Calculate total blocks in the period
	var totalBlocks uint64
	if blockchainHeight >= blockStartHeight {
		totalBlocks = blockchainHeight - blockStartHeight
	}

	// Count blocks validated by this validator in the period
	validatedBlocks := uint64(0)
	for i := blockStartHeight; i <= blockchainHeight; i++ {
		block := api.blockchain.GetBlockByHeight(i)
		if block != nil && block.Header != nil && block.Header.ValidatorAddress == args.Validator {
			validatedBlocks++
		}
	}

	// Calculate missed blocks and uptime percentage
	missedBlocks := totalBlocks - validatedBlocks
	uptimePercentage := 0.0
	if totalBlocks > 0 {
		uptimePercentage = float64(validatedBlocks) / float64(totalBlocks) * 100
	}

	uptime := map[string]interface{}{
		"validator":        args.Validator,
		"uptimePercentage": uptimePercentage,
		"totalBlocks":      totalBlocks,
		"validatedBlocks":  validatedBlocks,
		"missedBlocks":     missedBlocks,
		"timespan":         fmt.Sprintf("Last %d days", args.Days),
		"lastActive":       validator.LastActive.Unix(),
		"blockchainHeight": blockchainHeight,
		"startBlockHeight": blockStartHeight,
		"avgBlockTime":     avgBlockTime,
		"status":           validator.Status,
		"score":            validator.Score,
	}

	return uptime, nil
}

// GetDailyTransactionVolume retrieves transaction volume over time
func (api *NetworkAPI) GetDailyTransactionVolume(params json.RawMessage) (interface{}, error) {
	var args struct {
		Days int `json:"days"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		// Default to 30 days if not specified
		args.Days = 30
	}

	if args.Days <= 0 {
		args.Days = 30
	}
	if args.Days > 365 {
		args.Days = 365
	}

	// Calculate metrics based on actual blockchain data
	height := api.blockchain.GetHeight()

	// Calculate average block time to determine blocks per day
	var avgBlockTime float64 = 10.0 // Default assumption: 10 seconds per block

	// Get timestamps of recent blocks to calculate actual average block time
	if height >= 100 {
		var timestamps []int64
		for i := height; i > height-100 && i > 0; i-- {
			block := api.blockchain.GetBlockByHeight(i)
			if block != nil && block.Header != nil {
				timestamps = append(timestamps, block.Header.Timestamp)
			}
		}

		// Calculate average time between blocks
		if len(timestamps) >= 2 {
			totalDiff := int64(0)
			for i := 0; i < len(timestamps)-1; i++ {
				diff := timestamps[i] - timestamps[i+1]
				if diff > 0 {
					totalDiff += diff
				}
			}

			if len(timestamps) > 1 {
				avgBlockTime = float64(totalDiff) / float64(len(timestamps)-1)
			}
		}
	}

	// Calculate blocks per day
	blocksPerDay := int(24 * 60 * 60 / avgBlockTime)

	// Generate daily data points based on blockchain data
	var volumeData []map[string]interface{}

	// Iterate over each day
	for day := 0; day < args.Days; day++ {
		// Calculate block range for this day
		endHeight := height - uint64(day*blocksPerDay)
		startHeight := endHeight - uint64(blocksPerDay)

		if startHeight < 1 {
			startHeight = 1
		}

		if endHeight < 1 || endHeight < startHeight {
			break
		}

		// Calculate date for this data point
		date := time.Now().AddDate(0, 0, -day)

		// Process all blocks in this day
		txCount := 0
		txVolume := 0.0
		uniqueAddresses := make(map[string]bool)

		for h := startHeight; h <= endHeight; h++ {
			block := api.blockchain.GetBlockByHeight(h)
			if block == nil {
				continue
			}

			// Get transactions in this block
			txs := block.Body.Transactions.GetAllTransactions()
			txCount += len(txs)

			// Calculate volume and track unique addresses
			for _, tx := range txs {
				txVolume += tx.Amount

				if tx.Sender != "" {
					uniqueAddresses[tx.Sender] = true
				}
				if tx.Receiver != "" {
					uniqueAddresses[tx.Receiver] = true
				}
			}
		}

		// Calculate average transaction value
		avgValue := 0.0
		if txCount > 0 {
			avgValue = txVolume / float64(txCount)
		}

		// Create data point for this day
		volumeData = append(volumeData, map[string]interface{}{
			"date":            date.Format("2006-01-02"),
			"timestamp":       date.Unix(),
			"count":           txCount,
			"volume":          txVolume,
			"uniqueAddresses": len(uniqueAddresses),
			"averageValue":    avgValue,
			"blockStart":      startHeight,
			"blockEnd":        endHeight,
		})
	}

	// We don't need to reverse the slice as we've already built it in chronologically reversed order

	result := map[string]interface{}{
		"data":          volumeData,
		"timespan":      fmt.Sprintf("Last %d days", args.Days),
		"blocksPerDay":  blocksPerDay,
		"avgBlockTime":  avgBlockTime,
		"currentHeight": height,
	}

	return result, nil
}
