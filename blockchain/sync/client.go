package sync

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	pb "blockchain-core/blockchain/sync/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SyncClient handles communication with remote sync services
type SyncClient struct {
	chainClient   pb.ChainSyncClient
	networkClient pb.NetworkSyncClient
	conn          *grpc.ClientConn
	config        *SyncConfig
	mu            sync.RWMutex
}

// NewSyncClient creates a new sync client instance
func NewSyncClient(address string, config *SyncConfig) (*SyncClient, error) {
	if config == nil {
		config = DefaultSyncConfig()
	}

	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to sync service: %v", err)
	}

	return &SyncClient{
		chainClient:   pb.NewChainSyncClient(conn),
		networkClient: pb.NewNetworkSyncClient(conn),
		conn:          conn,
		config:        config,
	}, nil
}

// Close closes the client connection
func (c *SyncClient) Close() error {
	return c.conn.Close()
}

// GetChainInfo retrieves current blockchain information
func (c *SyncClient) GetChainInfo(ctx context.Context, currentHeight uint64, currentHash string) (*pb.ChainInfoResponse, error) {
	req := &pb.ChainInfoRequest{
		CurrentHeight: currentHeight,
		CurrentHash:   currentHash,
	}

	return c.chainClient.GetChainInfo(ctx, req)
}

// StreamBlocks retrieves blocks in the specified range
func (c *SyncClient) StreamBlocks(ctx context.Context, startHeight, endHeight uint64, includeTransactions bool) ([]*pb.BlockResponse, error) {
	req := &pb.BlockRequest{
		StartHeight:         startHeight,
		EndHeight:           endHeight,
		IncludeTransactions: includeTransactions,
	}

	stream, err := c.chainClient.StreamBlocks(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to start block stream: %v", err)
	}

	var blocks []*pb.BlockResponse
	for {
		block, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error receiving block: %v", err)
		}
		blocks = append(blocks, block)
	}

	return blocks, nil
}

// SyncUTXOSet synchronizes the UTXO set
func (c *SyncClient) SyncUTXOSet(ctx context.Context, chunkSize uint32, currentHash string) ([]*pb.UTXOResponse, error) {
	req := &pb.UTXORequest{
		ChunkSize:   chunkSize,
		CurrentHash: currentHash,
	}

	stream, err := c.chainClient.SyncUTXOSet(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to start UTXO sync: %v", err)
	}

	var chunks []*pb.UTXOResponse
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error receiving UTXO chunk: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// VerifyState verifies the current state
func (c *SyncClient) VerifyState(ctx context.Context, stateRoot, utxoRoot string) (*pb.VerifyStateResponse, error) {
	req := &pb.VerifyStateRequest{
		StateRoot: stateRoot,
		UtxoRoot:  utxoRoot,
	}

	return c.chainClient.VerifyState(ctx, req)
}

// DiscoverPeers discovers available peers
func (c *SyncClient) DiscoverPeers(ctx context.Context, maxPeers uint32, excludedPeers []string) ([]*pb.PeerInfo, error) {
	req := &pb.DiscoverRequest{
		MaxPeers:      maxPeers,
		ExcludedPeers: excludedPeers,
	}

	stream, err := c.networkClient.DiscoverPeers(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to start peer discovery: %v", err)
	}

	var peers []*pb.PeerInfo
	for {
		peer, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error receiving peer info: %v", err)
		}
		peers = append(peers, peer)
	}

	return peers, nil
}

// StreamNetworkStatus streams network status updates
func (c *SyncClient) StreamNetworkStatus(ctx context.Context, includePeerStats bool, callback func(*pb.NetworkStatus)) error {
	req := &pb.StatusRequest{
		IncludePeerStats: includePeerStats,
	}

	stream, err := c.networkClient.StreamStatus(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to start status stream: %v", err)
	}

	for {
		networkStatus, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.Canceled {
				return nil
			}
			return fmt.Errorf("error receiving network status: %v", err)
		}

		callback(networkStatus)
	}

	return nil
}

// PropagateTransaction broadcasts a transaction to the network
func (c *SyncClient) PropagateTransaction(ctx context.Context, txData []byte, txHash string) (*pb.PropagateResponse, error) {
	tx := &pb.Transaction{
		TransactionData: txData,
		TransactionHash: txHash,
		Timestamp:       timestamppb.New(time.Now().UTC()),
	}

	return c.networkClient.PropagateTransaction(ctx, tx)
}
