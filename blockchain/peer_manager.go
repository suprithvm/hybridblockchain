package blockchain

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/host"
)

const (
	maxPeers                = 50
	minPeers                = 10
	peerScoreInit           = 100
	peerScoreMax            = 1000
	peerScoreMin            = -100
	scoreDecayTime          = 1 * time.Hour
	ProtocolVersion         = "1.0.0"
	MinProtocolVersion      = "1.0.0"
	ScoreSuccessfulSync     = 10
	ScoreFailedSync         = -5
	ScoreValidBlock         = 5
	ScoreInvalidBlock       = -10
	ScoreGoodLatency        = 2
	ScorePoorLatency        = -2
	ScoreValidTransaction   = 1
	ScoreInvalidTx          = -2
	ReputationThresholdGood = 50
	ReputationThresholdBad  = -20
	VotingTimeout           = 30 * time.Second
	MinVotingQuorum         = 2 / 3
	MaxRollbackBlocks       = 100
	MsgNewBlock             = "NEW_BLOCK"
	MsgGetBlocks            = "GET_BLOCKS"
	MsgGetHeaders           = "GET_HEADERS"
	MsgBlockHeaders         = "BLOCK_HEADERS"
	MsgStateVerify          = "STATE_VERIFY"
	MaxBlocksPerRequest     = 500
	MaxHeadersPerRequest    = 2000
	BlockPropagationTimeout = 30 * time.Second
)

type SyncProgress struct {
	Current int
	Target  int
}

type PeerInfo struct {
	ID             peer.ID
	Score          int
	LastSeen       time.Time
	Connected      bool
	Blacklisted    bool
	ConnectedAt    time.Time
	DisconnectedAt time.Time
	FailCount      int
	IsSyncing      bool
	SyncProgress   SyncProgress
	Version        string
	Capabilities   []string
	LatencyStats   LatencyStats
	ValidBlocks    int
	InvalidBlocks  int
	ValidTxs       int
	InvalidTxs     int
	LastVote       string
	VoteTimestamp  int64
}

type LatencyStats struct {
	AverageLatency   time.Duration
	LastLatency      time.Duration
	LatencyHistory   []time.Duration
	LastLatencyCheck time.Time
}

type PeerManager struct {
	mutex     sync.RWMutex
	peers     map[peer.ID]*PeerInfo
	maxPeers  int
	blacklist map[peer.ID]time.Time
	blockchain *Blockchain
	host       host.Host
}

type StateVotingResult struct {
	WinningState  string         // The state that won the vote
	VoteCount     map[string]int // Count of votes per state
	QuorumReached bool           // Whether quorum was reached
}

type VotingResult struct {
	QuorumReached bool
	WinningState  string
	VoteCount     map[string]int
}

type ChainStateVerification struct {
	Height          uint64
	StateRoot       string
	ConsensusRoot   string
	Responses       map[peer.ID]string
	HasConsensus    bool
	ConsensusReached bool
}

// Create a serializable block structure
type BlockMessage struct {
    BlockNumber          int
    PreviousHash         string
    Timestamp           int64
    PatriciaRoot        string
    TransactionsRoot    string  // Instead of full trie, just send root
    Nonce               int
    Hash                string
    Difficulty          int
    CumulativeDifficulty int
    StateRoot           string
}

func NewPeerManager(h host.Host) *PeerManager {
	return &PeerManager{
		peers:     make(map[peer.ID]*PeerInfo),
		maxPeers:  maxPeers,
		blacklist: make(map[peer.ID]time.Time),
		host:      h,
	}
}

// AddPeer adds a new peer with initialized fields
func (pm *PeerManager) AddPeer(id peer.ID) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if _, exists := pm.peers[id]; !exists {
		pm.peers[id] = &PeerInfo{
			ID:          id,
			Score:       peerScoreInit,
			LastSeen:    time.Now(),
			Connected:   true,
			ConnectedAt: time.Now(),
			LatencyStats: LatencyStats{
				LatencyHistory: make([]time.Duration, 0),
			},
			Capabilities:  make([]string, 0),
			Version:       "",
			ValidBlocks:   0,
			InvalidBlocks: 0,
			ValidTxs:      0,
			InvalidTxs:    0,
		}
	}
}

// RemovePeer removes a peer from the manager
func (pm *PeerManager) RemovePeer(id peer.ID) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if peer, exists := pm.peers[id]; exists {
		peer.Connected = false
		peer.DisconnectedAt = time.Now()
	}
}

// UpdatePeerScore updates a peer's score
func (pm *PeerManager) UpdatePeerScore(id peer.ID, delta int) {
	if info, exists := pm.peers[id]; exists {
		info.Score += delta
		// Clamp score between min and max
		if info.Score > peerScoreMax {
			info.Score = peerScoreMax
		}
		if info.Score < peerScoreMin {
			info.Score = peerScoreMin
		}
		pm.peers[id] = info
	}
}

// blacklistPeer adds a peer to the blacklist
func (pm *PeerManager) blacklistPeer(id peer.ID) {
	pm.blacklist[id] = time.Now()
	if peer, exists := pm.peers[id]; exists {
		peer.Blacklisted = true
	}
}

// IsBlacklisted checks if a peer is blacklisted
func (pm *PeerManager) IsBlacklisted(id peer.ID) bool {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	if blacklistedTime, exists := pm.blacklist[id]; exists {
		// Remove from blacklist after 24 hours
		if time.Since(blacklistedTime) > 24*time.Hour {
			delete(pm.blacklist, id)
			return false
		}
		return true
	}
	return false
}

// GetBestPeers returns the top n peers by score
func (pm *PeerManager) GetBestPeers(n int) []peer.ID {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	type peerScore struct {
		id    peer.ID
		score int
	}

	var scores []peerScore
	for id, info := range pm.peers {
		if info.Connected && !info.Blacklisted {
			scores = append(scores, peerScore{id, info.Score})
		}
	}

	// Sort by score
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[i].score < scores[j].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	result := make([]peer.ID, 0, n)
	for i := 0; i < n && i < len(scores); i++ {
		result = append(result, scores[i].id)
	}
	return result
}

// NeedMorePeers checks if we need more peer connections
func (pm *PeerManager) NeedMorePeers() bool {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	connectedCount := 0
	for _, info := range pm.peers {
		if info.Connected && !info.Blacklisted {
			connectedCount++
		}
	}
	return connectedCount < minPeers
}

// GetConnectedPeers returns all connected peers
func (pm *PeerManager) GetConnectedPeers() []peer.ID {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	var connected []peer.ID
	for id, info := range pm.peers {
		if info.Connected && !info.Blacklisted {
			connected = append(connected, id)
		}
	}
	return connected
}

// CleanupPeers removes disconnected peers that haven't been seen in a while
func (pm *PeerManager) CleanupPeers() {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	now := time.Now()
	for id, info := range pm.peers {
		if !info.Connected && now.Sub(info.LastSeen) > 24*time.Hour {
			delete(pm.peers, id)
		}
	}
}

// SetSyncState updates the sync state for a peer
func (pm *PeerManager) SetSyncState(id peer.ID, syncing bool) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if info, exists := pm.peers[id]; exists {
		info.IsSyncing = syncing
		pm.peers[id] = info
	}
}

// IsSyncing checks if a peer is currently syncing
func (pm *PeerManager) IsSyncing(id peer.ID) bool {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	if info, exists := pm.peers[id]; exists {
		return info.IsSyncing
	}
	return false
}

// UpdateSyncProgress updates the sync progress for a peer
func (pm *PeerManager) UpdateSyncProgress(id peer.ID, current, target int) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if info, exists := pm.peers[id]; exists {
		info.SyncProgress = SyncProgress{
			Current: current,
			Target:  target,
		}
		pm.peers[id] = info
	}
}

// GetSyncProgress gets the current sync progress for a peer
func (pm *PeerManager) GetSyncProgress(id peer.ID) SyncProgress {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	if info, exists := pm.peers[id]; exists {
		return info.SyncProgress
	}
	return SyncProgress{}
}

// GetSyncingPeers returns all peers currently syncing
func (pm *PeerManager) GetSyncingPeers() []peer.ID {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	var syncing []peer.ID
	for id, info := range pm.peers {
		if info.IsSyncing && !info.Blacklisted {
			syncing = append(syncing, id)
		}
	}
	return syncing
}

// UpdatePeerLatency updates the latency stats for a peer
func (pm *PeerManager) UpdatePeerLatency(id peer.ID, latency time.Duration) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if info, exists := pm.peers[id]; exists {
		info.LatencyStats.LastLatency = latency
		info.LatencyStats.LastLatencyCheck = time.Now()

		// Update latency history
		if info.LatencyStats.LatencyHistory == nil {
			info.LatencyStats.LatencyHistory = make([]time.Duration, 0)
		}
		info.LatencyStats.LatencyHistory = append(info.LatencyStats.LatencyHistory, latency)

		// Keep only last 10 measurements
		if len(info.LatencyStats.LatencyHistory) > 10 {
			info.LatencyStats.LatencyHistory = info.LatencyStats.LatencyHistory[1:]
		}

		// Calculate average
		var sum time.Duration
		for _, l := range info.LatencyStats.LatencyHistory {
			sum += l
		}
		info.LatencyStats.AverageLatency = sum / time.Duration(len(info.LatencyStats.LatencyHistory))

		// Update score based on latency
		if latency < 500*time.Millisecond {
			info.Score += ScoreGoodLatency
		} else {
			info.Score += ScorePoorLatency
		}

		pm.peers[id] = info
	}
}

func (pm *PeerManager) RecordBlockValidation(id peer.ID, isValid bool) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if info, exists := pm.peers[id]; exists {
		oldScore := info.Score // Store the old score for comparison

		if isValid {
			info.ValidBlocks++
			info.Score += ScoreValidBlock
		} else {
			info.InvalidBlocks++
			info.Score += ScoreInvalidBlock
		}

		// Ensure score changed appropriately
		if isValid && info.Score <= oldScore {
			info.Score = oldScore + ScoreValidBlock // Force score increase
		}

		// Clamp score between min and max
		if info.Score > peerScoreMax {
			info.Score = peerScoreMax
		}
		if info.Score < peerScoreMin {
			info.Score = peerScoreMin
			pm.blacklistPeer(id)
		}

		pm.peers[id] = info
	}
}

func (pm *PeerManager) RecordTransactionValidation(id peer.ID, isValid bool) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if info, exists := pm.peers[id]; exists {
		if isValid {
			info.ValidTxs++
			info.Score += ScoreValidTransaction
		} else {
			info.InvalidTxs++
			info.Score += ScoreInvalidTx
		}

		// Clamp score
		if info.Score > peerScoreMax {
			info.Score = peerScoreMax
		}
		if info.Score < peerScoreMin {
			info.Score = peerScoreMin
			pm.blacklistPeer(id)
		}

		pm.peers[id] = info
	}
}

// Protocol version negotiation
func (pm *PeerManager) NegotiateProtocolVersion(id peer.ID, peerVersion string) bool {
	if !pm.isVersionCompatible(peerVersion) {
		return false
	}

	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if info, exists := pm.peers[id]; exists {
		info.Version = peerVersion
		pm.peers[id] = info
		return true
	}
	return false
}

func (pm *PeerManager) isVersionCompatible(version string) bool {
	// Simple version check - can be enhanced for more complex version comparison
	return version >= MinProtocolVersion
}

// GetPeerInfo returns information about a peer
func (pm *PeerManager) GetPeerInfo(id peer.ID) (*PeerInfo, bool) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	info, exists := pm.peers[id]
	if !exists {
		return nil, false
	}
	return info, true
}

// GetPeerScore returns the current score of a peer
func (pm *PeerManager) GetPeerScore(id peer.ID) int {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	if info, exists := pm.peers[id]; exists {
		return info.Score
	}
	return 0
}

func (pm *PeerManager) UpdatePeerCapabilities(id peer.ID, capabilities []string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if info, exists := pm.peers[id]; exists {
		info.Capabilities = capabilities
		pm.peers[id] = info
	}
}

func (pm *PeerManager) InitiateStateVoting(proposedState string) (*VotingResult, error) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	result := &VotingResult{
		VoteCount: make(map[string]int),
	}

	totalVotes := 0
	// Count votes from all connected peers
	for _, peer := range pm.peers {
		if !peer.Connected {
			continue
		}
		
		if peer.LastVote != "" {
			result.VoteCount[peer.LastVote]++
			totalVotes++
		}
	}

	// Find winning state
	maxVotes := 0
	for state, votes := range result.VoteCount {
		if votes > maxVotes {
			maxVotes = votes
			result.WinningState = state
		}
	}

	// Check if quorum is reached (simple majority)
	if totalVotes > 0 && maxVotes > totalVotes/2 {
		result.QuorumReached = true
	}

	return result, nil
}

func (pm *PeerManager) RollbackToState(targetState string) error {
	currentHeight := pm.blockchain.GetHeight()
	for i := uint64(0); i < MaxRollbackBlocks; i++ {
		if currentHeight-i == 0 {
			return fmt.Errorf("reached genesis block without finding target state")
		}
		
		block := pm.blockchain.GetBlockByHeight(currentHeight - i)
		if block.StateRoot == targetState {
			return pm.blockchain.RollbackToHeight(currentHeight - i)
		}
	}
	return fmt.Errorf("target state not found within rollback limit")
}

func (pm *PeerManager) requestPeerStateVote(peerID peer.ID, stateRoot string) (string, error) {
	info, exists := pm.peers[peerID]
	if !exists || !info.Connected {
		return "", fmt.Errorf("peer not available")
	}
	
	// For now, return the peer's last vote or the proposed state
	if info.LastVote != "" {
		return info.LastVote, nil
	}
	return stateRoot, nil
}

func (pm *PeerManager) PropagateBlock(block *Block) error {
	// Convert Block to BlockMessage
	blockMsg := BlockMessage{
		BlockNumber:          block.BlockNumber,
		PreviousHash:         block.PreviousHash,
		Timestamp:           block.Timestamp,
		PatriciaRoot:        block.PatriciaRoot,
		TransactionsRoot:    block.Transactions.GenerateRootHash(),
		Nonce:               block.Nonce,
		Hash:                block.Hash,
		Difficulty:          block.Difficulty,
		CumulativeDifficulty: block.CumulativeDifficulty,
		StateRoot:           block.StateRoot,
	}

	// Serialize the BlockMessage
	msgBytes, err := json.Marshal(blockMsg)
	if err != nil {
		return fmt.Errorf("failed to serialize block message: %v", err)
	}

	// Rest of propagation logic remains the same
	doneChan := make(chan bool, 1)
	errChan := make(chan error, 1)
	propagated := make(map[peer.ID]bool)

	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	for id, info := range pm.peers {
		if !info.Connected {
			continue
		}

		go func(peerID peer.ID) {
			if err := pm.sendToPeer(peerID, msgBytes); err != nil {
				errChan <- fmt.Errorf("failed to send to peer %s: %v", peerID, err)
				return
			}
			propagated[peerID] = true
			if len(propagated) >= len(pm.peers)/2 {
				doneChan <- true
			}
		}(id)
	}

	select {
	case <-doneChan:
		return nil
	case err := <-errChan:
		return err
	case <-time.After(BlockPropagationTimeout):
		return fmt.Errorf("block propagation timed out")
	}
}

func (pm *PeerManager) VerifyChainState(height uint64) (*ChainStateVerification, error) {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	// Get local state
	localBlock := pm.blockchain.GetBlockByHeight(int(height))
	if localBlock == nil {
		return nil, fmt.Errorf("local block not found at height %d", height)
	}

	verification := &ChainStateVerification{
		Height:    height,
		StateRoot: localBlock.StateRoot,
		Responses: make(map[peer.ID]string),
	}

	// Request state verification from peers
	for id, info := range pm.peers {
		if !info.Connected {
			continue
		}

		stateRoot, err := pm.requestPeerStateVerification(id, height)
		if err != nil {
			log.Printf("Failed to verify state with peer %s: %v", id, err)
			continue
		}
		verification.Responses[id] = stateRoot
	}

	// Check consensus
	rootCounts := make(map[string]int)
	for _, root := range verification.Responses {
		rootCounts[root]++
	}

	// Find majority state root
	maxCount := 0
	for root, count := range rootCounts {
		if count > maxCount {
			maxCount = count
			verification.ConsensusRoot = root
		}
	}

	verification.HasConsensus = maxCount > len(pm.peers)/2
	verification.ConsensusReached = verification.ConsensusRoot == localBlock.StateRoot

	return verification, nil
}

// Helper method for sending messages to peers (to be implemented with actual p2p)
func (pm *PeerManager) sendToPeer(peerID peer.ID, data []byte) error {
	// Get peer info
	info, exists := pm.peers[peerID]
	if !exists || !info.Connected {
		return fmt.Errorf("peer %s not available", peerID)
	}

	// Create new stream to peer
	stream, err := pm.host.NewStream(context.Background(), peerID, "/blockchain/1.0.0")
	if err != nil {
		return fmt.Errorf("failed to create stream to peer %s: %v", peerID, err)
	}
	defer stream.Close()

	// Write data length prefix
	length := uint32(len(data))
	if err := binary.Write(stream, binary.BigEndian, length); err != nil {
		return fmt.Errorf("failed to write length prefix: %v", err)
	}

	// Write actual data
	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("failed to write data: %v", err)
	}

	return nil
}

func (pm *PeerManager) requestPeerStateVerification(peerID peer.ID, height uint64) (string, error) {
	msg := NewMessage(MsgStateVerify, height)
	msgBytes, err := msg.ToJSON()
	if err != nil {
		return "", fmt.Errorf("failed to serialize message: %v", err)
	}

	// Send request
	if err := pm.sendToPeer(peerID, msgBytes); err != nil {
		return "", fmt.Errorf("failed to send request: %v", err)
	}

	// Create response stream
	stream, err := pm.host.NewStream(context.Background(), peerID, "/blockchain/1.0.0/state")
	if err != nil {
		return "", fmt.Errorf("failed to create response stream: %v", err)
	}
	defer stream.Close()

	// Read response length
	var responseLength uint32
	if err := binary.Read(stream, binary.BigEndian, &responseLength); err != nil {
		return "", fmt.Errorf("failed to read response length: %v", err)
	}

	// Read response data
	responseData := make([]byte, responseLength)
	if _, err := io.ReadFull(stream, responseData); err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}

	// Parse response
	var response struct {
		StateRoot string `json:"state_root"`
	}
	if err := json.Unmarshal(responseData, &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %v", err)
	}

	return response.StateRoot, nil
}
