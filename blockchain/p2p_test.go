package blockchain

import (
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestHost creates a new libp2p host for testing
func createTestHost(t testing.TB) host.Host {
	host, err := libp2p.New()
	require.NoError(t, err, "Failed to create libp2p host")
	return host
}

// TestPeerManagerInitialization tests the basic initialization of PeerManager
func TestPeerManagerInitialization(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	peerManager := NewPeerManager(host)

	assert.NotNil(t, peerManager, "PeerManager should be initialized")
	assert.Equal(t, host, peerManager.host, "Host should be correctly set")
	assert.NotNil(t, peerManager.peers, "Peers map should be initialized")
	assert.NotNil(t, peerManager.blacklist, "Blacklist map should be initialized")
}

// TestAddManualPeer tests the manual peer addition functionality
func TestAddManualPeer(t *testing.T) {
	// Create two hosts for peer connection
	host1 := createTestHost(t)
	host2 := createTestHost(t)
	defer host1.Close()
	defer host2.Close()

	// Create peer manager for host1
	peerManager1 := NewPeerManager(host1)

	// Use a valid port for the multiaddress (5050)
	addr2, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/5050/p2p/%s", host2.ID()))
	require.NoError(t, err, "Failed to create multiaddress")

	peerInfo2 := peer.AddrInfo{
		ID:    host2.ID(),
		Addrs: []multiaddr.Multiaddr{addr2},
	}

	// Introduce a small delay to ensure hosts are ready
	time.Sleep(200 * time.Millisecond)

	// Test cases for manual peer addition
	testCases := []struct {
		name           string
		config         *PeerAdditionConfig
		expectedResult bool
	}{
		{
			name: "Basic Validation - Successful Addition",
			config: &PeerAdditionConfig{
				MaxTrustedPeers:     50,
				ConnectionTimeout:   10 * time.Second,
				ValidationStrategy:  BasicValidation,
			},
			expectedResult: true,
		},
		{
			name: "Strict Validation - Successful Addition",
			config: &PeerAdditionConfig{
				MaxTrustedPeers:     50,
				ConnectionTimeout:   10 * time.Second,
				ValidationStrategy:  StrictValidation,
			},
			expectedResult: true,
		},
		{
			name: "Peer Limit Exceeded",
			config: &PeerAdditionConfig{
				MaxTrustedPeers:     0,
				ConnectionTimeout:   10 * time.Second,
				ValidationStrategy:  BasicValidation,
			},
			expectedResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := peerManager1.AddManualPeer(peerInfo2, tc.config)

			if tc.expectedResult {
				assert.NoError(t, err, "Peer addition should succeed")
				// Verify peer was added
				trustedPeers := peerManager1.GetTrustedPeers()
				assert.Contains(t, trustedPeers, host2.ID(), "Peer should be in trusted peers list")
			} else {
				assert.Error(t, err, "Peer addition should fail")
			}
		})
	}
}

// TestBlacklistPeer tests the peer blacklisting functionality
func TestBlacklistPeer(t *testing.T) {
	host := createTestHost(t)
	defer host.Close()

	peerManager := NewPeerManager(host)

	// Create a test peer
	testPeerID := host.ID()

	// Test blacklisting
	t.Run("Blacklist Peer", func(t *testing.T) {
		peerManager.BlacklistPeer(testPeerID, 1*time.Hour)

		// Check if peer is blacklisted
		assert.True(t, peerManager.IsBlacklisted(testPeerID), "Peer should be blacklisted")
	})

	// Test blacklist expiration
	t.Run("Blacklist Expiration", func(t *testing.T) {
		peerManager.BlacklistPeer(testPeerID, 1*time.Millisecond)

		// Wait for blacklist to expire
		time.Sleep(2 * time.Millisecond)

		assert.False(t, peerManager.IsBlacklisted(testPeerID), "Peer should no longer be blacklisted")
	})
}

// TestPeerValidation tests different peer validation strategies
func TestPeerValidation(t *testing.T) {
	host1 := createTestHost(t)
	host2 := createTestHost(t)
	defer host1.Close()
	defer host2.Close()

	peerManager1 := NewPeerManager(host1)

	addr2, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/0/p2p/%s", host2.ID()))
	require.NoError(t, err, "Failed to create multiaddress")

	peerInfo2 := peer.AddrInfo{
		ID:    host2.ID(),
		Addrs: []multiaddr.Multiaddr{addr2},
	}

	validationStrategies := []struct {
		name      string
		strategy  PeerValidationStrategy
		shouldAdd bool
	}{
		{"Basic Validation", BasicValidation, true},
		{"Strict Validation", StrictValidation, true},
		{"Custom Validation", CustomValidation, true},
	}

	for _, vs := range validationStrategies {
		t.Run(fmt.Sprintf("Validation Strategy: %s", vs.name), func(t *testing.T) {
			config := &PeerAdditionConfig{
				MaxTrustedPeers:     50,
				ConnectionTimeout:   10 * time.Second,
				ValidationStrategy:  vs.strategy,
			}

			err := peerManager1.AddManualPeer(peerInfo2, config)

			if vs.shouldAdd {
				assert.NoError(t, err, "Peer addition should succeed")
				// Verify peer was added
				trustedPeers := peerManager1.GetTrustedPeers()
				assert.Contains(t, trustedPeers, host2.ID(), "Peer should be in trusted peers list")
			} else {
				assert.Error(t, err, "Peer addition should fail")
			}
		})
	}
}

// TestIntegratedPeerManagement is an integration test for comprehensive peer management
func TestIntegratedPeerManagement(t *testing.T) {
	// Create multiple hosts to simulate a network
	hosts := make([]host.Host, 5)
	peerManagers := make([]*PeerManager, 5)

	// Initialize hosts and peer managers
	for i := 0; i < 5; i++ {
		hosts[i] = createTestHost(t)
		peerManagers[i] = NewPeerManager(hosts[i])
		defer hosts[i].Close()
	}

	// Simulate peer discovery and addition
	t.Run("Peer Discovery and Addition", func(t *testing.T) {
		for i := 0; i < len(hosts)-1; i++ {
			addr := peer.AddrInfo{
				ID:    hosts[i+1].ID(),
				Addrs: hosts[i+1].Addrs(),
			}

			err := peerManagers[i].AddManualPeer(addr, nil)
			assert.NoError(t, err, fmt.Sprintf("Should add peer %d to peer manager %d", i+1, i))
		}
	})

	// Test peer blacklisting in a network context
	t.Run("Network Blacklisting", func(t *testing.T) {
		// Blacklist a peer
		peerManagers[0].BlacklistPeer(hosts[1].ID(), 1*time.Hour)

		// Verify blacklisting
		assert.True(t, peerManagers[0].IsBlacklisted(hosts[1].ID()), "Peer should be blacklisted")
		// Ensure blacklisted peer is not in trusted peers
		trustedPeers := peerManagers[0].GetTrustedPeers()
		assert.NotContains(t, trustedPeers, hosts[1].ID(), "Blacklisted peer should not be in trusted peers")
	})

	// Test peer cleanup
	t.Run("Peer Cleanup", func(t *testing.T) {
		initialPeerCount := len(peerManagers[0].GetTrustedPeers())
		// Blacklist some peers
		peerManagers[0].BlacklistPeer(hosts[2].ID(), 1*time.Millisecond)
		peerManagers[0].BlacklistPeer(hosts[3].ID(), 1*time.Millisecond)

		// Wait for blacklist to expire
		time.Sleep(2 * time.Millisecond)

		// Cleanup blacklisted peers
		peerManagers[0].CleanupBlacklistedPeers()

		// Verify cleanup
		currentPeerCount := len(peerManagers[0].GetTrustedPeers())
		assert.GreaterOrEqual(t, currentPeerCount, initialPeerCount, "Peer count should be maintained after cleanup")
	})
}

// Benchmark performance of peer management operations
func BenchmarkPeerManagement(b *testing.B) {
	host := createTestHost(b)
	defer host.Close()

	peerManager := NewPeerManager(host)

	b.Run("AddManualPeer", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			addr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 10000+i))
			peerID, _ := peer.Decode(fmt.Sprintf("QmcustomPeer%d", i))
			peerInfo := peer.AddrInfo{
				ID:    peerID,
				Addrs: []multiaddr.Multiaddr{addr},
			}

			_ = peerManager.AddManualPeer(peerInfo, nil)
		}
	})

	b.Run("BlacklistPeer", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			peerID, _ := peer.Decode(fmt.Sprintf("QmcustomPeer%d", i))
			peerManager.BlacklistPeer(peerID, 1*time.Hour)
		}
	})
}
