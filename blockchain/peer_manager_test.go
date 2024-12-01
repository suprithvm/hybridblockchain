package blockchain

import (
	"encoding/json"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"context"
)

// Add after the imports
type StateVerificationResponse struct {
	Success   bool   `json:"success"`
	StateRoot string `json:"state_root"`
}

func createTestHost(t *testing.T) host.Host {
	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
		libp2p.NoSecurity,
	)
	assert.NoError(t, err)
	return h
}

func TestPeerReputation(t *testing.T) {
	testHost := createTestHost(t)
	defer testHost.Close()
	
	t.Run("Peer Scoring", func(t *testing.T) {
		pm := NewPeerManager(testHost)
		peerID := peer.ID("test-peer-1")
		pm.AddPeer(peerID)

		// Test latency scoring with shorter duration
		latency := 200 * time.Millisecond
		pm.UpdatePeerLatency(peerID, latency)

		// Verify score update
		info, exists := pm.GetPeerInfo(peerID)
		assert.True(t, exists, "Peer should exist")
		assert.Greater(t, info.Score, 0, "Good latency should increase score")
		assert.Equal(t, latency, info.LatencyStats.LastLatency)

		// Test block validation scoring
		pm.RecordBlockValidation(peerID, true)
		scoreAfterValid := pm.GetPeerScore(peerID)
		log.Printf("Score after valid block: %d", scoreAfterValid)

		pm.RecordBlockValidation(peerID, false)
		scoreAfterInvalid := pm.GetPeerScore(peerID)
		assert.Less(t, scoreAfterInvalid, scoreAfterValid, "Invalid block should decrease score")

		// Test transaction validation
		pm.RecordTransactionValidation(peerID, true)
		info, _ = pm.GetPeerInfo(peerID)
		assert.Greater(t, info.ValidTxs, 0, "Should track valid transactions")
	})

	t.Run("Protocol Version", func(t *testing.T) {
		pm := NewPeerManager(testHost)
		peerID := peer.ID("test-peer-2")
		pm.AddPeer(peerID)

		// Test compatible version
		compatible := pm.NegotiateProtocolVersion(peerID, "1.0.0")
		assert.True(t, compatible, "Should accept compatible version")

		info, _ := pm.GetPeerInfo(peerID)
		assert.Equal(t, "1.0.0", info.Version)

		// Test incompatible version
		incompatible := pm.NegotiateProtocolVersion(peerID, "0.9.0")
		assert.False(t, incompatible, "Should reject incompatible version")
	})

	t.Run("Capability Exchange", func(t *testing.T) {
		pm := NewPeerManager(testHost)
		peerID := peer.ID("test-peer-3")
		pm.AddPeer(peerID)

		capabilities := []string{"sync", "validator", "relay"}
		pm.UpdatePeerCapabilities(peerID, capabilities)

		info, _ := pm.GetPeerInfo(peerID)
		assert.ElementsMatch(t, capabilities, info.Capabilities)
	})
}

func TestStateConflictResolution(t *testing.T) {
	testHost := createTestHost(t)
	defer testHost.Close()

	t.Run("State Voting", func(t *testing.T) {
		pm := NewPeerManager(testHost)
		
		// Add test peers with different states
		states := []string{
			"state1", "state1", "state1", // Majority state
			"state2", "state2",           // Minority state
		}
		
		// Initialize peers map if not already initialized
		if pm.peers == nil {
			pm.peers = make(map[peer.ID]*PeerInfo)
		}
		
		for i, state := range states {
			peerID := peer.ID(fmt.Sprintf("test-peer-%d", i))
			// Initialize peer info properly
			pm.peers[peerID] = &PeerInfo{
				ID:            peerID,
				Connected:     true,
				LastVote:      state,
				VoteTimestamp: time.Now().Unix(),
				Score:         100,
			}
		}
		
		// Test voting with timeout
		votingDone := make(chan bool)
		go func() {
			result, err := pm.InitiateStateVoting("state1")
			assert.NoError(t, err)
			assert.True(t, result.QuorumReached)
			assert.Equal(t, "state1", result.WinningState)
			assert.Equal(t, 3, result.VoteCount["state1"])
			assert.Equal(t, 2, result.VoteCount["state2"])
			votingDone <- true
		}()
		
		// Add timeout to prevent test from hanging
		select {
		case <-votingDone:
			// Test completed successfully
		case <-time.After(5 * time.Second):
			t.Fatal("State voting test timed out")
		}
	})

	t.Run("State Rollback", func(t *testing.T) {
		pm := NewPeerManager(testHost)
		bc := InitialiseBlockchain()
		pm.blockchain = bc
		
		// Create test chain with different states
		lastBlock := bc.GetLatestBlock()
		
		for i := 0; i < 10; i++ {
			block := NewBlock(lastBlock, NewMempool(), make(map[string]UTXO), 1, "test-validator")
			block.StateRoot = fmt.Sprintf("state-%d", i) // Set known state
			bc.AddBlockWithoutValidation(&block)
			
			lastBlock = block
		}
		
		// Test rollback
		targetState := bc.GetBlockByHeight(5).StateRoot
		err := pm.RollbackToState(targetState)
		assert.NoError(t, err)
		assert.Equal(t, uint64(5), bc.GetHeight())
		assert.Equal(t, targetState, bc.GetLatestBlock().StateRoot)
	})
}

func TestBlockPropagation(t *testing.T) {
	testHost := createTestHost(t)
	defer testHost.Close()

	t.Run("Block Propagation", func(t *testing.T) {
		pm := NewPeerManager(testHost)
		bc := InitialiseBlockchain()
		pm.blockchain = bc

		// Create and connect test peers
		testPeers := make([]host.Host, 5)
		for i := 0; i < 5; i++ {
			peer := createTestHost(t)
			testPeers[i] = peer
			defer peer.Close()

			// Connect peers with retry
			err := retry(3, 100*time.Millisecond, func() error {
				return testHost.Connect(context.Background(), peer.Peerstore().PeerInfo(peer.ID()))
			})
			assert.NoError(t, err)

			// Add stream handler for block propagation
			peer.SetStreamHandler("/blocks/1.0.0", func(s network.Stream) {
				var block Block
				err := json.NewDecoder(s).Decode(&block)
				assert.NoError(t, err)
				s.Close()
			})

			// Add peer to manager and mark as connected
			pm.AddPeer(peer.ID())
			pm.peers[peer.ID()].Connected = true
		}

		// Create and propagate test block
		block := NewBlock(bc.GetLatestBlock(), NewMempool(), make(map[string]UTXO), 1, "test-validator")
		err := pm.PropagateBlock(&block)
		assert.NoError(t, err, "Block propagation should succeed")

		time.Sleep(100 * time.Millisecond)
	})

	t.Run("Chain State Verification", func(t *testing.T) {
		pm := NewPeerManager(testHost)
		bc := InitialiseBlockchain()
		pm.blockchain = bc

		// Create and connect test peers
		testPeers := make([]host.Host, 5)
		for i := 0; i < 5; i++ {
			peer := createTestHost(t)
			testPeers[i] = peer
			defer peer.Close()

			// Connect peers with retry
			err := retry(3, 100*time.Millisecond, func() error {
				return testHost.Connect(context.Background(), peer.Peerstore().PeerInfo(peer.ID()))
			})
			assert.NoError(t, err)

			// Add stream handler for state verification
			peer.SetStreamHandler("/state/verify/1.0.0", func(s network.Stream) {
				response := StateVerificationResponse{
					Success:   true,
					StateRoot: bc.GetLatestBlock().StateRoot,
				}
				json.NewEncoder(s).Encode(response)
				s.Close()
			})

			// Add peer to manager and mark as connected
			pm.AddPeer(peer.ID())
			pm.peers[peer.ID()].Connected = true
		}

		// Create test chain
		for i := 0; i < 5; i++ {
			lastBlock := bc.GetLatestBlock()
			mp := NewMempool()
			block := NewBlock(lastBlock, mp, make(map[string]UTXO), 1, "test-validator")
			bc.AddBlockWithoutValidation(&block)
		}

		// Test state verification with retry
		var verification *ChainStateVerification
		err := retry(3, 100*time.Millisecond, func() error {
			var err error
			verification, err = pm.VerifyChainState(3)
			return err
		})
		assert.NoError(t, err)
		assert.True(t, verification.HasConsensus, "Should reach consensus")

		time.Sleep(100 * time.Millisecond)
	})
}

// Helper function to retry operations
func retry(attempts int, sleep time.Duration, f func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		err = f()
		if err == nil {
			return nil
		}
		time.Sleep(sleep)
	}
	return err
}
