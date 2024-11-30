package blockchain

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a libp2p host with DHT
func createTestHost(t *testing.T, port int) (host.Host, *dht.IpfsDHT) {
	ctx := context.Background()
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port)),
	)
	require.NoError(t, err)

	// Create DHT in server mode
	dhtInstance, err := dht.New(ctx, h, dht.Mode(dht.ModeServer))
	require.NoError(t, err)

	err = dhtInstance.Bootstrap(ctx)
	require.NoError(t, err)

	return h, dhtInstance
}

// Helper function to create a complete test node
func createTestNode(t *testing.T, port int) *Node {
	h, dhtInstance := createTestHost(t, port)
	node := &Node{
		Host:        h,
		DHT:         dhtInstance,
		PeerManager: NewPeerManager(),
		Blockchain:  &Blockchain{Chain: []Block{GenesisBlock()}},
	}

	// Bootstrap DHT
	ctx := context.Background()
	err := node.bootstrapDHT(ctx)
	require.NoError(t, err, "DHT bootstrap should succeed")

	// Set up protocol handler
	node.Host.SetStreamHandler(protocol.ID("/blockchain/1.0.0"), func(s network.Stream) {
		defer s.Close()

		// Read the incoming message
		buf, err := io.ReadAll(s)
		if err != nil {
			t.Logf("❌ Error reading message: %v", err)
			return
		}

		// Parse the message
		msg, err := FromJSON(buf)
		if err != nil {
			t.Logf("❌ Error parsing message: %v", err)
			return
		}

		t.Logf("📥 Received message: %+v", msg)

		// Send acknowledgment
		ack := NewMessage("ack", "Message received")
		ackBytes, _ := ack.ToJSON()
		_, err = s.Write(ackBytes)
		if err != nil {
			t.Logf("❌ Error sending ack: %v", err)
		}
	})

	return node
}

// Unit Tests

func TestDHTInitialization(t *testing.T) {
	logger, err := NewTestLogger("TestDHTInitialization")
	require.NoError(t, err)
	defer logger.Close()

	startTime := time.Now()

	t.Run("DHT Bootstrap", func(t *testing.T) {
		logger.Log("=== RUN   TestDHTInitialization/DHT_Bootstrap")
		node := createTestNode(t, 10000)
		defer node.Host.Close()

		logger.LogAssert(t, node.DHT != nil, "DHT should be initialized")
		logger.LogAssert(t, node.DHT.RoutingTable() != nil, "DHT routing table should be initialized")

		logger.LogTestResult("TestDHTInitialization/DHT_Bootstrap", true, time.Since(startTime))
	})

	t.Run("DHT Routing Table", func(t *testing.T) {
		// Create nodes with specific DHT mode
		node1 := createTestNode(t, 10001)
		node2 := createTestNode(t, 10002)
		defer node1.Host.Close()
		defer node2.Host.Close()

		ctx := context.Background()

		// Bootstrap both DHTs
		err := node1.DHT.Bootstrap(ctx)
		require.NoError(t, err)
		err = node2.DHT.Bootstrap(ctx)
		require.NoError(t, err)

		// Connect nodes directly first
		addr := node2.Host.Addrs()[0]
		peerInfo := peer.AddrInfo{
			ID:    node2.Host.ID(),
			Addrs: []ma.Multiaddr{addr},
		}

		err = node1.Host.Connect(ctx, peerInfo)
		require.NoError(t, err)

		// Provide some data to ensure DHT is working
		key := "test-key"
		err = node1.DHT.PutValue(ctx, key, []byte("test-value"))
		require.NoError(t, err)

		// Wait for DHT to update
		time.Sleep(2 * time.Second)

		// Verify routing table
		peers := node1.DHT.RoutingTable().ListPeers()
		assert.Contains(t, peers, node2.Host.ID(), "Node2 should be in Node1's routing table")
	})

	logger.LogTestResult("TestDHTInitialization", true, time.Since(startTime))
}

func TestPeerDiscovery(t *testing.T) {
	logger, err := NewTestLogger("Peer_Discovery")
	require.NoError(t, err)
	defer logger.Close()

	t.Run("Discover Peers via DHT", func(t *testing.T) {
		ctx := context.Background()

		// Create nodes with DHT server mode
		node1 := createTestNode(t, 10003)
		node2 := createTestNode(t, 10004)
		node3 := createTestNode(t, 10005)
		defer node1.Host.Close()
		defer node2.Host.Close()
		defer node3.Host.Close()

		// Bootstrap all nodes
		for _, node := range []*Node{node1, node2, node3} {
			err := node.DHT.Bootstrap(ctx)
			require.NoError(t, err)
		}

		// Connect node1 to node2
		addr2 := node2.Host.Addrs()[0]
		err := node1.Host.Connect(ctx, peer.AddrInfo{
			ID:    node2.Host.ID(),
			Addrs: []ma.Multiaddr{addr2},
		})
		require.NoError(t, err)

		// Connect node2 to node3
		addr3 := node3.Host.Addrs()[0]
		err = node2.Host.Connect(ctx, peer.AddrInfo{
			ID:    node3.Host.ID(),
			Addrs: []ma.Multiaddr{addr3},
		})
		require.NoError(t, err)

		// Provide some data to help with discovery
		err = node3.DHT.PutValue(ctx, "discovery-key", []byte("test"))
		require.NoError(t, err)

		// Start peer discovery
		go node1.DiscoverPeers()
		go node2.DiscoverPeers()
		go node3.DiscoverPeers()

		// Wait and verify with retries
		discovered := false
		for i := 0; i < 5 && !discovered; i++ {
			time.Sleep(2 * time.Second)
			peers := node1.Host.Network().Peers()
			if contains(peers, node3.Host.ID()) {
				discovered = true
				break
			}
		}

		assert.True(t, discovered, "Node1 should discover Node3 through DHT")
	})
}

func contains(peers []peer.ID, id peer.ID) bool {
	for _, p := range peers {
		if p == id {
			return true
		}
	}
	return false
}

func TestStreamHandling(t *testing.T) {
	t.Run("Protocol Handler Registration", func(t *testing.T) {
		t.Logf("🔧 Creating test node on port 10006...")
		node := createTestNode(t, 10006)
		defer node.Host.Close()

		protocols := node.Host.Mux().Protocols()
		t.Logf("📋 Registered protocols: %v", protocols)
		assert.Contains(t, protocols, protocol.ID("/blockchain/1.0.0"),
			"Protocol /blockchain/1.0.0 should be registered")
	})

	t.Run("Message Exchange", func(t *testing.T) {
		node1 := createTestNode(t, 10007)
		node2 := createTestNode(t, 10008)
		defer node1.Host.Close()
		defer node2.Host.Close()

		// Connect nodes
		addr := node2.Host.Addrs()[0]
		err := node1.Host.Connect(context.Background(), peer.AddrInfo{
			ID:    node2.Host.ID(),
			Addrs: []ma.Multiaddr{addr},
		})
		require.NoError(t, err)

		// Wait for connection to stabilize
		time.Sleep(time.Second)

		// Send test message
		stream, err := node1.Host.NewStream(context.Background(), node2.Host.ID(), "/blockchain/1.0.0")
		require.NoError(t, err)
		defer stream.Close()

		testMsg := NewMessage("test", "Hello, Node2!")
		msgBytes, err := testMsg.ToJSON()
		require.NoError(t, err)

		_, err = stream.Write(msgBytes)
		require.NoError(t, err)

		// Read acknowledgment with timeout
		stream.SetDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 1024)
		n, err := stream.Read(buf)
		require.NoError(t, err)

		ack, err := FromJSON(buf[:n])
		require.NoError(t, err)
		assert.Equal(t, "ack", ack.Type)
	})
}

// Integration Tests

func TestNetworkIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("Full Network Formation", func(t *testing.T) {
		numNodes := 5
		nodes := make([]*Node, numNodes)
		ctx := context.Background()

		// Create nodes
		for i := 0; i < numNodes; i++ {
			nodes[i] = createTestNode(t, 20000+i)
			defer nodes[i].Host.Close()
		}

		// Connect nodes in a ring topology
		for i := 0; i < numNodes; i++ {
			nextIndex := (i + 1) % numNodes
			addr := nodes[nextIndex].Host.Addrs()[0]
			err := nodes[i].Host.Connect(ctx, peer.AddrInfo{
				ID:    nodes[nextIndex].Host.ID(),
				Addrs: []ma.Multiaddr{addr},
			})
			require.NoError(t, err)
		}

		// Start peer discovery on all nodes
		for _, node := range nodes {
			go node.DiscoverPeers()
		}

		// Wait for network formation
		time.Sleep(5 * time.Second)

		// Verify full mesh connectivity
		for i, node := range nodes {
			peers := node.Host.Network().Peers()
			assert.GreaterOrEqual(t, len(peers), numNodes-1,
				fmt.Sprintf("Node %d should be connected to all other nodes", i))
		}
	})

	t.Run("DHT Value Exchange", func(t *testing.T) {
		node1 := createTestNode(t, 20100)
		node2 := createTestNode(t, 20101)
		defer node1.Host.Close()
		defer node2.Host.Close()

		ctx := context.Background()

		// Connect nodes
		addr := node2.Host.Addrs()[0]
		err := node1.Host.Connect(ctx, peer.AddrInfo{
			ID:    node2.Host.ID(),
			Addrs: []ma.Multiaddr{addr},
		})
		require.NoError(t, err)

		// Store value in DHT
		key := "test-key"
		value := []byte("test-value")
		err = node1.DHT.PutValue(ctx, key, value)
		require.NoError(t, err)

		// Retrieve value from other node
		retrievedValue, err := node2.DHT.GetValue(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, value, retrievedValue)
	})
}

// Performance Tests

func TestNetworkPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("⏭️ Skipping performance test in short mode")
	}

	t.Run("Large Network Formation", func(t *testing.T) {
		numNodes := 20
		t.Logf("🔧 Creating network with %d nodes...", numNodes)
		nodes := make([]*Node, numNodes)
		var wg sync.WaitGroup

		startTime := time.Now()
		// Create nodes concurrently
		for i := 0; i < numNodes; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				nodes[index] = createTestNode(t, 30000+index)
				t.Logf("✅ Node %d created with ID: %s", index, nodes[index].Host.ID().String())
			}(i)
		}
		wg.Wait()
		t.Logf("⏱️ Node creation took: %v", time.Since(startTime))

		// Cleanup
		defer func() {
			t.Log("🧹 Cleaning up nodes...")
			for _, node := range nodes {
				node.Host.Close()
			}
		}()

		// Connect nodes in a mesh network
		t.Log("🤝 Establishing mesh network connections...")
		connectionStart := time.Now()
		ctx := context.Background()
		var connectionCount int
		for i := 0; i < numNodes; i++ {
			for j := i + 1; j < numNodes; j++ {
				addr := nodes[j].Host.Addrs()[0]
				err := nodes[i].Host.Connect(ctx, peer.AddrInfo{
					ID:    nodes[j].Host.ID(),
					Addrs: []ma.Multiaddr{addr},
				})
				if err != nil {
					t.Logf("⚠️ Connection failed between nodes %d and %d: %v", i, j, err)
				} else {
					connectionCount++
				}
			}
			if i%5 == 0 { // Log progress every 5 nodes
				t.Logf("📊 Connected node %d to all subsequent nodes", i)
			}
		}
		connectionDuration := time.Since(connectionStart)
		t.Logf("📈 Network Statistics:")
		t.Logf("   • Total Connections: %d", connectionCount)
		t.Logf("   • Connection Time: %v", connectionDuration)
		t.Logf("   • Average Time per Connection: %v", connectionDuration/time.Duration(connectionCount))

		// Test message broadcasting
		messageCount := 10
		t.Logf("📢 Broadcasting %d messages through the network...", messageCount)
		broadcastStart := time.Now()

		for i := 0; i < messageCount; i++ {
			msg := NewMessage("broadcast-test", fmt.Sprintf("message-%d", i))
			msgStr, err := msg.ToJSON()
			require.NoError(t, err)
			nodes[0].BroadcastMessage(string(msgStr))
			if i%20 == 0 { // Log progress every 20 messages
				t.Logf("   • Broadcasted %d messages...", i)
			}
		}

		broadcastDuration := time.Since(broadcastStart)
		t.Logf("📊 Broadcast Performance:")
		t.Logf("   • Total Messages: %d", messageCount)
		t.Logf("   • Total Time: %v", broadcastDuration)
		t.Logf("   • Messages per Second: %.2f", float64(messageCount)/broadcastDuration.Seconds())

		assert.Less(t, broadcastDuration, 30*time.Second,
			"Broadcasting should complete within reasonable time")
	})
}
