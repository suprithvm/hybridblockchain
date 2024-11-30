package blockchain

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// PeerManager tracks active peers and their addresses
type PeerManager struct {
	ActivePeers map[peer.ID]*peer.AddrInfo // Active peers
	LastSeen    map[peer.ID]time.Time      // Last time a peer was seen
	ConnectionPool map[peer.ID]bool          // Tracks active connections
	mu          sync.RWMutex               // Mutex for thread safety
}

// NewPeerManager initializes a PeerManager
func NewPeerManager() *PeerManager {
	return &PeerManager{
		ActivePeers:    make(map[peer.ID]*peer.AddrInfo),
		LastSeen:       make(map[peer.ID]time.Time),
		ConnectionPool: make(map[peer.ID]bool),
	}
}

const MaxConnections = 50 // Maximum number of active peer connections
// AddPeer adds a peer to the ActivePeers map and the connection pool if below the limit
func (pm *PeerManager) AddPeer(pInfo *peer.AddrInfo) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if connection pool is full
	if len(pm.ConnectionPool) >= MaxConnections {
		return false
	}

	pm.ActivePeers[pInfo.ID] = pInfo
	pm.ConnectionPool[pInfo.ID] = true
	return true
}


// RemovePeer removes a peer from ActivePeers and the connection pool
func (pm *PeerManager) RemovePeer(peerID peer.ID) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.ActivePeers, peerID)
	delete(pm.ConnectionPool, peerID)
}

// GetPeers returns a list of all active peers
func (pm *PeerManager) GetPeers() []*peer.AddrInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	var peers []*peer.AddrInfo
	for _, pInfo := range pm.ActivePeers {
		peers = append(peers, pInfo)
	}
	return peers
}

// UpdateLastSeen updates the last heartbeat timestamp for a peer
func (pm *PeerManager) UpdateLastSeen(peerID peer.ID) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.LastSeen[peerID] = time.Now()
}

// GetInactivePeers identifies peers that haven't sent a heartbeat recently
func (pm *PeerManager) GetInactivePeers(timeout int64) []peer.ID {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	inactivePeers := []peer.ID{}
	now := time.Now()

	for peerID, lastSeen := range pm.LastSeen {
		if now.Sub(lastSeen).Seconds() > float64(timeout) {
			inactivePeers = append(inactivePeers, peerID)
		}
	}

	return inactivePeers
}

// GetLastSeen gets the last seen timestamp for a peer
func (pm *PeerManager) GetLastSeen(peerID peer.ID) (time.Time, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	lastSeen, exists := pm.LastSeen[peerID]
	return lastSeen, exists
}



// PrunePeers removes inactive peers from the connection pool
func (pm *PeerManager) PrunePeers(timeout int64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	for peerID, lastSeen := range pm.LastSeen {
		if now.Sub(lastSeen).Seconds() > float64(timeout) {
			delete(pm.ActivePeers, peerID)
			delete(pm.ConnectionPool, peerID)
		}
	}
}


// GetConnectionPoolSize returns the current size of the connection pool
func (pm *PeerManager) GetConnectionPoolSize() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.ConnectionPool)
}