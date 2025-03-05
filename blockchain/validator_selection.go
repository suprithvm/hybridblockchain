package blockchain

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"
)

// SelectionProof represents the proof of validator selection
type SelectionProof struct {
	Seed      []byte    // Random seed for selection
	Timestamp time.Time // Time of selection
	Weight    uint64    // Stake weight used
	Score     uint64    // Selection score
}

// ValidatorSelector handles validator selection logic
type ValidatorSelector struct {
	stakePool *StakePool
	blockHash []byte
}

// NewValidatorSelector creates a new validator selector
func NewValidatorSelector(stakePool *StakePool) *ValidatorSelector {
	return &ValidatorSelector{
		stakePool: stakePool,
	}
}

// SelectValidator selects a validator based on VRF and stake weight
func (vs *ValidatorSelector) SelectValidator(blockHeight uint64, previousHash string) (*Validator, *SelectionProof, error) {
	// Generate seed from block height and previous hash
	seed := generateSeed(blockHeight, previousHash)

	// Get all eligible validators with their weights
	validators := vs.getEligibleValidators()
	if len(validators) == 0 {
		return nil, nil, fmt.Errorf("no eligible validators")
	}

	// Calculate weighted selection
	selectedValidator, proof := vs.weightedSelection(validators, seed)
	if selectedValidator == nil {
		return nil, nil, fmt.Errorf("validator selection failed")
	}

	return selectedValidator, proof, nil
}

// weightedSelection performs stake-weighted random selection
func (vs *ValidatorSelector) weightedSelection(validators []*Validator, seed []byte) (*Validator, *SelectionProof) {
	var totalWeight uint64
	weights := make(map[string]uint64)

	// Calculate weights based on stake and performance
	for _, v := range validators {
		weight := vs.calculateWeight(v)
		weights[v.Address] = weight
		totalWeight += weight
	}

	// Generate random number from seed
	target := generateRandomUint64(seed) % totalWeight

	// Select validator based on weight using VRF
	var cumulative uint64
	for _, v := range validators {
		cumulative += weights[v.Address]
		if cumulative >= target {
			// Create selection proof
			proof := &SelectionProof{
				Seed:      seed,
				Timestamp: time.Now(),
				Weight:    weights[v.Address],
				Score:     cumulative,
			}

			// Record selection in validator
			v.isSelected = true
			v.selectionProof = proof
			v.LastActive = time.Now()

			// Update stake pool
			if stakeInfo := vs.stakePool.Stakes[v.Address]; stakeInfo != nil {
				stakeInfo.LastActive = time.Now()
			}

			return v, proof
		}
	}

	return nil, nil
}

// calculateWeight determines validator selection weight based on stake and performance
func (vs *ValidatorSelector) calculateWeight(v *Validator) uint64 {
	stakeInfo := vs.stakePool.Stakes[v.Address]
	if stakeInfo == nil {
		return 0
	}

	baseWeight := vs.stakePool.GetStakeWeight(stakeInfo)

	// Factor in validator performance
	if v.Performance != nil {
		performanceMultiplier := float64(1)
		if v.Performance.UptimePercentage >= 99 {
			performanceMultiplier = 1.2 // 20% bonus for high uptime
		}
		return uint64(float64(baseWeight) * performanceMultiplier)
	}

	return baseWeight
}

// getEligibleValidators returns list of validators eligible for selection
func (vs *ValidatorSelector) getEligibleValidators() []*Validator {
	var eligible []*Validator

	// Get all active validators from stake pool
	for addr, stake := range vs.stakePool.Stakes {
		if !vs.isEligible(stake) {
			continue
		}

		// Get validator info
		validator := &Validator{
			Address:    addr,
			Status:     ValidatorStatusActive,
			Score:      calculateValidatorScore(stake),
			LastActive: stake.LastActive,
		}

		if stake.Performance != nil {
			validator.Performance = &ValidatorPerformance{
				BlocksProposed:    stake.Performance.BlocksProposed,
				BlocksValidated:   stake.Performance.BlocksValidated,
				MissedValidations: stake.Performance.MissedValidations,
				UptimePercentage:  stake.Performance.UptimePercentage,
				LastUpdate:        stake.Performance.LastUpdate,
			}
		}

		eligible = append(eligible, validator)
	}

	return eligible
}

// calculateValidatorScore determines validator score based on performance
func calculateValidatorScore(stake *StakeInfo) uint64 {
	if stake == nil || stake.Performance == nil {
		return 0
	}

	// Base score from successful validations
	baseScore := stake.Performance.BlocksValidated * 10

	// Penalties for missed validations
	penalties := stake.Performance.MissedValidations * 20

	// Factor in uptime
	uptimeScore := uint64(stake.Performance.UptimePercentage * 100)

	// Calculate final score
	if penalties > baseScore {
		return 0
	}

	return baseScore - penalties + uptimeScore
}

// isEligible checks if a stake/validator is eligible for selection
func (vs *ValidatorSelector) isEligible(stake *StakeInfo) bool {
	return time.Since(stake.StartTime) >= MinStakeAge &&
		stake.Violations < SlashingThreshold &&
		stake.WithdrawalReq == nil
}

// Helper functions
func generateSeed(height uint64, prevHash string) []byte {
	hasher := sha256.New()
	binary.Write(hasher, binary.BigEndian, height)
	hasher.Write([]byte(prevHash))
	return hasher.Sum(nil)
}

func generateRandomUint64(seed []byte) uint64 {
	hash := sha256.Sum256(seed)
	return binary.BigEndian.Uint64(hash[:8])
}

// UpdateValidatorPerformance updates validator performance metrics
func (vs *ValidatorSelector) UpdateValidatorPerformance(address string, metrics *ValidatorPerformance) error {
	stake, exists := vs.stakePool.Stakes[address]
	if !exists {
		return fmt.Errorf("no stake found for validator %s", address)
	}

	stake.Performance = metrics
	stake.LastActive = time.Now()
	return nil
}

// GetValidatorInfo returns validator information for selection
func (vs *ValidatorSelector) GetValidatorInfo(address string) (*Validator, error) {
	stake := vs.stakePool.Stakes[address]
	if stake == nil {
		return nil, fmt.Errorf("no stake found for validator %s", address)
	}

	return &Validator{
		Address:     address,
		Status:      ValidatorStatusActive,
		Score:       calculateValidatorScore(stake),
		LastActive:  stake.LastActive,
		Performance: stake.Performance,
	}, nil
}

// UpdateValidatorActivity updates the last active time for a validator
func (vs *ValidatorSelector) UpdateValidatorActivity(address string) error {
	stake, exists := vs.stakePool.Stakes[address]
	if !exists {
		return fmt.Errorf("no stake found for validator %s", address)
	}

	stake.LastActive = time.Now()
	if stake.Performance != nil {
		stake.Performance.LastUpdate = time.Now()
	}
	return nil
}
