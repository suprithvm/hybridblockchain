package sync

import (
	"fmt"
	"net"
	"sync"
	"time"

	"blockchain-core/blockchain"
	pb "blockchain-core/blockchain/sync/proto"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SyncService implements the ChainSync and NetworkSync services
type SyncService struct {
	pb.UnimplementedChainSyncServer
	pb.UnimplementedNetworkSyncServer
	config *SyncConfig
	state  *SyncState
	mu     sync.RWMutex

	// Blockchain components
	blockchain *blockchain.Blockchain
	store      *blockchain.Store

	// gRPC server
	server   *grpc.Server
	listener net.Listener
}

// NewSyncService creates a new sync service instance
func NewSyncService(config *SyncConfig, bc *blockchain.Blockchain, store *blockchain.Store) *SyncService {
	if config == nil {
		config = DefaultSyncConfig()
	}
	return &SyncService{
		config:     config,
		state:      &SyncState{},
		blockchain: bc,
		store:      store,
	}
}

// SyncConfig holds configuration for sync operations
type SyncConfig struct {
	SendBufferSize int
	RecvBufferSize int
	SendTimeout    time.Duration
	RecvTimeout    time.Duration
	WriteTimeout   time.Duration
	ReadTimeout    time.Duration
}

// DefaultSyncConfig returns default configuration values
func DefaultSyncConfig() *SyncConfig {
	return &SyncConfig{
		SendBufferSize: 1024,
		RecvBufferSize: 1024,
		SendTimeout:    time.Second * 10,
		RecvTimeout:    time.Second * 10,
		WriteTimeout:   time.Second * 5,
		ReadTimeout:    time.Second * 5,
	}
}

// SyncState represents the current synchronization state
type SyncState struct {
	ChainState   *ChainState
	UTXOState    *UTXOState
	NetworkState *NetworkState
	mu           sync.RWMutex
}

// ChainState represents blockchain state
type ChainState struct {
	Height        uint64
	LastBlockHash string
	StateRoot     string
	UTXORoot      string
	GetBlockFunc  func(height uint64) (*Block, error)
	Store         BlockchainStore
	mu            sync.RWMutex
}

// BlockchainStore defines the interface for blockchain storage operations
type BlockchainStore interface {
	SaveTransaction(tx *blockchain.Transaction) error
	GetTransaction(hash string) (*blockchain.Transaction, error)
}

// UTXOState represents UTXO set state
type UTXOState struct {
	CurrentHash string
	Chunks      []UTXOChunk
	mu          sync.RWMutex
}

// UTXOChunk represents a chunk of UTXO data
type UTXOChunk struct {
	Data        []byte
	MerkleProof string
}

// Block represents a blockchain block
type Block struct {
	Data       []byte
	MerkleRoot string
	StateRoot  string
	Timestamp  time.Time
}

// NetworkState represents network state
type NetworkState struct {
	Peers map[string]*PeerState
	Stats NetworkStats
	mu    sync.RWMutex
}

// PeerState represents peer information
type PeerState struct {
	ID        string
	Address   string
	Port      uint32
	Score     float32
	LastSeen  time.Time
	Connected bool
}

// NetworkStats represents network statistics
type NetworkStats struct {
	ConnectedPeers int
	InboundPeers   int
	OutboundPeers  int
	BandwidthUsage uint64
	PeerScores     map[string]float32
}

// UTXOSyncStatus represents UTXO set sync status
type UTXOSyncStatus struct {
	TotalChunks     uint32
	ProcessedChunks uint32
	CurrentHash     string
	TargetHash      string
	LastUpdateTime  time.Time
}

// BlockSyncStatus represents block sync status
type BlockSyncStatus struct {
	CurrentHeight   uint64
	TargetHeight    uint64
	ProcessedBlocks uint64
	FailedBlocks    []uint64
	LastBlockHash   string
	LastUpdateTime  time.Time
}

// NetworkStatus represents network sync status
type NetworkStatus struct {
	ConnectedPeers  uint32
	InboundPeers    uint32
	OutboundPeers   uint32
	BandwidthUsage  uint64
	LastMessageTime time.Time
	PeerScores      map[string]float64
}

// ChainInfo represents blockchain information
type ChainInfo struct {
	Height          uint64
	LastBlockHash   string
	StateRoot       string
	UTXORoot        string
	NetworkVersion  uint32
	ProtocolVersion uint32
	MinPeerVersion  uint32
	Timestamp       *timestamppb.Timestamp
}

// GetBlock retrieves a block at the specified height
func (c *ChainState) GetBlock(height uint64) (*Block, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.GetBlockFunc != nil {
		return c.GetBlockFunc(height)
	}

	// Default implementation
	if height > c.Height {
		return nil, fmt.Errorf("block height %d exceeds current height %d", height, c.Height)
	}
	return &Block{
		Data:       []byte{},
		MerkleRoot: "",
		StateRoot:  c.StateRoot,
		Timestamp:  time.Now(),
	}, nil
}

// GetChunks retrieves UTXO chunks of the specified size
func (u *UTXOState) GetChunks(chunkSize uint32) ([]UTXOChunk, error) {
	u.mu.RLock()
	defer u.mu.RUnlock()

	if len(u.Chunks) == 0 {
		return nil, fmt.Errorf("no UTXO chunks available")
	}

	// Return existing chunks or split into new chunks based on size
	return u.Chunks, nil
}

// GetPeers retrieves peer information
func (n *NetworkState) GetPeers(maxPeers uint32, excludedPeers []string) []*PeerState {
	n.mu.RLock()
	defer n.mu.RUnlock()

	excluded := make(map[string]bool)
	for _, peer := range excludedPeers {
		excluded[peer] = true
	}

	var peers []*PeerState
	for _, peer := range n.Peers {
		if !excluded[peer.ID] && peer.Connected {
			peers = append(peers, peer)
			if uint32(len(peers)) >= maxPeers {
				break
			}
		}
	}

	return peers
}

// GetStats retrieves network statistics
func (n *NetworkState) GetStats() NetworkStats {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Stats
}

// PropagateTransaction propagates a transaction to connected peers
func (n *NetworkState) PropagateTransaction(txData []byte, txHash string) (int, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	propagatedCount := 0
	for _, peer := range n.Peers {
		if peer.Connected {
			// In a real implementation, this would send the transaction to the peer
			propagatedCount++
		}
	}

	if propagatedCount == 0 {
		return 0, fmt.Errorf("no connected peers available for transaction propagation")
	}

	return propagatedCount, nil
}

// GetBlock retrieves a block at the specified height
func (s *SyncState) GetBlock(height uint64) (*Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.ChainState == nil {
		return nil, fmt.Errorf("chain state not initialized")
	}
	return s.ChainState.GetBlock(height)
}

// GetUTXOChunks retrieves UTXO chunks of the specified size
func (s *SyncState) GetUTXOChunks(chunkSize uint32) ([]UTXOChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.UTXOState == nil {
		return nil, fmt.Errorf("UTXO state not initialized")
	}
	return s.UTXOState.GetChunks(chunkSize)
}

// GetPeers retrieves peer information
func (s *SyncState) GetPeers(maxPeers uint32, excludedPeers []string) ([]*PeerState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.NetworkState == nil {
		return nil, fmt.Errorf("network state not initialized")
	}
	return s.NetworkState.GetPeers(maxPeers, excludedPeers), nil
}

// GetNetworkStats retrieves network statistics
func (s *SyncState) GetNetworkStats() (NetworkStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.NetworkState == nil {
		return NetworkStats{}, fmt.Errorf("network state not initialized")
	}
	return s.NetworkState.GetStats(), nil
}

// PropagateTransaction propagates a transaction to connected peers
func (s *SyncState) PropagateTransaction(txData []byte, txHash string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.NetworkState == nil {
		return 0, fmt.Errorf("network state not initialized")
	}
	return s.NetworkState.PropagateTransaction(txData, txHash)
}

// Start starts the sync service
func (s *SyncService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == nil {
		return fmt.Errorf("sync state not initialized")
	}

	// Initialize states if not already done
	if s.state.ChainState == nil {
		s.state.ChainState = &ChainState{}
	}
	if s.state.UTXOState == nil {
		s.state.UTXOState = &UTXOState{}
	}
	if s.state.NetworkState == nil {
		s.state.NetworkState = &NetworkState{
			Peers: make(map[string]*PeerState),
			Stats: NetworkStats{
				PeerScores: make(map[string]float32),
			},
		}
	}

	// Start gRPC server
	lis, err := net.Listen("tcp", ":50505")
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	s.server = grpc.NewServer()
	pb.RegisterChainSyncServer(s.server, s)
	pb.RegisterNetworkSyncServer(s.server, s)
	s.listener = lis

	go func() {
		if err := s.server.Serve(lis); err != nil {
			fmt.Printf("failed to serve: %v\n", err)
		}
	}()

	return nil
}

// Stop stops the sync service
func (s *SyncService) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		s.server.GracefulStop()
		s.server = nil
	}
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}

	return nil
}
