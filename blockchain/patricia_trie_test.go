package blockchain

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
)

// TestNodeInitialization verifies that a node is initialized correctly.
func TestNodeInitialization(t *testing.T) {
	node, err := NewNode("/ip4/127.0.0.1/tcp/0")
	if err != nil {
		t.Fatalf("Failed to initialize node: %v", err)
	}
	defer node.Host.Close()

	t.Logf("Node initialized with ID: %s", node.Host.ID().String())
	if len(node.Host.Addrs()) == 0 {
		t.Fatalf("Node has no listening addresses.")
	}
}

// TestPeerConnection verifies that nodes can connect to each other.
func TestPeerConnection(t *testing.T) {
	nodeA, _ := NewNode("/ip4/127.0.0.1/tcp/0")
	nodeB, _ := NewNode("/ip4/127.0.0.1/tcp/0")
	defer nodeA.Host.Close()
	defer nodeB.Host.Close()

	peerAddr := nodeB.Host.Addrs()[0].String() + "/p2p/" + nodeB.Host.ID().String()
	if err := nodeA.ConnectToPeer(peerAddr); err != nil {
		t.Fatalf("Failed to connect to peer: %v", err)
	}

	// Verify connection
	if len(nodeA.Host.Peerstore().Peers()) == 0 {
		t.Fatalf("Node A has no peers.")
	}
	t.Logf("Node A connected to Node B successfully.")
}

// TestBroadcastTransaction verifies broadcasting transactions between nodes.
func TestBroadcastTransaction(t *testing.T) {
	nodeA, _ := NewNode("/ip4/127.0.0.1/tcp/0")
	nodeB, _ := NewNode("/ip4/127.0.0.1/tcp/0")
	defer nodeA.Host.Close()
	defer nodeB.Host.Close()

	peerAddr := nodeB.Host.Addrs()[0].String() + "/p2p/" + nodeB.Host.ID().String()
	if err := nodeA.ConnectToPeer(peerAddr); err != nil {
		t.Fatalf("Failed to connect to peer: %v", err)
	}

	tx := Transaction{
		TransactionID: "tx123",
		Sender:        "Alice",
		Receiver:      "Bob",
		Amount:        50.0,
		Timestamp:     time.Now().Unix(),
	}

	go nodeB.Host.SetStreamHandler("/blockchain/1.0.0/transaction", func(s network.Stream) {
		defer s.Close()

		buf := make([]byte, 512)
		n, err := s.Read(buf)
		if err != nil {
			t.Errorf("Error reading transaction stream: %v", err)
			return
		}

		receivedTx, err := DeserializeTransaction(buf[:n])
		if err != nil {
			t.Errorf("Failed to deserialize transaction: %v", err)
			return
		}

		t.Logf("Transaction received: %+v", receivedTx)
	})

	time.Sleep(1 * time.Second) // Allow stream handler setup

	nodeA.BroadcastTransaction(tx)
	time.Sleep(1 * time.Second) // Allow broadcast time
}

// TestBroadcastBlock verifies broadcasting blocks between nodes.
func TestBroadcastBlock(t *testing.T) {
	nodeA, _ := NewNode("/ip4/127.0.0.1/tcp/0")
	nodeB, _ := NewNode("/ip4/127.0.0.1/tcp/0")
	defer nodeA.Host.Close()
	defer nodeB.Host.Close()

	peerAddr := nodeB.Host.Addrs()[0].String() + "/p2p/" + nodeB.Host.ID().String()
	if err := nodeA.ConnectToPeer(peerAddr); err != nil {
		t.Fatalf("Failed to connect to peer: %v", err)
	}

	block := Block{
		BlockNumber:  1,
		PreviousHash: "0x000...",
		Timestamp:    time.Now().Unix(),
		Hash:         "0xabc123",
		Difficulty:   1,
	}

	go nodeB.Host.SetStreamHandler("/blockchain/1.0.0/block", func(s network.Stream) {
		defer s.Close()

		buf := make([]byte, 4096)
		n, err := s.Read(buf)
		if err != nil {
			t.Errorf("Error reading block stream: %v", err)
			return
		}

		receivedBlock, err := DeserializeBlock(buf[:n])
		if err != nil {
			t.Errorf("Failed to deserialize block: %v", err)
			return
		}

		t.Logf("Block received: %+v", receivedBlock)
	})

	time.Sleep(1 * time.Second) // Allow stream handler setup

	nodeA.BroadcastBlock(block)
	time.Sleep(1 * time.Second) // Allow broadcast time
}
