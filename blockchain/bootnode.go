package blockchain

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/pion/stun"

	"github.com/multiformats/go-multiaddr"
)

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
	host        host.Host
	dht         *dht.IpfsDHT
	routing     routing.Routing
	ctx         context.Context
	cancel      context.CancelFunc
	peerStore   *PersistentPeerStore
	config      *BootstrapNodeConfig
	mu          sync.RWMutex
	started     bool
	listeners   []net.Listener
	peerScores  map[peer.ID]*PeerScore
	metrics     *NetworkMetrics
	rateLimiter *RateLimiter
	protocols   map[string]network.StreamHandler
	peers       map[peer.ID]peer.AddrInfo
	peersMutex  sync.RWMutex
	dataDir     string
	identity    crypto.PrivKey
	startTime   time.Time
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
type Notifier struct{}

// Connected is called when a new peer connects
func (n *Notifier) Connected(net network.Network, conn network.Conn) {
	log.Printf("New peer connected: %s", conn.RemotePeer().String())
}

// Disconnected is called when a peer disconnects
func (n *Notifier) Disconnected(net network.Network, conn network.Conn) {
	log.Printf("Peer disconnected: %s", conn.RemotePeer().String())
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
		protocols: make(map[string]network.StreamHandler),
		identity:  privKey,
		startTime: time.Now(),
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

// initializeProtocols sets up protocol handlers for different message types
func (bn *BootstrapNode) initializeProtocols() {
	// Block announcement protocol
	bn.protocols["/blockchain/block/1.0.0"] = func(stream network.Stream) {
		bn.handleBlockAnnouncement(stream)
	}

	// Transaction propagation protocol
	bn.protocols["/blockchain/tx/1.0.0"] = func(stream network.Stream) {
		bn.handleTransaction(stream)
	}

	// Peer discovery protocol
	bn.protocols["/blockchain/discovery/1.0.0"] = func(stream network.Stream) {
		bn.handlePeerDiscovery(stream)
	}
	// In initializeProtocols()
	bn.protocols["/blockchain/relay/1.0.0"] = bn.handleRelay
	bn.protocols["/blockchain/status/1.0.0"] = bn.handleStatus

	// Register all protocols
	for proto, handler := range bn.protocols {
		bn.host.SetStreamHandler(protocol.ID(proto), handler)
	}
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
	log.Printf("\n🚀 Initializing Bootstrap Node")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Print node status periodically
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for range ticker.C {
			peers := bn.host.Network().Peers()
			log.Printf("\n📊 Network Status Update:")
			log.Printf("   • Connected Peers: %d", len(peers))
			log.Printf("   • Active Connections: %d", len(bn.host.Network().Conns()))
			for _, p := range peers {
				conns := bn.host.Network().ConnsToPeer(p)
				for _, c := range conns {
					log.Printf("   • Peer: %s", p.String())
					log.Printf("     ‣ Address: %s", c.RemoteMultiaddr())
					log.Printf("     ‣ Direction: %s", c.Stat().Direction)
				}
			}
			log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		}
	}()

	return nil
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
	// Find peers in the network
	peerChan := bn.dht.FindProvidersAsync(bn.ctx, cid.NewCidV1(cid.Raw, []byte("blockchain")), 20)
	var peers []peer.AddrInfo
	for p := range peerChan {
		peers = append(peers, p)
	}

	// Process discovered peers
	for _, p := range peers {
		if err := bn.handleNewPeer(p); err != nil {
			log.Printf("⚠️ Failed to handle peer %s: %v", p.ID, err)
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
	Message string          `json:"message"`
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
