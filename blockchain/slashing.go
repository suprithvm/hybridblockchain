package blockchain

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	SlashingThreshold    = 3   // Number of violations before slashing
	SlashingPenaltyRate  = 0.5 // 50% stake penalty
	ProbationPeriod      = 24 * time.Hour
	MaxTimeoutViolations = 5
	MaxConsensusFailures = 3
	MinValidatorScore    = 50 // Minimum score before probation
)

// SlashingManager handles validator penalties
type SlashingManager struct {
	blockchain *Blockchain
	stakePool  *StakePool
	mu         sync.RWMutex
	evidence   map[string][]SlashingEvidence // Address -> Evidence list
}

// NewSlashingManager creates a new slashing manager
func NewSlashingManager(bc *Blockchain, sp *StakePool) *SlashingManager {
	return &SlashingManager{
		blockchain: bc,
		stakePool:  sp,
		evidence:   make(map[string][]SlashingEvidence),
	}
}

// SlashingEvidence represents evidence for slashing
type SlashingEvidence struct {
	ValidatorAddress string
	ViolationType    string
	BlockHeight      uint64
	Timestamp        time.Time
	Proof            []byte
	Description      string
}

// HandleViolation processes a validator violation
func (sm *SlashingManager) HandleViolation(validator *Validator, violationType string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if validator == nil {
		return fmt.Errorf("invalid validator")
	}

	// Record violation
	validator.Violations++

	// Create evidence
	evidence := SlashingEvidence{
		ValidatorAddress: validator.Address,
		ViolationType:    violationType,
		BlockHeight:      sm.blockchain.GetLatestBlock().Header.BlockNumber,
		Timestamp:        time.Now(),
		Description:      fmt.Sprintf("Violation %d of type %s", validator.Violations, violationType),
	}

	// Store evidence
	sm.evidence[validator.Address] = append(sm.evidence[validator.Address], evidence)

	// Check if slashing threshold is reached
	if validator.Violations >= SlashingThreshold {
		return sm.SlashValidator(validator)
	}

	// Put validator on probation
	validator.Status = ValidatorStatusProbation
	validator.ProbationEndTime = time.Now().Add(ProbationPeriod)

	return nil
}

// SlashValidator executes slashing penalty
func (sm *SlashingManager) SlashValidator(validator *Validator) error {
	stake, exists := sm.stakePool.Stakes[validator.Address]
	if !exists {
		return fmt.Errorf("no stake found for validator %s", validator.Address)
	}

	// Calculate penalty
	penaltyAmount := float64(stake.Amount) * SlashingPenaltyRate

	// Update stake
	stake.Amount -= uint64(penaltyAmount)

	// Update validator status
	validator.Status = ValidatorStatusSlashed
	validator.Score = 0

	// Add to community pool
	sm.blockchain.communityPool += penaltyAmount

	// Record slashing event
	if err := sm.recordSlashingEvent(validator, penaltyAmount); err != nil {
		return fmt.Errorf("failed to record slashing event: %w", err)
	}

	return nil
}

// CheckTimeoutViolations handles timeout-based violations
func (sm *SlashingManager) CheckTimeoutViolations(validator *Validator) error {
	if validator.Timeouts >= MaxTimeoutViolations {
		return sm.HandleViolation(validator, "timeout")
	}
	return nil
}

// CheckConsensusViolations handles consensus-related violations
func (sm *SlashingManager) CheckConsensusViolations(validator *Validator) error {
	if validator.ConsensusFailures >= MaxConsensusFailures {
		return sm.HandleViolation(validator, "consensus")
	}
	return nil
}

// recordSlashingEvent records a slashing event to the database
func (sm *SlashingManager) recordSlashingEvent(validator *Validator, amount float64) error {
	event := struct {
		Timestamp  time.Time
		Address    string
		Amount     float64
		Violations int
		Evidence   []SlashingEvidence
	}{
		Timestamp:  time.Now(),
		Address:    validator.Address,
		Amount:     amount,
		Violations: validator.Violations,
		Evidence:   sm.evidence[validator.Address],
	}

	// Store in database
	key := fmt.Sprintf("slash:%s:%d", validator.Address, time.Now().Unix())
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return sm.blockchain.db.Put([]byte(key), data)
}

// GetValidatorEvidence returns all evidence for a validator
func (sm *SlashingManager) GetValidatorEvidence(address string) []SlashingEvidence {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.evidence[address]
}

// ClearExpiredProbation checks and clears expired probation periods
func (sm *SlashingManager) ClearExpiredProbation(validator *Validator) {
	if validator.Status == ValidatorStatusProbation &&
		time.Now().After(validator.ProbationEndTime) {
		validator.Status = ValidatorStatusActive
		validator.Violations = 0
	}
}
