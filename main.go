package main

import (
	"blockchain-core/blockchain"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	// Define configuration for the nodes
	nodeConfigs := []struct {
		NodeID       string
		ListenAddr   string
		GenesisFunds float64
	}{
		{"node1", "/ip4/127.0.0.1/tcp/10001", 100},
		{"node2", "/ip4/127.0.0.1/tcp/10002", 50},
		{"node3", "/ip4/127.0.0.1/tcp/10003", 75},
	}

	var nodes []*blockchain.Node
	var chains []*blockchain.Blockchain
	var mempools []*blockchain.Mempool
	var stakePools []*blockchain.StakePool
	var wg sync.WaitGroup

	// Initialize nodes, blockchains, mempools, and stake pools
	for _, config := range nodeConfigs {
		node, err := blockchain.NewNode(config.ListenAddr)
		if err != nil {
			log.Fatalf("Failed to create node %s: %v", config.NodeID, err)
		}
		nodes = append(nodes, node)

		chain := blockchain.InitialiseBlockchain()
		chains = append(chains, &chain)

		mempool := blockchain.NewMempool()
		mempools = append(mempools, mempool)

		stakePool := blockchain.NewStakePool()
		stakePools = append(stakePools, stakePool)

		// Add initial stake to the stake pool
		err = stakePool.AddStake(config.NodeID, node.Host.ID().String(), config.GenesisFunds)
		if err != nil {
			log.Fatalf("Failed to add stake for node %s: %v", config.NodeID, err)
		}
	}

	// Connect nodes to each other
	for i, node := range nodes {
		for j, peerNode := range nodes {
			if i != j {
				addr := peerNode.Host.Addrs()[0].String() + "/p2p/" + peerNode.Host.ID().String()
				if err := node.ConnectToPeer(addr); err != nil {
					log.Printf("Node %s failed to connect to peer %s: %v", node.Host.ID(), addr, err)
				}
			}
		}
	}

	// Simulate adding transactions to the mempool
	wg.Add(1)
	go func() {
		defer wg.Done()
		tx := blockchain.NewTransaction("node1", "node2", 10, 1)
		tx.Signature = "test-signature" // Simulate a signed transaction

		for _, mempool := range mempools {
			mempool.AddTransaction(*tx, map[string]blockchain.UTXO{})
		}
		log.Println("Transaction added to all mempools.")
	}()

	// Simulate mining blocks on each node
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i, chain := range chains {
			previousBlock := chain.GetLatestBlock()
			newBlock := blockchain.NewBlock(previousBlock, mempools[i], map[string]blockchain.UTXO{}, previousBlock.Difficulty, nodeConfigs[i].NodeID)

			if err := blockchain.MineBlock(&newBlock, previousBlock, stakePools[i], 10, nodes[i].Host); err != nil {
				log.Printf("Node %s failed to mine block: %v", nodeConfigs[i].NodeID, err)
				continue
			}

			chain.AddBlock(mempools[i], stakePools[i], map[string]blockchain.UTXO{}, nodes[i].Host)
			log.Printf("Node %s mined and added a new block: %+v", nodeConfigs[i].NodeID, newBlock)
		}
	}()

	// Broadcast and synchronize chains
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Second) // Wait for mining to complete
		for i, node := range nodes {
			node.BroadcastChain(chains[i])
		}
		log.Println("Chains broadcasted by all nodes.")
	}()

	// Wait for all operations to complete
	wg.Wait()

	// Graceful shutdown on interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	for _, node := range nodes {
		if err := node.Host.Close(); err != nil {
			log.Printf("Failed to close node: %v", err)
		}
	}
	log.Println("All nodes shut down.")
}
