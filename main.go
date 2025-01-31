package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"blockchain-core/blockchain"
)

// GetOutboundIP gets the preferred outbound IP of this machine
func GetOutboundIP() string {
	return "49.204.110.41" // Set to the provided public IP
}

func main() {
	log.Printf("\n🔑 Node Identity Configuration")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Get the current working directory
	currentDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("❌ Failed to get current directory: %v", err)
	}

	// Create a persistent directory for node data if it doesn't exist
	nodeDataDir := filepath.Join(currentDir, "node_data")
	if err := os.MkdirAll(nodeDataDir, 0755); err != nil {
		log.Fatalf("❌ Failed to create node data directory: %v", err)
	}

	// Store key file in the node_data directory
	keyFilePath := filepath.Join(nodeDataDir, "bootnode.key")
	peerStorePath := filepath.Join(nodeDataDir, "peers.json")

	log.Printf("📂 Node Data Directory: %s", nodeDataDir)
	log.Printf("🔐 Key File Location: %s", keyFilePath)
	log.Printf("👥 Peer Store Location: %s", peerStorePath)

	config := blockchain.BootstrapNodeConfig{
		ListenPort:         50505,
		PublicIP:           GetOutboundIP(),
		KeyFile:            keyFilePath,
		PeerStoreFile:      peerStorePath,
		EnablePeerExchange: true,
		EnableNAT:         true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	node, err := blockchain.NewBootstrapNode(ctx, &config)
	if err != nil {
		log.Fatalf("❌ Failed to create bootnode: %v", err)
	}

	// Start the bootnode
	if err := node.Start(); err != nil {
		log.Fatalf("❌ Failed to start bootnode: %v", err)
	}
	log.Println("✅ Bootnode is running...")

	// Keep the application running
	select {}
}
