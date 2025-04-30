package api

import (
	"blockchain-core/blockchain"
	"encoding/json"
	"fmt"
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
	nodeStatus := map[string]interface{}{
		"nodeID":        api.node.Host.ID().String(),
		"version":       "1.0.0",           // In a real implementation, this would be from a version constant
		"uptime":        time.Now().Unix(), // In a real implementation, this would be time since startup
		"blockHeight":   api.blockchain.GetHeight(),
		"lastBlockTime": api.blockchain.GetLatestBlock().Header.Timestamp,
		"peerCount":     len(api.node.Host.Network().Peers()),
		"syncing":       api.node.IsSyncing(),
		"mining":        false, // In a real implementation, this would be determined based on node role
		"validating":    false, // In a real implementation, this would be determined based on node role
		"cpu":           0,     // In a real implementation, this would be tracked
		"memory":        0,     // In a real implementation, this would be tracked
		"diskSpace":     0,     // In a real implementation, this would be tracked
	}

	return nodeStatus, nil
}

// GetSyncStatus retrieves blockchain sync status
func (api *NetworkAPI) GetSyncStatus(params json.RawMessage) (interface{}, error) {
	isSyncing := api.node.IsSyncing()

	// Basic sync status
	syncStatus := map[string]interface{}{
		"syncing":            isSyncing,
		"currentBlockHeight": api.blockchain.GetHeight(),
	}

	// If the node is syncing, you might add more detailed information
	if isSyncing {
		// In a real implementation, you would track sync progress
		syncStatus["startingBlock"] = 0
		syncStatus["targetBlock"] = 0
		syncStatus["peersUsed"] = []string{}
		syncStatus["estimatedTimeRemaining"] = 0
		syncStatus["downloadedBlocks"] = 0
		syncStatus["downloadRate"] = 0
	}

	return syncStatus, nil
}

// GetProtocolVersion retrieves node protocol version
func (api *NetworkAPI) GetProtocolVersion(params json.RawMessage) (interface{}, error) {
	// In a real implementation, this would be from a version constant
	return map[string]interface{}{
		"version":    "1.0.0",
		"minVersion": "1.0.0",
		"maxVersion": "1.0.0",
	}, nil
}

// GetNodePerformance retrieves performance metrics of node
func (api *NetworkAPI) GetNodePerformance(params json.RawMessage) (interface{}, error) {
	// In a real implementation, these would be measured metrics
	performance := map[string]interface{}{
		"transactionsPerSecond": 0,
		"blocksPerSecond":       0,
		"peakTransactions":      0,
		"averageBlockTime":      0,
		"cpuUsage":              0,
		"memoryUsage":           0,
		"diskUsage":             0,
		"networkInBandwidth":    0,
		"networkOutBandwidth":   0,
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
	// In a real implementation, bandwidth would be tracked
	bandwidthStats := map[string]interface{}{
		"total": map[string]interface{}{
			"in":  0,
			"out": 0,
		},
		"rate": map[string]interface{}{
			"in":  0,
			"out": 0,
		},
		"byPeer": map[string]interface{}{},
		"byProtocol": map[string]interface{}{
			"blocks":       0,
			"transactions": 0,
			"dht":          0,
			"pubsub":       0,
		},
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

	// In a real implementation, these metrics would be tracked over time
	growthMetrics := map[string]interface{}{
		"activeNodes":  []int{10, 15, 20, 25, 30},      // Example data points
		"transactions": []int{100, 150, 200, 250, 300}, // Example data points
		"blocks":       []int{10, 15, 20, 25, 30},      // Example data points
		"newAddresses": []int{5, 7, 10, 12, 15},        // Example data points
		"timespan":     fmt.Sprintf("Last %d days", args.Days),
		"interval":     "daily",
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

	// In a real implementation, active addresses would be tracked over time
	// For this example, we'll return a simulated response
	activeAddrs := map[string]interface{}{
		"total":     100, // Example value
		"new":       10,  // Example value
		"returning": 90,  // Example value
		"byActivity": map[string]int{
			"send":    70, // Example value
			"receive": 80, // Example value
			"both":    50, // Example value
		},
		"timespan": fmt.Sprintf("Last %d days", args.Days),
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

	// In a real implementation, slashing events would be retrieved from the database
	// For this example, we'll return a simulated response

	// Create sample slashing events
	events := []map[string]interface{}{
		{
			"validator":   "sup1234567890abcdef",
			"amount":      1000.0,
			"reason":      "missed_blocks",
			"blockHeight": 1000,
			"timestamp":   time.Now().Add(-24 * time.Hour).Unix(),
		},
		{
			"validator":   "supabcdef1234567890",
			"amount":      500.0,
			"reason":      "double_sign",
			"blockHeight": 1200,
			"timestamp":   time.Now().Add(-12 * time.Hour).Unix(),
		},
	}

	// Filter by validator if specified
	if args.Validator != "" {
		var filtered []map[string]interface{}
		for _, event := range events {
			if event["validator"] == args.Validator {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	}

	// Apply offset and limit
	startIdx := args.Offset
	if startIdx >= len(events) {
		startIdx = len(events)
	}

	endIdx := startIdx + args.Limit
	if endIdx > len(events) {
		endIdx = len(events)
	}

	if startIdx < endIdx {
		events = events[startIdx:endIdx]
	} else {
		events = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"events": events,
		"total":  2, // Total count in the database
	}, nil
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

	// In a real implementation, validator uptime would be tracked
	// For this example, we'll return a simulated response
	uptime := map[string]interface{}{
		"validator":        args.Validator,
		"uptimePercentage": 99.5, // Example value
		"totalBlocks":      1000, // Example value
		"validatedBlocks":  995,  // Example value
		"missedBlocks":     5,    // Example value
		"timespan":         fmt.Sprintf("Last %d days", args.Days),
		"lastUpdate":       time.Now().Unix(),
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

	// In a real implementation, transaction volume would be tracked daily
	// For this example, we'll return a simulated response with random data points

	// Generate sample data for the requested number of days
	volumeData := make([]map[string]interface{}, args.Days)

	for i := 0; i < args.Days; i++ {
		// Calculate the date for this data point (days ago)
		date := time.Now().AddDate(0, 0, -i)

		// Create a data point with simulated values
		volumeData[i] = map[string]interface{}{
			"date":            date.Format("2006-01-02"),
			"timestamp":       date.Unix(),
			"count":           100 + i*5,     // Example value increasing by 5 each day
			"volume":          10000 + i*500, // Example value increasing by 500 each day
			"uniqueAddresses": 50 + i*2,      // Example value increasing by 2 each day
			"averageValue":    100.0,         // Example value
		}
	}

	// Reverse the slice so it's in chronological order
	for i, j := 0, len(volumeData)-1; i < j; i, j = i+1, j-1 {
		volumeData[i], volumeData[j] = volumeData[j], volumeData[i]
	}

	result := map[string]interface{}{
		"data":     volumeData,
		"timespan": fmt.Sprintf("Last %d days", args.Days),
	}

	return result, nil
}
