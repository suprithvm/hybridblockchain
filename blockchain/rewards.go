package blockchain

import (
	"math"
	"sync"
	"time"
)

const (
	BaseBlockReward      = 50.0           // Base reward for block validation
	StakeRewardRate      = 0.05           // 5% annual stake reward
	ValidatorRewardShare = 0.70           // 70% of rewards go to validator
	StakerRewardShare    = 0.30           // 30% of rewards go to stakers
	RewardHalvingPeriod  = 210000         // Number of blocks between reward halvings
	MinStakeTime         = 24 * time.Hour // Minimum time before earning rewards
	MaxRewardMultiplier  = 2.0            // Maximum performance multiplier
)

// RewardCalculator handles reward calculations and distribution
type RewardCalculator struct {
	blockchain    *Blockchain
	stakePool     *StakePool
	mu            sync.RWMutex
	lastUpdate    time.Time
	rewardHistory map[string][]RewardEvent
}

// RewardEvent represents a reward distribution event
type RewardEvent struct {
	Address     string
	Amount      float64
	Type        string // "validator" or "stake"
	BlockHeight uint64
	Timestamp   time.Time
}

// NewRewardCalculator creates a new reward calculator instance
func NewRewardCalculator(bc *Blockchain, sp *StakePool) *RewardCalculator {
	return &RewardCalculator{
		blockchain:    bc,
		stakePool:     sp,
		lastUpdate:    time.Now(),
		rewardHistory: make(map[string][]RewardEvent),
	}
}

// CalculateBlockReward calculates the reward for a block
func (rc *RewardCalculator) CalculateBlockReward(blockHeight uint64) float64 {
	halvings := blockHeight / RewardHalvingPeriod
	reward := BaseBlockReward / math.Pow(2, float64(halvings))
	return math.Max(reward, 0)
}

// CalculateValidatorReward calculates validator's reward
func (rc *RewardCalculator) CalculateValidatorReward(validator *Validator, blockReward float64) float64 {
	if validator == nil {
		return 0
	}

	baseReward := blockReward * ValidatorRewardShare

	// Calculate performance multiplier
	performanceMultiplier := rc.calculatePerformanceMultiplier(validator)

	return baseReward * performanceMultiplier
}

// calculatePerformanceMultiplier calculates reward multiplier based on validator performance
func (rc *RewardCalculator) calculatePerformanceMultiplier(validator *Validator) float64 {
	if validator.Score < 50 {
		return 0.5 // Minimum 50% rewards for poor performance
	}

	multiplier := float64(validator.Score) / 100.0
	return math.Min(multiplier, MaxRewardMultiplier)
}

// CalculateStakeReward calculates staking rewards
func (rc *RewardCalculator) CalculateStakeReward(stake *StakeInfo, blockReward float64) float64 {
	if stake == nil || stake.Amount == 0 {
		return 0
	}

	// Check minimum stake time
	if time.Since(stake.StartTime) < MinStakeTime {
		return 0
	}

	// Calculate time-based stake reward
	timeSinceLastReward := time.Since(stake.LastRewardTime)
	annualReward := float64(stake.Amount) * StakeRewardRate
	timeBasedReward := annualReward * timeSinceLastReward.Hours() / (24 * 365)

	// Calculate proportional block reward
	totalStake := rc.stakePool.GetTotalStake()
	if totalStake == 0 {
		return timeBasedReward
	}

	blockStakeReward := blockReward * StakerRewardShare * (float64(stake.Amount) / float64(totalStake))

	return timeBasedReward + blockStakeReward
}

// DistributeRewards handles reward distribution
func (rc *RewardCalculator) DistributeRewards(block *Block, validator *Validator) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	blockReward := rc.CalculateBlockReward(block.Header.BlockNumber)

	// Distribute validator reward
	validatorReward := rc.CalculateValidatorReward(validator, blockReward)
	if validatorReward > 0 {
		if err := rc.blockchain.AddBalance(validator.Address, validatorReward); err != nil {
			return err
		}

		// Record validator reward
		rc.recordRewardEvent(validator.Address, validatorReward, "validator", block.Header.BlockNumber)
	}

	// Distribute stake rewards
	for addr, stake := range rc.stakePool.Stakes {
		stakeReward := rc.CalculateStakeReward(stake, blockReward)
		if stakeReward > 0 {
			if err := rc.blockchain.AddBalance(addr, stakeReward); err != nil {
				return err
			}
			stake.LastRewardTime = time.Now()

			// Record stake reward
			rc.recordRewardEvent(addr, stakeReward, "stake", block.Header.BlockNumber)
		}
	}

	return nil
}

// recordRewardEvent records a reward distribution event
func (rc *RewardCalculator) recordRewardEvent(address string, amount float64, rewardType string, blockHeight uint64) {
	event := RewardEvent{
		Address:     address,
		Amount:      amount,
		Type:        rewardType,
		BlockHeight: blockHeight,
		Timestamp:   time.Now(),
	}

	rc.rewardHistory[address] = append(rc.rewardHistory[address], event)
}

// GetRewardHistory returns the reward history for an address
func (rc *RewardCalculator) GetRewardHistory(address string) []RewardEvent {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.rewardHistory[address]
}
