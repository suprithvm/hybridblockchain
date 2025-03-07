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
	"sync"
	"time"

	"blockchain-core/blockchain/gas"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

// Protocol IDs
const (
	BlockProtocolID         = "/blockchain/blocks/1.0.0"
	TransactionProtocolID   = "/blockchain/txs/1.0.0"
	HeartbeatProtocolID     = "/blockchain/heartbeat/1.0.0"
	HeartbeatInterval       = 30 * time.Second
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

// Node represents a blockchain network node
type Node struct {
	Host              host.Host
	DHT               *dht.IpfsDHT
	PeerManager       *PeerManager
	Blockchain        *Blockchain
	Mempool           *Mempool
	UTXOSet           *UTXOPool
	StakePool         *StakePool
	config            *NetworkConfig
	ctx               context.Context
	cancel            context.CancelFunc
	keepAliveCtx      context.Context
	keepAliveCancel   context.CancelFunc
	UTXOPool          *UTXOPool
	isSyncing         bool
	syncMu            sync.RWMutex
	gasModel          *gas.GasModel
	accountManager    *AccountManager
	P2PPort           int
	RPCPort           int
	NetworkPath       string
	ChainID           uint64
	NetworkID         string
	wallet            *Wallet
	isRunning         bool
	runningMu         sync.RWMutex
	knownPeers        map[peer.ID]bool
	PubSub            *pubsub.PubSub
	validatorProtocol *ValidatorProtocol
	bootstrapNodes    map[peer.ID]bool
	wg                sync.WaitGroup
}

// Add getter method for gas model
func (n *Node) GetGasModel() *gas.GasModel {
	return n.gasModel
}

// NewNode creates a new blockchain node
func NewNode(config *NetworkConfig) (*Node, error) {
	ctx, cancel := context.WithCancel(context.Background())
	keepAliveCtx, keepAliveCancel := context.WithCancel(ctx)

	if err := config.ValidateConfig(); err != nil {
		cancel()
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// If no bootstrap nodes specified, try to read from bootnode.addr file
	if len(config.BootstrapNodes) == 0 {
		if addr, err := os.ReadFile("bootnode.addr"); err == nil {
			config.BootstrapNodes = []string{string(addr)}
		}
	}

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
		cancel()
		return nil, fmt.Errorf("failed to create host: %w", err)
	}

	// Initialize pubsub
	ps, err := pubsub.NewGossipSub(ctx, host)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create pubsub: %w", err)
	}

	// Create the node instance first
	n := &Node{
		Host:           host,
		config:         config,
		ctx:            ctx,
		cancel:         cancel,
		knownPeers:     make(map[peer.ID]bool),
		PubSub:         ps,
		bootstrapNodes: make(map[peer.ID]bool),
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

	// Connect to bootstrap nodes if provided
	if len(config.BootstrapNodes) > 0 {
		log.Printf("🔄 Connecting to bootstrap nodes:")
		n.ConnectToBootstrapNodes(ctx)
	}

	// Create DHT with appropriate mode
	dhtOpts := []dht.Option{
		dht.ProtocolPrefix("/blockchain"),
		dht.Validator(blankValidator{}),
	}
	if config.DHTServerMode {
		dhtOpts = append(dhtOpts, dht.Mode(dht.ModeServer))
	}

	dhtInstance, err := dht.New(ctx, host, dhtOpts...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	node := &Node{
		Host:            host,
		DHT:             dhtInstance,
		PeerManager:     NewPeerManager(host),
		config:          config,
		ctx:             ctx,
		cancel:          cancel,
		keepAliveCtx:    keepAliveCtx,
		keepAliveCancel: keepAliveCancel,
		gasModel:        gas.NewGasModel(gas.MinGasPrice, gas.BaseGasLimit),
		accountManager:  NewAccountManager(config.Blockchain.GetDB()),
		P2PPort:         config.P2PPort,
		RPCPort:         config.RPCPort,
		NetworkPath:     config.NetworkPath,
		ChainID:         config.ChainID,
		NetworkID:       config.NetworkID,
		Blockchain:      config.Blockchain,
		wallet:          config.Wallet,
		knownPeers:      make(map[peer.ID]bool),
		PubSub:          ps,
		bootstrapNodes:  make(map[peer.ID]bool),
	}
	node.StakePool = NewStakePool(config.Blockchain)

	// Now initialize mempool with the node reference
	node.Mempool = NewMempool(node)

	// Start keep-alive routine
	go node.startKeepAlive()

	// Start reconnection routine
	go node.maintainConnections()

	// Initialize validator protocol
	node.validatorProtocol = NewValidatorProtocol(node)

	// Subscribe to validator topics
	if err := node.subscribeToValidatorTopics(); err != nil {
		return nil, fmt.Errorf("failed to subscribe to validator topics: %w", err)
	}

	// Initialize bootstrap nodes
	for _, addrStr := range config.BootstrapNodes {
		addr, err := ma.NewMultiaddr(addrStr)
		if err != nil {
			log.Printf("Warning: Failed to parse bootstrap node address %s: %v", addrStr, err)
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			log.Printf("Warning: Failed to get peer info from address %s: %v", addrStr, err)
			continue
		}
		node.bootstrapNodes[info.ID] = true
	}

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
	// First connect to bootstrap nodes
	if err := n.ConnectToBootstrapNodes(n.ctx); err != nil {
		log.Printf("⚠️ Warning: Failed to connect to some bootstrap nodes: %v", err)
	}

	// Wait for initial peer discovery
	time.Sleep(5 * time.Second)

	// Start periodic peer discovery
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-n.ctx.Done():
				return
			case <-ticker.C:
				peers := n.Host.Network().Peers()
				log.Printf("📊 Current connections: %d peers", len(peers))

				for _, peer := range peers {
					peerInfo := n.Host.Peerstore().PeerInfo(peer)
					log.Printf("   • Peer: %s", peer.String())
					for _, addr := range peerInfo.Addrs {
						log.Printf("     ‣ Address: %s", addr)
					}
					if n.Host.Network().Connectedness(peer) == network.Connected {
						log.Printf("     ‣ Direction: %s", n.Host.Network().ConnsToPeer(peer)[0].Stat().Direction)
					}
				}

				// Try to discover new peers
				n.findAndConnectPeers()
				log.Printf("📡 Peer discovery completed")
			}
		}
	}()

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
	n.SetStreamHandler("/blockchain/1.0.0", n.handleBlockStream)
	n.SetStreamHandler("/blockchain/tx/1.0.0", n.handleTransactionStream)
	n.SetStreamHandler("/blockchain/heartbeat/1.0.0", n.handleHeartbeatStream)
	n.SetStreamHandler("/blockchain/sync/1.0.0", n.handleBlockSync)
	n.SetStreamHandler("/blockchain/state/1.0.0", n.handleStateVerification)
	n.SetStreamHandler("/blockchain/fork/1.0.0", n.handleForkResolution)
	n.SetStreamHandler("/blockchain/mempool/1.0.0", n.handleMempoolSync)
	n.SetStreamHandler("/blockchain/validator/1.0.0", n.handleValidatorStream)

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
		if err := json.Unmarshal(msg.Payload, &heartbeat); err != nil {
			n.handleStreamError(s, fmt.Errorf("failed to parse validator heartbeat: %v", err))
			return
		}
		n.validatorProtocol.HandleHeartbeat(heartbeat.ValidatorAddress)
	case "selection":
		var selection ValidatorSelectionMessage
		if err := json.Unmarshal(msg.Payload, &selection); err != nil {
			n.handleStreamError(s, fmt.Errorf("failed to parse validator selection: %v", err))
			return
		}
		// Handle selection message
	case "vote":
		var vote ValidatorVoteMessage
		if err := json.Unmarshal(msg.Payload, &vote); err != nil {
			n.handleStreamError(s, fmt.Errorf("failed to parse validator vote: %v", err))
			return
		}
		// Handle vote message
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
	ticker := time.NewTicker(1 * time.Minute)
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
				if n.PeerManager.NeedMorePeers() {
					if err := n.DiscoverPeers(); err != nil {
						log.Printf("⚠️ Peer discovery failed: %v", err)
					}
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
	// Bootstrap the DHT
	if err := n.DHT.Bootstrap(ctx); err != nil {
		return fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	// Connect to bootstrap peers
	bootstrapPeers := n.DHT.RoutingTable().ListPeers()
	for _, peer := range bootstrapPeers {
		if peer == n.Host.ID() {
			continue
		}

		peerInfo := n.Host.Peerstore().PeerInfo(peer)
		if err := n.Host.Connect(ctx, peerInfo); err != nil {
			log.Printf("Failed to connect to bootstrap peer %s: %v", peer, err)
		}
	}

	return nil
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
	for _, addr := range n.config.BootstrapNodes {
		maddr, err := ma.NewMultiaddr(addr)
		if err != nil {
			log.Printf("Invalid bootstrap address: %s", addr)
			continue
		}

		peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			log.Printf("Failed to parse bootstrap peer info: %s", addr)
			continue
		}

		if err := n.Host.Connect(ctx, *peerInfo); err != nil {
			log.Printf("Failed to connect to bootstrap node %s: %v", addr, err)
			continue
		}

		n.PeerManager.AddPeer(peerInfo.ID)
		log.Printf("Connected to bootstrap node: %s", addr)
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
		if !ValidateBlock(block, previousBlock, block.Header.ValidatedBy, bc.stakePool) {
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
	// Enhance existing protocol
	n.Host.SetStreamHandler("/block/sync/1.0.0", func(s network.Stream) {
		// Add better error handling
		defer func() {
			if err := s.Close(); err != nil {
				log.Printf("Error closing stream: %v", err)
			}
		}()

		// Add fork resolution
		var msg Message
		if err := json.NewDecoder(s).Decode(&msg); err != nil {
			log.Printf("Error decoding sync message: %v", err)
			return
		}

		switch msg.Type {
		case "SYNC_REQUEST":
			n.handleSyncRequest(s)
		case "FORK_DETECTED":
			n.handleForkResolution(s)
		case "CHAIN_VALIDATION":
			n.handleChainValidation(s)
		}
	})
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
	request := struct {
		StartHeight int
		EndHeight   int
	}{
		StartHeight: startHeight,
		EndHeight:   endHeight,
	}

	// Open sync stream
	s, err := n.Host.NewStream(n.ctx, peerID, protocol.ID("/blockchain/1.0.0/sync"))
	if err != nil {
		return fmt.Errorf("failed to open sync stream: %v", err)
	}
	defer s.Close()

	if err := json.NewEncoder(s).Encode(request); err != nil {
		return fmt.Errorf("failed to send sync request: %v", err)
	}

	// Update peer sync state
	n.PeerManager.SetSyncState(peerID, true)
	defer n.PeerManager.SetSyncState(peerID, false)

	// Receive and process blocks
	var blocks []Block
	if err := json.NewDecoder(s).Decode(&blocks); err != nil {
		return fmt.Errorf("failed to receive blocks: %v", err)
	}

	// Convert UTXOPool to map[string]UTXO
	utxoMap := make(map[string]UTXO)
	for _, utxo := range n.UTXOSet.GetAllUTXOs() {
		utxoMap[utxo.TransactionID] = utxo
	}

	// Validate and add blocks
	for i, block := range blocks {
		// Validate block structure and hash
		if err := validateBlockStructure(&block); err != nil {
			return fmt.Errorf("invalid block structure at height %d: %v", startHeight+i, err)
		}

		// Add block to blockchain
		if err := n.Blockchain.AddBlock(&block, n.Mempool, n.StakePool, utxoMap, n.Host); err != nil {
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
		return fmt.Errorf("nil block")
	}

	if len(block.Hash()) == 0 {
		return fmt.Errorf("empty block hash")
	}

	// Validate previous hash (except for genesis block)
	if block.Header.BlockNumber > 0 && len(block.Header.PreviousHash) == 0 {
		return fmt.Errorf("empty previous hash for non-genesis block")
	}

	// Validate timestamp
	if block.Header.Timestamp <= 0 {
		return fmt.Errorf("invalid block timestamp")
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
	// Send mempool transactions
	txs := n.Mempool.GetPrioritizedTransactions(100) // Get top 100 transactions
	if err := json.NewEncoder(s).Encode(txs); err != nil {
		log.Printf("Error sending mempool: %v", err)
	}
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

func (n *Node) handleSyncRequest(s network.Stream) {
	var request struct {
		StartHeight int
		EndHeight   int
	}

	if err := json.NewDecoder(s).Decode(&request); err != nil {
		log.Printf("Error decoding sync request: %v", err)
		return
	}

	// Validate request range
	if request.StartHeight < 0 || request.EndHeight > len(n.Blockchain.Chain) || request.StartHeight >= request.EndHeight {
		log.Printf("Invalid sync range requested: %d to %d", request.StartHeight, request.EndHeight)
		return
	}

	// Send requested blocks
	chainSegment := n.Blockchain.Chain[request.StartHeight:request.EndHeight]
	if err := json.NewEncoder(s).Encode(chainSegment); err != nil {
		log.Printf("Error sending chain segment: %v", err)
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
	if added := n.Mempool.AddTransaction(tx, n.UTXOPool.utxos); !added {
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
func (bc *Blockchain) GetUTXOSet() map[string]UTXO {
	// Access UTXOs directly from the Node's UTXOPool
	return bc.Node.UTXOPool.utxos
}

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
	var req BlockSyncRequest
	if err := json.NewDecoder(s).Decode(&req); err != nil {
		log.Printf("Error decoding block sync request: %v", err)
		return
	}

	// Validate request range
	if req.StartHeight > req.EndHeight || req.EndHeight > uint64(len(n.Blockchain.Chain)) {
		log.Printf("Error: invalid height range in block sync request")
		return
	}

	switch req.RequestType {
	case "headers":
		headers := n.getBlockHeaders(req.StartHeight, req.EndHeight)
		// Convert BlockHeader to SyncBlockHeader
		syncHeaders := make([]SyncBlockHeader, len(headers))
		for i, h := range headers {
			block := n.Blockchain.Chain[h.BlockNumber] // Get corresponding block
			syncHeaders[i] = SyncBlockHeader{
				Hash:              block.Hash(), // Get hash from block
				PreviousHash:      h.PreviousHash,
				Height:            h.BlockNumber,
				Timestamp:         h.Timestamp,
				MerkleRoot:        h.MerkleRoot,
				StateRoot:         h.StateRoot,
				Difficulty:        uint64(h.Difficulty),
				TotalTransactions: block.numTx, // Get transaction count from block
			}
		}
		json.NewEncoder(s).Encode(BlockHeaderResponse{
			Headers:     syncHeaders,
			StartHeight: req.StartHeight,
			EndHeight:   req.EndHeight,
		})
	case "full":
		blocks := n.Blockchain.Chain[req.StartHeight : req.EndHeight+1]
		json.NewEncoder(s).Encode(blocks)
	}
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

// Add method to check sync status
func (n *Node) IsSyncing() bool {
	n.syncMu.RLock()
	defer n.syncMu.RUnlock()
	return n.isSyncing
}

// Start starts the node and its services
func (n *Node) Start() error {
	// Start DHT in server mode
	if err := n.DHT.Bootstrap(n.ctx); err != nil {
		return fmt.Errorf("failed to bootstrap DHT: %v", err)
	}

	// Start peer discovery
	go n.discoverPeers()

	// Start blockchain sync
	if n.config.Blockchain != nil {
		go n.startBlockchainSync()
	}

	return nil
}

func (n *Node) discoverPeers() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			peers := n.Host.Network().Peers()
			for _, peerID := range peers {
				// Skip if we already know this peer
				if n.knownPeers[peerID] {
					continue
				}

				// Add to known peers
				n.knownPeers[peerID] = true

				// Get peer info
				addrs := n.Host.Peerstore().Addrs(peerID)
				if len(addrs) == 0 {
					continue
				}

				// Notify about new peer
				log.Printf("📡 Discovered new peer: %s at %s", peerID.String(), addrs[0])

				// Try to connect
				peerInfo := peer.AddrInfo{
					ID:    peerID,
					Addrs: addrs,
				}
				if err := n.Host.Connect(n.ctx, peerInfo); err != nil {
					log.Printf("⚠️ Failed to connect to discovered peer: %v", err)
				}
			}
		}
	}
}

// RegisterBlockchainHandlers registers blockchain handlers for the node
func (n *Node) RegisterBlockchainHandlers(bc *Blockchain) error {
	log.Printf("📝 Registering blockchain handlers for node")

	// The blockchain is already set in the node constructor, but we can update it here if needed
	n.Blockchain = bc

	// Set up protocol handlers for blockchain operations
	n.Host.SetStreamHandler("/blockchain/blocks/1.0.0", n.handleBlockStream)
	n.Host.SetStreamHandler("/blockchain/transactions/1.0.0", n.handleTransactionStream)
	n.Host.SetStreamHandler("/blockchain/sync/1.0.0", n.handleSyncRequest)

	// Advertise our node as a provider for this blockchain network
	h := sha256.New()
	h.Write([]byte(n.NetworkID))
	mh := h.Sum(nil)
	c := cid.NewCidV1(cid.Raw, mh)

	// Provide on the DHT
	if n.DHT != nil {
		if err := n.DHT.Provide(n.ctx, c, true); err != nil {
			log.Printf("⚠️ Failed to announce as provider: %v", err)
		} else {
			log.Printf("✅ Successfully announced as blockchain provider on the DHT")
		}
	}

	return nil
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
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			// Get latest block from peers
			for _, peer := range n.Host.Network().Peers() {
				if err := n.syncWithPeer(peer); err != nil {
					log.Printf("⚠️ Failed to sync with peer %s: %v", peer.String(), err)
				}
			}
		}
	}
}

func (n *Node) syncWithPeer(peerID peer.ID) error {
	// Skip sync if this is a bootstrap node
	if n.IsPeerBootstrapNode(peerID) {
		return nil
	}

	log.Printf("Syncing blockchain with peer: %s", peerID)

	stream, err := n.Host.NewStream(n.ctx, peerID, protocol.ID("/blockchain/1.0.0/sync"))
	if err != nil {
		return fmt.Errorf("failed to open sync stream: %v", err)
	}
	defer stream.Close()

	// Send sync request
	request := struct {
		StartHeight int
		EndHeight   int
	}{
		StartHeight: 0,
		EndHeight:   -1,
	}

	if err := json.NewEncoder(stream).Encode(request); err != nil {
		return fmt.Errorf("failed to send sync request: %v", err)
	}

	// Receive and process blocks
	var blocks []Block
	if err := json.NewDecoder(stream).Decode(&blocks); err != nil {
		return fmt.Errorf("failed to receive blocks: %v", err)
	}

	// Validate and add blocks
	for _, block := range blocks {
		if err := validateBlockStructure(&block); err != nil {
			return fmt.Errorf("invalid block structure at height %d: %v", block.Header.BlockNumber, err)
		}

		// Add block to blockchain
		if err := n.Blockchain.AddBlock(&block, n.Mempool, n.StakePool, n.UTXOSet.GetAllUTXOs(), n.Host); err != nil {
			return fmt.Errorf("failed to add block at height %d: %v", block.Header.BlockNumber, err)
		}
	}

	return nil
}

func (n *Node) BroadcastValidatorSelection(validatorAddr string, proof *SelectionProof) error {
	msg := &ValidatorSelectionMessage{
		ValidatorAddress: validatorAddr,
		Proof:            proof,
		BlockHeight:      n.Blockchain.GetLatestBlock().Header.BlockNumber,
		Timestamp:        time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal validator selection: %w", err)
	}

	return n.publishMessage(ValidatorSelectionTopic, data)
}

func (n *Node) BroadcastValidatorVote(validatorAddr string, vote bool, blockHash string) error {
	msg := &ValidatorVoteMessage{
		ValidatorAddress: validatorAddr,
		Vote:             vote,
		BlockHash:        blockHash,
		Timestamp:        time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal validator vote: %w", err)
	}

	return n.publishMessage(ValidatorVoteTopic, data)
}

// Add message types
type ValidatorSelectionMessage struct {
	ValidatorAddress string
	Proof            *SelectionProof
	BlockHeight      uint64
	Timestamp        time.Time
}

type ValidatorVoteMessage struct {
	ValidatorAddress string
	Vote             bool
	BlockHash        string
	Timestamp        time.Time
}

// Add this method to Node struct
func (n *Node) publishMessage(topic string, data []byte) error {
	// Create a new pubsub topic
	t, err := n.PubSub.Join(topic)
	if err != nil {
		return fmt.Errorf("failed to join topic %s: %w", topic, err)
	}

	// Publish the message
	err = t.Publish(n.ctx, data)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

// Add these methods to Node struct
func (n *Node) BroadcastValidatorTimeout(validatorAddr string) error {
	msg := &ValidatorTimeoutMessage{
		ValidatorAddress: validatorAddr,
		Timestamp:        time.Now(),
		TimeoutCount:     n.validatorProtocol.validators[validatorAddr].Timeouts,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal validator timeout: %w", err)
	}

	return n.publishMessage(ValidatorTimeoutTopic, data)
}

func (n *Node) BroadcastValidatorSetUpdate(activeValidators []string) error {
	msg := &ValidatorSetUpdateMessage{
		ActiveValidators: activeValidators,
		Timestamp:        time.Now(),
		BlockHeight:      n.Blockchain.GetLatestBlock().Header.BlockNumber,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal validator set update: %w", err)
	}

	return n.publishMessage(ValidatorSetUpdateTopic, data)
}

// Add this method to Node
func (n *Node) subscribeToValidatorTopics() error {
	topics := []string{
		"/validator/heartbeat",
		"/validator/selection",
		"/validator/vote",
	}

	for _, topic := range topics {
		// Create a wrapper function to adapt the stream handler
		handler := func(topic string, data []byte) error {
			// Convert the message to a stream and process it
			msg := &ValidatorMessage{}
			if err := json.Unmarshal(data, msg); err != nil {
				return fmt.Errorf("failed to unmarshal validator message: %w", err)
			}

			// Process based on message type
			switch msg.Type {
			case "heartbeat":
				var heartbeat ValidatorHeartbeatMessage
				if err := json.Unmarshal(msg.Payload, &heartbeat); err != nil {
					return err
				}
				n.validatorProtocol.HandleHeartbeat(heartbeat.ValidatorAddress)
			case "selection":
				var selection ValidatorSelectionMessage
				if err := json.Unmarshal(msg.Payload, &selection); err != nil {
					return err
				}
				// Handle selection message
			case "vote":
				var vote ValidatorVoteMessage
				if err := json.Unmarshal(msg.Payload, &vote); err != nil {
					return err
				}
				// Handle vote message
			}
			return nil
		}

		if err := n.subscribeTopic(topic, handler); err != nil {
			return fmt.Errorf("failed to subscribe to %s: %w", topic, err)
		}
	}
	return nil
}

// Add subscribeTopic method to Node struct
func (n *Node) subscribeTopic(topic string, handler func(string, []byte) error) error {
	ps, err := n.PubSub.Join(topic)
	if err != nil {
		return fmt.Errorf("failed to join topic %s: %w", topic, err)
	}

	sub, err := ps.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to topic %s: %w", topic, err)
	}

	go func() {
		for {
			msg, err := sub.Next(n.ctx)
			if err != nil {
				log.Printf("Error receiving message: %v", err)
				continue
			}
			if err := handler(topic, msg.Data); err != nil {
				log.Printf("Error handling message: %v", err)
			}
		}
	}()

	return nil
}

// Add to Node struct methods
func (n *Node) SetStreamHandler(protocolID string, handler network.StreamHandler) {
	n.Host.SetStreamHandler(protocol.ID(protocolID), handler)
}

// IsPeerBootstrapNode checks if a peer is a bootstrap node
func (n *Node) IsPeerBootstrapNode(peerID peer.ID) bool {
	n.syncMu.RLock()
	defer n.syncMu.RUnlock()
	return n.bootstrapNodes[peerID]
}

// SyncWithPeer attempts to sync blockchain data with a peer
func (n *Node) SyncWithPeer(peerID peer.ID) error {
	// Skip sync if this is a bootstrap node
	if n.IsPeerBootstrapNode(peerID) {
		log.Printf("Skipping blockchain sync with bootstrap node: %s", peerID)
		return nil
	}

	log.Printf("Syncing blockchain with peer: %s", peerID)

	stream, err := n.Host.NewStream(n.ctx, peerID, protocol.ID("/blockchain/1.0.0/sync"))
	if err != nil {
		return fmt.Errorf("failed to open sync stream: %v", err)
	}
	defer stream.Close()

	// Send sync request
	req := SyncRequest{
		NodeID: n.Host.ID().String(),
	}

	if err := json.NewEncoder(stream).Encode(req); err != nil {
		return fmt.Errorf("failed to send sync request: %v", err)
	}

	// Receive blocks
	var blocks []Block
	if err := json.NewDecoder(stream).Decode(&blocks); err != nil {
		return fmt.Errorf("failed to receive blocks: %v", err)
	}

	if len(blocks) > 0 {
		log.Printf("Received %d blocks from peer %s", len(blocks), peerID)
		for _, block := range blocks {
			if err := n.Blockchain.AddBlock(&block, n.Mempool, n.StakePool, n.UTXOSet.GetAllUTXOs(), n.Host); err != nil {
				log.Printf("Failed to add block: %v", err)
			}
		}
	} else {
		log.Printf("No new blocks received from peer %s", peerID)
	}

	return nil
}

// Message types for validator communication
type ValidatorMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type ValidatorHeartbeatMessage struct {
	ValidatorAddress string    `json:"validator_address"`
	Timestamp        time.Time `json:"timestamp"`
	BlockHeight      uint64    `json:"block_height"`
}

type ValidatorTimeoutMessage struct {
	ValidatorAddress string    `json:"validator_address"`
	Timestamp        time.Time `json:"timestamp"`
	TimeoutCount     uint64    `json:"timeout_count"`
}

type ValidatorSetUpdateMessage struct {
	ActiveValidators []string  `json:"active_validators"`
	Timestamp        time.Time `json:"timestamp"`
	BlockHeight      uint64    `json:"block_height"`
}
