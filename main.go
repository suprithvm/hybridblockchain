package main

import (
	"blockchain-core/blockchain"
	"flag"
	"fmt"
	"log"
	"os"
	"time"
)

func main() {
	// Parse command-line flags
	nodeID := flag.String("node", "", "Specify the node ID (e.g., node1 or node2)")
	connectPeer := flag.String("connect", "", "Peer address to connect to (optional)")
	flag.Parse()

	// Ensure node ID is provided
	if *nodeID == "" {
		fmt.Println("Error: Please specify a node ID using --node flag (e.g., --node=node1)")
		os.Exit(1)
	}

	// Assign listening address based on node ID
	var listenAddr string
	switch *nodeID {
	case "node1":
		listenAddr = "/ip4/127.0.0.1/tcp/4001"
	case "node2":
		listenAddr = "/ip4/127.0.0.1/tcp/4002"
	default:
		fmt.Println("Error: Invalid node ID. Use 'node1' or 'node2'.")
		os.Exit(1)
	}

	// Initialize the P2P node
	node, err := blockchain.NewNode(listenAddr)
	if err != nil {
		log.Fatalf("Failed to create node: %v", err)
	}
	fmt.Printf("Node %s initialized with ID: %s\n", *nodeID, node.Host.ID().String())
	fmt.Printf("Listening on: %s\n", listenAddr)

	// Optionally connect to a peer
	if *connectPeer != "" {
		fmt.Printf("Connecting to peer: %s\n", *connectPeer)
		err := node.ConnectToPeer(*connectPeer)
		if err != nil {
			log.Printf("Failed to connect to peer %s: %v", *connectPeer, err)
		} else {
			fmt.Printf("Node %s successfully connected to peer: %s\n", *nodeID, *connectPeer)
		}
	}

	// Initialize blockchain instance
	blockchainInstance := blockchain.InitialiseBlockchain()

	// Validate genesis block consistency
	genesis := blockchain.GenesisBlock()
	if !blockchain.ValidateGenesisBlock(blockchainInstance, genesis) {
		log.Fatal("Genesis block mismatch! Ensure all nodes use the same genesis block.")
	}

	fmt.Println("Blockchain initialized with genesis block:", blockchainInstance.GetLatestBlock())

	// Role-specific behavior: Node 1 mines blocks
	if *nodeID == "node1" {
		go func() {
			for {
				time.Sleep(10 * time.Second) // Periodically mine blocks

				// Create new transactions
				transactions := []blockchain.Transaction{
					blockchain.NewTransaction("Alice", "Bob", 10, "Signature1"),
					blockchain.NewTransaction("Charlie", "Dave", 5, "Signature2"),
				}

				// Create and mine a new block
				newBlock := blockchain.NewBlock(blockchainInstance.GetLatestBlock(), transactions, blockchainInstance.GetLatestBlock().Difficulty)
				blockchain.MineBlock(&newBlock)

				// Add the mined block to the blockchain
				blockchainInstance.AddBlock(newBlock)
				fmt.Printf("Node 1 mined Block %d: %+v\n", newBlock.BlockNumber, newBlock)

				// Broadcast the new block to peers
				node.BroadcastBlock(newBlock)
			}
		}()
	}

	// Keep the node running
	select {}
}
