package blockchain

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	ConsensusTimeout    = 30 * time.Second
	MinValidatorQuorum  = 2.0 / 3.0 // 66.7% quorum required
	ConsensusRoundDelay = 5 * time.Second
)

// ConsensusEngine handles the consensus process
type ConsensusEngine struct {
	blockchain *Blockchain
	validators map[string]*Validator
	selector   *ValidatorSelector
	state      *ConsensusState
	votes      map[string]map[string]bool // blockHash -> validatorAddr -> vote
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
}

// ConsensusState tracks current consensus status
type ConsensusState struct {
	Round            uint64
	CurrentValidator string
	ProposedBlock    *Block
	ValidatorVotes   map[string]bool
	VotingStartTime  time.Time
	ConsensusReached bool
	mu               sync.RWMutex
}

// NewConsensusEngine creates a new consensus engine
func NewConsensusEngine(bc *Blockchain, selector *ValidatorSelector) *ConsensusEngine {
	ctx, cancel := context.WithCancel(context.Background())
	return &ConsensusEngine{
		blockchain: bc,
		validators: make(map[string]*Validator),
		selector:   selector,
		state:      newConsensusState(),
		votes:      make(map[string]map[string]bool),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// StartConsensusRound begins a new consensus round
func (ce *ConsensusEngine) StartConsensusRound(block *Block) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	// Select validator for this round
	validator, proof, err := ce.selector.SelectValidator(
		block.Header.BlockNumber,
		block.Header.PreviousHash,
	)
	if err != nil {
		return fmt.Errorf("validator selection failed: %w", err)
	}

	// Initialize new consensus state
	ce.state.newRound(block, validator.Address)

	// Broadcast validator selection
	if err := ce.blockchain.Node.BroadcastValidatorSelection(validator.Address, proof); err != nil {
		return fmt.Errorf("failed to broadcast validator selection: %w", err)
	}

	// Start vote collection
	go ce.collectValidatorVotes()

	return nil
}

// ProcessValidatorVote handles incoming validator votes
func (ce *ConsensusEngine) ProcessValidatorVote(validatorAddr string, vote bool) error {
	ce.state.mu.Lock()
	defer ce.state.mu.Unlock()

	// Verify validator eligibility
	if !ce.isEligibleVoter(validatorAddr) {
		return fmt.Errorf("ineligible validator: %s", validatorAddr)
	}

	// Record vote
	ce.state.ValidatorVotes[validatorAddr] = vote

	// Check if consensus is reached
	if ce.hasReachedConsensus() {
		ce.state.ConsensusReached = true
		ce.finalizeBlock()
	}

	return nil
}

// Helper methods
func (ce *ConsensusEngine) isEligibleVoter(addr string) bool {
	validator, exists := ce.validators[addr]
	if !exists {
		return false
	}
	return validator.IsReady() && !validator.IsSlashed()
}

func (ce *ConsensusEngine) hasReachedConsensus() bool {
	totalVotes := len(ce.state.ValidatorVotes)
	positiveVotes := 0

	for _, vote := range ce.state.ValidatorVotes {
		if vote {
			positiveVotes++
		}
	}

	return float64(positiveVotes) >= float64(totalVotes)*MinValidatorQuorum
}

func (ce *ConsensusEngine) finalizeBlock() {
	if !ce.state.ConsensusReached {
		return
	}

	// Add block to chain
	if err := ce.blockchain.AddBlock(
		ce.state.ProposedBlock, // Add the proposed block as first argument
		ce.blockchain.mempool,
		ce.blockchain.stakePool,
		ce.blockchain.utxoSet,
		ce.blockchain.p2pHost,
	); err != nil {
		log.Printf("Failed to add block to chain: %v", err)
		return
	}

	// Broadcast finalized block
	block := *ce.state.ProposedBlock // Dereference the pointer
	ce.blockchain.Node.BroadcastBlock(block)
}

// ConsensusState methods
func newConsensusState() *ConsensusState {
	return &ConsensusState{
		ValidatorVotes:  make(map[string]bool),
		VotingStartTime: time.Now(),
	}
}

func (cs *ConsensusState) newRound(block *Block, validator string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.Round++
	cs.CurrentValidator = validator
	cs.ProposedBlock = block
	cs.ValidatorVotes = make(map[string]bool)
	cs.VotingStartTime = time.Now()
	cs.ConsensusReached = false
}

func (ce *ConsensusEngine) collectValidatorVotes() {
	timeout := time.NewTimer(ConsensusTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-timeout.C:
			// Handle timeout - not enough votes received
			ce.handleConsensusTimeout()
			return

		case <-ce.ctx.Done():
			return

		default:
			if ce.hasReachedConsensus() {
				ce.finalizeBlock()
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (ce *ConsensusEngine) handleConsensusTimeout() {
	ce.state.mu.Lock()
	defer ce.state.mu.Unlock()

	if !ce.state.ConsensusReached {
		// Reset consensus state and potentially select new validator
		ce.state = newConsensusState()
	}
}

// SubmitVote submits a validator's vote for a block
func (ce *ConsensusEngine) SubmitVote(validatorAddr string, blockHash string, vote bool) error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if ce.votes[blockHash] == nil {
		ce.votes[blockHash] = make(map[string]bool)
	}

	ce.votes[blockHash][validatorAddr] = vote

	// Also update state votes for backward compatibility
	ce.state.ValidatorVotes[validatorAddr] = vote

	// Check consensus
	if ce.IsConsensusReached(blockHash) {
		ce.state.ConsensusReached = true
		ce.finalizeBlock()
	}

	return nil
}

// IsConsensusReached checks if consensus is reached for a block
func (ce *ConsensusEngine) IsConsensusReached(blockHash string) bool {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	if ce.votes[blockHash] == nil {
		return false
	}

	trueVotes := 0
	totalVotes := len(ce.votes[blockHash])

	for _, vote := range ce.votes[blockHash] {
		if vote {
			trueVotes++
		}
	}

	// Require 2/3 majority for consensus
	return totalVotes > 0 && float64(trueVotes)/float64(totalVotes) >= MinValidatorQuorum
}
