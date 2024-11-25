package test

import (
	"blockchain-core/blockchain"
	"testing"
	"time"
)

func TestBlockchainIntegration(t *testing.T) {
	t.Run("Full Network Operation", func(t *testing.T) {
		// Setup network with 3 nodes (2 miners, 1 user)
		miner1, bc1, sp1, mp1 := setupTestNode()
		miner2, bc2, sp2, mp2 := setupTestNode()
		user, bc3, _, mp3 := setupTestNode()

		// Create wallets
		minerWallet1, _ := blockchain.NewWallet()
		minerWallet2, _ := blockchain.NewWallet()
		userWallet, _ := blockchain.NewWallet()

		// Add stakes for miners
		sp1.AddStake(minerWallet1.Address, 100.0)
		sp2.AddStake(minerWallet2.Address, 100.0)

		// Connect nodes
		connectNodes(t, []*blockchain.Node{miner1, miner2, user})

		// Start mining operations
		go func() {
			for i := 0; i < 5; i++ {
				// Create and broadcast transaction
				tx, _ := blockchain.NewTransaction(
					userWallet.Address,
					"receiver123",
					1.0,
					0.1,
					userWallet,
				)
				user.BroadcastTransaction(tx)
				time.Sleep(time.Second)
			}
		}()

		// Wait for operations to complete
		time.Sleep(30 * time.Second)

		// Verify network state
		verifyNetworkState(t, []*blockchain.Blockchain{bc1, bc2, bc3})
	})
}

func connectNodes(t *testing.T, nodes []*blockchain.Node) {
	for i := 1; i < len(nodes); i++ {
		addr := nodes[0].Host.Addrs()[0].String() + "/p2p/" + nodes[0].Host.ID().String()
		err := nodes[i].ConnectToPeer(addr)
		if err != nil {
			t.Fatalf("Failed to connect nodes: %v", err)
		}
	}
	time.Sleep(time.Second)
}

func verifyNetworkState(t *testing.T, blockchains []*blockchain.Blockchain) {
	// Verify all nodes have the same chain length
	expectedLength := len(blockchains[0].Chain)
	for i, bc := range blockchains {
		if len(bc.Chain) != expectedLength {
			t.Errorf("Node %d has different chain length. Expected %d, got %d",
				i, expectedLength, len(bc.Chain))
		}
	}

	// Verify all nodes have the same last block
	lastBlockHash := blockchains[0].GetLatestBlock().Hash
	for i, bc := range blockchains {
		if bc.GetLatestBlock().Hash != lastBlockHash {
			t.Errorf("Node %d has different last block hash", i)
		}
	}
}
