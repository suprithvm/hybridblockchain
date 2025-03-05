package blockchain

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"time"

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
	Stakes       map[string]*StakeInfo // Wallet address -> Stake info
	WalletToHost map[string]string     // Wallet address -> Host ID mapping
	mu           sync.Mutex            // Protects concurrent access
}

// NewStakePool initializes a new StakePool.
func NewStakePool(bc interface{}) *StakePool {
	return &StakePool{
		Stakes:       make(map[string]*StakeInfo),
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
	if _, exists := sp.Stakes[walletAddress]; !exists {
		sp.Stakes[walletAddress] = &StakeInfo{
			Address:    walletAddress,
			Amount:     uint64(amount),
			StartTime:  time.Now(),
			LastActive: time.Now(),
		}
	} else {
		sp.Stakes[walletAddress].Amount += uint64(amount)
	}
	sp.WalletToHost[walletAddress] = hostID
	return nil
}

// RemoveStake removes a stake from a wallet address.
func (sp *StakePool) RemoveStake(walletAddress string, amount float64) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	stake, exists := sp.Stakes[walletAddress]
	if !exists || stake.Amount < uint64(amount) {
		return errors.New("not enough stake to remove")
	}
	stake.Amount -= uint64(amount)
	if stake.Amount == 0 {
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
		total += float64(stake.Amount)
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
		totalStake += float64(stake.Amount)
	}

	// Select validator based on weighted probability
	r := rand.Float64() * totalStake
	var cumulativeStake float64

	for walletAddr, stake := range sp.Stakes {
		cumulativeStake += float64(stake.Amount)
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
			stake:   float64(stake.Amount),
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

// Add these constants at the top
const (
	MinStakeAge          = 24 * time.Hour       // Minimum time before stake becomes active
	MaxStakeAge          = 365 * 24 * time.Hour // Maximum age for stake weight calculation
	BaseStakeWeight      = 100                  // Base weight for stake calculations
	WithdrawalLockPeriod = 72 * time.Hour       // Time required before withdrawal
)

// StakeInfo represents staking information
type StakeInfo struct {
	Address        string                `json:"address"`
	Amount         uint64                `json:"amount"`
	StartTime      time.Time             `json:"start_time"`
	LastRewardTime time.Time             `json:"last_reward_time"`
	LastActive     time.Time             `json:"last_active"`
	WithdrawalReq  *WithdrawalRequest    `json:"withdrawal_req,omitempty"`
	Violations     int                   `json:"violations"`
	Performance    *ValidatorPerformance `json:"performance"`
}

// Add these methods to StakePool
func (sp *StakePool) GetStakeWeight(stakeInfo *StakeInfo) uint64 {
	if stakeInfo == nil {
		return 0
	}

	age := time.Since(stakeInfo.StartTime)
	if age < MinStakeAge {
		return 0 // Stake not mature yet
	}

	// Cap the age at MaxStakeAge
	if age > MaxStakeAge {
		age = MaxStakeAge
	}

	// Calculate weight based on age and amount
	ageWeight := uint64(age.Hours() / 24)                    // Days staked
	baseWeight := (stakeInfo.Amount * BaseStakeWeight) / 1e9 // Normalize by billion

	return baseWeight + (baseWeight * ageWeight / 365) // Add up to 100% over a year
}

// RequestWithdrawal initiates stake withdrawal
func (sp *StakePool) RequestWithdrawal(address string) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	stake, exists := sp.Stakes[address]
	if !exists {
		return fmt.Errorf("no stake found for address: %s", address)
	}

	// Create a proper WithdrawalRequest instead of just using time.Time
	stake.WithdrawalReq = &WithdrawalRequest{
		RequestTime: time.Now(),
		Amount:      stake.Amount,
		Status:      "pending",
	}
	sp.Stakes[address] = stake

	return nil
}

// ProcessWithdrawal handles stake withdrawal after lockup period
func (sp *StakePool) ProcessWithdrawal(address string) (uint64, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	stake, exists := sp.Stakes[address]
	if !exists {
		return 0, fmt.Errorf("no stake found for address: %s", address)
	}

	if stake.WithdrawalReq == nil {
		return 0, fmt.Errorf("no withdrawal request found")
	}

	// Check time since request using the RequestTime field
	if time.Since(stake.WithdrawalReq.RequestTime) < WithdrawalLockPeriod {
		return 0, fmt.Errorf("withdrawal locked for %v more",
			WithdrawalLockPeriod-time.Since(stake.WithdrawalReq.RequestTime))
	}

	amount := stake.Amount
	delete(sp.Stakes, address)
	return amount, nil
}

// RecordViolation records a validator violation and handles slashing if needed
func (sp *StakePool) RecordViolation(address string) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	stake, exists := sp.Stakes[address]
	if !exists {
		return fmt.Errorf("no stake found for address: %s", address)
	}

	stake.Violations++
	if stake.Violations >= SlashingThreshold {
		// Slash 50% of stake
		slashedAmount := stake.Amount / 2
		stake.Amount -= slashedAmount
		// Could add slashed amounts to a community pool
	}

	sp.Stakes[address] = stake
	return nil
}

func (sp *StakePool) CreateStake(address string, amount uint64) (*StakeInfo, error) {
	now := time.Now()
	stake := &StakeInfo{
		Address:        address,
		Amount:         amount,
		StartTime:      now,
		LastRewardTime: now,
		LastActive:     now,
		WithdrawalReq:  nil,
		Violations:     0,
		Performance: &ValidatorPerformance{
			LastUpdate: now,
		},
	}
	sp.Stakes[address] = stake
	return stake, nil
}
