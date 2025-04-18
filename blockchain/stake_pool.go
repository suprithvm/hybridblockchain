package blockchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// StakeSyncRequest represents a request to sync stake pool state
type StakeSyncRequest struct {
	Type string `json:"type"`
}

// Stake represents a stake entry
type Stake struct {
	Amount uint64 `json:"amount"`
}



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
	stateRoot    string                // Added for GetValidatorInfo
	blockchain   *Blockchain           // Reference to blockchain
}

// NewStakePool initializes a new StakePool.
func NewStakePool(bc *Blockchain) *StakePool {
	sp := &StakePool{
		Stakes:       make(map[string]*StakeInfo),
		WalletToHost: make(map[string]string),
		blockchain:   bc,
	}
	return sp
}

// AddValidator adds a new validator to the stake pool
func (sp *StakePool) AddValidator(walletAddress string, stake float64, hostID string) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	log.Printf("🔐 Adding validator %s with stake %.4f", walletAddress, stake)

	// Validate inputs
	if walletAddress == "" {
		return fmt.Errorf("wallet address cannot be empty")
	}
	if hostID == "" {
		return fmt.Errorf("host ID cannot be empty")
	}
	if stake < 0 {
		return fmt.Errorf("stake amount cannot be negative")
	}

	// Create or update stake info
	if _, exists := sp.Stakes[walletAddress]; !exists {
		log.Printf("📝 Creating new stake entry for validator %s", walletAddress)
		sp.Stakes[walletAddress] = &StakeInfo{
			Address:        walletAddress,
			Amount:         uint64(stake),
			StartTime:      time.Now(),
			LastActive:     time.Now(),
			LastRewardTime: time.Now(),
			Performance: &ValidatorPerformance{
				LastUpdate: time.Now(),
			},
			HostID:      hostID,
			Timestamp:   time.Now().Unix(),
			IsValidator: true,
		}
	} else {
		log.Printf("📝 Updating existing stake entry for validator %s", walletAddress)
		sp.Stakes[walletAddress].Amount += uint64(stake)
		sp.Stakes[walletAddress].LastActive = time.Now()
		sp.Stakes[walletAddress].HostID = hostID
		sp.Stakes[walletAddress].IsValidator = true
		sp.Stakes[walletAddress].Timestamp = time.Now().Unix()
	}

	// Update host mapping
	sp.WalletToHost[walletAddress] = hostID
	log.Printf("✅ Successfully registered validator %s with host ID %s", walletAddress, hostID)

	// Log the current state of the stake pool
	log.Printf("📊 Current Stake Pool State:")
	log.Printf("   • Total Validators: %d", len(sp.Stakes))
	for addr, stake := range sp.Stakes {
		log.Printf("   • Validator %s:", addr)
		log.Printf("     - Stake Amount: %.4f", float64(stake.Amount))
		log.Printf("     - Host ID: %s", stake.HostID)
		log.Printf("     - Is Validator: %v", stake.IsValidator)
		log.Printf("     - Last Active: %s", stake.LastActive.Format(time.RFC3339))
	}

	return nil
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

	// For genesis block, select the first validator
	if len(sp.Stakes) == 1 {
		for walletAddr := range sp.Stakes {
			hostID := sp.WalletToHost[walletAddr]
			log.Printf("🎯 Selected genesis validator: %s", walletAddr)
			return walletAddr, hostID, nil
		}
	}

	// For non-genesis blocks, use weighted selection
	var totalStake float64
	for _, stake := range sp.Stakes {
		totalStake += float64(stake.Amount)
	}

	r := rand.Float64() * totalStake
	var cumulativeStake float64

	for walletAddr, stake := range sp.Stakes {
		cumulativeStake += float64(stake.Amount)
		if cumulativeStake >= r {
			hostID := sp.WalletToHost[walletAddr]
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

// GetValidators returns a list of active validators
func (sp *StakePool) GetValidators(count int) ([]ValidatorNode, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if len(sp.Stakes) == 0 {
		return nil, fmt.Errorf("no validators available in stake pool")
	}

	validators := make([]ValidatorNode, 0)
	for addr, stake := range sp.Stakes {
		if stake.Amount > 0 && time.Since(stake.StartTime) >= MinStakeAge {
			validators = append(validators, ValidatorNode{
				Address: addr,
				Stake:   float64(stake.Amount),
				hostID:  sp.WalletToHost[addr],
			})
		}
	}

	if len(validators) == 0 {
		return nil, fmt.Errorf("no active validators available")
	}

	// Sort validators by stake
	sort.Slice(validators, func(i, j int) bool {
		return validators[i].Stake > validators[j].Stake
	})

	// Return requested number of validators
	if count > len(validators) {
		count = len(validators)
	}

	log.Printf("🔍 Found %d active validators", count)
	return validators[:count], nil
}

// Helper function for Go versions before 1.21

// Add these constants at the top
const (
	MinStakeAge          = 24 * time.Hour       // Minimum time before stake becomes active
	MaxStakeAge          = 365 * 24 * time.Hour // Maximum age for stake weight calculation
	BaseStakeWeight      = 100                  // Base weight for stake calculations
	WithdrawalLockPeriod = 72 * time.Hour       // Time required before withdrawal
	MinValidatorStake    = 0.0                  // Minimum stake required for validation (0 for genesis validators)
	MaxInactivityPeriod  = 24 * time.Hour       // Maximum allowed inactivity period
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
	SelectionCount uint64                `json:"selection_count"`
	HostID         string                `json:"host_id"`
	Timestamp      int64                 `json:"timestamp"`
	IsValidator    bool                  `json:"is_validator"`
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

// StakePoolSync represents the sync state of stake pool
type StakePoolSync struct {
	Stakes       map[string]StakeInfo `json:"stakes"`
	StateRoot    string               `json:"state_root"`
	Timestamp    int64                `json:"timestamp"`
	LastSyncTime int64                `json:"last_sync_time"`
	PeerID       string               `json:"peer_id"`
}

// SyncWithPeer synchronizes the stake pool with a specific peer
func (sp *StakePool) SyncWithPeer(peerID peer.ID) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	// Log local stake pool state before sync
	log.Printf("Local stake pool state before sync:")
	log.Printf("Total validators: %d", len(sp.Stakes))
	for wallet, stake := range sp.Stakes {
		log.Printf("Validator %s: Stake %.4f", wallet, stake.Amount)
	}

	// Create a stream to the peer
	stream, err := sp.blockchain.Node.Host.NewStream(sp.blockchain.Node.ctx, peerID, protocol.ID(StakeSyncProtocol))
	if err != nil {
		return fmt.Errorf("failed to create stream: %v", err)
	}
	defer stream.Close()

	// Send sync request
	request := StakeSyncRequest{
		Type: "sync",
	}
	if err := json.NewEncoder(stream).Encode(request); err != nil {
		return fmt.Errorf("failed to send sync request: %v", err)
	}

	// Receive peer's stake pool data
 	var peerStakes map[string]*StakeInfo
	if err := json.NewDecoder(stream).Decode(&peerStakes); err != nil {
		return fmt.Errorf("failed to decode peer stakes: %v", err)
	}

	// Log received stake pool state
	log.Printf("Received stake pool state from peer:")
	log.Printf("Total validators: %d", len(peerStakes))
	for wallet, stake := range peerStakes {
		log.Printf("Validator %s: Stake %.4f", wallet, stake.Amount)
	}

	// Merge stakes from peer
	for wallet, peerStake := range peerStakes {
		// If we don't have this validator or peer has higher stake, update our stake
		if existingStake, exists := sp.Stakes[wallet]; !exists || peerStake.Amount > existingStake.Amount {
			sp.Stakes[wallet] = peerStake
			log.Printf("Updated stake for validator %s to %.4f", wallet, peerStake.Amount)
		}
	}

	// Log final stake pool state
	log.Printf("Final stake pool state after sync:")
	log.Printf("Total validators: %d", len(sp.Stakes))
	for wallet, stake := range sp.Stakes {
		log.Printf("Validator %s: Stake %.4f", wallet, stake.Amount)
	}

	return nil
}

// GetValidatorInfo returns the current validator information
func (sp *StakePool) GetValidatorInfo() ([]byte, error) {
	// Get all validators
	validators, err := sp.GetValidators(0) // Get all validators
	if err != nil {
		return nil, err
	}

	// Create validator info struct
	info := struct {
		Validators []ValidatorNode `json:"validators"`
		StateRoot  string          `json:"state_root"`
	}{
		Validators: validators,
		StateRoot:  sp.stateRoot,
	}

	// Marshal to JSON
	return json.Marshal(info)
}

// UpdateValidators updates the stake pool with received validator information
func (sp *StakePool) UpdateValidators(data []byte) error {
	// Unmarshal validator info
	var info struct {
		Validators []ValidatorNode `json:"validators"`
		StateRoot  string          `json:"state_root"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}

	// Update validators
	for _, validator := range info.Validators {
		if err := sp.AddStake(validator.Address, validator.hostID, float64(validator.Stake)); err != nil {
			return err
		}
	}

	// Update state root
	sp.stateRoot = info.StateRoot

	return nil
}
