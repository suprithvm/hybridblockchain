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
)

// BootstrapNodeConfig represents the configuration for a bootstrap node
type BootstrapNodeConfig struct {
	ListenPort         int
	PublicIP           string
	KeyFile            string
	EnableRelay        bool
	EnableNAT          bool
	EnablePeerExchange bool
	SeedNodes          []peer.AddrInfo
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

// NewBootstrapNode creates a comprehensive bootstrap node
func NewBootstrapNode(ctx context.Context, config *BootstrapNodeConfig) (*BootstrapNode, error) {
	log.Printf("Initializing bootnode with config: %+v\n", config)

	ctx, cancel := context.WithCancel(ctx)

	privKey, err := loadOrCreatePrivateKey(config)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to load/create private key: %w", err)
	}

	// Get peer ID from private key
	peerID, err := peer.IDFromPrivateKey(privKey)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get peer ID: %w", err)
	}

	// Create multiaddress for listening
	listenAddr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", config.ListenPort))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create listen multiaddr: %w", err)
	}

	// Create libp2p host with options
	hostOpts := []libp2p.Option{
		libp2p.ListenAddrs(listenAddr),
		libp2p.Identity(privKey),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
	}

	// Add NAT traversal if enabled
	if config.EnableNAT {
		hostOpts = append(hostOpts, libp2p.NATPortMap())
	}

	host, err := libp2p.New(hostOpts...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create libp2p host: %v", err)
	}

	// Print node addresses
	log.Printf("Bootstrap node started with ID: %s", peerID.String())
	log.Printf("Local addresses: %v", host.Addrs())
	if config.PublicIP != "" {
		log.Printf("Public address: %s", config.GetMultiaddr(peerID))
	}

	kdht, err := dht.New(ctx, host, dht.Mode(dht.ModeServer))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	peerStoreFile := filepath.Join(os.TempDir(), "blockchain_bootnode_peers.json")
	peerStore := NewPersistentPeerStore(peerStoreFile)

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
func loadOrCreatePrivateKey(config *BootstrapNodeConfig) (crypto.PrivKey, error) {
	if config.KeyFile == "" {
		config.KeyFile = "bootnode.key"
	}

	// Try to load existing key
	if keyData, err := os.ReadFile(config.KeyFile); err == nil {
		return crypto.UnmarshalPrivateKey(keyData)
	}

	// Generate new key if none exists
	priv, _, err := crypto.GenerateKeyPair(
		crypto.Ed25519, // Using Ed25519 for better security
		-1,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Save the key
	keyBytes, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := os.WriteFile(config.KeyFile, keyBytes, 0600); err != nil {
		return nil, fmt.Errorf("failed to save private key: %w", err)
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

	// Process the block announcement
	// Implementation depends on your blockchain's block structure
}

// handleTransaction processes incoming transactions
func (bn *BootstrapNode) handleTransaction(stream network.Stream) {
	defer stream.Close()

	// Enforce rate limits
	if err := bn.enforceRateLimits(stream.Conn().RemotePeer(), "tx"); err != nil {
		log.Printf("Rate limit exceeded: %v", err)
		return
	}

	// Process the transaction
	// Implementation depends on your blockchain's transaction structure
}

// handlePeerDiscovery processes peer discovery requests
func (bn *BootstrapNode) handlePeerDiscovery(stream network.Stream) {
	defer stream.Close()

	// Enforce rate limits
	if err := bn.enforceRateLimits(stream.Conn().RemotePeer(), "discovery"); err != nil {
		log.Printf("Rate limit exceeded: %v", err)
		return
	}

	// Share known peers
	peers := bn.peerStore.GetPeers()
	if err := json.NewEncoder(stream).Encode(peers); err != nil {
		log.Printf("Failed to send peer list: %v", err)
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
			// Update connection metrics
			for _, peer := range bn.host.Network().Peers() {
				conns := bn.host.Network().ConnsToPeer(peer)
				if len(conns) > 0 {
					// If we have an active connection, consider it a successful connection
					bn.metrics.ConnectionSuccess[peer] = 1.0
				} else {
					bn.metrics.ConnectionSuccess[peer] = 0.0
				}
			}
			bn.mu.Unlock()
		}
	}
}

// Start starts the bootstrap node and its DHT
func (bn *BootstrapNode) Start() error {
	bn.mu.Lock()
	defer bn.mu.Unlock()

	if bn.started {
		return fmt.Errorf("bootstrap node already started")
	}

	if err := bn.dht.Bootstrap(bn.ctx); err != nil {
		return fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	go bn.startPeriodicTasks()
	go bn.collectMetrics()

	bn.started = true
	log.Printf("Bootstrap node started. ID: %s", bn.host.ID())
	for _, addr := range bn.host.Addrs() {
		log.Printf("Listening on: %s/p2p/%s", addr, bn.host.ID())
	}

	return nil
}

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

// startPeriodicTasks starts periodic maintenance tasks
func (bn *BootstrapNode) startPeriodicTasks() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-bn.ctx.Done():
			return
		case <-ticker.C:
			if err := bn.dht.Bootstrap(bn.ctx); err != nil {
				log.Printf("Error refreshing DHT: %v", err)
			}
		}
	}
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
func NewPersistentPeerStore(filename string) *PersistentPeerStore {
	// If filename is not an absolute path, make it relative to current directory
	if !filepath.IsAbs(filename) {
		currentDir, err := os.Getwd()
		if err != nil {
			currentDir = "."
		}
		filename = filepath.Join(currentDir, filename)
	}

	store := &PersistentPeerStore{
		filename: filename,
		peers:    make(map[peer.ID]peer.AddrInfo),
		logger:   log.New(os.Stdout, "PersistentPeerStore: ", log.Ldate|log.Ltime|log.Lshortfile),
	}

	store.logger.Printf("Initializing peer store with file: %s", filename)

	// Load peers with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	loadDone := make(chan struct{})
	go func() {
		store.load()
		close(loadDone)
	}()

	select {
	case <-loadDone:
		store.logger.Printf("Peer store load completed")
	case <-ctx.Done():
		store.logger.Printf("Peer store load timed out")
	}

	return store
}

// load reads peer information from persistent storage
func (ps *PersistentPeerStore) load() {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.logger.Printf("Attempting to load peers from %s", ps.filename)

	// Check if file exists before attempting to read
	_, err := os.Stat(ps.filename)
	if os.IsNotExist(err) {
		ps.logger.Printf("Peer store file %s does not exist. Creating new store.", ps.filename)
		return
	}

	data, err := os.ReadFile(ps.filename)
	if err != nil {
		ps.logger.Printf("Error reading peer store %s: %v", ps.filename, err)
		return
	}

	// Handle empty file case
	if len(data) == 0 {
		ps.logger.Printf("Peer store file %s is empty", ps.filename)
		return
	}

	var loadedPeers map[string]peer.AddrInfo
	if err := json.Unmarshal(data, &loadedPeers); err != nil {
		ps.logger.Printf("Error unmarshaling peer store %s: %v", ps.filename, err)
		return
	}

	for _, addrInfo := range loadedPeers {
		ps.peers[addrInfo.ID] = addrInfo
	}

	ps.logger.Printf("Loaded %d peers from %s", len(ps.peers), ps.filename)
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
	bootstrapNode, err := NewBootstrapNode(context.Background(), config)
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
