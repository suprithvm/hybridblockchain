package test

import (
	"blockchain-core/blockchain"
	"testing"
	"time"
)

func TestP2P(t *testing.T) {
	// Setup test nodes
	setupTestNode := func() (*blockchain.Node, *blockchain.Blockchain, *blockchain.StakePool, *blockchain.Mempool) {
		bc := blockchain.InitialiseBlockchain()
		sp := blockchain.NewStakePool()
		mp := blockchain.NewMempool()
		node, err := blockchain.NewNode("/ip4/127.0.0.1/tcp/0", &bc, sp, mp)
		if err != nil {
			t.Fatalf("Failed to create node: %v", err)
		}
		return node, &bc, sp, mp
	}

	t.Run("Node Creation", func(t *testing.T) {
		node, _, _, _ := setupTestNode()
		if node.Host == nil {
			t.Error("Node host should not be nil")
		}
	})

	t.Run("Peer Connection", func(t *testing.T) {
		node1, _, _, _ := setupTestNode()
		node2, _, _, _ := setupTestNode()

		addr := node2.Host.Addrs()[0].String() + "/p2p/" + node2.Host.ID().String()
		err := node1.ConnectToPeer(addr)
		if err != nil {
			t.Errorf("Failed to connect peers: %v", err)
		}

		// Wait for connection to establish
		time.Sleep(time.Second)

		if len(node1.Host.Network().Peers()) != 1 {
			t.Error("Peers not properly connected")
		}
	})

	t.Run("Transaction Broadcasting", func(t *testing.T) {
		node1, _, _, mp1 := setupTestNode()
		node2, _, _, mp2 := setupTestNode()

		// Connect nodes
		addr := node2.Host.Addrs()[0].String() + "/p2p/" + node2.Host.ID().String()
		node1.ConnectToPeer(addr)
		time.Sleep(time.Second)

		// Create and broadcast transaction
		wallet, _ := blockchain.NewWallet()
		tx, _ := blockchain.NewTransaction(wallet.Address, "receiver123", 10.0, 1.0, wallet)

		err := node1.BroadcastTransaction(tx)
		if err != nil {
			t.Errorf("Failed to broadcast transaction: %v", err)
		}

		// Wait for propagation
		time.Sleep(time.Second)

		// Check if transaction reached node2's mempool
		txs2 := mp2.GetTransactions()
		found := false
		for _, tx2 := range txs2 {
			if tx2.ID == tx.ID {
				found = true
				break
			}
		}
		if !found {
			t.Error("Transaction not propagated to peer")
		}
	})

	t.Run("Block Broadcasting", func(t *testing.T) {
		node1, bc1, sp1, _ := setupTestNode()
		node2, bc2, _, _ := setupTestNode()

		// Connect nodes
		addr := node2.Host.Addrs()[0].String() + "/p2p/" + node2.Host.ID().String()
		node1.ConnectToPeer(addr)
		time.Sleep(time.Second)

		// Create and broadcast block
		wallet, _ := blockchain.NewWallet()
		sp1.AddStake(wallet.Address, 100.0)

		prevBlock := bc1.GetLatestBlock()
		newBlock := blockchain.NewBlock(prevBlock, blockchain.NewMempool(), prevBlock.Difficulty)
		blockchain.MineBlock(&newBlock, prevBlock, sp1, 10)

		node1.BroadcastBlock(newBlock)
		time.Sleep(time.Second)

		// Verify block propagation
		if len(bc2.Chain) != len(bc1.Chain) {
			t.Error("Block not propagated to peer")
		}
	})

	t.Run("Blockchain Sync", func(t *testing.T) {
		node1, bc1, sp1, _ := setupTestNode()
		node2, bc2, _, _ := setupTestNode()

		// Add some blocks to node1's chain
		wallet, _ := blockchain.NewWallet()
		sp1.AddStake(wallet.Address, 100.0)

		for i := 0; i < 3; i++ {
			prevBlock := bc1.GetLatestBlock()
			newBlock := blockchain.NewBlock(prevBlock, blockchain.NewMempool(), prevBlock.Difficulty)
			blockchain.MineBlock(&newBlock, prevBlock, sp1, 10)
			bc1.AddBlock(newBlock, wallet.Address, sp1)
		}

		// Connect and sync
		addr := node1.Host.Addrs()[0].String() + "/p2p/" + node1.Host.ID().String()
		node2.ConnectToPeer(addr)
		time.Sleep(2 * time.Second)

		if len(bc2.Chain) != len(bc1.Chain) {
			t.Errorf("Blockchain sync failed. Expected %d blocks, got %d",
				len(bc1.Chain), len(bc2.Chain))
		}
	})
}
