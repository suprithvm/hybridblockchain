package blockchain

import "encoding/json"

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// NewMessage creates a new message with the given type and payload
func NewMessage(msgType string, payload interface{}) *Message {
	return &Message{
		Type:    msgType,
		Payload: payload,
	}
}

// ToJSON converts the message to JSON bytes
func (m *Message) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

// FromJSON parses JSON bytes into a message
func FromJSON(data []byte) (*Message, error) {
	var msg Message
	err := json.Unmarshal(data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// Sync protocol message types
const (
	MsgRequestStateSync = "REQUEST_STATE_SYNC"
	MsgStateChunk       = "STATE_CHUNK"
	MsgStateSyncDone    = "STATE_SYNC_DONE"
	MsgVerifyState      = "VERIFY_STATE"
	MsgVerifyDelta      = "VERIFY_DELTA"
	MsgVerifyResponse   = "VERIFY_RESPONSE"
)

// StateSyncRequest represents a request for state synchronization
type StateSyncRequest struct {
	RequesterID string `json:"requester_id"`
	StateRoot   string `json:"state_root"`
	ChunkSize   int    `json:"chunk_size"`
}

// StateChunk represents a portion of the state being synced
type StateChunk struct {
	ChunkID     int             `json:"chunk_id"`
	UTXOs       map[string]UTXO `json:"utxos"`
	MerkleProof []string        `json:"merkle_proof"`
	Total       int             `json:"total_chunks"`
}

// StateSyncComplete represents completion of state sync
type StateSyncComplete struct {
	StateRoot string `json:"state_root"`
	UTXOCount int    `json:"utxo_count"`
}

// VerificationResponse represents a response to a verification request
type VerificationResponse struct {
	Success     bool   `json:"success"`
	StateRoot   string `json:"state_root"`
	ErrorReason string `json:"error_reason,omitempty"`
}

// Add after existing types
type DeltaUpdate struct {
	LastSyncTime    int64           `json:"last_sync_time"`
	UTXOUpdates     map[string]UTXO `json:"utxo_updates"`
	UTXODeletions   []string        `json:"utxo_deletions"`
	StateRoot       string          `json:"state_root"`
	UpdateTimestamp int64           `json:"update_timestamp"`
}

// BlockSyncRequest represents a request for block synchronization
type BlockSyncRequest struct {
	StartHeight uint64 `json:"start_height"`
	EndHeight   uint64 `json:"end_height"`
	RequestType string `json:"request_type"` // "headers" or "full"
}

// BlockHeaderResponse represents block headers for sync
type BlockHeaderResponse struct {
	Headers     []SyncBlockHeader `json:"headers"`
	StartHeight uint64            `json:"start_height"`
	EndHeight   uint64            `json:"end_height"`
}

// SyncBlockHeader represents lightweight block information for sync
type SyncBlockHeader struct {
	Hash              string `json:"hash"`
	PreviousHash      string `json:"previous_hash"`
	Height            uint64 `json:"height"`
	Timestamp         int64  `json:"timestamp"`
	MerkleRoot        string `json:"merkle_root"`
	StateRoot         string `json:"state_root"`
	Difficulty        uint64 `json:"difficulty"`
	TotalTransactions uint32 `json:"total_transactions"`
	GasLimit          uint64 `json:"gas_limit"`
	GasUsed           uint64 `json:"gas_used"`
	MinedBy           string `json:"mined_by"`
	ValidatedBy       string `json:"validated_by"`
	ExtraData         []byte `json:"extra_data"`
}

// Checkpoint represents a verified blockchain state at a specific height
type Checkpoint struct {
	Height           uint64 `json:"height"`
	Hash             string `json:"hash"`
	StateRoot        string `json:"state_root"`
	UTXORoot         string `json:"utxo_root"`
	Timestamp        int64  `json:"timestamp"`
	ValidatorSetHash string `json:"validator_set_hash"`
}
