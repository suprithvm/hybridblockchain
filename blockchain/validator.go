package blockchain

import (
	"crypto/sha256"
	"fmt"
	"log"
	"sync"
	"time"
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
	blockchain   *Blockchain
	config       *ValidatorConfig
	mu           sync.RWMutex
	isValidating bool
	missedBlocks int
	lastBlock    uint64
	rewards      float64
	slashed      bool
	validators   map[string]float64 // Address -> Stake mapping
}

// NewValidator creates a new validator instance
func NewValidator(bc *Blockchain, config *ValidatorConfig) (*Validator, error) {
	if config.Stake < config.MinStake {
		return nil, fmt.Errorf("stake amount %f is below minimum required %f",
			config.Stake, config.MinStake)
	}

	if config.BlockTimeout == 0 {
		config.BlockTimeout = 30 * time.Second
	}

	if config.MaxMissed == 0 {
		config.MaxMissed = 10
	}

	return &Validator{
		blockchain: bc,
		config:     config,
		validators: make(map[string]float64),
		lastBlock:  bc.GetLatestBlock().Header.BlockNumber,
	}, nil
}

// Start begins the validation process
func (v *Validator) Start() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.isValidating {
		return fmt.Errorf("validator is already running")
	}

	if v.slashed {
		return fmt.Errorf("validator has been slashed and cannot participate")
	}

	v.isValidating = true
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

	for {
		select {
		case <-ticker.C:
			if !v.isValidating {
				log.Printf("🛑 Validation process terminated")
				return
			}

			// Get latest block
			currentBlock := v.blockchain.GetLatestBlock()

			// Check if we missed any blocks
			if currentBlock.Header.BlockNumber > v.lastBlock+1 {
				missed := currentBlock.Header.BlockNumber - v.lastBlock - 1
				v.handleMissedBlocks(int(missed))
			}

			// Validate new block if available
			if currentBlock.Header.BlockNumber > v.lastBlock {
				log.Printf("🔍 New block #%d detected - beginning validation", currentBlock.Header.BlockNumber)
				if err := v.validateBlock(currentBlock); err != nil {
					log.Printf("❌ Block validation failed: %v", err)
					continue
				}
				log.Printf("✅ Block #%d successfully validated", currentBlock.Header.BlockNumber)
				v.lastBlock = currentBlock.Header.BlockNumber
				v.distributeRewards(currentBlock)
			}
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
	// Verify signature
	if !tx.VerifySignature() {
		return fmt.Errorf("invalid transaction signature")
	}

	// Verify balance
	if !v.blockchain.VerifyBalance(tx.Sender, tx.Amount+tx.GasFee) {
		return fmt.Errorf("insufficient balance")
	}

	// Verify nonce
	expectedNonce := v.blockchain.GetNonce(tx.Sender)
	if tx.Nonce != expectedNonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", expectedNonce, tx.Nonce)
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
	// Verify state root
	stateRoot := v.calculateStateRoot(block.Body.Transactions.GetAllTransactions())
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
	reward := calculateBlockReward(block)
	v.rewards += reward
	v.config.Stake += reward

	log.Printf("💰 Received validation reward: %.8f tokens for block #%d",
		reward, block.Header.BlockNumber)
	log.Printf("📈 Updated validator stake: %.8f tokens (total rewards: %.8f)",
		v.config.Stake, v.rewards)
}

func calculateBlockReward(block Block) float64 {
	// Base reward + transaction fees
	baseReward := 1.0 // Example base reward
	fees := 0.0
	for _, tx := range block.Body.Transactions.GetAllTransactions() {
		fees += tx.GasFee
	}
	return baseReward + fees
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
