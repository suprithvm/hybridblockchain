package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"

	"blockchain-core/blockchain"
	"blockchain-core/blockchain/db"
	pb "blockchain-core/blockchain/sync/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

type SyncService struct {
	pb.UnimplementedChainSyncServer
	pb.UnimplementedNetworkSyncServer
	server     *grpc.Server
	blockchain *blockchain.Blockchain
	store      *blockchain.Store
	config     *SyncConfig
	state      *SyncState
	ctx        context.Context
	mu         sync.RWMutex
	listener   net.Listener
	host       host.Host
}

func NewSyncService(config *SyncConfig, bc *blockchain.Blockchain, store *blockchain.Store, h host.Host) *SyncService {
	if config == nil {
		config = DefaultSyncConfig()
	}
	return &SyncService{
		server:     grpc.NewServer(),
		blockchain: bc,
		store:      store,
		config:     config,
		state:      &SyncState{},
		ctx:        context.Background(),
		mu:         sync.RWMutex{},
		host:       h,
	}
}

// Start starts the sync service
func (s *SyncService) Start(listenAddr string) error {
	// Register sync protocol handler
	s.host.SetStreamHandler("/blockchain/sync/1.0.0", s.handleSyncRequest)
	log.Printf("✅ Sync protocol registered on %s", listenAddr)
	return nil
}

// Stop stops the sync service
func (s *SyncService) Stop() {
	if s.server != nil {
		s.server.GracefulStop()
	}
}

// GetChainInfo returns information about the current blockchain state
func (s *SyncService) GetChainInfo(ctx context.Context, req *pb.ChainInfoRequest) (*pb.ChainInfoResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.blockchain == nil {
		return nil, status.Error(codes.Internal, "blockchain not initialized")
	}

	// Get current blockchain state
	height := s.blockchain.GetHeight()
	latestBlock := s.blockchain.GetLatestBlock()
	state, err := s.store.GetState()
	if err != nil {
		return nil, fmt.Errorf("failed to get blockchain state: %v", err)
	}

	return &pb.ChainInfoResponse{
		Height:          height,
		LastBlockHash:   latestBlock.Hash(),
		StateRoot:       state.StateRoot,
		UtxoRoot:        state.UTXOSetRoot,
		NetworkVersion:  1,
		ProtocolVersion: 1,
		MinPeerVersion:  1,
		Timestamp:       timestamppb.Now(),
	}, nil
}

// StreamBlocks implements ChainSync.StreamBlocks
func (s *SyncService) StreamBlocks(req *pb.BlockRequest, stream pb.ChainSync_StreamBlocksServer) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.blockchain == nil {
		return fmt.Errorf("blockchain not initialized")
	}

	for height := req.StartHeight; height <= req.EndHeight; height++ {
		block := s.blockchain.GetBlockByHeight(height)
		if block == nil {
			return fmt.Errorf("failed to get block at height %d", height)
		}

		blockResp := ConvertBlockToProto(block)
		if blockResp == nil {
			return fmt.Errorf("failed to convert block at height %d", height)
		}

		if err := stream.Send(blockResp); err != nil {
			return fmt.Errorf("failed to send block: %v", err)
		}
	}

	return nil
}

// SyncUTXOSet implements ChainSync.SyncUTXOSet
func (s *SyncService) SyncUTXOSet(req *pb.UTXORequest, stream pb.ChainSync_SyncUTXOSetServer) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.blockchain == nil {
		return fmt.Errorf("blockchain not initialized")
	}

	// Get current state to check UTXO root
	state, err := s.store.GetState()
	if err != nil {
		return fmt.Errorf("failed to get state: %v", err)
	}

	// If client's UTXO hash matches ours, no need to sync
	if req.CurrentHash == state.UTXOSetRoot {
		return nil
	}

	// Get UTXO iterator from store
	iter := s.store.Iterator()
	defer iter.Release()

	chunkNumber := uint32(0)
	utxoBuffer := make([]*blockchain.UTXO, 0, req.ChunkSize)

	// Iterate through UTXOs and send in chunks
	for iter.Next() {
		key := iter.Key()
		if len(key) > 0 && db.KeyPrefix(key[0]) == db.UTXOPrefix {
			value := iter.Value()
			var utxo blockchain.UTXO
			if err := json.Unmarshal(value, &utxo); err != nil {
				continue
			}
			utxoBuffer = append(utxoBuffer, &utxo)

			if uint32(len(utxoBuffer)) >= req.ChunkSize {
				resp := ConvertUTXOToProto(utxoBuffer[0], chunkNumber, 0)
				if err := stream.Send(resp); err != nil {
					return fmt.Errorf("failed to send UTXO chunk: %v", err)
				}
				utxoBuffer = utxoBuffer[:0]
				chunkNumber++
			}
		}
	}

	// Send remaining UTXOs
	if len(utxoBuffer) > 0 {
		resp := ConvertUTXOToProto(utxoBuffer[0], chunkNumber, chunkNumber+1)
		if err := stream.Send(resp); err != nil {
			return fmt.Errorf("failed to send final UTXO chunk: %v", err)
		}
	}

	return nil
}

// VerifyState implements ChainSync.VerifyState
func (s *SyncService) VerifyState(ctx context.Context, req *pb.VerifyStateRequest) (*pb.VerifyStateResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.blockchain == nil {
		return nil, fmt.Errorf("blockchain not initialized")
	}

	state, err := s.store.GetState()
	if err != nil {
		return nil, fmt.Errorf("failed to get state: %v", err)
	}

	valid := req.StateRoot == state.StateRoot && req.UtxoRoot == state.UTXOSetRoot

	return &pb.VerifyStateResponse{
		Valid: valid,
	}, nil
}

// DiscoverPeers implements NetworkSync.DiscoverPeers
func (s *SyncService) DiscoverPeers(req *pb.DiscoverRequest, stream pb.NetworkSync_DiscoverPeersServer) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.state.NetworkState == nil {
		return fmt.Errorf("network state not initialized")
	}

	excluded := make(map[string]bool)
	for _, peer := range req.ExcludedPeers {
		excluded[peer] = true
	}

	count := uint32(0)
	for id, peer := range s.state.NetworkState.Peers {
		if count >= req.MaxPeers {
			break
		}
		if excluded[id] {
			continue
		}

		info := &pb.PeerInfo{
			Id:       peer.ID,
			Address:  peer.Address,
			Port:     peer.Port,
			Score:    peer.Score,
			LastSeen: timestamppb.New(peer.LastSeen),
		}

		if err := stream.Send(info); err != nil {
			return fmt.Errorf("failed to send peer info: %v", err)
		}
		count++
	}

	return nil
}

// StreamStatus implements NetworkSync.StreamStatus
func (s *SyncService) StreamStatus(req *pb.StatusRequest, stream pb.NetworkSync_StreamStatusServer) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.state.NetworkState == nil {
		return fmt.Errorf("network state not initialized")
	}

	status := &pb.NetworkStatus{
		ConnectedPeers: uint32(s.state.NetworkState.Stats.ConnectedPeers),
		InboundPeers:   uint32(s.state.NetworkState.Stats.InboundPeers),
		OutboundPeers:  uint32(s.state.NetworkState.Stats.OutboundPeers),
		BandwidthUsage: s.state.NetworkState.Stats.BandwidthUsage,
		PeerScores:     s.state.NetworkState.Stats.PeerScores,
		Timestamp:      timestamppb.Now(),
	}

	return stream.Send(status)
}

// PropagateTransaction implements NetworkSync.PropagateTransaction
func (s *SyncService) PropagateTransaction(ctx context.Context, tx *pb.Transaction) (*pb.PropagateResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.blockchain == nil {
		return nil, fmt.Errorf("blockchain not initialized")
	}

	// Convert and validate transaction
	transaction, err := ConvertProtoToTransaction(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert transaction: %v", err)
	}

	// Save transaction to store
	if err := s.store.SaveTransaction(transaction); err != nil {
		return nil, fmt.Errorf("failed to save transaction: %v", err)
	}

	return &pb.PropagateResponse{
		Success:      true,
		PropagatedTo: 1, // For now, just indicate success
	}, nil
}

// SyncValidatorSet implements ChainSync.SyncValidatorSet
func (s *SyncService) SyncValidatorSet(ctx context.Context, req *pb.ValidatorSetRequest) (*pb.ValidatorSetResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.blockchain == nil {
		return nil, fmt.Errorf("blockchain not initialized")
	}

	activeValidators := make([]*pb.ValidatorInfo, 0)
	for addr, validator := range s.blockchain.Validators {
		if validator.Status == blockchain.ValidatorStatusActive {
			activeValidators = append(activeValidators, &pb.ValidatorInfo{
				Address:  addr,
				Score:    validator.Score,
				LastSeen: timestamppb.New(validator.LastActive),
				IsActive: true,
			})
		}
	}

	return &pb.ValidatorSetResponse{
		Validators:  activeValidators,
		BlockHeight: s.blockchain.GetLatestBlock().Header.BlockNumber,
		Timestamp:   timestamppb.Now(),
	}, nil
}

// RecoverValidator implements ChainSync.RecoverValidator
func (s *SyncService) RecoverValidator(ctx context.Context, req *pb.ValidatorRecoveryRequest) (*pb.ValidatorRecoveryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	validator, exists := s.blockchain.Validators[req.ValidatorAddress]
	if !exists {
		return nil, fmt.Errorf("validator not found")
	}

	// Attempt to recover validator
	if err := validator.Recover(); err != nil {
		return nil, fmt.Errorf("failed to recover validator: %w", err)
	}

	return &pb.ValidatorRecoveryResponse{
		Success:   true,
		NewStatus: int32(validator.Status),
		Message:   "Validator recovered successfully",
	}, nil
}

// handleSyncRequest handles incoming sync requests over libp2p
func (s *SyncService) handleSyncRequest(stream network.Stream) {
	defer stream.Close()

	// Read sync request
	var req SyncRequest
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		log.Printf("❌ Failed to decode sync request: %v", err)
		return
	}

	// Get our current height
	currentHeight := s.blockchain.GetHeight()

	// Special handling for genesis block
	if currentHeight == 0 {
		// Check if we are the genesis validator
		genesisValidator := s.blockchain.GetGenesisValidator()
		if genesisValidator != nil && genesisValidator.Address == s.blockchain.Node.Host.ID().String() {
			// We are the genesis validator, create and broadcast genesis block
			if err := s.blockchain.InitializeChain(); err != nil {
				log.Printf("❌ Failed to initialize chain: %v", err)
				return
			}
			// Broadcast genesis block
			if err := s.broadcastGenesisBlock(); err != nil {
				log.Printf("❌ Failed to broadcast genesis block: %v", err)
				return
			}
		} else {
			// We are not the genesis validator, wait for genesis block
			log.Printf("⏳ Waiting for genesis block from validator...")
			return
		}
	}

	// Normal sync handling for non-genesis blocks
	if req.Height > currentHeight {
		log.Printf("📥 Peer height %d is higher than our height %d, initiating sync", req.Height, currentHeight)
		go s.SyncWithPeer(stream.Conn().RemotePeer())
		return
	}

	if currentHeight > req.Height {
		log.Printf("📤 Our height %d is higher than peer's height %d, sending chain", currentHeight, req.Height)
		if err := s.sendChainToPeer(stream, req.Height, currentHeight); err != nil {
			log.Printf("❌ Failed to send chain to peer: %v", err)
		}
	}
}

func (s *SyncService) broadcastGenesisBlock() error {
	genesisBlock := s.blockchain.GetBlockByHeight(0)
	if genesisBlock == nil {
		return fmt.Errorf("genesis block not found")
	}

	// Broadcast to all peers
	peers := s.host.Network().Peers()
	for _, peerID := range peers {
		if s.host.ID() == peerID {
			continue
		}

		stream, err := s.host.NewStream(s.ctx, peerID, protocol.ID("/blockchain/sync/1.0.0"))
		if err != nil {
			log.Printf("⚠️ Failed to open stream to peer %s: %v", peerID, err)
			continue
		}
		defer stream.Close()

		// Send genesis block
		if err := json.NewEncoder(stream).Encode(genesisBlock); err != nil {
			log.Printf("⚠️ Failed to send genesis block to peer %s: %v", peerID, err)
			continue
		}
	}

	log.Printf("✅ Genesis block broadcasted to %d peers", len(peers))
	return nil
}

func (s *SyncService) sendChainToPeer(stream network.Stream, startHeight, endHeight uint64) error {
	// Send chain info first
	chainInfo := &ChainInfo{
		Height:        endHeight,
		LastBlockHash: s.blockchain.GetLatestBlock().Hash(),
		StateRoot:     s.blockchain.CalculateStateHash(),
	}

	if err := json.NewEncoder(stream).Encode(chainInfo); err != nil {
		return fmt.Errorf("failed to send chain info: %w", err)
	}

	// Send blocks in batches
	batchSize := 10
	for height := startHeight + 1; height <= endHeight; height += uint64(batchSize) {
		batchEnd := height + uint64(batchSize) - 1
		if batchEnd > endHeight {
			batchEnd = endHeight
		}

		blocks := make([]*blockchain.Block, 0)
		for h := height; h <= batchEnd; h++ {
			block := s.blockchain.GetBlockByHeight(h)
			if block == nil {
				return fmt.Errorf("failed to get block at height %d", h)
			}
			blocks = append(blocks, block)
		}

		if err := json.NewEncoder(stream).Encode(blocks); err != nil {
			return fmt.Errorf("failed to send block batch: %w", err)
		}
	}

	return nil
}

// SyncWithPeer initiates blockchain synchronization with a specific peer
func (s *SyncService) SyncWithPeer(peerID peer.ID) error {
	return s.syncWithPeer(peerID)
}

func (s *SyncService) syncWithPeer(peerID peer.ID) error {
	// Open sync stream
	stream, err := s.host.NewStream(s.ctx, peerID, protocol.ID("/blockchain/sync/1.0.0"))
	if err != nil {
		return fmt.Errorf("failed to open sync stream: %w", err)
	}
	defer stream.Close()

	// Send sync request
	currentHeight := uint64(0)
	if s.blockchain != nil {
		currentHeight = s.blockchain.GetHeight()
	}

	req := struct {
		StartHeight uint64 `json:"start_height"`
		EndHeight   uint64 `json:"end_height"`
	}{
		StartHeight: currentHeight,
		EndHeight:   0, // 0 means get all available blocks
	}

	if err := json.NewEncoder(stream).Encode(req); err != nil {
		return fmt.Errorf("failed to send sync request: %w", err)
	}

	// Read response
	var resp struct {
		Blocks []blockchain.Block `json:"blocks"`
		Error  string             `json:"error,omitempty"`
	}

	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return fmt.Errorf("failed to receive blocks: %w", err)
	}

	if resp.Error != "" {
		if resp.Error == "bootnode does not maintain blockchain" {
			// This is expected for bootnodes
			return nil
		}
		log.Printf("⚠️ Sync response error: %s", resp.Error)
		return nil
	}

	// Process received blocks
	for _, block := range resp.Blocks {
		if err := s.blockchain.AddBlockWithoutValidation(&block); err != nil {
			log.Printf("⚠️ Failed to add block: %v", err)
			continue
		}
	}

	log.Printf("✅ Successfully synced %d blocks from peer %s", len(resp.Blocks), peerID.String())
	return nil
}
