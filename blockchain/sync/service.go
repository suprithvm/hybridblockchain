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
)

// SyncService implements the ChainSync and NetworkSync services
type SyncService struct {
	pb.UnimplementedChainSyncServer
	pb.UnimplementedNetworkSyncServer
	config     *SyncConfig
	blockchain *blockchain.Blockchain
	store      *blockchain.Store
	mu         sync.RWMutex
	server     *grpc.Server
}

// NewSyncService creates a new sync service instance
func NewSyncService(config *SyncConfig, bc *blockchain.Blockchain, store *blockchain.Store) (*SyncService, error) {
	if config == nil {
		config = DefaultSyncConfig()
	}

	if bc == nil {
		return nil, fmt.Errorf("blockchain instance required")
	}

	if store == nil {
		return nil, fmt.Errorf("store instance required")
	}

	return &SyncService{
		config:     config,
		blockchain: bc,
		store:      store,
	}, nil
}

// Start starts the sync service
func (s *SyncService) Start(listenAddr string) error {
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	s.server = grpc.NewServer()
	pb.RegisterChainSyncServer(s.server, s)
	pb.RegisterNetworkSyncServer(s.server, s)

	log.Printf("Starting sync service on %s", listenAddr)
	go func() {
		if err := s.server.Serve(lis); err != nil {
			log.Printf("failed to serve: %v", err)
		}
	}()

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
