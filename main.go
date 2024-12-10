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
	keyFilePath := filepath.Join(filepath.Dir(os.Args[0]), "bootnode.key") // Key file will be in the same directory as main.go
	config := blockchain.BootstrapNodeConfig{
		ListenPort: 50505, // Fixed port for the bootnode
		PublicIP:   GetOutboundIP(),
		KeyFile:    keyFilePath,
		EnablePeerExchange: true,
		EnableNAT: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	node, err := blockchain.NewBootstrapNode(ctx, &config) // Added context argument
	if err != nil {
		log.Fatalf("Failed to create bootnode: %v", err)
	}

	// Start the bootnode
	if err := node.Start(); err != nil {
		log.Fatalf("Failed to start bootnode: %v", err)
	}
	log.Println("Bootnode is running...")

	// Keep the application running
	select {}
}
