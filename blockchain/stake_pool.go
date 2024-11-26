package blockchain

import (
	"errors"
	"math/rand"
	"sync"
	"context"
	"log"
	"github.com/libp2p/go-libp2p/core/host"

)




// StakePool represents the pool of stakes for all nodes.
type StakePool struct {
	Stakes      map[string]float64 // Wallet address -> Stake amount
	WalletToHost map[string]string // Wallet address -> Host ID mapping
	mu          sync.Mutex         // Protects concurrent access
}

// NewStakePool initializes a new StakePool.
func NewStakePool() *StakePool {
	return &StakePool{
		Stakes:      make(map[string]float64),
		WalletToHost: make(map[string]string),
	}
}

// AddStake adds a stake for a wallet address.
func (sp *StakePool) AddStake(walletAddress, hostID string, amount float64) error {
	if amount <= 0 {
		return errors.New("stake amount must be positive")
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.Stakes[walletAddress] += amount
	sp.WalletToHost[walletAddress] = hostID
	return nil
}

// RemoveStake removes a stake from a wallet address.
func (sp *StakePool) RemoveStake(walletAddress string, amount float64) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.Stakes[walletAddress] < amount {
		return errors.New("not enough stake to remove")
	}
	sp.Stakes[walletAddress] -= amount
	if sp.Stakes[walletAddress] == 0 {
		delete(sp.Stakes, walletAddress)
		delete(sp.WalletToHost, walletAddress)
	}
	return nil
}

// GetTotalStake calculates the total stake in the pool.
func (sp *StakePool) GetTotalStake() float64 {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	total := 0.0
	for _, stake := range sp.Stakes {
		total += stake
	}
	return total
}


// SelectValidator selects a validator based on stakes.
func (sp *StakePool) SelectValidator(peerHost host.Host) (string, string, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	// Calculate total stake
	totalStake := 0.0
	for _, stake := range sp.Stakes {
		totalStake += stake
	}

	if totalStake == 0 {
		return "", "", errors.New("no stakes in the pool")
	}

	// Weighted random selection
	randPoint := rand.Float64() * totalStake
	accumulated := 0.0
	var selectedWallet string
	for wallet, stake := range sp.Stakes {
		accumulated += stake
		if randPoint <= accumulated {
			selectedWallet = wallet
			break
		}
	}

	selectedHostID := sp.WalletToHost[selectedWallet]
	if err := sp.BroadcastValidator(peerHost, selectedWallet, selectedHostID); err != nil {
		return "", "", err
	}
	return selectedWallet, selectedHostID, nil
}




// BroadcastValidator sends the selected validator's wallet address and host ID to all nodes.
func (sp *StakePool) BroadcastValidator(peerHost host.Host, walletAddress, hostID string) error {
	data := walletAddress + "," + hostID // Serialize wallet address and host ID
	for _, peer := range peerHost.Peerstore().Peers() {
		if peer == peerHost.ID() {
			continue
		}
		stream, err := peerHost.NewStream(context.Background(), peer, "/blockchain/1.0.0/validator")
		if err != nil {
			log.Printf("Error opening stream to peer %s: %v", peer, err)
			continue
		}
		defer stream.Close()
		if _, err := stream.Write([]byte(data)); err != nil {
			log.Printf("Error writing validator data to stream: %v", err)
		}
	}
	log.Printf("Validator broadcasted: Wallet: %s, HostID: %s", walletAddress, hostID)
	return nil
}

