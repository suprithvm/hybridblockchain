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

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

// Protocol IDs
const (
	BlockProtocolID       = "/blockchain/blocks/1.0.0"
	TransactionProtocolID = "/blockchain/txs/1.0.0"
	HeartbeatProtocolID   = "/blockchain/heartbeat/1.0.0"
)

// blankValidator is a no-op validator for DHT records
type blankValidator struct{}

func (v blankValidator) Validate(_ string, _ []byte) error        { return nil }
func (v blankValidator) Select(_ string, _ [][]byte) (int, error) { return 0, nil }

// Node represents a blockchain network node
type Node struct {
	Host           host.Host
	DHT            *dht.IpfsDHT
	PeerManager    *PeerManager
	Blockchain     *Blockchain
	Mempool        *Mempool
	UTXOSet        *UTXOPool
	StakePool      *StakePool
	config         *NetworkConfig
	ctx            context.Context
	cancel         context.CancelFunc
	UTXOPool       *UTXOPool
	isSyncing      bool
	syncMu         sync.RWMutex
	gasModel       *gas.GasModel
	accountManager *AccountManager
	P2PPort        int
	RPCPort        int
	NetworkPath    string
	ChainID        uint64
	NetworkID      string
}

// Add getter method for gas model
func (n *Node) GetGasModel() *gas.GasModel {
	return n.gasModel
}

// NewNode creates a new blockchain node
func NewNode(config *NetworkConfig) (*Node, error) {
	ctx, cancel := context.WithCancel(context.Background())

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

	// Create the node instance first
	n := &Node{
		Host:   host,
		config: config,
		ctx:    ctx,
		cancel: cancel,
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
		n.connectToBootstrapNodes(ctx)
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

	n.DHT = dhtInstance
	n.PeerManager = NewPeerManager(host)
	n.StakePool = NewStakePool()
	n.gasModel = gas.NewGasModel(gas.MinGasPrice, gas.BaseGasLimit)
	n.accountManager = NewAccountManager(config.Blockchain.GetDB())
	n.Mempool = NewMempool(n)

	return n, nil
}

// Close gracefully shuts down the node
func (n *Node) Close() error {
	n.cancel()
	if err := n.Host.Close(); err != nil {
		return err
	}
	return n.DHT.Close()
}

// DiscoverPeers uses DHT to discover peers
func (n *Node) DiscoverPeers() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			if n.PeerManager.NeedMorePeers() {
				n.findAndConnectPeers()
			}
			n.PeerManager.CleanupPeers()
		}
	}
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

// BroadcastTransaction broadcasts a transaction to all peers except excluded ones
func (n *Node) BroadcastTransaction(tx *Transaction, excludePeers []peer.ID) {
	msg := NewMessage("NewTransaction", tx)
	msgBytes, err := msg.ToJSON()
	if err != nil {
		log.Printf("Failed to serialize transaction message: %v", err)
		return
	}

	// Get connected peers
	peers := n.PeerManager.GetBestPeers(10)

	// Broadcast to each peer
	for _, peerID := range peers {
		// Skip excluded peers
		if contains(excludePeers, peerID) {
			continue
		}

		// Open stream
		s, err := n.Host.NewStream(n.ctx, peerID, TransactionProtocolID)
		if err != nil {
			log.Printf("Failed to open stream to peer %s: %v", peerID, err)
			continue
		}

		// Send transaction
		if err := json.NewEncoder(s).Encode(msgBytes); err != nil {
			log.Printf("Failed to send transaction to peer %s: %v", peerID, err)
			s.Close()
			continue
		}
		s.Close()
	}
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
	// Block protocol handler
	n.Host.SetStreamHandler(BlockProtocolID, func(s network.Stream) {
		n.handleBlockStream(s)
	})

	// Transaction protocol handler
	n.Host.SetStreamHandler(TransactionProtocolID, func(s network.Stream) {
		n.handleTransactionStream(s)
	})

	// Heartbeat protocol handler
	n.Host.SetStreamHandler(HeartbeatProtocolID, func(s network.Stream) {
		n.handleHeartbeatStream(s)
	})
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

		// Implement block serialization and sending
		// TODO: Add proper block serialization
		if _, err := stream.Write([]byte("block-data")); err != nil {
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
	// TODO: Implement proper chain serialization
	chainData := []byte("chain-data")

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

		if _, err := stream.Write(chainData); err != nil {
			stream.Close()
			log.Printf("Failed to send chain to peer %s: %v", peerID, err)
			continue
		}
		stream.Close()
	}
	return nil
}

// VerifyPeerConnection checks if two nodes are connected
func VerifyPeerConnection(node1, node2 *Node) bool {
	peers := node1.Host.Network().Peers()
	for _, peer := range peers {
		if peer == node2.Host.ID() {
			return true
		}
	}
	return false
}

// RequestChain requests a blockchain segment from peers
func (n *Node) RequestChain(start, end int) ([]Block, error) {
	peers := n.PeerManager.GetConnectedPeers()
	if len(peers) == 0 {
		return nil, fmt.Errorf("no peers available")
	}

	// TODO: Implement proper chain request/response protocol
	for _, peerID := range peers {
		stream, err := n.Host.NewStream(n.ctx, peerID, protocol.ID("/blockchain/chain/request/1.0.0"))
		if err != nil {
			log.Printf("Failed to open stream to peer %s: %v", peerID, err)
			continue
		}
		defer stream.Close()

		// Send request
		request := fmt.Sprintf("%d,%d", start, end)
		if _, err := stream.Write([]byte(request)); err != nil {
			log.Printf("Failed to send chain request to peer %s: %v", peerID, err)
			continue
		}

		// Read response
		buf := make([]byte, 1024*1024) // 1MB buffer
		_, err = io.ReadFull(stream, buf)
		if err != nil {
			log.Printf("Failed to read chain response from peer %s: %v", peerID, err)
			continue
		}

		// TODO: Implement proper chain deserialization
		// For now, return empty slice
		return []Block{}, nil
	}

	return nil, fmt.Errorf("failed to retrieve chain from any peer")
}

// ValidateAndUpdateChain validates and integrates a received chain
func (n *Node) ValidateAndUpdateChain(newChain []Block) bool {
	// TODO: Implement proper chain validation
	return true
}

// Additional protocol handlers
func (n *Node) registerAdditionalHandlers() {
	// Chain request handler
	n.Host.SetStreamHandler(protocol.ID("/blockchain/chain/request/1.0.0"), func(s network.Stream) {
		defer s.Close()

		// Read request
		buf := make([]byte, 1024)
		_, err := io.ReadFull(s, buf)
		if err != nil {
			log.Printf("Error reading chain request: %v", err)
			return
		}

		// TODO: Implement proper chain segment response
		response := []byte("chain-segment-data")
		if _, err := s.Write(response); err != nil {
			log.Printf("Error sending chain response: %v", err)
			return
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
	// Open sync stream
	s, err := n.Host.NewStream(context.Background(), peerID, "/blockchain/1.0.0/sync")
	if err != nil {
		return fmt.Errorf("failed to open sync stream: %v", err)
	}
	defer s.Close()

	// Send sync request
	request := struct {
		StartHeight int
		EndHeight   int
	}{
		StartHeight: startHeight,
		EndHeight:   endHeight,
	}

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

	// Create temporary mempool and UTXO set for synced blocks
	tempMempool := NewMempool(n)
	tempUTXOSet := make(map[string]UTXO)

	// Validate and add blocks
	for i, block := range blocks {
		// Validate block structure and hash
		if err := validateBlockStructure(&block); err != nil {
			return fmt.Errorf("invalid block structure at height %d: %v", startHeight+i, err)
		}

		// Add block to blockchain
		n.Blockchain.AddBlock(tempMempool, n.StakePool, tempUTXOSet, n.Host)

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

// Remove duplicated PeerManager-related code and types
// All PeerManager-related code is now in peer_manager.go

func (n *Node) connectToBootstrapNodes(ctx context.Context) {
	maxRetries := 5
	retryDelay := time.Second * 5

	for _, addr := range n.config.BootstrapNodes {
		for retry := 0; retry < maxRetries; retry++ {
			log.Printf("   • Attempting to connect to bootstrap node (attempt %d/%d)", retry+1, maxRetries)

			if err := n.connectToNode(ctx, addr); err != nil {
				log.Printf("   ❌ Connection failed: %s", err)
				time.Sleep(retryDelay)
				continue
			}

			log.Printf("   ✅ Successfully connected to bootstrap node")
			break
		}
	}
}

func (n *Node) Start() error {
	// ... existing code ...

	// Monitor connection status
	go func() {
		ticker := time.NewTicker(time.Second * 10)
		defer ticker.Stop()

		for {
			select {
			case <-n.ctx.Done():
				return
			case <-ticker.C:
				if len(n.Host.Network().Peers()) == 0 {
					log.Printf("⚠️ No peers connected, attempting to reconnect...")
					n.connectToBootstrapNodes(n.ctx)
				}
			}
		}
	}()

	return nil
}

func (n *Node) connectToNode(ctx context.Context, addr string) error {
	// Parse multiaddr
	maddr, err := ma.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}

	// Extract peer info
	peerInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("invalid peer info: %w", err)
	}

	// Connect to the peer
	if err := n.Host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	return nil
}
