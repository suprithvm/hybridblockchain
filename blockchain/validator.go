package blockchain

import (
	"crypto/sha256"
	"fmt"
	"log"
	"sync"
	"time"
)

// Add these validator status constants
const (
	ValidatorStatusPending   = iota // Initial state
	ValidatorStatusActive           // Actively validating
	ValidatorStatusProbation        // Under probation due to poor performance
	ValidatorStatusSlashed          // Slashed due to violations
	ValidatorStatusInactive         // Voluntarily inactive
)

// ValidatorConfig holds configuration for a validator node
type ValidatorConfig struct {
	Stake        float64
	MinStake     float64
	RewardRate   float64
	SlashingRate float64
	BlockTimeout time.Duration // Maximum time to wait for block validation
	MaxMissed    int           // Maximum missed blocks before slashing
}

// Validator represents a validator node in the network
type Validator struct {
	Address           string                `json:"address"`
	PublicKey         []byte                `json:"public_key"`
	Status            int                   `json:"status"`
	Score             uint64                `json:"score"`
	LastActive        time.Time             `json:"last_active"`
	Performance       *ValidatorPerformance `json:"performance"`
	Violations        int                   `json:"violations"`
	Timeouts          int                   `json:"timeouts"`
	ProbationEndTime  time.Time             `json:"probation_end_time"`
	ConsensusFailures int                   `json:"consensus_failures"`

	// Private fields
	blockchain     *Blockchain
	config         *ValidatorConfig
	mu             sync.RWMutex
	isValidating   bool
	missedBlocks   int
	lastBlock      uint64
	rewards        float64
	slashed        bool
	validators     map[string]float64
	isSelected     bool
	selectionProof *SelectionProof
}

// NewValidator creates a new validator instance
func NewValidator(bc *Blockchain, config *ValidatorConfig, walletAddress string) (*Validator, error) {
	if config == nil {
		config = &ValidatorConfig{
			MinStake:     0,
			RewardRate:   0.01,
			SlashingRate: 0.5,
			BlockTimeout: 30 * time.Second,
			MaxMissed:    10,
		}
	}

	// Check if validator already exists
	if bc != nil && bc.Validators != nil {
		if existingValidator, exists := bc.Validators[walletAddress]; exists {
			log.Printf("ℹ️ Validator %s already registered", walletAddress)
			return existingValidator, nil
		}
	}

	validator := &Validator{
		blockchain:  bc,
		config:      config,
		Address:     walletAddress,
		Status:      ValidatorStatusPending,
		LastActive:  time.Now(),
		validators:  make(map[string]float64),
		Performance: &ValidatorPerformance{LastUpdate: time.Now()},
	}

	return validator, nil
}

// Start begins the validation process
func (v *Validator) Start() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Check if already running
	if v.isValidating {
		log.Printf("ℹ️ Validator is already running")
		return nil
	}

	if v.Status == ValidatorStatusSlashed {
		return fmt.Errorf("validator has been slashed and cannot participate")
	}

	// Wait for blockchain sync before starting validation
	if v.blockchain != nil && v.blockchain.Node != nil {
		if v.blockchain.Node.IsSyncing() {
			return fmt.Errorf("cannot start validation while blockchain is syncing")
		}
	}

	v.isValidating = true
	v.Status = ValidatorStatusActive
	log.Printf("🔐 Validator node activated with stake: %.4f tokens", v.config.Stake)
	log.Printf("📊 Validation parameters: Min Stake: %.4f, Reward Rate: %.2f%%",
		v.config.MinStake, v.config.RewardRate*100)
	log.Printf("⏱️ Block timeout: %s, Max missed blocks: %d",
		v.config.BlockTimeout, v.config.MaxMissed)

	// Start validation in background
	go v.validate()

	return nil
}

func (v *Validator) validate() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	log.Printf("👀 Validator watching for new blocks - last processed: #%d", v.lastBlock)
	log.Printf("🔍 Validator status: Active=%v, Stake=%.4f, Score=%d", v.isValidating, v.config.Stake, v.Score)

	for range ticker.C {
		if !v.isValidating {
			log.Printf("🛑 Validation process terminated")
			return
		}

		// Get latest block
		currentBlock := v.blockchain.GetLatestBlock()
		if currentBlock.Header.BlockNumber == 0 {
			continue
		}

		// Check if we missed any blocks
		if currentBlock.Header.BlockNumber > v.lastBlock+1 {
			missed := currentBlock.Header.BlockNumber - v.lastBlock - 1
			log.Printf("⚠️ Missed %d blocks between #%d and #%d", missed, v.lastBlock, currentBlock.Header.BlockNumber)
			v.handleMissedBlocks(int(missed))
		}

		// Validate new block if available
		if currentBlock.Header.BlockNumber > v.lastBlock {
			log.Printf("🔍 New block #%d detected - beginning validation", currentBlock.Header.BlockNumber)
			log.Printf("   • Hash: %s", currentBlock.Hash())
			log.Printf("   • Previous Hash: %s", currentBlock.Header.PreviousHash)
			log.Printf("   • Timestamp: %s", time.Unix(currentBlock.Header.Timestamp, 0).Format(time.RFC3339))
			log.Printf("   • Transactions: %d", len(currentBlock.Body.Transactions.GetAllTransactions()))

			if err := v.validateBlock(currentBlock); err != nil {
				log.Printf("❌ Block validation failed: %v", err)
				continue
			}
			log.Printf("✅ Block #%d successfully validated", currentBlock.Header.BlockNumber)
			v.lastBlock = currentBlock.Header.BlockNumber
			v.distributeRewards(currentBlock)
		} else {
			log.Printf("👀 Waiting for new blocks... Current height: #%d", currentBlock.Header.BlockNumber)
		}
	}
}

func (v *Validator) validateBlock(block Block) error {
	log.Printf("🔐 Validating block #%d with hash %s", block.Header.BlockNumber, block.Hash())

	// Verify block hash
	if calculatedHash := block.CalculateHash(); calculatedHash != block.Hash() {
		log.Printf("❌ Hash verification failed - calculated: %s, provided: %s",
			calculatedHash, block.Hash())
		return fmt.Errorf("invalid block hash")
	}
	log.Printf("✓ Block hash verified successfully")

	// Verify timestamp
	if block.Header.Timestamp > time.Now().Unix() {
		log.Printf("❌ Block timestamp is in the future: %s",
			time.Unix(block.Header.Timestamp, 0).Format(time.RFC3339))
		return fmt.Errorf("block timestamp is in the future")
	}
	log.Printf("✓ Block timestamp verified: %s",
		time.Unix(block.Header.Timestamp, 0).Format(time.RFC3339))

	// Verify transactions
	txCount := len(block.Body.Transactions.GetAllTransactions())
	log.Printf("🧾 Validating %d transactions in block #%d", txCount, block.Header.BlockNumber)

	for i, tx := range block.Body.Transactions.GetAllTransactions() {
		log.Printf("  ↳ Validating transaction %d/%d: %s", i+1, txCount, tx.TransactionID)
		if err := v.validateTransaction(tx); err != nil {
			log.Printf("  ❌ Transaction %s validation failed: %v", tx.TransactionID, err)
			return fmt.Errorf("transaction validation failed: %v", err)
		}
		log.Printf("  ✓ Transaction %s valid", tx.TransactionID)
	}
	log.Printf("✓ All transactions verified successfully")

	// Verify state transitions
	log.Printf("🔄 Verifying state transitions for block #%d", block.Header.BlockNumber)
	if err := v.validateStateTransitions(block); err != nil {
		log.Printf("❌ State transition validation failed: %v", err)
		return fmt.Errorf("state transition validation failed: %v", err)
	}
	log.Printf("✓ State transitions verified successfully")
	log.Printf("🎉 Block #%d fully validated and confirmed", block.Header.BlockNumber)

	return nil
}

func (v *Validator) validateTransaction(tx Transaction) error {
	// Special handling for system transactions (coinbase and validator rewards)
	if tx.IsCoinbase() {
		// Verify basic coinbase requirements
		if tx.Sender != "coinbase" {
			return fmt.Errorf("invalid coinbase sender: %s", tx.Sender)
		}
		if len(tx.Outputs) != 1 {
			return fmt.Errorf("coinbase must have exactly one output")
		}
		if tx.Outputs[0].Amount <= 0 {
			return fmt.Errorf("coinbase amount must be positive")
		}
		return nil
	}

	if tx.IsValidatorReward() {
		// Verify basic validator reward requirements
		if tx.Sender != "system" {
			return fmt.Errorf("invalid system sender: %s", tx.Sender)
		}
		if len(tx.Outputs) != 1 {
			return fmt.Errorf("validator reward must have exactly one output")
		}
		if tx.Outputs[0].Amount <= 0 {
			return fmt.Errorf("validator reward amount must be positive")
		}
		return nil
	}

	// Verify signature for regular transactions
	if !tx.VerifySignature() {
		return fmt.Errorf("invalid transaction signature in tx %s", tx.TransactionID)
	}

	// Verify balance
	if !v.blockchain.VerifyBalance(tx.Sender, tx.Amount+tx.GasFee) {
		return fmt.Errorf("insufficient balance for tx %s", tx.TransactionID)
	}

	// Verify nonce
	expectedNonce := v.blockchain.GetNonce(tx.Sender)
	if tx.Nonce != expectedNonce {
		return fmt.Errorf("invalid nonce in tx %s: expected %d, got %d",
			tx.TransactionID, expectedNonce, tx.Nonce)
	}

	return nil
}

func (v *Validator) calculateStateRoot(txs []Transaction) string {
	// Calculate merkle root of state changes
	hash := sha256.New()
	for _, tx := range txs {
		data := fmt.Sprintf("%s%s%f%d", tx.Sender, tx.Receiver, tx.Amount, tx.Nonce)
		hash.Write([]byte(data))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (v *Validator) validateStateTransitions(block Block) error {
	// Create a temporary UTXO pool clone to simulate the state transitions
	tempPool := v.blockchain.utxoPool.Clone()

	// Apply the transactions to the temporary pool
	for _, tx := range block.Body.Transactions.GetAllTransactions() {
		// Process the transaction on the temporary UTXO pool
		txCopy := tx
		tempPool.AddUTXO(&txCopy, block.Header.BlockNumber)
	}

	// Calculate the state root using the same method as the block processor
	stateRoot := tempPool.GetMerkleRoot()

	if stateRoot != block.Header.StateRoot {
		return fmt.Errorf("invalid state root")
	}
	return nil
}

func (v *Validator) handleMissedBlocks(missed int) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.missedBlocks += missed
	log.Printf("⚠️ Validator missed %d blocks (total: %d)", missed, v.missedBlocks)

	// Check if validator should be slashed
	if v.missedBlocks >= v.config.MaxMissed {
		v.slash()
	}
}

func (v *Validator) slash() {
	slashAmount := v.config.Stake * v.config.SlashingRate
	v.config.Stake -= slashAmount
	v.slashed = true

	log.Printf("⚡ Validator slashed! Lost %f tokens", slashAmount)

	// Stop validation if stake falls below minimum
	if v.config.Stake < v.config.MinStake {
		log.Printf("❌ Stake below minimum, stopping validation")
		v.Stop()
	}
}

func (v *Validator) distributeRewards(block Block) {
	// Use blockchain's calculateBlockReward function
	reward := calculateBlockReward(block)
	v.rewards += reward
	v.config.Stake += reward

	log.Printf("💰 Received validation reward: %.8f tokens for block #%d",
		reward, block.Header.BlockNumber)
	log.Printf("📈 Updated validator stake: %.8f tokens (total rewards: %.8f)",
		v.config.Stake, v.rewards)
}

// Stop stops the validation process
func (v *Validator) Stop() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.isValidating {
		return nil
	}

	v.isValidating = false
	log.Printf("🛑 Validator stopped. Total rewards: %f", v.rewards)
	return nil
}

// GetStats returns validator statistics
func (v *Validator) GetStats() map[string]interface{} {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return map[string]interface{}{
		"stake":        v.config.Stake,
		"rewards":      v.rewards,
		"missedBlocks": v.missedBlocks,
		"isValidating": v.isValidating,
		"slashed":      v.slashed,
		"lastBlock":    v.lastBlock,
	}
}

// UpdateValidatorScore calculates and updates validator score
func (v *Validator) UpdateValidatorScore() {
	if v.Performance == nil {
		v.Performance = &ValidatorPerformance{
			LastUpdate: time.Now(),
		}
		return
	}

	// Calculate base score from successful validations
	baseScore := v.Performance.BlocksValidated * 10

	// Subtract penalties for missed validations
	penalties := v.Performance.MissedValidations * 20

	// Factor in uptime
	uptimeScore := uint64(v.Performance.UptimePercentage * 100)

	// Calculate final score
	if penalties > baseScore {
		v.Score = 0
	} else {
		v.Score = baseScore - penalties + uptimeScore
	}

	// Update status based on score
	v.updateStatus()
}

// updateStatus updates validator status based on score and violations
func (v *Validator) updateStatus() {
	switch {
	case v.Score < 100:
		v.Status = ValidatorStatusProbation
	case v.Score >= 100 && v.Status != ValidatorStatusSlashed:
		v.Status = ValidatorStatusActive
	}
}

// RecordValidation records a successful block validation
func (v *Validator) RecordValidation(success bool) {
	if v.Performance == nil {
		v.Performance = &ValidatorPerformance{}
	}

	if success {
		v.Performance.BlocksValidated++
		v.LastActive = time.Now()
	} else {
		v.Performance.MissedValidations++
	}

	// Update uptime percentage
	total := v.Performance.BlocksValidated + v.Performance.MissedValidations
	if total > 0 {
		v.Performance.UptimePercentage = float64(v.Performance.BlocksValidated) / float64(total) * 100
	}

	v.UpdateValidatorScore()
}

func (v *Validator) CheckSelectionEligibility() error {
	if v.Status != ValidatorStatusActive {
		return fmt.Errorf("validator not active")
	}

	if v.Score < 100 {
		return fmt.Errorf("validator score too low")
	}

	if v.slashed {
		return fmt.Errorf("validator has been slashed")
	}

	return nil
}

func (v *Validator) NotifySelection(proof *SelectionProof) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.CheckSelectionEligibility(); err != nil {
		return err
	}

	v.isSelected = true
	v.selectionProof = proof
	v.LastActive = time.Now()

	log.Printf("🎯 Validator %s selected for next block validation", v.Address)
	return nil
}

func (v *Validator) IsReady() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return v.isValidating && !v.slashed && v.Status == ValidatorStatusActive
}

// Add these methods
func (v *Validator) IsSlashed() bool {
	return v.Status == ValidatorStatusSlashed
}

func (v *Validator) Vote(blockHash string, vote bool) error {
	if !v.IsReady() {
		return fmt.Errorf("validator not ready to vote")
	}

	return v.blockchain.Node.BroadcastValidatorVote(v.Address, vote, blockHash)
}

func (v *Validator) ProcessSelection(proof *SelectionProof) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.CheckSelectionEligibility(); err != nil {
		return err
	}

	v.isSelected = true
	v.selectionProof = proof
	v.LastActive = time.Now()

	return nil
}

// Add this method to Validator struct
func (v *Validator) Recover() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.Status != ValidatorStatusProbation && v.Status != ValidatorStatusSlashed {
		return fmt.Errorf("validator not in recoverable state")
	}

	// Reset validator metrics
	v.Score = 100
	v.Timeouts = 0
	v.Status = ValidatorStatusActive
	v.LastActive = time.Now()

	return nil
}
