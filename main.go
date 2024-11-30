package main

import (
	"log"
	"blockchain-core/blockchain"
	"context"
	"github.com/libp2p/go-libp2p/core/peer"
)

func StartNodeWithHeartbeat(addr string, bootstrapPeers []peer.AddrInfo, interval, timeout int64) (*blockchain.Node, error) {
	node, err := blockchain.NewNode(addr, bootstrapPeers)
	if err != nil {
		return nil, err
	}
	// Start the heartbeat routine
	ctx := context.Background()
	node.StartHeartbeatRoutine(ctx, int(interval), int(timeout))

	log.Printf("Node started with heartbeat at %s", addr)
	return node, nil
}

func main() {
	bootstrapPeers := []peer.AddrInfo{} // Add bootstrap peers here
	node, err := StartNodeWithHeartbeat("/ip4/127.0.0.1/tcp/4002", bootstrapPeers, 30, 60)
	log.Println("Node started with heartbeat")
	log.Print("Node ID: ", node.Host.ID())
	if err != nil {
		log.Fatalf("Failed to start node: %v", err)
	}

	select {} // Keep the application running
}
