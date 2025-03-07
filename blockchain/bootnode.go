package blockchain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/routing"
	discovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/multiformats/go-multiaddr"
	"github.com/pion/stun"
)

// Heartbeat represents a node's heartbeat message
type Heartbeat struct {
	Timestamp int64  `json:"timestamp"`
	NodeID    string `json:"node_id"`
}

// Constants for blockchain network configuration
const (
	BlockchainDefaultPort = 50505
	BlockchainNamespace   = "/blockchain/v1"

	// Bootstrap node configuration
	MaxPeerConnections    = 50
	PeerDiscoveryInterval = 5 * time.Minute
	DHTProviderInterval   = 10 * time.Minute

	DefaultBootstrapAddress = "/ip4/49.204.107.251/tcp/50505/p2p/<PEER_ID>"
	DefaultListenPort       = 50505

	// Add constant for heartbeat timeout
	HeartbeatTimeout = 90 * time.Second // 3x HeartbeatInterval

	MaxPeers          = 100
	ConnectionTimeout = 30 * time.Second
	HeartbeatInterval = 10 * time.Second
	MaxRetries        = 3
	RetryDelay        = 5 * time.Second
)

// Store known bootstrap nodes
var KnownBootstrapPeers = []string{
	DefaultBootstrapAddress,
	// Add more bootstrap nodes here
}

// BootstrapNodeConfig represents the configuration for a bootstrap node
type BootstrapNodeConfig struct {
	ListenPort         int
	PublicIP           string
	KeyFile            string
	PeerStoreFile      string
	DataDir            string
	EnableRelay        bool
	EnableNAT          bool
	EnablePeerExchange bool
	SeedNodes          []peer.AddrInfo
	StoragePath        string
	NetworkID          string
	EnableMetrics      bool
}

// BootstrapNode represents a dedicated bootstrap node for the blockchain network
type BootstrapNode struct {
	host          host.Host
	dht           *dht.IpfsDHT
	routing       routing.Routing
	ctx           context.Context
	cancel        context.CancelFunc
	peerStore     *PersistentPeerStore
	config        *BootstrapNodeConfig
	mu            sync.RWMutex
	started       bool
	listeners     []net.Listener
	peerScores    map[peer.ID]*PeerScore
	metrics       *NetworkMetrics
	rateLimiter   *RateLimiter
	protocols     map[string]network.StreamHandler
	peers         map[peer.ID]peer.ID
	peersMutex    sync.RWMutex
	dataDir       string
	identity      crypto.PrivKey
	startTime     time.Time
	lastHeartbeat map[peer.ID]time.Time
	heartbeatMu   sync.RWMutex
	node          *Node
	blockchain    *Blockchain
}

// PeerScore represents the scoring metrics for a peer
type PeerScore struct {
	ConnectionUptime  float64   // Duration of stable connection
	ResponseTime      float64   // Average response time
	MessageSuccess    uint64    // Successful message count
	BandwidthUsage    uint64    // Bytes transferred
	ValidationSuccess uint64    // Successful validations
	LastUpdated       time.Time // Last score update
}

// NetworkMetrics tracks various network performance metrics
type NetworkMetrics struct {
	Latency            map[peer.ID]time.Duration
	BandwidthUsage     map[peer.ID]uint64
	ConnectionSuccess  map[peer.ID]float64
	MessagePropagation map[peer.ID]time.Duration
	StartTime          time.Time
	PeerCount          int
	mu                 sync.RWMutex
}

// RateLimiter manages connection and message rate limits
type RateLimiter struct {
	connectionLimits map[string]struct {
		count     uint64
		lastReset time.Time
	}
	messageLimits map[peer.ID]struct {
		count     uint64
		lastReset time.Time
	}
	mu sync.RWMutex
}

// Notifier implements network.Notifiee interface for handling peer events
type Notifier struct {
	ConnectedF    func(n network.Network, conn network.Conn)
	DisconnectedF func(n network.Network, conn network.Conn)
}

// Connected is called when a new peer connects
func (n *Notifier) Connected(net network.Network, conn network.Conn) {
	if n.ConnectedF != nil {
		n.ConnectedF(net, conn)
	}
}

// Disconnected is called when a peer disconnects
func (n *Notifier) Disconnected(net network.Network, conn network.Conn) {
	if n.DisconnectedF != nil {
		n.DisconnectedF(net, conn)
	}
}

// Listen is called when the network starts listening
func (n *Notifier) Listen(net network.Network, ma multiaddr.Multiaddr) {
	log.Printf("Network started listening on: %s", ma.String())
}

// ListenClose is called when the network stops listening
func (n *Notifier) ListenClose(net network.Network, ma multiaddr.Multiaddr) {
	log.Printf("Network stopped listening on: %s", ma.String())
}

// NewBootstrapNode creates a new bootstrap node
func NewBootstrapNode(config *BootstrapNodeConfig) (*BootstrapNode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Get public IP first
	publicIP, err := getPublicIP()
	if err != nil {
		log.Printf("⚠️ Warning: Could not get public IP: %v", err)
		publicIP = "0.0.0.0"
	}
	config.PublicIP = publicIP

	// Load or create private key
	privKey, err := loadOrCreatePrivateKey(config.DataDir)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create/load identity: %v", err)
	}

	// Get peer ID from private key
	peerID, err := peer.IDFromPrivateKey(privKey)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get peer ID: %v", err)
	}

	// Setup host options
	hostOpts := []libp2p.Option{
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/%s/tcp/%d", publicIP, config.ListenPort),
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", config.ListenPort),
			fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", config.ListenPort),
		),
		libp2p.Identity(privKey),
		libp2p.EnableRelay(),
		libp2p.EnableAutoRelayWithStaticRelays([]peer.AddrInfo{}),
		libp2p.EnableHolePunching(),
	}

	// Print configuration
	log.Printf("\n🚀 Initializing Bootstrap Node")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("📋 Configuration:")
	log.Printf("   • Listen Port: %d", config.ListenPort)
	log.Printf("   • Public IP: %s", publicIP)
	log.Printf("   • NAT Enabled: %v", config.EnableNAT)
	log.Printf("   • Peer Exchange: %v", config.EnablePeerExchange)
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Store bootnode address with public IP in a file that miners can read
	multiAddr := fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s",
		publicIP,
		config.ListenPort,
		peerID.String())

	if err := os.WriteFile("bootnode.addr", []byte(multiAddr), 0644); err != nil {
		log.Printf("⚠️ Warning: Could not save bootnode address: %v", err)
	}

	// Create node data directory if it doesn't exist
	nodeDir := filepath.Dir(config.KeyFile)
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create node directory: %w", err)
	}

	// Create libp2p host
	host, err := libp2p.New(hostOpts...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create libp2p host: %v", err)
	}

	// Print node addresses
	log.Printf("✅ Node Identity:")
	log.Printf("   • Peer ID: %s", peerID.String())
	log.Printf("   • Listening on: %v", host.Addrs())
	if config.PublicIP != "" {
		log.Printf("   • Public Address: %s", config.GetMultiaddr(peerID))
	}

	// Create peerStore with the configured file path instead of temp directory
	peerStore, err := NewPersistentPeerStore(config.PeerStoreFile)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create peer store: %w", err)
	}

	// Create DHT with custom logging
	kdht, err := dht.New(ctx, host, dht.Mode(dht.ModeServer))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	// Pretty print DHT info
	log.Printf("\n📊 DHT Configuration")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("   • Mode: Server")

	// Get routing table info
	rt := kdht.RoutingTable()
	peers := rt.ListPeers()
	log.Printf("   • Routing Table Peers: %d", len(peers))

	// Get network info
	netPeers := kdht.Host().Network().Peers()
	log.Printf("   • Network Peers: %d", len(netPeers))

	// Get connection info
	conns := kdht.Host().Network().Conns()
	log.Printf("   • Active Connections: %d", len(conns))

	// Get address info
	addrs := kdht.Host().Addrs()
	log.Printf("   • Listening Addresses: %d", len(addrs))
	for _, addr := range addrs {
		log.Printf("     ‣ %s", addr.String())
	}

	// Get peer info
	if len(peers) > 0 {
		log.Printf("   • Connected Peers:")
		for i, p := range peers {
			if i >= 5 { // Show only first 5 peers
				log.Printf("     ‣ ... and %d more", len(peers)-5)
				break
			}
			log.Printf("     ‣ %s", p.String())
		}
	}

	log.Printf("━━━━━━━━━━━━━━━━━━━━━━\n")

	bn := &BootstrapNode{
		host:       host,
		dht:        kdht,
		routing:    kdht,
		ctx:        ctx,
		cancel:     cancel,
		peerStore:  peerStore,
		config:     config,
		started:    false,
		peerScores: make(map[peer.ID]*PeerScore),
		metrics: &NetworkMetrics{
			Latency:            make(map[peer.ID]time.Duration),
			BandwidthUsage:     make(map[peer.ID]uint64),
			ConnectionSuccess:  make(map[peer.ID]float64),
			MessagePropagation: make(map[peer.ID]time.Duration),
			StartTime:          time.Now(),
		},
		rateLimiter: &RateLimiter{
			connectionLimits: make(map[string]struct {
				count     uint64
				lastReset time.Time
			}),
			messageLimits: make(map[peer.ID]struct {
				count     uint64
				lastReset time.Time
			}),
		},
		protocols:     make(map[string]network.StreamHandler),
		identity:      privKey,
		startTime:     time.Now(),
		lastHeartbeat: make(map[peer.ID]time.Time),
		node:          nil,
		peers:         make(map[peer.ID]peer.ID),
	}

	// Initialize protocol handlers
	bn.initializeProtocols()

	// Create a notifier instance
	notifier := &Notifier{}

	// Register the notifier
	bn.host.Network().Notify(notifier)

	// Log when a new node joins
	// bn.host.Network().Notify(&network.NotifyBundle{
	// 	Connected: func(n network.Network, conn network.Conn) {
	// 		log.Printf(" New node joined the network: %s\n", conn.RemotePeer().String())
	// 	},
	// 	Disconnected: func(n network.Network, conn network.Conn) {
	// 		log.Printf(" Node disconnected from the network: %s\n", conn.RemotePeer().String())
	// 	},
	// })

	log.Printf(" DHT Table: %+v\n", bn.dht)

	log.Println(" Network is active and listening...")

	log.Printf("✨ Bootstrap node initialization complete\n")

	// Set up protocol handlers
	bn.host.SetStreamHandler("/blockchain/1.0.0/sync", func(stream network.Stream) {
		defer stream.Close()

		// Handle sync request
		var req SyncRequest
		if err := json.NewDecoder(stream).Decode(&req); err != nil {
			log.Printf("Error decoding sync request: %v", err)
			return
		}

		// Initialize response
		resp := SyncResponse{
			Success: true,
		}

		// Check if blockchain is initialized
		if bn.blockchain != nil {
			// Get actual blockchain data
			latestBlock := bn.blockchain.GetLatestBlock()
			resp.Height = latestBlock.Header.BlockNumber
			resp.LastBlockHash = latestBlock.Hash()
			log.Printf("📊 Syncing peer with blockchain height %d, hash %s",
				resp.Height, resp.LastBlockHash)
		} else {
			// No blockchain available - this is a fresh network
			resp.Height = 0
			resp.LastBlockHash = ""
			resp.IsGenesisNode = true
			log.Printf("🆕 No blockchain available for sync - informing peer this is a fresh network")
		}

		// Send response
		if err := json.NewEncoder(stream).Encode(resp); err != nil {
			log.Printf("❌ Error encoding sync response: %v", err)
			return
		}

		log.Printf("✅ Responded to sync request from: %s", stream.Conn().RemotePeer().String())
	})

	// Start heartbeat monitor
	go bn.monitorHeartbeats()

	return bn, nil
}

// GetMultiaddr returns the complete multiaddr string for the bootnode
func (config *BootstrapNodeConfig) GetMultiaddr(peerID peer.ID) string {
	// Local address
	local := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", config.ListenPort)

	// If public IP is provided, add it as well
	if config.PublicIP != "" {
		return fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s",
			config.PublicIP,
			config.ListenPort,
			peerID.String(),
		)
	}
	return local
}

// loadOrCreatePrivateKey loads an existing private key or creates a new one
func loadOrCreatePrivateKey(dataDir string) (crypto.PrivKey, error) {
	keyFile := filepath.Join(dataDir, "node.key")

	// Try to load existing key
	if keyBytes, err := os.ReadFile(keyFile); err == nil {
		return crypto.UnmarshalPrivateKey(keyBytes)
	}

	// Generate new key
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, err
	}

	// Save the key
	keyBytes, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(keyFile, keyBytes, 0600); err != nil {
		return nil, err
	}

	return priv, nil
}

// initializeProtocols sets up all supported protocols for the bootstrap node
func (bn *BootstrapNode) initializeProtocols() {
	// Register core blockchain protocols
	bn.protocols["/blockchain/1.0.0"] = bn.handleBlockAnnouncement
	bn.protocols["/blockchain/tx/1.0.0"] = bn.handleTransaction
	bn.protocols["/blockchain/heartbeat/1.0.0"] = bn.handleHeartbeat
	bn.protocols["/blockchain/sync/1.0.0"] = bn.handleSync
	bn.protocols["/blockchain/state/1.0.0"] = bn.handleStatus
	bn.protocols["/blockchain/fork/1.0.0"] = bn.handleForkResolution
	bn.protocols["/blockchain/mempool/1.0.0"] = bn.handleMempoolSync
	bn.protocols["/blockchain/validator/1.0.0"] = bn.handleValidatorMessage

	// Register stream handlers
	for proto, handler := range bn.protocols {
		bn.host.SetStreamHandler(protocol.ID(proto), handler)
	}

	// Start periodic tasks
	go bn.startPeriodicTasks()
	go bn.monitorHeartbeats()
	go bn.runPeerDiscovery()

	log.Printf("✅ Bootstrap node protocols initialized")
}

// handleValidatorMessage processes validator-related messages
func (bn *BootstrapNode) handleValidatorMessage(s network.Stream) {
	peerID := s.Conn().RemotePeer()

	// Read message
	buf := make([]byte, 1024)
	_, err := io.ReadFull(s, buf)
	if err != nil {
		log.Printf("⚠️ Failed to read validator message from %s: %v", peerID, err)
		s.Reset()
		return
	}

	// Parse message
	var msg struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(buf, &msg); err != nil {
		log.Printf("⚠️ Failed to parse validator message from %s: %v", peerID, err)
		s.Reset()
		return
	}

	// Process message based on type
	switch msg.Type {
	case "VALIDATOR_HEARTBEAT":
		var heartbeat ValidatorHeartbeatMessage
		if err := json.Unmarshal(msg.Payload, &heartbeat); err != nil {
			log.Printf("⚠️ Failed to parse validator heartbeat from %s: %v", peerID, err)
			s.Reset()
			return
		}
		bn.updatePeerScore(peerID, "heartbeat", 1)

	case "VALIDATOR_TIMEOUT":
		var timeout ValidatorTimeoutMessage
		if err := json.Unmarshal(msg.Payload, &timeout); err != nil {
			log.Printf("⚠️ Failed to parse validator timeout from %s: %v", peerID, err)
			s.Reset()
			return
		}
		bn.updatePeerScore(peerID, "timeout", -1)

	case "VALIDATOR_SET_UPDATE":
		var update ValidatorSetUpdateMessage
		if err := json.Unmarshal(msg.Payload, &update); err != nil {
			log.Printf("⚠️ Failed to parse validator set update from %s: %v", peerID, err)
			s.Reset()
			return
		}
		bn.broadcastToOtherPeers(peerID, "VALIDATOR_SET_UPDATE", update)
	}

	// Update peer info
	bn.updatePeerLastSeen(peerID)
}

// updatePeerLastSeen updates the last seen timestamp for a peer
func (bn *BootstrapNode) updatePeerLastSeen(peerID peer.ID) {
	bn.heartbeatMu.Lock()
	defer bn.heartbeatMu.Unlock()
	bn.lastHeartbeat[peerID] = time.Now()
}

// handleMempoolSync processes mempool synchronization requests
func (bn *BootstrapNode) handleMempoolSync(s network.Stream) {
	// Implementation for mempool sync
	log.Printf("📬 Mempool sync request from %s", s.Conn().RemotePeer())
	s.Close()
}

// handleSync processes blockchain sync requests
func (bn *BootstrapNode) handleSync(s network.Stream) {
	// Implementation for blockchain sync
	log.Printf("🔄 Sync request from %s", s.Conn().RemotePeer())
	s.Close()
}

// handleForkResolution processes fork resolution requests
func (bn *BootstrapNode) handleForkResolution(s network.Stream) {
	// Implementation for fork resolution
	log.Printf("🔀 Fork resolution request from %s", s.Conn().RemotePeer())
	s.Close()
}

// updatePeerScore updates the score for a peer based on their behavior
func (bn *BootstrapNode) updatePeerScore(peerID peer.ID, metric string, value float64) {
	bn.mu.Lock()
	defer bn.mu.Unlock()

	score, exists := bn.peerScores[peerID]
	if !exists {
		score = &PeerScore{LastUpdated: time.Now()}
		bn.peerScores[peerID] = score
	}

	switch metric {
	case "uptime":
		score.ConnectionUptime = value
	case "response":
		score.ResponseTime = value
	case "messages":
		score.MessageSuccess++
	case "bandwidth":
		score.BandwidthUsage += uint64(value)
	case "validation":
		score.ValidationSuccess++
	}
	score.LastUpdated = time.Now()
}

// enforceRateLimits checks if a peer has exceeded rate limits
func (bn *BootstrapNode) enforceRateLimits(peerID peer.ID, msgType string) error {
	bn.rateLimiter.mu.Lock()
	defer bn.rateLimiter.mu.Unlock()

	now := time.Now()
	resetInterval := time.Minute

	// Check message rate limits
	if limits, exists := bn.rateLimiter.messageLimits[peerID]; exists {
		if now.Sub(limits.lastReset) > resetInterval {
			limits.count = 0
			limits.lastReset = now
		}
		if limits.count >= 100 { // 100 messages per minute
			return fmt.Errorf("message rate limit exceeded for peer %s", peerID)
		}
		limits.count++
		bn.rateLimiter.messageLimits[peerID] = limits
	} else {
		bn.rateLimiter.messageLimits[peerID] = struct {
			count     uint64
			lastReset time.Time
		}{1, now}
	}

	return nil
}

// handleBlockAnnouncement processes incoming block announcements
func (bn *BootstrapNode) handleBlockAnnouncement(stream network.Stream) {
	defer stream.Close()

	// Enforce rate limits
	if err := bn.enforceRateLimits(stream.Conn().RemotePeer(), "block"); err != nil {
		log.Printf("Rate limit exceeded: %v", err)
		return
	}

	// Read the block announcement
	var block Block
	if err := json.NewDecoder(stream).Decode(&block); err != nil {
		log.Printf("Failed to decode block: %v", err)
		return
	}

	// Validate timestamp
	if time.Since(time.Unix(block.Header.Timestamp, 0)) > time.Hour {
		log.Printf("Block announcement too old")
		return
	}

	// Update metrics
	bn.metrics.mu.Lock()
	bn.metrics.MessagePropagation[stream.Conn().RemotePeer()] = time.Since(time.Unix(block.Header.Timestamp, 0))
	bn.metrics.mu.Unlock()

	// Broadcast to other peers
	bn.broadcastToOtherPeers(stream.Conn().RemotePeer(), "block", block)
}

// handleTransaction processes incoming transactions
func (bn *BootstrapNode) handleTransaction(stream network.Stream) {
	defer stream.Close()

	// Enforce rate limits
	if err := bn.enforceRateLimits(stream.Conn().RemotePeer(), "tx"); err != nil {
		log.Printf("Rate limit exceeded: %v", err)
		return
	}

	// Read the transaction
	var tx Transaction
	if err := json.NewDecoder(stream).Decode(&tx); err != nil {
		log.Printf("Failed to decode transaction: %v", err)
		return
	}

	// Basic validation
	if tx.Amount <= 0 {
		log.Printf("Invalid transaction amount")
		return
	}

	// Update metrics
	bn.metrics.mu.Lock()
	bn.metrics.MessagePropagation[stream.Conn().RemotePeer()] = time.Since(time.Unix(tx.Timestamp, 0))
	bn.metrics.mu.Unlock()

	// Broadcast to other peers
	bn.broadcastToOtherPeers(stream.Conn().RemotePeer(), "tx", tx)
}

// handlePeerDiscovery processes peer discovery requests
func (bn *BootstrapNode) handlePeerDiscovery(stream network.Stream) {
	defer stream.Close()

	// Read peer request
	var req PeerDiscoveryRequest
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		log.Printf("⚠️ Failed to decode peer discovery request: %v", err)
		return
	}

	// Get peers excluding the requester and excluded peers
	peers := bn.peerStore.GetPeers()
	response := PeerDiscoveryResponse{
		Success: true,
		Peers:   make([]peer.AddrInfo, 0, len(peers)),
	}

	excluded := make(map[string]bool)
	for _, p := range req.ExcludedPeers {
		excluded[p] = true
	}

	count := 0
	for _, p := range peers {
		if count >= req.MaxPeers {
			break
		}
		if p.ID != stream.Conn().RemotePeer() && !excluded[p.ID.String()] {
			response.Peers = append(response.Peers, p)
			count++
		}
	}

	// Send response
	if err := json.NewEncoder(stream).Encode(response); err != nil {
		log.Printf("⚠️ Failed to send peer discovery response: %v", err)
	}
}

// collectMetrics periodically collects network metrics
func (bn *BootstrapNode) collectMetrics() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			bn.mu.Lock()

			log.Printf("\n📊 Network Metrics Update")
			log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━")

			activePeers := bn.host.Network().Peers()
			log.Printf("• Active Peers: %d", len(activePeers))

			var connectedPeers int
			for _, peer := range activePeers {
				conns := bn.host.Network().ConnsToPeer(peer)
				if len(conns) > 0 {
					connectedPeers++
					bn.metrics.ConnectionSuccess[peer] = 1.0
				}
			}
			log.Printf("• Connected Peers: %d", connectedPeers)
			log.Printf("• Uptime: %s", time.Since(bn.metrics.StartTime).Round(time.Second))
			log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

			bn.mu.Unlock()
		}
	}
}

// Start starts the bootstrap node
func (bn *BootstrapNode) Start() error {
	// Set up connection handler
	bn.host.Network().Notify(&network.NotifyBundle{
		ConnectedF: func(n network.Network, conn network.Conn) {
			peerID := conn.RemotePeer()
			log.Printf("✅ New peer connected: %s", peerID)

			// Add to known peers
			bn.peers[peerID] = peerID

			// Send welcome message
			go bn.sendWelcomeMessage(peerID)
		},
		DisconnectedF: func(n network.Network, conn network.Conn) {
			peerID := conn.RemotePeer()
			log.Printf("❌ Peer disconnected: %s", peerID)
			delete(bn.peers, peerID)
		},
	})

	// Set up stream handler for welcome protocol
	bn.host.SetStreamHandler(protocol.ID("/blockchain/welcome/1.0.0"), func(s network.Stream) {
		defer s.Close()

		var msg struct {
			Type string `json:"type"`
		}

		if err := json.NewDecoder(s).Decode(&msg); err != nil {
			log.Printf("❌ Failed to decode welcome request: %v", err)
			return
		}

		if msg.Type == "REQUEST_WELCOME" {
			bn.sendWelcomeMessage(s.Conn().RemotePeer())
		}
	})

	// Start periodic peer status updates
	go bn.updatePeerStatus()

	log.Printf("✨ Bootstrap node is running on port %d", bn.config.ListenPort)
	return nil
}

// sendWelcomeMessage sends a welcome message to a newly connected peer
func (bn *BootstrapNode) sendWelcomeMessage(peerID peer.ID) {
	stream, err := bn.host.NewStream(context.Background(), peerID, "/blockchain/welcome/1.0.0")
	if err != nil {
		log.Printf("❌ Failed to create welcome stream: %v", err)
		return
	}
	defer stream.Close()

	// Get peer addresses
	addrs := bn.host.Peerstore().Addrs(peerID)
	addrStrings := make([]string, len(addrs))
	for i, addr := range addrs {
		addrStrings[i] = addr.String()
	}

	welcomeMsg := struct {
		PeerID    string   `json:"peer_id"`
		Addresses []string `json:"addresses"`
	}{
		PeerID:    bn.host.ID().String(),
		Addresses: addrStrings,
	}

	if err := json.NewEncoder(stream).Encode(welcomeMsg); err != nil {
		log.Printf("❌ Failed to send welcome message: %v", err)
		return
	}

	log.Printf("📨 Sent welcome message to peer %s", peerID)
}

// updatePeerStatus periodically logs the current status of connected peers
func (bn *BootstrapNode) updatePeerStatus() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			log.Printf("\n📊 Bootstrap Node Status:")
			log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			log.Printf("• Connected Peers: %d", len(bn.peers))
			log.Printf("• Listening Addresses:")
			for _, addr := range bn.host.Addrs() {
				log.Printf("  ‣ %s/p2p/%s", addr, bn.host.ID())
			}
			log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		}
	}
}

// Helper function to load or create node identity
func loadOrCreateIdentity(dataDir string) (crypto.PrivKey, error) {
	keyFile := filepath.Join(dataDir, "node.key")

	// Try to load existing key
	if data, err := os.ReadFile(keyFile); err == nil {
		return crypto.UnmarshalPrivateKey(data)
	}

	// Generate new key
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, err
	}

	// Save key
	keyBytes, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(keyFile, keyBytes, 0600); err != nil {
		return nil, err
	}

	return priv, nil
}

func (bn *BootstrapNode) startNetworkServices() error {
	// Set stream handlers
	bn.host.SetStreamHandler(protocol.ID(BlockchainNamespace+"/discovery"), bn.handlePeerDiscovery)
	bn.host.SetStreamHandler(protocol.ID(BlockchainNamespace+"/relay"), bn.handleRelay)
	bn.host.SetStreamHandler(protocol.ID(BlockchainNamespace+"/status"), bn.handleStatus)

	// Bootstrap DHT
	if err := bn.dht.Bootstrap(bn.ctx); err != nil {
		return fmt.Errorf("failed to bootstrap DHT: %v", err)
	}

	// Start NAT traversal if enabled
	if bn.config.EnableNAT {
		if err := bn.setupNAT(); err != nil {
			log.Printf("⚠️ NAT setup failed: %v", err)
		}
	}

	return nil
}

func (bn *BootstrapNode) startPeriodicTasks() {
	// Start peer discovery
	go bn.runPeerDiscovery()

	// Start metrics collection if enabled
	if bn.metrics != nil {
		go bn.collectMetrics()
	}

	// Start peer score updates
	go bn.updatePeerScores()
}

func (bn *BootstrapNode) runPeerDiscovery() {
	ticker := time.NewTicker(PeerDiscoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			bn.discoverPeers()
		}
	}
}

func (bn *BootstrapNode) discoverPeers() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			// Get peers from DHT routing table
			peers := bn.dht.RoutingTable().ListPeers()
			for _, peerID := range peers {
				// Skip if we already know this peer
				bn.peersMutex.RLock()
				if _, exists := bn.peers[peerID]; exists {
					bn.peersMutex.RUnlock()
					continue
				}
				bn.peersMutex.RUnlock()

				// Try to connect to peer
				if err := bn.node.ConnectToPeer(peerID.String()); err != nil {
					log.Printf("Failed to connect to peer %s: %v", peerID, err)
					continue
				}

				// Add to known peers
				bn.peersMutex.Lock()
				bn.peers[peerID] = peerID
				bn.peersMutex.Unlock()
				log.Printf("Connected to new peer: %s", peerID)
			}
		}
	}
}

func (bn *BootstrapNode) handleNewPeer(p peer.AddrInfo) error {
	// Skip if we already know this peer
	if bn.peerStore.HasPeer(p.ID) {
		return nil
	}

	// Connect to the peer
	if err := bn.host.Connect(bn.ctx, p); err != nil {
		return fmt.Errorf("failed to connect to peer: %v", err)
	}

	// Add to peer store
	bn.peerStore.AddPeer(p)

	// Update metrics
	bn.metrics.IncrementPeerCount()

	log.Printf("✅ New peer connected: %s", p.ID.String())
	return nil
}

// Add more methods for handling relay, status, and other functionality...

// FindPeer finds a peer in the network using the DHT
func (bn *BootstrapNode) FindPeer(id peer.ID) (peer.AddrInfo, error) {
	return bn.dht.FindPeer(bn.ctx, id)
}

// Provide announces that this node can provide a value for the given key
func (bn *BootstrapNode) Provide(key string) error {
	keyBytes := []byte(key)
	c := cid.NewCidV1(cid.Raw, keyBytes)
	return bn.dht.Provide(bn.ctx, c, true)
}

// FindProviders finds nodes that can provide a value for the given key
func (bn *BootstrapNode) FindProviders(key string) (<-chan peer.AddrInfo, error) {
	keyBytes := []byte(key)
	c := cid.NewCidV1(cid.Raw, keyBytes)
	return bn.dht.FindProvidersAsync(bn.ctx, c, 20), nil
}

// Stop stops the bootstrap node
func (bn *BootstrapNode) Stop() {
	bn.mu.Lock()
	defer bn.mu.Unlock()

	if !bn.started {
		return
	}

	bn.cancel()

	if err := bn.dht.Close(); err != nil {
		log.Printf("Error closing DHT: %v", err)
	}

	if err := bn.host.Close(); err != nil {
		log.Printf("Error closing host: %v", err)
	}

	for _, listener := range bn.listeners {
		if err := listener.Close(); err != nil {
			log.Printf("Error closing listener: %v", err)
		}
	}

	bn.started = false
	log.Println("Bootstrap node stopped")
}

// PersistentPeerStore manages persistent storage of peer information
type PersistentPeerStore struct {
	filename string
	peers    map[peer.ID]peer.AddrInfo
	mu       sync.RWMutex
	logger   *log.Logger
}

// NewPersistentPeerStore creates a new persistent peer store
func NewPersistentPeerStore(filename string) (*PersistentPeerStore, error) {
	store := &PersistentPeerStore{
		filename: filename,
		peers:    make(map[peer.ID]peer.AddrInfo),
		logger:   log.New(os.Stdout, "📡 PeerStore: ", log.Ltime),
	}

	// Load existing peers from file
	if err := store.load(); err != nil {
		return nil, fmt.Errorf("failed to load peer store: %w", err)
	}

	return store, nil
}

// load reads peer information from persistent storage
func (ps *PersistentPeerStore) load() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.logger.Printf("Loading peers from: %s", ps.filename)

	// Check if file exists before attempting to read
	_, err := os.Stat(ps.filename)
	if os.IsNotExist(err) {
		ps.logger.Printf("📝 Creating new peer store")
		return nil
	}

	data, err := os.ReadFile(ps.filename)
	if err != nil {
		ps.logger.Printf("❌ Error reading peer store: %v", err)
		return err
	}

	// Handle empty file case
	if len(data) == 0 {
		ps.logger.Printf("ℹ️  Peer store file is empty")
		return nil
	}

	var loadedPeers map[string]peer.AddrInfo
	if err := json.Unmarshal(data, &loadedPeers); err != nil {
		ps.logger.Printf("❌ Error parsing peer data: %v", err)
		return err
	}

	for _, addrInfo := range loadedPeers {
		ps.peers[addrInfo.ID] = addrInfo
	}

	ps.logger.Printf("✅ Loaded %d peers", len(ps.peers))
	return nil
}

// save writes peer information to persistent storage
func (ps *PersistentPeerStore) save() {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	// Prevent saving if no peers
	if len(ps.peers) == 0 {
		return
	}

	data, err := json.MarshalIndent(ps.peers, "", "  ")
	if err != nil {
		ps.logger.Printf("Error marshaling peer store: %v", err)
		return
	}

	// Use WriteFile with exclusive write mode
	if err := os.WriteFile(ps.filename, data, 0600); err != nil {
		ps.logger.Printf("Error saving peer store %s: %v", ps.filename, err)
	} else {
		ps.logger.Printf("Saved %d peers to %s", len(ps.peers), ps.filename)
	}
}

// AddPeer adds a peer to the store
func (ps *PersistentPeerStore) AddPeer(info peer.AddrInfo) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Validate peer info before adding
	if info.ID == "" {
		ps.logger.Printf("Attempted to add peer with empty ID")
		return
	}

	ps.peers[info.ID] = info

	// Save asynchronously to prevent blocking
	go ps.save()
}

// GetPeers returns all stored peers
func (ps *PersistentPeerStore) GetPeers() []peer.AddrInfo {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	peers := make([]peer.AddrInfo, 0, len(ps.peers))
	for _, peerInfo := range ps.peers {
		peers = append(peers, peerInfo)
	}
	return peers
}

// Example usage function for running a bootstrap node
func RunBootstrapNode() {
	// Create bootstrap node configuration
	config := &BootstrapNodeConfig{
		ListenPort:         BlockchainDefaultPort,
		PublicIP:           "",
		KeyFile:            "",
		EnableRelay:        false,
		EnableNAT:          true,
		EnablePeerExchange: true,
		// Optional: Add seed nodes if known
		// SeedNodes: []peer.AddrInfo{
		//     {ID: peerID1, Addrs: []multiaddr.Multiaddr{addr1}},
		//     {ID: peerID2, Addrs: []multiaddr.Multiaddr{addr2}},
		// },
	}

	// Create bootstrap node
	bootstrapNode, err := NewBootstrapNode(config)
	if err != nil {
		log.Fatalf("Failed to create bootstrap node: %v", err)
	}

	// Start the bootstrap node
	if err := bootstrapNode.Start(); err != nil {
		log.Fatalf("Failed to start bootstrap node: %v", err)
	}

	// Keep the bootstrap node running
	select {}
}

// Add these handler methods
func (bn *BootstrapNode) handleRelay(stream network.Stream) {
	defer stream.Close()

	// Read relay request
	var req RelayRequest
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		log.Printf("Failed to decode relay request: %v", err)
		return
	}

	// Parse target peer ID
	targetPID, err := peer.Decode(req.TargetPeer)
	if err != nil {
		log.Printf("Invalid target peer ID: %v", err)
		return
	}

	// Check if target peer is connected
	if bn.host.Network().Connectedness(targetPID) != network.Connected {
		response := RelayResponse{
			Success: false,
			Error:   "target peer not connected",
		}
		json.NewEncoder(stream).Encode(response)
		return
	}

	// Create stream to target peer
	targetStream, err := bn.host.NewStream(bn.ctx, targetPID, protocol.ID("/blockchain/relay/1.0.0"))
	if err != nil {
		log.Printf("Failed to create stream to target peer: %v", err)
		return
	}
	defer targetStream.Close()

	// Forward the data
	if _, err := targetStream.Write(req.Data); err != nil {
		log.Printf("Failed to relay data: %v", err)
		return
	}

	// Send success response
	response := RelayResponse{
		Success: true,
	}
	json.NewEncoder(stream).Encode(response)
}

func (bn *BootstrapNode) handleStatus(stream network.Stream) {
	defer stream.Close()

	// Read status request
	var req StatusRequest
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		log.Printf("Failed to decode status request: %v", err)
		return
	}

	// Prepare status response
	status := NodeStatus{
		PeerCount:    len(bn.host.Network().Peers()),
		Uptime:       time.Since(bn.startTime),
		Version:      "1.0.0",
		NetworkState: bn.getNetworkState(),
	}

	// Send response
	if err := json.NewEncoder(stream).Encode(status); err != nil {
		log.Printf("Failed to send status response: %v", err)
		return
	}
}

// Add these types for request/response handling
type RelayRequest struct {
	TargetPeer string
	Data       []byte
}

type RelayResponse struct {
	Success bool
	Error   string
}

type StatusRequest struct {
	IncludeMetrics bool
}

type NodeStatus struct {
	PeerCount    int
	Uptime       time.Duration
	Version      string
	NetworkState string
}

// setupNAT configures NAT traversal for the node
// setupNAT configures NAT traversal using modern libp2p methods
func (bn *BootstrapNode) setupNAT() error {
	// Get the list of external addresses
	externalAddrs := bn.host.Addrs()
	log.Printf("🌐 External addresses: %v", externalAddrs)

	// If NAT is enabled, libp2p will automatically handle NAT traversal
	// using the AutoNAT service and other mechanisms.
	// You can check if the host is behind a NAT by looking at the addresses.
	if len(externalAddrs) == 0 {
		log.Printf("⚠️ Node appears to be behind a NAT with no external addresses")
	} else {
		log.Printf("✅ Node has external addresses: %v", externalAddrs)
	}

	// If you need to explicitly handle NAT traversal, you can use the AutoNAT service.
	// However, this is usually not necessary as libp2p handles it automatically.
	return nil
}

// Helper function to get public IP
func getPublicIP() (string, error) {
	var publicIP string

	done := make(chan bool)

	// Try multiple STUN servers until we get an IPv4
	stunServers := []string{
		"stun.l.google.com:19302",
		"stun1.l.google.com:19302",
		"stun.stunprotocol.org:3478",
	}

	for _, server := range stunServers {
		c, err := stun.Dial("udp4", server) // Force IPv4
		if err != nil {
			continue
		}
		defer c.Close()

		message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

		c.Start(message, func(res stun.Event) {
			if res.Error != nil {
				err = res.Error
				done <- true
				return
			}

			var xorAddr stun.XORMappedAddress
			if getErr := xorAddr.GetFrom(res.Message); getErr != nil {
				err = getErr
				done <- true
				return
			}

			// Verify we got an IPv4
			if ip4 := xorAddr.IP.To4(); ip4 != nil {
				publicIP = ip4.String()
				done <- true
			} else {
				err = fmt.Errorf("got IPv6 address, want IPv4")
				done <- true
			}
		})

		<-done // Wait for STUN response

		if err == nil && publicIP != "" {
			return publicIP, nil
		}
	}

	if publicIP == "" {
		return "", fmt.Errorf("could not get public IPv4 address from any STUN server")
	}

	return publicIP, nil
}

// updatePeerScores periodically updates peer scores based on their performance
func (bn *BootstrapNode) updatePeerScores() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			bn.mu.Lock()
			for peerID := range bn.peerScores {
				score := bn.peerScores[peerID]

				// Update connection uptime
				if bn.host.Network().Connectedness(peerID) == network.Connected {
					score.ConnectionUptime += 1
				}

				// Update response time from metrics
				if latency, ok := bn.metrics.Latency[peerID]; ok {
					score.ResponseTime = float64(latency.Milliseconds())
				}

				// Update bandwidth usage
				if bandwidth, ok := bn.metrics.BandwidthUsage[peerID]; ok {
					score.BandwidthUsage = bandwidth
				}

				score.LastUpdated = time.Now()
				bn.peerScores[peerID] = score
			}
			bn.mu.Unlock()
		}
	}
}

// Add these types at the top of the file
type PeerDiscoveryRequest struct {
	MaxPeers      int      `json:"max_peers"`
	ExcludedPeers []string `json:"excluded_peers"`
}

type PeerDiscoveryResponse struct {
	Success bool            `json:"success"`
	Peers   []peer.AddrInfo `json:"peers"`
}

// Add methods for NetworkMetrics
func (nm *NetworkMetrics) IncrementPeerCount() {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.PeerCount++
}

// Add methods for PersistentPeerStore
func (ps *PersistentPeerStore) HasPeer(p peer.ID) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	_, exists := ps.peers[p]
	return exists
}

// setupPortMapping configures port forwarding using UPnP
// setupPortMapping configures port forwarding using UPnP

// Add the broadcast helper method
func (bn *BootstrapNode) broadcastToOtherPeers(source peer.ID, msgType string, data interface{}) {
	bn.peersMutex.RLock()
	defer bn.peersMutex.RUnlock()

	for peerID := range bn.peers {
		// Skip the source peer
		if peerID == source {
			continue
		}

		// Create new stream
		protocolID := protocol.ID(fmt.Sprintf("/blockchain/%s/1.0.0", msgType))
		stream, err := bn.host.NewStream(bn.ctx, peerID, protocolID)
		if err != nil {
			log.Printf("Failed to create stream to peer %s: %v", peerID, err)
			continue
		}

		// Send the data
		if err := json.NewEncoder(stream).Encode(data); err != nil {
			log.Printf("Failed to send data to peer %s: %v", peerID, err)
			stream.Close()
			continue
		}

		stream.Close()

		// Update metrics
		bn.metrics.mu.Lock()
		bn.metrics.BandwidthUsage[peerID]++
		bn.metrics.mu.Unlock()
	}
}

// getNetworkState determines the current network state
func (bn *BootstrapNode) getNetworkState() string {
	peerCount := len(bn.host.Network().Peers())

	switch {
	case peerCount >= 10:
		return "healthy"
	case peerCount >= 5:
		return "stable"
	case peerCount > 0:
		return "developing"
	default:
		return "starting"
	}
}

// Add new methods for heartbeat handling
func (bn *BootstrapNode) handleHeartbeat(stream network.Stream) {
	defer stream.Close()

	var heartbeat Heartbeat
	if err := json.NewDecoder(stream).Decode(&heartbeat); err != nil {
		log.Printf("Error decoding heartbeat: %v", err)
		return
	}

	peerID := stream.Conn().RemotePeer()
	bn.heartbeatMu.Lock()
	bn.lastHeartbeat[peerID] = time.Now()
	bn.heartbeatMu.Unlock()

	// Send acknowledgment
	if err := json.NewEncoder(stream).Encode(Heartbeat{
		Timestamp: time.Now().Unix(),
		NodeID:    bn.host.ID().String(),
	}); err != nil {
		log.Printf("Error sending heartbeat ack: %v", err)
	}
}

func (bn *BootstrapNode) monitorHeartbeats() {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			bn.checkPeerHeartbeats()
		}
	}
}

func (bn *BootstrapNode) checkPeerHeartbeats() {
	bn.heartbeatMu.Lock()
	defer bn.heartbeatMu.Unlock()

	now := time.Now()
	for peerID, lastBeat := range bn.lastHeartbeat {
		if now.Sub(lastBeat) > HeartbeatTimeout {
			log.Printf("⚠️ No heartbeat from peer %s for %v, considering disconnected",
				peerID.String(), HeartbeatTimeout)
			delete(bn.lastHeartbeat, peerID)

			// Try to close the connection to trigger reconnection
			if conn := bn.host.Network().ConnsToPeer(peerID); len(conn) > 0 {
				for _, c := range conn {
					c.Close()
				}
			}
		}
	}
}

// Add method to check peer health
func (bn *BootstrapNode) IsPeerHealthy(peerID peer.ID) bool {
	bn.heartbeatMu.RLock()
	defer bn.heartbeatMu.RUnlock()

	lastBeat, exists := bn.lastHeartbeat[peerID]
	if !exists {
		return false
	}

	return time.Since(lastBeat) <= HeartbeatTimeout
}

type Bootnode struct {
	host        host.Host
	peerManager *PeerManager
	natManager  *NATManager
	config      *NetworkConfig
	ctx         context.Context
	cancel      context.CancelFunc
	connected   bool
	mutex       sync.RWMutex
}

// NewBootnode creates a new bootnode instance
func NewBootnode(config *NetworkConfig) (*Bootnode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create libp2p host
	h, err := createHost(config)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create host: %w", err)
	}

	// Create peer manager
	peerManager := NewPeerManager(h)

	// Create NAT manager
	natManager := NewNATManager(h, config)

	return &Bootnode{
		host:        h,
		peerManager: peerManager,
		natManager:  natManager,
		config:      config,
		ctx:         ctx,
		cancel:      cancel,
		connected:   false,
	}, nil
}

// Start initializes and starts the bootnode
func (bn *Bootnode) Start() error {
	log.Printf("Starting bootnode...")

	// Start NAT traversal
	if err := bn.natManager.Start(); err != nil {
		log.Printf("Warning: NAT traversal failed: %v", err)
	}

	// Start peer discovery
	if err := bn.startPeerDiscovery(); err != nil {
		return fmt.Errorf("failed to start peer discovery: %w", err)
	}

	// Start heartbeat
	go bn.startHeartbeat()

	// Start connection monitoring
	go bn.monitorConnections()

	bn.connected = true
	log.Printf("Bootnode started successfully")
	return nil
}

// startPeerDiscovery initializes peer discovery mechanisms
func (bn *Bootnode) startPeerDiscovery() error {
	// Start DHT
	dht, err := dht.New(bn.ctx, bn.host)
	if err != nil {
		return fmt.Errorf("failed to create DHT: %w", err)
	}

	// Bootstrap the DHT
	if err = dht.Bootstrap(bn.ctx); err != nil {
		return fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	// Start peer discovery service
	discovery := discovery.NewRoutingDiscovery(dht)
	_, err = discovery.Advertise(bn.ctx, "blockchain-bootnode")
	if err != nil {
		return fmt.Errorf("failed to advertise bootnode: %w", err)
	}

	// Start peer discovery
	go bn.discoverPeers(discovery)

	return nil
}

// discoverPeers continuously discovers and connects to peers
func (bn *Bootnode) discoverPeers(discovery *discovery.RoutingDiscovery) {
	for {
		select {
		case <-bn.ctx.Done():
			return
		default:
			peers, err := discovery.FindPeers(bn.ctx, "blockchain-bootnode")
			if err != nil {
				log.Printf("Error finding peers: %v", err)
				time.Sleep(RetryDelay)
				continue
			}

			for p := range peers {
				if p.ID == bn.host.ID() {
					continue
				}

				// Attempt connection with retries
				for i := 0; i < MaxRetries; i++ {
					err := bn.connectToPeer(p)
					if err == nil {
						break
					}
					log.Printf("Failed to connect to peer %s (attempt %d/%d): %v",
						p.ID, i+1, MaxRetries, err)
					time.Sleep(RetryDelay)
				}
			}
		}
	}
}

// connectToPeer attempts to connect to a peer
func (bn *Bootnode) connectToPeer(p peer.AddrInfo) error {
	if bn.peerManager.IsBlacklisted(p.ID) {
		return fmt.Errorf("peer is blacklisted")
	}

	ctx, cancel := context.WithTimeout(bn.ctx, ConnectionTimeout)
	defer cancel()

	err := bn.host.Connect(ctx, p)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Verify connection
	if len(bn.host.Network().ConnsToPeer(p.ID)) == 0 {
		return fmt.Errorf("connection verification failed")
	}

	// Add peer to manager
	bn.peerManager.AddPeer(p.ID)
	log.Printf("Connected to peer: %s", p.ID)
	return nil
}

// sendToPeer sends a message to a specific peer
func (bn *Bootnode) sendToPeer(peerID peer.ID, message string) error {
	stream, err := bn.host.NewStream(context.Background(), peerID, "/blockchain/1.0.0")
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}
	defer stream.Close()

	_, err = stream.Write([]byte(message))
	return err
}

// startHeartbeat sends periodic heartbeats to connected peers
func (bn *Bootnode) startHeartbeat() {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			bn.sendHeartbeat()
		}
	}
}

// sendHeartbeat sends heartbeat messages to all connected peers
func (bn *Bootnode) sendHeartbeat() {
	peers := bn.peerManager.GetConnectedPeers()
	for _, peerID := range peers {
		if err := bn.sendToPeer(peerID, "HEARTBEAT"); err != nil {
			log.Printf("Failed to send heartbeat to %s: %v", peerID, err)
		}
	}
}

// monitorConnections monitors and maintains peer connections
func (bn *Bootnode) monitorConnections() {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			bn.checkConnections()
		}
	}
}

// checkConnections verifies and maintains peer connections
func (bn *Bootnode) checkConnections() {
	peers := bn.peerManager.GetConnectedPeers()
	for _, peerID := range peers {
		if len(bn.host.Network().ConnsToPeer(peerID)) == 0 {
			log.Printf("Peer %s disconnected, removing from peer list", peerID)
			bn.peerManager.RemovePeer(peerID)
		}
	}
}

// Stop gracefully shuts down the bootnode
func (bn *Bootnode) Stop() error {
	log.Printf("Stopping bootnode...")

	bn.cancel()
	bn.connected = false

	if err := bn.host.Close(); err != nil {
		return fmt.Errorf("failed to close host: %w", err)
	}

	log.Printf("Bootnode stopped successfully")
	return nil
}
