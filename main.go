package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"strings"

	"blockchain-core/blockchain"
)

func main() {
	// Parse command line flags
	listenHost := flag.String("host", "0.0.0.0", "Host to listen on")
	listenPort := flag.Int("port", 9000, "Port to listen on")
	bootstrapNodes := flag.String("bootstrap", "", "Comma-separated list of bootstrap nodes")
	flag.Parse()

	// Create network configuration
	config := &blockchain.NetworkConfig{
		ListenHost:     *listenHost,
		ListenPort:     *listenPort,
		BootstrapNodes: strings.Split(*bootstrapNodes, ","),
		DHTServerMode:  true,
	}

	// Create and start node
	node, err := blockchain.NewNode(config)
	if err != nil {
		log.Fatalf("Failed to create node: %v", err)
	}
	defer node.Host.Close()

	// Connect to bootstrap nodes
	ctx := context.Background()
	if err := node.ConnectToBootstrapNodes(ctx); err != nil {
		log.Printf("Warning: Failed to connect to some bootstrap nodes: %v", err)
	}

	// Start peer discovery
	go node.DiscoverPeers()

	// Print node addresses
	log.Printf("Node started with ID: %s", node.Host.ID())
	for _, addr := range node.Host.Addrs() {
		log.Printf("Listening on: %s/p2p/%s", addr, node.Host.ID())
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
}
