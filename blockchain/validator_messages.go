package blockchain

import (
	"encoding/json"
	"fmt"
	"time"
)

// Validator message types
type ValidatorSelectionMessage struct {
	ValidatorAddress string
	Proof            *SelectionProof
	BlockHeight      uint64
	Timestamp        time.Time
}

type ValidatorVoteMessage struct {
	ValidatorAddress string
	Vote             bool
	BlockHash        string
	Timestamp        time.Time
}

type ValidatorHeartbeatMessage struct {
	ValidatorAddress string    `json:"validator_address"`
	Timestamp        time.Time `json:"timestamp"`
	BlockHeight      uint64    `json:"block_height"`
}

type ValidatorTimeoutMessage struct {
	ValidatorAddress string    `json:"validator_address"`
	Timestamp        time.Time `json:"timestamp"`
	TimeoutCount     uint64    `json:"timeout_count"`
}

type ValidatorSetUpdateMessage struct {
	ActiveValidators []string  `json:"active_validators"`
	Timestamp        time.Time `json:"timestamp"`
	BlockHeight      uint64    `json:"block_height"`
}

// ValidatorRegistrationMessage represents a validator registration
type ValidatorRegistrationMessage struct {
	ValidatorAddress string    `json:"validator_address"`
	Stake            float64   `json:"stake"`
	HostID           string    `json:"host_id"`
	Timestamp        time.Time `json:"timestamp"`
	TransactionID    string    `json:"transaction_id"`
}

// ValidatorMessage represents a generic validator message
type ValidatorMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Add these methods to Node struct
func (n *Node) BroadcastValidatorSelection(validatorAddr string, proof *SelectionProof) error {
	msg := &ValidatorSelectionMessage{
		ValidatorAddress: validatorAddr,
		Proof:            proof,
		BlockHeight:      n.Blockchain.GetHeight(),
		Timestamp:        time.Now(),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal validator selection: %w", err)
	}
	return n.publishMessage(ValidatorSelectionTopic, data)
}

func (n *Node) BroadcastValidatorVote(validatorAddr string, vote bool, blockHash string) error {
	msg := &ValidatorVoteMessage{
		ValidatorAddress: validatorAddr,
		Vote:             vote,
		BlockHash:        blockHash,
		Timestamp:        time.Now(),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal validator vote: %w", err)
	}
	return n.publishMessage(ValidatorVoteTopic, data)
}

func (n *Node) BroadcastValidatorTimeout(validatorAddr string) error {
	msg := &ValidatorTimeoutMessage{
		ValidatorAddress: validatorAddr,
		Timestamp:        time.Now(),
		TimeoutCount:     n.validatorProtocol.validators[validatorAddr].Timeouts,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal timeout message: %w", err)
	}
	return n.publishMessage(ValidatorTimeoutTopic, data)
}

func (n *Node) BroadcastValidatorSetUpdate(activeValidators []string) error {
	msg := &ValidatorSetUpdateMessage{
		ActiveValidators: activeValidators,
		Timestamp:        time.Now(),
		BlockHeight:      n.Blockchain.GetHeight(),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal validator set update: %w", err)
	}
	return n.publishMessage(ValidatorSetUpdateTopic, data)
}
