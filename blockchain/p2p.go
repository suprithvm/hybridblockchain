package blockchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"blockchain-core/blockchain/gas"

	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	ma "github.com/multiformats/go-multiaddr"
)

// Protocol IDs
const (
	BlockProtocolID         = "/blockchain/blocks/1.0.0"
	TransactionProtocolID   = "/blockchain/txs/1.0.0"
	HeartbeatProtocolID     = "/blockchain/heartbeat/1.0.0"
	ReconnectInterval       = 10 * time.Second
	MaxReconnectAttempts    = 5
	ConnectionRetryInterval = 30 * time.Second
	MaxConnectionRetries    = 10
	WalletSetupTimeout      = 5 * time.Minute
	ValidatorSelectionTopic = "/blockchain/validator/selection/1.0.0"
	ValidatorVoteTopic      = "/blockchain/validator/vote/1.0.0"
	ValidatorProtocolID     = "/blockchain/validator/1.0.0"
	ValidatorHeartbeatTopic = "/blockchain/validator/heartbeat/1.0.0"
	ValidatorTimeoutTopic   = "/blockchain/validator/timeout/1.0.0"
	ValidatorSetUpdateTopic = "/blockchain/validator/set/1.0.0"
	// Protocol paths
	BlockchainSyncProtocol = "/blockchain/sync/1.0.0"
	ChainStateProtocol     = "/blockchain/state/1.0.0"
	BlockProtocol          = "/blockchain/block/1.0.0"
	SyncProtocol           = "/blockchain/sync/1.0.0"
	StakeSyncProtocol      = "/stake/sync/1.0.0"
)

// Add message type constants at the top of the file
const (
	MessageTypeValidatorRequest  = "validator_request"
	MessageTypeValidatorResponse = "validator_response"
	ProtocolID                   = "/blockchain/1.0.0"
)

// NodeOptions contains options for creating a node
type NodeOptions struct {
	ListenAddr     string
	BootstrapNodes []string
	NetworkID      string
	NodeID         string
	WalletAddress  string
}

// blankValidator is a no-op validator for DHT records
type blankValidator struct{}

func (v blankValidator) Validate(_ string, _ []byte) error        { return nil }
func (v blankValidator) Select(_ string, _ [][]byte) (int, error) { return 0, nil }

// SyncRequest represents a request to sync blockchain state
type SyncRequest struct {
	Height      uint64 `json:"height"`
	TotalBlocks uint64 `json:"total_blocks"` // Total number of blocks to be sent
}

// SyncResponse represents a response to a sync request
type SyncResponse struct {
	Height       uint64 `json:"height"`
	HasChain     bool   `json:"has_chain"`
	TotalBlocks  uint64 `json:"total_blocks"`  // Total number of blocks available
	SyncComplete bool   `json:"sync_complete"` // Flag indicating sync completion
}

// Node represents a blockchain network node
type Node struct {
	Host                   host.Host
	DHT                    *dht.IpfsDHT
	PeerManager            *PeerManager
	Blockchain             *Blockchain
	Mempool                *Mempool
	UTXOSet                *UTXOPool
	StakePool              *StakePool
	config                 *NetworkConfig
	ctx                    context.Context
	cancel                 context.CancelFunc
	keepAliveCtx           context.Context
	keepAliveCancel        context.CancelFunc
	UTXOPool               *UTXOPool
	isSyncing              bool
	syncMu                 sync.RWMutex
	gasModel               *gas.GasModel
	accountManager         *AccountManager
	P2PPort                int
	RPCPort                int
	NetworkPath            string
	ChainID                uint64
	NetworkID              string
	wallet                 *Wallet
	isRunning              bool
	runningMu              sync.RWMutex
	knownPeers             map[peer.ID]bool
	PubSub                 *pubsub.PubSub
	validatorProtocol      *ValidatorProtocol
	bootstrapNodes         map[peer.ID]bool
	wg                     sync.WaitGroup
	isInitializedValidator bool
	mu                     sync.RWMutex
	lastSyncTime           time.Time
	syncComplete           bool
	lastPeerDiscovery      time.Time
}

// publishMessage publishes a message to a specific topic using pubsub
func (n *Node) publishMessage(topic string, message interface{}) error {
	if n.PubSub == nil {
		return fmt.Errorf("pubsub not initialized")
	}

	// Join the topic
	t, err := n.PubSub.Join(topic)
	if err != nil {
		return fmt.Errorf("failed to join topic %s: %v", topic, err)
	}

	// Marshal the message
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	// Publish the message
	err = t.Publish(n.ctx, data)
	if err != nil {
		return fmt.Errorf("failed to publish message: %v", err)
	}

	return nil
}

// Add getter method for gas model
func (n *Node) GetGasModel() *gas.GasModel {
	return n.gasModel
}

// NewNode creates a new blockchain node
func NewNode(config *NetworkConfig) (*Node, error) {
	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize host
	host, err := createHost(config)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create host: %v", err)
	}

	// Initialize DHT with server mode
	log.Printf("🔄 Initializing DHT in server mode...")
	kadDHT, err := dht.New(ctx, host, dht.Mode(dht.ModeServer))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %v", err)
	}

	log.Printf("✅ DHT initialized successfully")
	log.Printf("📊 DHT Status:")
	log.Printf("• Routing Table Size: %d", kadDHT.RoutingTable().Size())
	log.Printf("• Connected Peers: %d", len(host.Network().Peers()))

	// Create keep-alive context
	keepAliveCtx, keepAliveCancel := context.WithCancel(ctx)

	// Initialize peer manager first
	peerManager := NewPeerManager(host)

	// Create node instance
	node := &Node{
		Host:              host,
		DHT:               kadDHT,
		PeerManager:       peerManager,
		Blockchain:        config.Blockchain,
		Mempool:           NewMempool(nil),
		UTXOSet:           NewUTXOPool(config.Blockchain.db),
		StakePool:         NewStakePool(config.Blockchain),
		config:            config,
		ctx:               ctx,
		cancel:            cancel,
		keepAliveCtx:      keepAliveCtx,
		keepAliveCancel:   keepAliveCancel,
		UTXOPool:          NewUTXOPool(config.Blockchain.db),
		isSyncing:         false,
		syncMu:            sync.RWMutex{},
		gasModel:          gas.NewGasModel(1000000, 100000),
		accountManager:    NewAccountManager(config.Blockchain.db),
		P2PPort:           config.P2PPort,
		RPCPort:           config.RPCPort,
		NetworkPath:       config.NetworkPath,
		ChainID:           config.ChainID,
		NetworkID:         config.NetworkID,
		wallet:            config.Wallet,
		isRunning:         false,
		runningMu:         sync.RWMutex{},
		knownPeers:        make(map[peer.ID]bool),
		PubSub:            nil,
		validatorProtocol: nil,
		bootstrapNodes:    make(map[peer.ID]bool),
		wg:                sync.WaitGroup{},
	}

	// Set validator mode if specified
	if config.ValidatorMode {
		node.isInitializedValidator = true
		log.Printf("🔐 Node initialized in validator mode")
	}

	// Initialize validator protocol if in validator mode
	if config.ValidatorMode {
		node.validatorProtocol = NewValidatorProtocol(node)
	}

	// Initialize pubsub with validator-specific options
	pubsubOpts := []pubsub.Option{
		pubsub.WithMessageSigning(true),
		pubsub.WithStrictSignatureVerification(true),
	}

	if config.ValidatorMode {
		pubsubOpts = append(pubsubOpts,
			pubsub.WithPeerScore(
				&pubsub.PeerScoreParams{
					AppSpecificScore: func(p peer.ID) float64 {
						if node.PeerManager.IsValidatorPeer(p) {
							return 100.0
						}
						return 0
					},
					AppSpecificWeight: 1,
					DecayInterval:     time.Hour,
					DecayToZero:       0.01,
				},
				&pubsub.PeerScoreThresholds{
					GossipThreshold:             -100,
					PublishThreshold:            -500,
					GraylistThreshold:           -1000,
					AcceptPXThreshold:           100,
					OpportunisticGraftThreshold: 5,
				}))
	}

	pubsub, err := pubsub.NewGossipSub(ctx, host, pubsubOpts...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create pubsub: %v", err)
	}
	node.PubSub = pubsub

	// Register protocol handlers
	node.registerProtocolHandlers()

	// Start keep-alive routine
	go node.startKeepAlive()

	// Start connection maintenance
	go node.maintainConnections()

	// Start heartbeat
	go node.StartHeartbeat()

	return node, nil
}

// Close gracefully shuts down the node
func (n *Node) Close() error {
	n.cancel()
	if err := n.Host.Close(); err != nil {
		return err
	}
	return n.DHT.Close()
}

// DiscoverPeers finds and connects to peers in the network
func (n *Node) DiscoverPeers() error {
	// Skip peer discovery if this is an initialized validator
	if n.isInitializedValidator {
		log.Printf("🔐 Skipping peer discovery for initialized validator")
		return nil
	}

	log.Printf("🔍 Starting peer discovery...")
	n.mu.RLock()
	currentPeers := len(n.Host.Network().Peers())
	n.mu.RUnlock()
	log.Printf("📊 Current connections: %d peers", currentPeers)

	// If we have no peers, try to reconnect to bootstrap nodes
	if currentPeers == 0 {
		log.Printf("⚠️ No peers found, attempting to reconnect to bootstrap nodes")
		if err := n.ConnectToBootstrapNodes(n.ctx); err != nil {
			log.Printf("⚠️ Failed to reconnect to bootstrap nodes: %v", err)
		}
		// Wait a bit for connections to establish
		time.Sleep(5 * time.Second)
	}

	n.mu.RLock()
	finalPeers := len(n.Host.Network().Peers())
	n.mu.RUnlock()
	log.Printf("📊 Final peer count: %d", finalPeers)

	if finalPeers > 0 {
		log.Printf("✅ Successfully connected to %d peers", finalPeers)
		for _, p := range n.Host.Network().Peers() {
			log.Printf("   • Peer: %s", p.String())
		}
	} else {
		log.Printf("⚠️ No peers connected after discovery")
	}

	return nil
}

// findAndConnectPeers finds and connects to new peers
func (n *Node) findAndConnectPeers() {
	ctx, cancel := context.WithTimeout(n.ctx, 20*time.Second)
	defer cancel()

	// Start DHT if not already bootstrapped
	if err := n.DHT.Bootstrap(ctx); err != nil {
		log.Printf("Error bootstrapping DHT: %v", err)
		return
	}

	// Get peers from routing table
	peers := n.DHT.RoutingTable().ListPeers()
	for _, peerID := range peers {
		if peerID == n.Host.ID() || n.PeerManager.IsBlacklisted(peerID) {
			continue
		}

		// Get peer info from peerstore
		peerInfo := n.Host.Peerstore().PeerInfo(peerID)
		if err := n.Host.Connect(ctx, peerInfo); err != nil {
			log.Printf("Failed to connect to peer %s: %v", peerID, err)
			continue
		}

		n.PeerManager.AddPeer(peerID)
		if peer, exists := n.PeerManager.peers[peerID]; exists {
			peer.LastSeen = time.Now()
		}
		log.Printf("Connected to peer: %s", peerID)
	}
}

// handleStreamError handles stream errors and updates peer scores
func (n *Node) handleStreamError(s network.Stream, err error) {
	peerID := s.Conn().RemotePeer()
	log.Printf("Error handling stream from peer %s: %v", peerID, err)
	n.PeerManager.UpdatePeerScore(peerID, -10)
}

// handleBlockStream processes incoming block streams
func (n *Node) handleBlockStream(s network.Stream) {
	defer s.Close()

	peerID := s.Conn().RemotePeer()

	// Read the block data
	buf := make([]byte, 1024*1024) // 1MB buffer
	_, err := io.ReadFull(s, buf)
	if err != nil {
		n.handleStreamError(s, err)
		return
	}

	// Process the block (implement your block processing logic here)
	// ...

	// Update peer score positively for good behavior
	n.PeerManager.UpdatePeerScore(peerID, 5)
}

// handleTransactionStream processes incoming transaction streams
func (n *Node) handleTransactionStream(s network.Stream) {
	defer s.Close()

	peerID := s.Conn().RemotePeer()

	// Read the transaction message
	var msg Message
	if err := json.NewDecoder(s).Decode(&msg); err != nil {
		n.handleStreamError(s, err)
		return
	}

	// Process based on message type
	switch msg.Type {
	case "NewTransaction":
		var tx Transaction
		if err := json.Unmarshal(msg.Payload.([]byte), &tx); err != nil {
			n.PeerManager.UpdatePeerScore(peerID, -1)
			return
		}

		// Validate transaction using Node's UTXOSet
		if !n.validateTransaction(&tx) {
			n.PeerManager.UpdatePeerScore(peerID, -2)
			return
		}

		// Add to mempool if valid, using Node's Mempool
		if !n.Mempool.AddTransaction(tx, n.UTXOSet.utxos) {
			log.Printf("Failed to add transaction to mempool: %v", tx.TransactionID)
			return
		}

		// Broadcast to other peers
		n.BroadcastTransaction(&tx, []peer.ID{peerID})

		// Update peer score positively
		n.PeerManager.UpdatePeerScore(peerID, 1)

	case "GetTransactions":
		// Send mempool transactions
		transactions := n.Mempool.GetTransactions()
		response := NewMessage("Transactions", transactions)
		if err := json.NewEncoder(s).Encode(response); err != nil {
			log.Printf("Failed to send transactions: %v", err)
		}
	}
}

// validateTransaction performs comprehensive transaction validation
func (n *Node) validateTransaction(tx *Transaction) bool {
	// Check if transaction already exists in mempool
	for _, memTx := range n.Mempool.GetTransactions() {
		if memTx.TransactionID == tx.TransactionID {
			return false
		}
	}

	// Verify transaction signature
	if !tx.ValidateSignatures(&Wallet{}, nil) {
		log.Printf("Transaction signature verification failed")
		return false
	}

	// Validate using UTXOSet
	return n.UTXOSet.ValidateTransaction(tx)
}

// BroadcastTransaction broadcasts a transaction to all connected peers except those in excludePeers
func (n *Node) BroadcastTransaction(tx *Transaction, excludePeers []peer.ID) error {
	peers := n.PeerManager.GetConnectedPeers()
	for _, peerID := range peers {
		// Skip excluded peers and self
		if peerID == n.Host.ID() || contains(excludePeers, peerID) {
			continue
		}

		stream, err := n.Host.NewStream(n.ctx, peerID, TransactionProtocolID)
		if err != nil {
			log.Printf("Failed to open stream to peer %s: %v", peerID, err)
			continue
		}

		// Create transaction message
		msg := Message{
			Type:    "NEW_TRANSACTION",
			Payload: tx,
		}

		// Encode and send transaction
		if err := json.NewEncoder(stream).Encode(msg); err != nil {
			stream.Close()
			log.Printf("Failed to send transaction to peer %s: %v", peerID, err)
			continue
		}
		stream.Close()
	}
	return nil
}

// handleHeartbeatStream processes incoming heartbeat streams
func (n *Node) handleHeartbeatStream(s network.Stream) {
	defer s.Close()

	peerID := s.Conn().RemotePeer()

	// Read the heartbeat data
	buf := make([]byte, 1024)
	_, err := io.ReadFull(s, buf)
	if err != nil {
		n.handleStreamError(s, err)
		return
	}

	// Update peer info
	if peer, exists := n.PeerManager.peers[peerID]; exists {
		peer.LastSeen = time.Now()
	}
}

// registerProtocolHandlers sets up all protocol handlers for the node
func (n *Node) registerProtocolHandlers() {
	// Register blockchain protocol handlers
	n.SetStreamHandler(BlockProtocolID, n.handleBlockStream)
	n.SetStreamHandler(TransactionProtocolID, n.handleTransactionStream)
	n.SetStreamHandler(HeartbeatProtocolID, n.handleHeartbeatStream)
	n.SetStreamHandler(SyncProtocol, n.handleSyncRequest)
	n.SetStreamHandler(ValidatorProtocolID, n.handleValidatorStream)
	n.SetStreamHandler("/stake/sync/1.0.0", n.handleStakeSync)
	n.SetStreamHandler("/mempool/sync/1.0.0", n.handleMempoolSync)
	// Initialize validator protocol if node is a validator
	if n.validatorProtocol != nil {
		n.validatorProtocol.Start()
	}

	log.Printf("✅ Protocol handlers registered successfully")

}

// handleValidatorStream processes incoming validator-related messages
func (n *Node) handleValidatorStream(s network.Stream) {
	peerID := s.Conn().RemotePeer()

	// Read message
	buf := make([]byte, 1024)
	_, err := io.ReadFull(s, buf)
	if err != nil {
		n.handleStreamError(s, fmt.Errorf("failed to read validator message: %v", err))
		return
	}

	// Parse message
	var msg ValidatorMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		n.handleStreamError(s, fmt.Errorf("failed to parse validator message: %v", err))
		return
	}

	// Process message based on type
	switch msg.Type {
	case "heartbeat":
		var heartbeat ValidatorHeartbeatMessage
		if err := json.Unmarshal([]byte(msg.Payload), &heartbeat); err != nil {
			n.handleStreamError(s, fmt.Errorf("failed to parse validator heartbeat: %v", err))
			return
		}
		n.validatorProtocol.HandleHeartbeat(heartbeat.ValidatorAddress)
	case "selection":
		var selection ValidatorSelectionMessage
		if err := json.Unmarshal([]byte(msg.Payload), &selection); err != nil {
			n.handleStreamError(s, fmt.Errorf("failed to parse validator selection: %v", err))
			return
		}
		// Handle selection message
	case "vote":
		var vote ValidatorVoteMessage
		if err := json.Unmarshal([]byte(msg.Payload), &vote); err != nil {
			n.handleStreamError(s, fmt.Errorf("failed to parse validator vote: %v", err))
			return
		}
		// Handle vote message
	default:
		n.handleStreamError(s, fmt.Errorf("unknown validator message type: %s", msg.Type))
	}

	// Update peer info
	if peer, exists := n.PeerManager.peers[peerID]; exists {
		peer.LastSeen = time.Now()
	}
}

// startKeepAlive starts periodic heartbeat to maintain connections
func (n *Node) startKeepAlive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			for _, peer := range n.PeerManager.GetConnectedPeers() {
				if err := n.SendHeartbeat(peer); err != nil {
					log.Printf("⚠️ Failed to send heartbeat to peer %s: %v", peer, err)
				}
			}
		}
	}
}

// maintainConnections ensures minimum peer connections
func (n *Node) maintainConnections() {
	ticker := time.NewTicker(5 * time.Minute) // Increased from 1 minute to 5 minutes
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			peers := n.Host.Network().Peers()
			if len(peers) == 0 {
				log.Printf("🔄 No peers connected, attempting to reconnect...")
				n.ConnectToBootstrapNodes(n.ctx)
			} else {
				// Only discover new peers if we have less than 3 peers
				// and haven't discovered peers in the last 15 minutes
				if len(peers) < 3 && time.Since(n.lastPeerDiscovery) > 15*time.Minute {
					if err := n.DiscoverPeers(); err != nil {
						log.Printf("⚠️ Peer discovery failed: %v", err)
					}
					n.lastPeerDiscovery = time.Now()
				}
			}
		}
	}
}

// BroadcastBlock broadcasts a block to all connected peers
func (n *Node) BroadcastBlock(block Block) error {
	peers := n.PeerManager.GetConnectedPeers()
	for _, peerID := range peers {
		if peerID == n.Host.ID() {
			continue
		}

		stream, err := n.Host.NewStream(n.ctx, peerID, BlockProtocolID)
		if err != nil {
			log.Printf("Failed to open stream to peer %s: %v", peerID, err)
			continue
		}

		// Serialize block data
		blockData := struct {
			Header    *BlockHeader
			Body      *BlockBody
			Hash      string
			Timestamp int64
		}{
			Header:    block.Header,
			Body:      block.Body,
			Hash:      block.Hash(),
			Timestamp: time.Now().Unix(),
		}

		// Encode and send block data
		if err := json.NewEncoder(stream).Encode(blockData); err != nil {
			stream.Close()
			log.Printf("Failed to send block to peer %s: %v", peerID, err)
			continue
		}
		stream.Close()
	}
	return nil
}

// SendHeartbeat sends a heartbeat to a specific peer
func (n *Node) SendHeartbeat(peerID peer.ID) error {
	stream, err := n.Host.NewStream(n.ctx, peerID, HeartbeatProtocolID)
	if err != nil {
		return fmt.Errorf("failed to open heartbeat stream: %w", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte("ping")); err != nil {
		return fmt.Errorf("failed to send heartbeat: %w", err)
	}
	return nil
}

// StartHeartbeat starts the heartbeat routine
func (n *Node) StartHeartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			peers := n.PeerManager.GetConnectedPeers()
			for _, peerID := range peers {
				if err := n.SendHeartbeat(peerID); err != nil {
					log.Printf("Failed to send heartbeat to peer %s: %v", peerID, err)
					n.PeerManager.RemovePeer(peerID)
				}
			}
		}
	}
}

// bootstrapDHT bootstraps the DHT and connects to initial peers
func (n *Node) bootstrapDHT(ctx context.Context) error {
	log.Printf("🔄 Starting DHT bootstrap process...")
	log.Printf("📊 Initial DHT Status:")
	log.Printf("• Routing Table Size: %d", n.DHT.RoutingTable().Size())
	log.Printf("• Connected Peers: %d", len(n.Host.Network().Peers()))
	log.Printf("• Bootstrap Peers: %d", len(n.config.BootstrapNodes))

	// Bootstrap the DHT
	log.Printf("🔍 Attempting to bootstrap DHT...")
	if err := n.DHT.Bootstrap(ctx); err != nil {
		log.Printf("❌ DHT bootstrap failed: %v", err)
		return fmt.Errorf("failed to bootstrap DHT: %v", err)
	}

	// Wait for routing table to populate
	log.Printf("⏳ Waiting for routing table to populate...")
	ticker := time.NewTicker(100 * time.Millisecond)
	timeout := time.After(10 * time.Second)

	for {
		select {
		case <-ticker.C:
			rtSize := n.DHT.RoutingTable().Size()
			connCount := len(n.Host.Network().Peers())
			log.Printf("📊 DHT Status Update:")
			log.Printf("• Routing Table Size: %d", rtSize)
			log.Printf("• Connected Peers: %d", connCount)

			if rtSize > 0 {
				log.Printf("✅ DHT bootstrap completed successfully")
				ticker.Stop()
				return nil
			}
		case <-timeout:
			log.Printf("⚠️ DHT bootstrap timeout - continuing with current state")
			ticker.Stop()
			return nil
		case <-ctx.Done():
			log.Printf("⚠️ DHT bootstrap cancelled")
			ticker.Stop()
			return ctx.Err()
		}
	}
}

// ConnectToPeer connects to a peer using multiaddr
func (n *Node) ConnectToPeer(addr string) error {
	maddr, err := ma.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}

	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("invalid peer address: %w", err)
	}

	if err := n.Host.Connect(n.ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	n.PeerManager.AddPeer(peerInfo.ID)
	return nil
}

// BroadcastMessage sends a message to all connected peers
func (n *Node) BroadcastMessage(msg string) error {
	peers := n.PeerManager.GetConnectedPeers()
	for _, peerID := range peers {
		if peerID == n.Host.ID() {
			continue
		}

		stream, err := n.Host.NewStream(n.ctx, peerID, protocol.ID("/blockchain/message/1.0.0"))
		if err != nil {
			log.Printf("Failed to open stream to peer %s: %v", peerID, err)
			continue
		}

		_, err = stream.Write([]byte(msg))
		if err != nil {
			stream.Close()
			log.Printf("Failed to send message to peer %s: %v", peerID, err)
			continue
		}
		stream.Close()
	}
	return nil
}

// ConnectToBootstrapNodes connects to the configured bootstrap nodes
func (n *Node) ConnectToBootstrapNodes(ctx context.Context) error {
	if len(n.config.BootstrapNodes) == 0 {
		return fmt.Errorf("no bootstrap nodes configured")
	}

	log.Printf("🔌 Attempting to connect to %d bootstrap nodes", len(n.config.BootstrapNodes))

	var lastErr error
	connected := false
	maxRetries := 5
	retryDelay := time.Second * 5

	for _, addr := range n.config.BootstrapNodes {
		for attempt := 1; attempt <= maxRetries; attempt++ {
			// Parse the multiaddr
			multiaddr, err := ma.NewMultiaddr(addr)
			if err != nil {
				log.Printf("⚠️ Invalid bootstrap node address %s: %v", addr, err)
				lastErr = err
				continue
			}

			// Extract peer info
			peerInfo, err := peer.AddrInfoFromP2pAddr(multiaddr)
			if err != nil {
				log.Printf("⚠️ Failed to parse peer info from %s: %v", addr, err)
				lastErr = err
				continue
			}

			// Skip if already connected
			if n.Host.Network().Connectedness(peerInfo.ID) == network.Connected {
				log.Printf("✅ Already connected to bootstrap node %s", peerInfo.ID)
				n.bootstrapNodes[peerInfo.ID] = true
				connected = true
				continue
			}

			log.Printf("📡 Attempt %d/%d: Connecting to bootstrap node %s", attempt, maxRetries, peerInfo.ID)

			// Try to connect with timeout
			ctxTimeout, cancel := context.WithTimeout(ctx, time.Second*10)
			err = n.Host.Connect(ctxTimeout, *peerInfo)
			cancel()

			if err != nil {
				log.Printf("⚠️ Attempt %d/%d: Failed to connect to bootstrap node %s: %v",
					attempt, maxRetries, peerInfo.ID, err)
				lastErr = err

				if attempt < maxRetries {
					log.Printf("⏳ Waiting %v before next attempt...", retryDelay)
					time.Sleep(retryDelay)
					continue
				}
				break
			}

			// Verify connection
			if n.Host.Network().Connectedness(peerInfo.ID) == network.Connected {
				log.Printf("✅ Successfully connected to bootstrap node %s", peerInfo.ID)
				n.bootstrapNodes[peerInfo.ID] = true
				connected = true
				break
			}
		}
	}

	if !connected {
		return fmt.Errorf("failed to connect to any bootstrap nodes after retries: %v", lastErr)
	}

	return nil
}

// BroadcastChain sends the blockchain to all connected peers
func (n *Node) BroadcastChain(blockchain *Blockchain) error {
	// Get current chain height
	height := blockchain.GetHeight()

	// Create chain data structure
	chainData := struct {
		Height    uint64
		Blocks    []Block
		Timestamp int64
		NetworkID string
	}{
		Height:    height,
		Blocks:    make([]Block, 0),
		Timestamp: time.Now().Unix(),
		NetworkID: n.config.NetworkID,
	}

	// Get blocks in batches to avoid memory issues
	batchSize := 100
	for i := uint64(0); i < height; i += uint64(batchSize) {
		end := i + uint64(batchSize)
		if end > height {
			end = height
		}

		blocks := make([]Block, 0, end-i)
		for j := i; j < end; j++ {
			if block := blockchain.GetBlockByHeight(int(j)); block != nil {
				blocks = append(blocks, *block)
			}
		}
		chainData.Blocks = blocks

		// Broadcast to peers
		peers := n.PeerManager.GetConnectedPeers()
		for _, peerID := range peers {
			if peerID == n.Host.ID() {
				continue
			}

			stream, err := n.Host.NewStream(n.ctx, peerID, protocol.ID("/blockchain/chain/1.0.0"))
			if err != nil {
				log.Printf("Failed to open stream to peer %s: %v", peerID, err)
				continue
			}

			if err := json.NewEncoder(stream).Encode(chainData); err != nil {
				stream.Close()
				log.Printf("Failed to send chain to peer %s: %v", peerID, err)
				continue
			}
			stream.Close()
		}
	}
	return nil
}

// RequestChain requests a blockchain segment from peers
func (n *Node) RequestChain(start, end int) ([]Block, error) {
	peers := n.PeerManager.GetConnectedPeers()
	if len(peers) == 0 {
		return nil, fmt.Errorf("no peers available")
	}

	// Create chain request
	request := struct {
		Start     int
		End       int
		NetworkID string
	}{
		Start:     start,
		End:       end,
		NetworkID: n.config.NetworkID,
	}

	for _, peerID := range peers {
		stream, err := n.Host.NewStream(n.ctx, peerID, protocol.ID("/blockchain/chain/request/1.0.0"))
		if err != nil {
			log.Printf("Failed to open stream to peer %s: %v", peerID, err)
			continue
		}
		defer stream.Close()

		// Send request
		if err := json.NewEncoder(stream).Encode(request); err != nil {
			log.Printf("Failed to send chain request to peer %s: %v", peerID, err)
			continue
		}

		// Read response
		var response struct {
			Blocks  []Block
			Success bool
			Error   string
		}

		if err := json.NewDecoder(stream).Decode(&response); err != nil {
			log.Printf("Failed to decode chain response from peer %s: %v", peerID, err)
			continue
		}

		if response.Success {
			return response.Blocks, nil
		}

		log.Printf("Peer %s returned error: %s", peerID, response.Error)
	}

	return nil, fmt.Errorf("failed to retrieve chain from any peer")
}

// ValidateAndUpdateChain validates and integrates a received chain
func (n *Node) ValidateAndUpdateChain(newChain []Block) bool {
	if len(newChain) == 0 {
		return false
	}

	// Validate chain continuity
	for i := 1; i < len(newChain); i++ {
		if newChain[i].Header.PreviousHash != newChain[i-1].Hash() {
			log.Printf("Chain discontinuity detected at block %d", i)
			return false
		}
	}

	// Get current blockchain state
	bc := n.Blockchain
	if bc == nil {
		log.Printf("Blockchain not initialized")
		return false
	}

	// Validate each block
	for i, block := range newChain {
		// Get previous block for validation
		var previousBlock Block
		if i == 0 {
			// First block in chain should connect to our current tip
			previousBlock = bc.GetLatestBlock()
		} else {
			previousBlock = newChain[i-1]
		}

		// Validate block using blockchain's validation function
		if !ValidateBlock(block, previousBlock, block.Header.ValidatedBy, bc.StakePool) {
			log.Printf("Invalid block detected at height %d", block.Header.BlockNumber)
			return false
		}
	}

	return true
}

// Additional protocol handlers
func (n *Node) registerAdditionalHandlers() {
	// Chain request handler
	n.Host.SetStreamHandler(protocol.ID("/blockchain/chain/request/1.0.0"), func(s network.Stream) {
		defer s.Close()

		// Read request
		var request struct {
			Start     int
			End       int
			NetworkID string
		}
		if err := json.NewDecoder(s).Decode(&request); err != nil {
			log.Printf("Failed to decode chain request: %v", err)
			return
		}

		// Validate network ID
		if request.NetworkID != n.config.NetworkID {
			sendError(s, "invalid network ID")
			return
		}

		// Get blockchain instance
		bc := n.Blockchain
		if bc == nil {
			sendError(s, "blockchain not initialized")
			return
		}

		// Validate request range
		currentHeight := bc.GetHeight()
		if request.Start < 0 || request.End > int(currentHeight) || request.Start > request.End {
			sendError(s, "invalid block range")
			return
		}

		// Get requested blocks
		blocks := make([]Block, 0, request.End-request.Start+1)
		for height := request.Start; height <= request.End; height++ {
			block := bc.GetBlockByHeight(height)
			if block == nil {
				sendError(s, fmt.Sprintf("block not found at height %d", height))
				return
			}
			blocks = append(blocks, *block)
		}

		// Send response
		response := struct {
			Blocks  []Block
			Success bool
			Error   string
		}{
			Blocks:  blocks,
			Success: true,
		}

		if err := json.NewEncoder(s).Encode(response); err != nil {
			log.Printf("Failed to send chain response: %v", err)
		}
	})

	// General message handler
	n.Host.SetStreamHandler(protocol.ID("/blockchain/message/1.0.0"), func(s network.Stream) {
		defer s.Close()

		buf := make([]byte, 1024)
		_, err := io.ReadFull(s, buf)
		if err != nil {
			log.Printf("Error reading message: %v", err)
			return
		}

		// Process the message
		log.Printf("Received message from peer %s", s.Conn().RemotePeer())
	})
}

// Helper function to send error response
func sendError(s network.Stream, errMsg string) {
	response := struct {
		Success bool
		Error   string
	}{
		Success: false,
		Error:   errMsg,
	}
	if err := json.NewEncoder(s).Encode(response); err != nil {
		log.Printf("Failed to send error response: %v", err)
	}
}

// setupBlockSyncProtocol sets up the block sync protocol handlers
func (n *Node) setupBlockSyncProtocol() {
	// Register sync protocol handler
	n.SetStreamHandler("/blockchain/sync/1.0.0", n.handleBlockSync)
	log.Printf("✅ Block sync protocol registered")
}

func (n *Node) handleForkResolution(s network.Stream) {
	var receivedChain []Block
	if err := json.NewDecoder(s).Decode(&receivedChain); err != nil {
		log.Printf("Error decoding fork chain: %v", err)
		return
	}

	// Validate received chain structure
	if !n.Blockchain.ValidateCandidateChain(receivedChain) {
		log.Printf("Received invalid chain during fork resolution")
		return
	}

	// Compare chains and resolve fork
	resolved := n.Blockchain.ResolveFork(receivedChain)

	// Send resolution result back to peer
	response := struct {
		Accepted bool
		Height   int
		Hash     string
	}{
		Accepted: resolved,
		Height:   len(n.Blockchain.Chain),
		Hash:     n.Blockchain.GetLatestBlock().hash,
	}

	if err := json.NewEncoder(s).Encode(response); err != nil {
		log.Printf("Error sending fork resolution response: %v", err)
	}

	// If fork was resolved, propagate new chain state
	if resolved {
		n.broadcastNewChainState()
	}
}

// Add method to broadcast new chain state after fork resolution
func (n *Node) broadcastNewChainState() {
	// Create chain state message
	chainState := struct {
		Height uint64
		Hash   string
	}{
		Height: n.Blockchain.GetLatestBlock().Header.BlockNumber,
		Hash:   n.Blockchain.GetLatestBlock().hash,
	}

	// Broadcast to all peers except the one that sent us the fork
	for _, peer := range n.Host.Network().Peers() {
		s, err := n.Host.NewStream(context.Background(), peer, "/chain/state/1.0.0")
		if err != nil {
			continue
		}
		defer s.Close()

		if err := json.NewEncoder(s).Encode(chainState); err != nil {
			log.Printf("Error broadcasting chain state to peer %s: %v", peer.String(), err)
		}
	}
}

// SyncBlocks initiates block synchronization with a peer
func (n *Node) SyncBlocks(peerID peer.ID, startHeight, endHeight int) error {
	// Create sync request
	syncRequest := struct {
		StartHeight int
		EndHeight   int
	}{
		StartHeight: startHeight,
		EndHeight:   endHeight,
	}

	// Serialize the request
	requestData, err := json.Marshal(syncRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal sync request: %v", err)
	}

	// Create stream to peer
	stream, err := n.Host.NewStream(n.ctx, peerID, "/blockchain/sync/1.0.0")
	if err != nil {
		return fmt.Errorf("failed to create stream: %v", err)
	}
	defer stream.Close()

	// Send request
	if _, err := stream.Write(requestData); err != nil {
		return fmt.Errorf("failed to write request: %v", err)
	}

	// Receive and process blocks
	var blocks []Block
	if err := json.NewDecoder(stream).Decode(&blocks); err != nil {
		return fmt.Errorf("failed to receive blocks: %v", err)
	}

	// Convert UTXOPool to map[string]UTXO
	utxoMap := make(map[string]UTXO)
	for _, utxo := range n.UTXOSet.GetAllUTXOs() {
		utxoMap[utxo.TransactionID] = utxo
	}

	// Validate and add blocks
	for i, block := range blocks {
		// Create a deep copy of the block to prevent modifications
		blockCopy := block
		blockCopy.mu.Lock()
		originalHash := blockCopy.hash
		blockCopy.mu.Unlock()

		// Validate block structure and hash
		if err := validateBlockStructure(&blockCopy); err != nil {
			return fmt.Errorf("invalid block structure at height %d: %v", startHeight+i, err)
		}

		// Verify hash consistency
		if blockCopy.Hash() != originalHash {
			return fmt.Errorf("block hash mismatch at height %d: expected %s, got %s",
				startHeight+i, originalHash, blockCopy.Hash())
		}

		// Add block to blockchain
		if err := n.Blockchain.AddBlock(&blockCopy, n.Mempool, n.StakePool, utxoMap, n.Host); err != nil {
			return fmt.Errorf("failed to add block at height %d: %v", startHeight+i, err)
		}

		// Update sync progress
		n.PeerManager.UpdateSyncProgress(peerID, startHeight+i+1, endHeight)
	}

	return nil
}

// validateBlockStructure validates the basic structure of a block
func validateBlockStructure(block *Block) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	// Validate block data
	if len(block.Hash()) == 0 {
		return fmt.Errorf("block data is empty")
	}

	// Validate merkle root
	if block.Header.MerkleRoot == "" {
		return fmt.Errorf("block merkle root is empty")
	}

	// Validate state root
	if block.Header.StateRoot == "" {
		return fmt.Errorf("block state root is empty")
	}

	// Validate timestamp
	if block.Header.Timestamp <= 0 {
		return fmt.Errorf("block timestamp is zero or negative")
	}

	return nil
}

func contains(list []peer.ID, item peer.ID) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

func (n *Node) setupTransactionProtocol() {
	n.Host.SetStreamHandler("/tx/1.0.0", func(s network.Stream) {
		defer s.Close()

		var msg Message
		if err := json.NewDecoder(s).Decode(&msg); err != nil {
			log.Printf("Error decoding transaction message: %v", err)
			return
		}

		switch msg.Type {
		case "NEW_TRANSACTION":
			n.handleNewTransaction(s, msg.Payload)
		case "MEMPOOL_SYNC":
			n.handleMempoolSync(s)
		}
	})
}

func (n *Node) handleMempoolSync(s network.Stream) {
	log.Printf("📥 Received mempool sync request from peer %s", s.Conn().RemotePeer())
	defer s.Close()

	// Log local mempool state
	n.Mempool.mu.RLock()
	log.Printf("📊 Local Mempool State:")
	log.Printf("   • Total Transactions: %d", len(n.Mempool.Transactions))
	for txID, tx := range n.Mempool.Transactions {
		log.Printf("   • Transaction %s:", txID)
		log.Printf("     - Sender: %s", tx.Sender)
		log.Printf("     - Receiver: %s", tx.Receiver)
		log.Printf("     - Amount: %.4f", tx.Amount)
		log.Printf("     - Timestamp: %s", time.Unix(tx.Timestamp, 0).Format(time.RFC3339))
	}
	n.Mempool.mu.RUnlock()

	// Send mempool data
	log.Printf("📤 Sending mempool data to peer %s", s.Conn().RemotePeer())
	if err := json.NewEncoder(s).Encode(n.Mempool.Transactions); err != nil {
		log.Printf("❌ Failed to send mempool data: %v", err)
		return
	}
	log.Printf("✅ Successfully sent mempool data to peer %s", s.Conn().RemotePeer())
}

func (n *Node) setupStateSync() {
	n.Host.SetStreamHandler("/state/sync/1.0.0", func(s network.Stream) {
		defer s.Close()

		var msg Message
		if err := json.NewDecoder(s).Decode(&msg); err != nil {
			log.Printf("Error decoding state sync message: %v", err)
			return
		}

		switch msg.Type {
		case "UTXO_SYNC_REQUEST":
			// Send UTXO set
			if err := json.NewEncoder(s).Encode(n.UTXOPool.utxos); err != nil {
				log.Printf("Error sending UTXO set: %v", err)
			}
		case "STATE_VERIFICATION":
			// This explicitly shows that handleStateVerification is used
			n.handleStateVerification(s)
		}
	})

	// Add periodic state verification
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			n.broadcastStateVerification()
		}
	}()
}

func (n *Node) handleSyncRequest(stream network.Stream) {
	defer stream.Close()

	// Read sync request
	var req SyncRequest
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		log.Printf("❌ Error reading sync request: %v", err)
		return
	}

	log.Printf("📥 Received sync request from peer %s", stream.Conn().RemotePeer())
	log.Printf("   • Requested height: %d", req.Height)

	// Get current blockchain state
	height := n.Blockchain.GetHeight()
	hasChain := height >= 0

	// Calculate total blocks to send
	totalBlocks := uint64(0)
	if hasChain {
		// Always send at least the genesis block if we have a chain
		if req.Height == 0 {
			totalBlocks = height + 1 // Include genesis block
		} else if req.Height < height {
			totalBlocks = height - req.Height
		}
	}

	// Send initial response
	resp := SyncResponse{
		Height:       height,
		HasChain:     hasChain,
		TotalBlocks:  totalBlocks,
		SyncComplete: false,
	}

	if err := json.NewEncoder(stream).Encode(resp); err != nil {
		log.Printf("❌ Error sending sync response: %v", err)
		return
	}

	log.Printf("📤 Sent sync response:")
	log.Printf("   • Current height: %d", height)
	log.Printf("   • Has chain: %v", hasChain)
	log.Printf("   • Total blocks to send: %d", totalBlocks)

	// Send blocks if available
	if hasChain && totalBlocks > 0 {
		log.Printf("🔄 Starting block transfer...")
		blocksSent := uint64(0)

		// Always send genesis block if requested height is 0
		if req.Height == 0 {
			genesisBlock := n.Blockchain.GetBlockByHeight(0)
			if genesisBlock != nil {
				if err := json.NewEncoder(stream).Encode(genesisBlock); err != nil {
					log.Printf("❌ Error sending genesis block: %v", err)
					return
				}
				blocksSent++
				log.Printf("✅ Sent genesis block")
			}
		}

		// Send remaining blocks if any
		if req.Height < height {
			for i := req.Height + 1; i <= height; i++ {
				block := n.Blockchain.GetBlockByHeight(i)
				if block == nil {
					log.Printf("❌ Block not found at height %d", i)
					continue
				}
				// Log block details before sending
				log.Printf("📦 Sending block #%d:", i)
				log.Printf("   • Hash: %s", block.Hash())
				log.Printf("   • Previous Hash: %s", block.Header.PreviousHash)
				log.Printf("   • Validator: %s", block.Header.ValidatedBy)
				log.Printf("   • Transaction Count: %d", len(block.Body.Transactions.GetAllTransactions()))
				log.Printf("   • Timestamp: %s", time.Unix(block.Header.Timestamp, 0).Format(time.RFC3339))
				log.Printf("   • Difficulty: %d", block.Header.Difficulty)
				log.Printf("   • Gas Used: %d", block.Header.GasUsed)

				if err := json.NewEncoder(stream).Encode(block); err != nil {
					log.Printf("❌ Error sending block %d: %v", i, err)
					return
				}
				blocksSent++
			}
		}

		// Send sync completion message
		completeResp := SyncResponse{
			Height:       height,
			HasChain:     true,
			TotalBlocks:  blocksSent,
			SyncComplete: true,
		}
		if err := json.NewEncoder(stream).Encode(completeResp); err != nil {
			log.Printf("❌ Error sending sync completion: %v", err)
			return
		}

		log.Printf("✅ Block sync completed successfully")
		log.Printf("   • Total blocks sent: %d", blocksSent)
		log.Printf("   • Final height: %d", height)
	}
}

func (n *Node) handleChainValidation(s network.Stream) {
	var chain []Block
	if err := json.NewDecoder(s).Decode(&chain); err != nil {
		log.Printf("Error decoding chain for validation: %v", err)
		return
	}

	// Validate the chain
	valid := true
	for i := 1; i < len(chain); i++ {
		if err := validateBlockStructure(&chain[i]); err != nil || chain[i].Header.PreviousHash != chain[i-1].hash {
			valid = false
			break
		}
	}

	// Send validation result
	response := struct {
		Valid bool
	}{
		Valid: valid,
	}

	if err := json.NewEncoder(s).Encode(response); err != nil {
		log.Printf("Error sending validation result: %v", err)
	}
}

func (n *Node) handleNewTransaction(s network.Stream, payload interface{}) {
	// Convert payload to Transaction
	txData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshaling transaction payload: %v", err)
		return
	}

	var tx Transaction
	if err := json.Unmarshal(txData, &tx); err != nil {
		log.Printf("Error unmarshaling transaction: %v", err)
		return
	}

	// Use UTXOPool's ValidateTransaction
	if !n.UTXOPool.ValidateTransaction(&tx) {
		log.Printf("Invalid transaction received: %s", tx.TransactionID)
		return
	}

	// Add to mempool using UTXOPool's utxos map
	if added := n.Mempool.AddTransaction(tx, n.UTXOSet.utxos); !added {
		log.Printf("Failed to add transaction to mempool: %s", tx.TransactionID)
		return
	}

	// Broadcast to other peers
	excludePeers := []peer.ID{s.Conn().RemotePeer()}
	n.BroadcastTransaction(&tx, excludePeers)
}

func (n *Node) handleStateVerification(s network.Stream) {
	var stateHash string
	if err := json.NewDecoder(s).Decode(&stateHash); err != nil {
		log.Printf("Error decoding state hash: %v", err)
		return
	}

	// Calculate local state hash
	localHash := n.Blockchain.CalculateStateHash()

	// Send verification result
	response := struct {
		Match bool
		Hash  string
	}{
		Match: localHash == stateHash,
		Hash:  localHash,
	}

	if err := json.NewEncoder(s).Encode(response); err != nil {
		log.Printf("Error sending state verification result: %v", err)
	}
}

// Fix for state hash calculation
func (bc *Blockchain) CalculateStateHash() string {
	// Combine latest block hash and UTXOPool state
	state := bc.GetLatestBlock().hash

	// Get UTXOs from Node's UTXOPool
	utxos := bc.GetUTXOSet()
	for _, utxo := range utxos {
		state += fmt.Sprintf("%s-%d-%f", utxo.TransactionID, utxo.OutputIndex, utxo.Amount)
	}

	hash := sha256.Sum256([]byte(state))
	return hex.EncodeToString(hash[:])
}

// Add helper method to Blockchain to get UTXO set
// func (bc *Blockchain) GetUTXOSet() map[string]UTXO {
// 	// Access UTXOs directly from the Node's UTXOPool
// 	return bc.Node.UTXOPool.utxos
// }

// Add method to broadcast state verification
func (n *Node) broadcastStateVerification() {
	peers := n.Host.Network().Peers()
	for _, peer := range peers {
		if s, err := n.Host.NewStream(context.Background(), peer, "/state/sync/1.0.0"); err == nil {
			stateHash := n.Blockchain.CalculateStateHash()
			msg := NewMessage("STATE_VERIFICATION", stateHash)
			if err := json.NewEncoder(s).Encode(msg); err != nil {
				log.Printf("Error sending state verification to peer %s: %v", peer.String(), err)
			}
			s.Close()
		}
	}
}

// handleBlockSync processes block sync requests
func (n *Node) handleBlockSync(s network.Stream) {
	peerID := s.Conn().RemotePeer()

	// Don't sync with bootstrap nodes
	if n.IsPeerBootstrapNode(peerID) {
		log.Printf("⚠️ Ignoring block sync from bootstrap node %s", peerID)
		return
	}

	n.syncMu.Lock()
	if n.isSyncing {
		n.syncMu.Unlock()
		log.Printf("⚠️ Already syncing, ignoring block sync from %s", peerID)
		return
	}
	n.isSyncing = true
	n.syncMu.Unlock()

	defer func() {
		n.syncMu.Lock()
		n.isSyncing = false
		n.lastSyncTime = time.Now()
		n.syncComplete = true
		n.syncMu.Unlock()
	}()

	log.Printf("🔍 [ENGINEERING] Starting block sync handler")
	defer s.Close()

	// Read sync request
	var request SyncRequest
	if err := json.NewDecoder(s).Decode(&request); err != nil {
		log.Printf("❌ [ENGINEERING] Failed to decode sync request: %v", err)
		sendError(s, "invalid sync request")
		return
	}
	log.Printf("📥 [ENGINEERING] Received sync request for height %d", request.Height)

	// Get requested block
	block := n.Blockchain.GetBlockByHeight(request.Height)
	if block == nil {
		log.Printf("❌ [ENGINEERING] Failed to get block at height %d", request.Height)
		sendError(s, "block not found")
		return
	}

	// Log block state before any operations
	log.Printf("📦 [ENGINEERING] Block state before sync:")
	log.Printf("    • Block Number: %d", block.Header.BlockNumber)
	log.Printf("    • Original Hash: %s", block.Hash())
	log.Printf("    • Previous Hash: %s", block.Header.PreviousHash)
	log.Printf("    • ValidatedBy: %s", block.Header.ValidatedBy)
	log.Printf("    • ValidatorAddress: %s", block.Header.ValidatorAddress)
	log.Printf("    • Timestamp: %d", block.Header.Timestamp)
	log.Printf("    • State Root: %s", block.Header.StateRoot)
	log.Printf("    • Merkle Root: %s", block.Header.MerkleRoot)
	log.Printf("    • Receipts Root: %s", block.Header.ReceiptsRoot)

	// Create a deep copy of the block
	blockCopy := block.Copy()

	// Log block copy state
	log.Printf("📦 [ENGINEERING] Block copy state:")
	log.Printf("    • Block Number: %d", blockCopy.Header.BlockNumber)
	log.Printf("    • Copy Hash: %s", blockCopy.Hash())
	log.Printf("    • Previous Hash: %s", blockCopy.Header.PreviousHash)
	log.Printf("    • ValidatedBy: %s", blockCopy.Header.ValidatedBy)
	log.Printf("    • ValidatorAddress: %s", blockCopy.Header.ValidatorAddress)
	log.Printf("    • Timestamp: %d", blockCopy.Header.Timestamp)
	log.Printf("    • State Root: %s", blockCopy.Header.StateRoot)
	log.Printf("    • Merkle Root: %s", blockCopy.Header.MerkleRoot)
	log.Printf("    • Receipts Root: %s", blockCopy.Header.ReceiptsRoot)

	// Verify hash consistency
	originalHash := block.Hash()
	copyHash := blockCopy.Hash()
	if originalHash != copyHash {
		log.Printf("⚠️ [ENGINEERING] Hash mismatch detected!")
		log.Printf("    • Original Hash: %s", originalHash)
		log.Printf("    • Copy Hash: %s", copyHash)
		log.Printf("    • Difference in fields:")
		log.Printf("      - Original ValidatedBy: %s", block.Header.ValidatedBy)
		log.Printf("      - Copy ValidatedBy: %s", blockCopy.Header.ValidatedBy)
		log.Printf("      - Original ValidatorAddress: %s", block.Header.ValidatorAddress)
		log.Printf("      - Copy ValidatorAddress: %s", blockCopy.Header.ValidatorAddress)
	}

	// Serialize the block copy
	blockData, err := json.Marshal(blockCopy)
	if err != nil {
		log.Printf("❌ [ENGINEERING] Failed to marshal block: %v", err)
		sendError(s, "failed to serialize block")
		return
	}

	// Log serialized data
	log.Printf("📦 [ENGINEERING] Serialized block data:")
	log.Printf("    • Data Length: %d bytes", len(blockData))
	log.Printf("    • Serialized Hash: %s", blockCopy.Hash())

	// Send the block
	if _, err := s.Write(blockData); err != nil {
		log.Printf("❌ [ENGINEERING] Failed to send block: %v", err)
		return
	}

	log.Printf("✅ [ENGINEERING] Successfully sent block #%d", block.Header.BlockNumber)
	log.Printf("    • Final Hash: %s", blockCopy.Hash())
	log.Printf("    • Original Hash: %s", originalHash)
}

// getBlockHeaders returns block headers for the specified range
func (n *Node) getBlockHeaders(start, end uint64) []BlockHeader {
	headers := make([]BlockHeader, 0, end-start+1)
	for i := start; i <= end; i++ {
		block := n.Blockchain.Chain[i]

		// Calculate merkle root from transactions
		txHashes := make([]string, 0)
		for _, tx := range block.Body.Transactions.GetAllTransactions() {
			txHashes = append(txHashes, tx.Hash())
		}

		merkleRoot := CalculateMerkleRoot(txHashes)

		header := BlockHeader{
			PreviousHash: block.Header.PreviousHash,
			BlockNumber:  block.Header.BlockNumber,
			Timestamp:    block.Header.Timestamp,
			MerkleRoot:   merkleRoot,
			StateRoot:    block.Header.StateRoot,
			Difficulty:   block.Header.Difficulty,
		}

		headers = append(headers, header)
	}
	return headers
}

// Helper function to calculate merkle root from transaction hashes
func CalculateMerkleRoot(hashes []string) string {
	if len(hashes) == 0 {
		return ""
	}

	// If odd number of hashes, duplicate the last one
	if len(hashes)%2 == 1 {
		hashes = append(hashes, hashes[len(hashes)-1])
	}

	for len(hashes) > 1 {
		var nextLevel []string
		for i := 0; i < len(hashes); i += 2 {
			hash := sha256.Sum256([]byte(hashes[i] + hashes[i+1]))
			nextLevel = append(nextLevel, hex.EncodeToString(hash[:]))
		}
		hashes = nextLevel
	}

	return hashes[0]
}

// verifyBlockHeaders verifies a sequence of block headers
func (n *Node) verifyBlockHeaders(headers []BlockHeader) error {
	if len(headers) == 0 {
		return fmt.Errorf("empty headers")
	}

	// Verify header chain
	for i := 1; i < len(headers); i++ {
		block := n.Blockchain.Chain[i-1]
		if headers[i].PreviousHash != block.hash {
			return fmt.Errorf("invalid header chain at height %d", headers[i].BlockNumber)
		}
	}

	return nil
}

// IsSyncing returns whether the node is currently syncing
func (n *Node) IsSyncing() bool {
	n.syncMu.RLock()
	defer n.syncMu.RUnlock()
	return n.isSyncing
}

// Start starts the node and initializes all necessary components
func (n *Node) Start() error {
	n.runningMu.Lock()
	if n.isRunning {
		n.runningMu.Unlock()
		return fmt.Errorf("node is already running")
	}
	n.isRunning = true
	n.runningMu.Unlock()

	// Bootstrap DHT
	log.Printf("🔄 Starting DHT bootstrap...")
	if err := n.bootstrapDHT(n.ctx); err != nil {
		return fmt.Errorf("failed to bootstrap DHT: %v", err)
	}

	// Start peer discovery
	log.Printf("🔍 Starting peer discovery...")
	go n.discoverPeers()

	// Only start blockchain sync if this is not a genesis validator
	if !n.IsInitializedValidator() {
		go n.startBlockchainSync()
	} else {
		log.Printf("🔐 Genesis validator detected - skipping blockchain sync")
	}

	log.Printf("✅ Node started successfully with ID: %s", n.Host.ID())
	return nil
}

func (n *Node) discoverPeers() {
	// Only discover if we don't have enough non-bootnode peers
	if n.hasEnoughPeers() {
		return
	}

	log.Printf("🔍 Starting peer discovery...")
	log.Printf("📊 Current connections: %d peers", len(n.Host.Network().Peers()))

	// Find peers through DHT
	ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
	defer cancel()

	// Bootstrap DHT if not already bootstrapped
	if err := n.DHT.Bootstrap(ctx); err != nil {
		log.Printf("⚠️ Failed to bootstrap DHT: %v", err)
		return
	}

	// Find peers through DHT
	peers, err := n.findPeersWithRendezvous(ctx)
	if err != nil {
		log.Printf("⚠️ Peer discovery error: %v", err)
		return
	}

	// Connect to discovered peers
	for _, peerInfo := range peers {
		// Skip if it's a bootnode
		if n.IsPeerBootstrapNode(peerInfo.ID) {
			continue
		}

		// Skip if we're already connected
		if n.Host.Network().Connectedness(peerInfo.ID) == network.Connected {
			continue
		}

		if err := n.Host.Connect(ctx, peerInfo); err != nil {
			log.Printf("⚠️ Failed to connect to peer %s: %v", peerInfo.ID, err)
			continue
		}

		log.Printf("✅ Connected to new peer: %s", peerInfo.ID)
		n.PeerManager.AddPeer(peerInfo.ID)
	}

	// Log final peer count
	nonBootnodePeers := n.countNonBootnodePeers()
	log.Printf("📊 Final peer count: %d (non-bootnode peers: %d)",
		len(n.Host.Network().Peers()), nonBootnodePeers)
}

func (n *Node) hasEnoughPeers() bool {
	nonBootnodePeers := n.countNonBootnodePeers()
	return nonBootnodePeers >= 1
}

func (n *Node) countNonBootnodePeers() int {
	count := 0
	for _, peerID := range n.Host.Network().Peers() {
		if !n.IsPeerBootstrapNode(peerID) {
			count++
		}
	}
	return count
}

// RegisterBlockchainHandlers registers all blockchain-related protocol handlers
func (n *Node) RegisterBlockchainHandlers(bc *Blockchain) error {
	// Register block sync protocol
	n.setupBlockSyncProtocol()

	// Register transaction handlers
	n.Host.SetStreamHandler("/tx/1.0.0", n.handleTransactionStream)

	// Register mempool sync
	n.Host.SetStreamHandler("/mempool/sync/1.0.0", n.handleMempoolSync)

	// Register stake pool sync
	n.Host.SetStreamHandler("/stake/sync/1.0.0", n.handleStakeSync)

	// Register state sync
	n.setupStateSync()

	// Register chain validation
	n.Host.SetStreamHandler("/chain/validate/1.0.0", n.handleChainValidation)

	// Register state verification
	n.Host.SetStreamHandler("/state/verify/1.0.0", n.handleStateVerification)

	return nil
}

// handleStakeSync processes stake pool sync requests
func (n *Node) handleStakeSync(s network.Stream) {
	defer s.Close()

	// Read sync request
	var req struct {
		Type      string `json:"type"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.NewDecoder(s).Decode(&req); err != nil {
		log.Printf("❌ Failed to decode stake sync request: %v", err)
		return
	}

	// Get current stake pool state
	n.StakePool.mu.Lock()
	defer n.StakePool.mu.Unlock()

	// Create a copy of the stakes to avoid race conditions
	stakes := make(map[string]StakeInfo)
	for addr, stake := range n.StakePool.Stakes {
		// Create a deep copy of the stake info
		stakes[addr] = StakeInfo{
			Address:        stake.Address,
			Amount:         stake.Amount,
			StartTime:      stake.StartTime,
			LastRewardTime: stake.LastRewardTime,
			LastActive:     stake.LastActive,
			WithdrawalReq:  stake.WithdrawalReq,
			Violations:     stake.Violations,
			Performance:    stake.Performance,
			SelectionCount: stake.SelectionCount,
			HostID:         stake.HostID,
			Timestamp:      stake.Timestamp,
			IsValidator:    stake.IsValidator,
		}
	}

	// Log the stake pool state being sent
	log.Printf("📊 Sending stake pool state to peer %s:", s.Conn().RemotePeer())
	log.Printf("   • Total Validators: %d", len(stakes))
	for addr, stake := range stakes {
		log.Printf("   • Validator %s:", addr)
		log.Printf("     - Stake Amount: %.4f", float64(stake.Amount))
		log.Printf("     - Host ID: %s", stake.HostID)
		log.Printf("     - Is Validator: %v", stake.IsValidator)
		log.Printf("     - Last Active: %s", stake.LastActive.Format(time.RFC3339))
	}

	// Send stake pool state
	if err := json.NewEncoder(s).Encode(stakes); err != nil {
		log.Printf("❌ Failed to send stake pool state: %v", err)
		return
	}

	// Receive peer's stake pool
	var peerStakes map[string]StakeInfo
	log.Printf("📥 Waiting for peer's stake pool data...")
	if err := json.NewDecoder(s).Decode(&peerStakes); err != nil {
		log.Printf("❌ Failed to receive peer stakes: %v", err)
		return
	}

	// Log received stake pool data
	log.Printf("📊 Received Stake Pool Data from peer %s:", s.Conn().RemotePeer())
	log.Printf("   • Total Validators Received: %d", len(peerStakes))
	for addr, stake := range peerStakes {
		log.Printf("   • Validator %s:", addr)
		log.Printf("     - Stake Amount: %.4f", float64(stake.Amount))
		log.Printf("     - Host ID: %s", stake.HostID)
		log.Printf("     - Is Validator: %v", stake.IsValidator)
		log.Printf("     - Last Active: %s", stake.LastActive.Format(time.RFC3339))
	}

	// Merge stakes
	updates := 0
	for addr, peerStake := range peerStakes {
		localStake, exists := n.StakePool.Stakes[addr]
		if !exists || peerStake.Amount > localStake.Amount {
			// Update local stake if peer has higher amount
			n.StakePool.Stakes[addr] = &StakeInfo{
				Address:        peerStake.Address,
				Amount:         peerStake.Amount,
				StartTime:      peerStake.StartTime,
				LastRewardTime: peerStake.LastRewardTime,
				LastActive:     peerStake.LastActive,
				WithdrawalReq:  peerStake.WithdrawalReq,
				Violations:     peerStake.Violations,
				Performance:    peerStake.Performance,
				SelectionCount: peerStake.SelectionCount,
				HostID:         peerStake.HostID,
				Timestamp:      peerStake.Timestamp,
				IsValidator:    peerStake.IsValidator,
			}
			updates++
			log.Printf("✅ Updated stake for %s:", addr)
			log.Printf("   • New Amount: %.4f", float64(peerStake.Amount))
			log.Printf("   • New Host ID: %s", peerStake.HostID)
			log.Printf("   • New Validator Status: %v", peerStake.IsValidator)
		}
	}

	// Log final state
	log.Printf("📊 Final Stake Pool State:")
	log.Printf("   • Total Validators: %d", len(n.StakePool.Stakes))
	log.Printf("   • Updates Applied: %d", updates)
	for addr, stake := range n.StakePool.Stakes {
		log.Printf("   • Validator %s:", addr)
		log.Printf("     - Final Stake: %.4f", float64(stake.Amount))
		log.Printf("     - Final Host ID: %s", stake.HostID)
		log.Printf("     - Final Validator Status: %v", stake.IsValidator)
	}

	log.Printf("✅ Completed stake pool sync with peer %s", s.Conn().RemotePeer())
}

// NewNodeFromOptions creates a new node from NodeOptions
func NewNodeFromOptions(opts NodeOptions) (*Node, error) {
	// Convert NodeOptions to NetworkConfig
	config := &NetworkConfig{
		P2PPort:        extractPort(opts.ListenAddr),
		BootstrapNodes: opts.BootstrapNodes,
		NetworkID:      opts.NetworkID,
		ChainID:        parseChainID(opts.NetworkID),
		NetworkPath:    opts.NodeID,
		DHTServerMode:  true,
	}

	return NewNode(config)
}

// Helper function to extract port from address string
func extractPort(addr string) int {
	port := 0
	fmt.Sscanf(addr, ":%d", &port)
	return port
}

// Helper function to parse chain ID from network ID
func parseChainID(networkID string) uint64 {
	switch networkID {
	case "mainnet":
		return 1
	case "testnet":
		return 2
	default:
		return 3 // devnet
	}
}

func (n *Node) startBlockchainSync() {
	ticker := time.NewTicker(30 * time.Second) // Increased interval to 30 seconds
	defer ticker.Stop()

	lastSyncTime := time.Now()
	minSyncInterval := 10 * time.Second // Minimum time between syncs

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.syncMu.RLock()
			if !n.syncComplete {
				n.syncMu.RUnlock()
				continue
			}
			n.syncMu.RUnlock()

			// Only sync if we have new peers or it's been a while since last sync
			if n.hasNewPeers() || time.Since(lastSyncTime) > 5*time.Minute {
				// Ensure minimum time between syncs
				if time.Since(lastSyncTime) < minSyncInterval {
					continue
				}

				// Get latest block from peers
				for _, peer := range n.Host.Network().Peers() {
					if err := n.SyncWithPeer(peer); err != nil {
						log.Printf("⚠️ Failed to sync with peer %s: %v", peer.String(), err)
					}
				}
				lastSyncTime = time.Now()
			}
		}
	}
}

// Helper function to check if we have new peers since last sync
func (n *Node) hasNewPeers() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()

	currentPeers := n.Host.Network().Peers()
	for _, peer := range currentPeers {
		if !n.knownPeers[peer] {
			return true
		}
	}
	return false
}

// SyncWithPeer synchronizes blockchain state with a peer
func (n *Node) SyncWithPeer(peerID peer.ID) error {
	n.syncMu.Lock()
	defer n.syncMu.Unlock()

	// Check if we're already syncing
	if n.isSyncing {
		return fmt.Errorf("already syncing with another peer")
	}

	n.isSyncing = true
	defer func() {
		n.isSyncing = false
		n.lastSyncTime = time.Now()
	}()

	log.Printf("🔄 Starting sync with peer %s", peerID)

	// Create sync stream
	stream, err := n.Host.NewStream(context.Background(), peerID, SyncProtocol)
	if err != nil {
		return fmt.Errorf("failed to create sync stream: %v", err)
	}
	defer stream.Close()

	// Send sync request
	req := SyncRequest{
		Height: n.Blockchain.GetHeight(),
	}
	if err := json.NewEncoder(stream).Encode(req); err != nil {
		return fmt.Errorf("failed to send sync request: %v", err)
	}

	// Read initial response
	var resp SyncResponse
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return fmt.Errorf("failed to read sync response: %v", err)
	}

	if !resp.HasChain {
		log.Printf("⚠️ Peer %s has no blockchain", peerID)
		return nil
	}

	/*if resp.Height <= req.Height {
		log.Printf("ℹ️ Peer %s has same or lower height (%d <= %d)", peerID, resp.Height, req.Height)
		return nil
	}*/

	log.Printf("📊 Sync Progress:")
	log.Printf("• Current Height: %d", req.Height)
	log.Printf("• Peer Height: %d", resp.Height)
	log.Printf("• Total Blocks to Sync: %d", resp.TotalBlocks)

	// Process blocks
	blocksReceived := uint64(0)
	for {
		var block Block
		if err := json.NewDecoder(stream).Decode(&block); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read block: %v", err)
		}

		// Log block details before adding
		log.Printf("📦 Received block:")
		log.Printf("   • Block Number: %d", block.Header.BlockNumber)
		log.Printf("   • Hash: %s", block.Hash())
		log.Printf("   • Previous Hash: %s", block.Header.PreviousHash)

		// Process the block
		if err := n.Blockchain.AddBlockWithoutValidation(&block); err != nil {
			log.Printf("⚠️ Failed to add block: %v", err)
			continue
		}

		blocksReceived++
		log.Printf("✅ Added block %d/%d (%.1f%%)",
			blocksReceived, resp.TotalBlocks,
			float64(blocksReceived)/float64(resp.TotalBlocks)*100)

		// Check if this is actually a sync completion message
		var syncComplete SyncResponse
		if err := json.NewDecoder(stream).Decode(&syncComplete); err == nil && syncComplete.SyncComplete {
			log.Printf("✅ Sync completed with peer %s", peerID)
			log.Printf("📊 Final Stats:")
			log.Printf("• Blocks Received: %d/%d", blocksReceived, resp.TotalBlocks)
			return nil
		}
	}

	return nil
}

func (n *Node) findPeersWithRendezvous(ctx context.Context) ([]peer.AddrInfo, error) {
	log.Printf("🔍 Starting peer discovery with rendezvous...")
	log.Printf("📊 Initial DHT Status:")
	log.Printf("• Routing Table Size: %d", n.DHT.RoutingTable().Size())
	log.Printf("• Connected Peers: %d", len(n.Host.Network().Peers()))
	log.Printf("• Network ID: %s", n.NetworkID)

	routingDiscovery := discovery.NewRoutingDiscovery(n.DHT)
	discoveryTag := fmt.Sprintf("blockchain/%s", n.NetworkID)
	log.Printf("📝 Using discovery tag: %s", discoveryTag)

	// Advertise ourselves
	log.Printf("📢 Advertising node %s...", n.Host.ID())
	ttl, err := routingDiscovery.Advertise(ctx, discoveryTag)
	if err != nil {
		log.Printf("❌ Failed to advertise: %v", err)
		log.Printf("📊 DHT Status:")
		log.Printf("• Routing Table Size: %d", n.DHT.RoutingTable().Size())
		log.Printf("• Connected Peers: %d", len(n.Host.Network().Peers()))
		log.Printf("• Bootstrap Peers: %d", len(n.config.BootstrapNodes))
		return nil, fmt.Errorf("failed to advertise: %v", err)
	}
	log.Printf("✅ Successfully advertised with TTL: %v", ttl)

	// Find peers
	log.Printf("🔍 Finding peers with tag: %s", discoveryTag)
	peerChan, err := routingDiscovery.FindPeers(ctx, discoveryTag)
	if err != nil {
		log.Printf("❌ Failed to find peers: %v", err)
		return nil, fmt.Errorf("failed to find peers: %v", err)
	}

	// Collect peers from channel
	var peers []peer.AddrInfo
	for p := range peerChan {
		if p.ID == n.Host.ID() {
			log.Printf("⏭️ Skipping self: %s", p.ID)
			continue
		}

		// Skip if it's a bootnode
		if n.IsPeerBootstrapNode(p.ID) {
			log.Printf("⏭️ Skipping bootnode: %s", p.ID)
			continue
		}

		// Skip if we're already connected
		if n.Host.Network().Connectedness(p.ID) == network.Connected {
			log.Printf("⏭️ Skipping already connected peer: %s", p.ID)
			continue
		}

		peers = append(peers, p)
		log.Printf("🔍 Found potential peer: %s", p.ID)
	}

	log.Printf("📊 Peer discovery results:")
	log.Printf("• Total peers found: %d", len(peers))
	log.Printf("• Current connections: %d", len(n.Host.Network().Peers()))
	log.Printf("• DHT routing table size: %d", n.DHT.RoutingTable().Size())
	log.Printf("• Network ID: %s", n.NetworkID)

	return peers, nil
}

// createHost initializes and returns a new libp2p host
func createHost(config *NetworkConfig) (host.Host, error) {
	// Setup P2P host options
	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", config.P2PPort),
			fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", config.P2PPort),
		),
		libp2p.EnableRelay(),
		libp2p.EnableAutoRelayWithStaticRelays([]peer.AddrInfo{}),
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),       // Enable NAT port mapping
		libp2p.EnableNATService(), // Enable NAT service
	}

	// Create libp2p host
	host, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create host: %w", err)
	}

	// Add connection logging
	host.Network().Notify(&network.NotifyBundle{
		ConnectedF: func(n network.Network, conn network.Conn) {
			remotePeer := conn.RemotePeer()
			remoteAddr := conn.RemoteMultiaddr()
			log.Printf("✅ Connected to peer: %s", remotePeer.String())
			log.Printf("   • Address: %s", remoteAddr)
			log.Printf("   • Direction: %s", conn.Stat().Direction)
		},
		DisconnectedF: func(n network.Network, conn network.Conn) {
			log.Printf("❌ Disconnected from peer: %s", conn.RemotePeer().String())
		},
	})

	return host, nil
}

// SetStreamHandler sets a handler for a specific protocol
func (n *Node) SetStreamHandler(protocolStr string, handler func(network.Stream)) {
	n.Host.SetStreamHandler(protocol.ID(protocolStr), handler)
}

// IsPeerBootstrapNode checks if a peer is a bootstrap node
func (n *Node) IsPeerBootstrapNode(peerID peer.ID) bool {
	// First check the bootstrapNodes map
	n.runningMu.RLock()
	if isBootNode := n.bootstrapNodes[peerID]; isBootNode {
		n.runningMu.RUnlock()
		return true
	}
	n.runningMu.RUnlock()

	// Read bootnode.addr file
	bootnodeAddr, err := os.ReadFile("bootnode.addr")
	if err != nil {
		log.Printf("⚠️ Failed to read bootnode.addr: %v", err)
		return false
	}

	// Extract peer ID from bootnode address
	// Format: /ip4/<ip>/tcp/<port>/p2p/<peerID>
	addrStr := strings.TrimSpace(string(bootnodeAddr))
	parts := strings.Split(addrStr, "/p2p/")
	if len(parts) != 2 {
		log.Printf("⚠️ Invalid bootnode address format")
		return false
	}

	bootnodeID := parts[1]
	return peerID.String() == bootnodeID
}

// CountNonBootnodePeers returns the number of connected peers that are not bootstrap nodes
func (n *Node) CountNonBootnodePeers() int {
	count := 0
	for _, peer := range n.Host.Network().Peers() {
		if !n.IsPeerBootstrapNode(peer) {
			count++
		}
	}
	return count
}

// SyncBlockchain syncs the blockchain with connected peers
func (n *Node) SyncBlockchain() error {
	for _, peer := range n.Host.Network().Peers() {
		if !n.IsPeerBootstrapNode(peer) {
			if err := n.SyncWithPeer(peer); err != nil {
				log.Printf("⚠️ Failed to sync with peer %s: %v", peer, err)
				continue
			}
			return nil
		}
	}
	return fmt.Errorf("no suitable peers found for sync")
}

// SetInitializedValidator sets whether this node is an initialized validator
func (n *Node) SetInitializedValidator(isInitialized bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.isInitializedValidator = isInitialized
	log.Printf("🔐 Node validator initialization state set to: %v", isInitialized)
}

// IsInitializedValidator returns whether this node is an initialized validator
func (n *Node) IsInitializedValidator() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.isInitializedValidator
}

// BroadcastValidatorRequest broadcasts a validator request to all connected peers
func (n *Node) BroadcastValidatorRequest() error {
	// Create a validator request message
	msg := &Message{
		Type:    MessageTypeValidatorRequest,
		Payload: []byte("request_validators"),
	}

	// Broadcast to all connected peers
	for _, peer := range n.Host.Network().Peers() {
		if err := n.SendMessage(peer, msg); err != nil {
			log.Printf("Failed to send validator request to peer %s: %v", peer.String(), err)
		}
	}

	return nil
}

// HandleValidatorRequest handles validator request messages from peers
func (n *Node) HandleValidatorRequest(peerID peer.ID, msg *Message) error {
	// Get validator information from stake pool
	validatorInfo, err := n.StakePool.GetValidatorInfo()
	if err != nil {
		return err
	}

	// Create response message
	response := &Message{
		Type:    MessageTypeValidatorResponse,
		Payload: validatorInfo,
	}

	// Send response to requesting peer
	return n.SendMessage(peerID, response)
}

// HandleValidatorResponse handles validator response messages from peers
func (n *Node) HandleValidatorResponse(peerID peer.ID, msg *Message) error {
	// Update stake pool with received validator information
	payload, ok := msg.Payload.([]byte)
	if !ok {
		return fmt.Errorf("invalid payload type: expected []byte")
	}
	if err := n.StakePool.UpdateValidators(payload); err != nil {
		return err
	}

	log.Printf("✅ Updated validator information from peer %s", peerID.String())
	return nil
}

// SendMessage sends a message to a specific peer
func (n *Node) SendMessage(peerID peer.ID, msg *Message) error {
	// Serialize the message
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// Send the message
	stream, err := n.Host.NewStream(context.Background(), peerID, protocol.ID(ProtocolID))
	if err != nil {
		return err
	}
	defer stream.Close()

	_, err = stream.Write(data)
	return err
}
