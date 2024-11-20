package blockchain

import (
	"errors"
	"math/rand"
	"sync"
)


// StakePool represents the pool of stakes for all nodes.
type StakePool struct {
	Stakes map[string]float64 // Node ID -> Stake amount
	mu     sync.Mutex         // Protects concurrent access
}

// NewStakePool initializes a new StakePool.
func NewStakePool() *StakePool {
	return &StakePool{
		Stakes: make(map[string]float64),
	}
}

// AddStake adds a stake for a node.
func (sp *StakePool) AddStake(nodeID string, amount float64) error {
	if amount <= 0 {
		return errors.New("stake amount must be positive")
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.Stakes[nodeID] += amount
	return nil
}

// RemoveStake removes a stake from a node.
func (sp *StakePool) RemoveStake(nodeID string, amount float64) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.Stakes[nodeID] < amount {
		return errors.New("not enough stake to remove")
	}
	sp.Stakes[nodeID] -= amount
	if sp.Stakes[nodeID] == 0 {
		delete(sp.Stakes, nodeID) // Remove node if no stake left
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

// SelectValidator randomly selects a validator based on weighted stakes.
func (sp *StakePool) SelectValidator() (string, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	// Calculate total stake
	totalStake := 0.0
	for _, stake := range sp.Stakes {
		totalStake += stake
	}

	// Handle case where no stakes exist
	if totalStake == 0 {
		return "", errors.New("no stakes in the pool")
	}

	// Weighted random selection
	randPoint := rand.Float64() * totalStake
	accumulated := 0.0
	for nodeID, stake := range sp.Stakes {
		accumulated += stake
		if randPoint <= accumulated {
			return nodeID, nil
		}
	}

	return "", errors.New("validator selection failed")
}
