package blockchain

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"sort"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
)

// ValidatorNode represents a node that can validate blocks
type ValidatorNode struct {
	Address string
	Stake   float64
	hostID  string
}

// HostID returns the host ID of the validator
func (v ValidatorNode) HostID() (string, bool) {
	if v.hostID == "" {
		return "", false
	}
	return v.hostID, true
}

// StakePool represents the pool of stakes for all nodes.
type StakePool struct {
	Stakes       map[string]float64 // Wallet address -> Stake amount
	WalletToHost map[string]string  // Wallet address -> Host ID mapping
	mu           sync.Mutex         // Protects concurrent access
}

// NewStakePool initializes a new StakePool.
func NewStakePool() *StakePool {
	return &StakePool{
		Stakes:       make(map[string]float64),
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

// SelectValidator selects a validator based on stake weight
func (sp *StakePool) SelectValidator(peerHost host.Host) (string, string, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if len(sp.Stakes) == 0 {
		return "", "", errors.New("no validators available")
	}

	// For testing with a single validator, return it directly
	if len(sp.Stakes) == 1 {
		for walletAddr := range sp.Stakes {
			hostID := sp.WalletToHost[walletAddr]
			// Skip broadcasting during testing
			if peerHost != nil {
				if err := sp.BroadcastValidator(peerHost, walletAddr, hostID); err != nil {
					return "", "", err
				}
			}
			return walletAddr, hostID, nil
		}
	}

	// Calculate total stake
	var totalStake float64
	for _, stake := range sp.Stakes {
		totalStake += stake
	}

	// Select validator based on weighted probability
	r := rand.Float64() * totalStake
	var cumulativeStake float64

	for walletAddr, stake := range sp.Stakes {
		cumulativeStake += stake
		if cumulativeStake >= r {
			hostID := sp.WalletToHost[walletAddr]
			// Skip broadcasting during testing (when peerHost is nil)
			if peerHost != nil {
				if err := sp.BroadcastValidator(peerHost, walletAddr, hostID); err != nil {
					return "", "", err
				}
			}
			return walletAddr, hostID, nil
		}
	}

	return "", "", errors.New("failed to select validator")
}

// BroadcastValidator sends the selected validator's wallet address and host ID to all nodes.
func (sp *StakePool) BroadcastValidator(peerHost host.Host, walletAddress, hostID string) error {
	// Skip broadcasting if no peer host is provided (e.g. during testing)
	if peerHost == nil {
		return nil
	}

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

// GetValidators returns a specified number of validators
func (sp *StakePool) GetValidators(count int) ([]ValidatorNode, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if len(sp.Stakes) == 0 {
		return nil, errors.New("no validators available in stake pool")
	}

	// If we have fewer validators than requested, return all of them
	validatorCount := min(count, len(sp.Stakes))
	validators := make([]ValidatorNode, 0, validatorCount)

	// Sort validators by stake to get the highest staked validators
	type stakedValidator struct {
		address string
		hostID  string
		stake   float64
	}

	allValidators := make([]stakedValidator, 0, len(sp.Stakes))
	for addr, stake := range sp.Stakes {
		hostID := sp.WalletToHost[addr]
		allValidators = append(allValidators, stakedValidator{
			address: addr,
			hostID:  hostID,
			stake:   stake,
		})
	}

	// Sort by stake in descending order
	sort.Slice(allValidators, func(i, j int) bool {
		return allValidators[i].stake > allValidators[j].stake
	})

	// Take the top validators
	for i := 0; i < validatorCount; i++ {
		v := allValidators[i]
		validators = append(validators, ValidatorNode{
			Address: v.address,
			Stake:   v.stake,
			hostID:  v.hostID,
		})
	}

	log.Printf("🔍 Selected %d validators from stake pool", len(validators))
	return validators, nil
}

// Helper function for Go versions before 1.21
