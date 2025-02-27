package blockchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"blockchain-core/blockchain/db"
)

// UTXO represents an unspent transaction output
type UTXO struct {
	TransactionID string  `json:"txid"`
	OutputIndex   int     `json:"vout"`
	Amount        float64 `json:"amount"`
	Owner         string  `json:"owner"` // This is the receiver/address
	Spent         bool    `json:"spent"`
	BlockHeight   uint64  `json:"block_height"`
	Timestamp     int64   `json:"timestamp"`
}

// StateSnapshot represents a point-in-time snapshot of the UTXO pool state
type StateSnapshot struct {
	Height     uint64
	UTXOStates map[string]UTXO
	BlockHash  string
	Timestamp  int64
}

// UTXOPool manages all UTXOs with Merkle tree support
type UTXOPool struct {
	utxos             map[string]UTXO
	mu                sync.RWMutex
	merkleRoot        string
	lastVerifiedState string
	lastUpdateTime    int64
	updates           map[string]UTXO // Track updates since last sync
	deletions         []string        // Track deletions since last sync
	db                db.Database
	snapshots         []StateSnapshot
	node              *Node
}

// NewUTXOPool initializes a new UTXO pool
func NewUTXOPool(database db.Database) *UTXOPool {
	return &UTXOPool{
		utxos: make(map[string]UTXO),
		db:    database,
	}
}

// CalculateMerkleRoot calculates the Merkle root of the UTXO set
func (pool *UTXOPool) CalculateMerkleRoot() string {
	if len(pool.utxos) == 0 {
		log.Printf("[INFO] Empty UTXO pool, returning empty Merkle root")
		return ""
	}

	// Get all UTXOs in a deterministic order
	var keys []string
	for key := range pool.utxos {
		keys = append(keys, key)
	}
	sort.Strings(keys) // Sort keys to ensure consistent ordering

	// Calculate hashes in sorted order
	var hashes []string
	for _, key := range keys {
		utxo := pool.utxos[key]
		data := fmt.Sprintf("%s-%d-%s-%.8f",
			utxo.TransactionID,
			utxo.OutputIndex,
			utxo.Owner,
			utxo.Amount,
		)
		hash := sha256.Sum256([]byte(data))
		hashes = append(hashes, hex.EncodeToString(hash[:]))
	}

	// Calculate Merkle root
	level := 0
	for len(hashes) > 1 {
		nextLevel := make([]string, 0, (len(hashes)+1)/2)
		log.Printf("[DEBUG] Merkle Tree Level %d, Nodes: %d", level, len(hashes))

		for i := 0; i < len(hashes); i += 2 {
			var combined string
			if i+1 < len(hashes) {
				combined = hashes[i] + hashes[i+1]
			} else {
				combined = hashes[i] + hashes[i] // Duplicate last hash if odd number
			}
			hash := sha256.Sum256([]byte(combined))
			nextLevel = append(nextLevel, hex.EncodeToString(hash[:]))
		}
		hashes = nextLevel
		level++
	}

	log.Printf("[INFO] Final Merkle Root: %s", hashes[0])
	return hashes[0]
}

// GetMerkleRoot returns the current Merkle root
func (pool *UTXOPool) GetMerkleRoot() string {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return pool.merkleRoot
}

// AddUTXO adds a new UTXO to the pool
func (pool *UTXOPool) AddUTXO(tx *Transaction, blockHeight uint64) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Add new UTXOs from transaction outputs
	for i, output := range tx.Outputs {
		utxoKey := fmt.Sprintf("%s-%d", tx.Hash(), i)
		utxo := UTXO{
			TransactionID: tx.Hash(),
			OutputIndex:   i,
			Amount:        output.Amount,
			Owner:         tx.Receiver, // Use Receiver instead of Address
			BlockHeight:   blockHeight,
			Timestamp:     tx.Timestamp,
		}
		pool.utxos[utxoKey] = utxo
	}

	// Mark spent inputs
	for _, input := range tx.Inputs {
		inputKey := fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)
		if utxo, exists := pool.utxos[inputKey]; exists {
			utxo.Spent = true
			pool.utxos[inputKey] = utxo
		}
	}

	// Track update
	if pool.updates == nil {
		pool.updates = make(map[string]UTXO)
	}
	for k, v := range pool.utxos {
		pool.updates[k] = v
	}
	pool.lastUpdateTime = time.Now().Unix()

	pool.merkleRoot = pool.CalculateMerkleRoot()

	// Persist changes
	pool.saveState()

	// Update account states
	accountStates := make(map[string]*AccountState)
	for _, output := range tx.Outputs {
		state, _ := pool.node.accountManager.GetAccountState(tx.Receiver)
		state.Balance += output.Amount
		accountStates[tx.Receiver] = state
	}
	pool.node.accountManager.BatchUpdateAccounts(accountStates)
}

// RemoveUTXO removes a spent UTXO
func (pool *UTXOPool) RemoveUTXO(txID string, outputIndex int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	key := fmt.Sprintf("%s:%d", txID, outputIndex)
	delete(pool.utxos, key)

	// Track deletion
	if pool.deletions == nil {
		pool.deletions = make([]string, 0)
	}
	pool.deletions = append(pool.deletions, key)
	pool.lastUpdateTime = time.Now().Unix()

	pool.merkleRoot = pool.CalculateMerkleRoot()

	// Persist changes
	pool.saveState()
}

// ValidateTransaction checks if the sender has enough balance
func (pool *UTXOPool) ValidateTransaction(tx *Transaction) bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	totalInput := 0.0
	for _, input := range tx.Inputs {
		key := fmt.Sprintf("%s:%d", input.TransactionID, input.OutputIndex)
		utxo, exists := pool.utxos[key]
		if !exists || utxo.Owner != tx.Sender {
			return false
		}
		totalInput += utxo.Amount
	}
	return totalInput >= tx.Amount
}

func UpdateUTXOSet(tx Transaction, utxoSet map[string]UTXO) {
	// Remove spent UTXOs
	for _, input := range tx.Inputs {
		key := fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)
		delete(utxoSet, key)
	}

	// Add new UTXOs
	for index := range tx.Outputs {
		key := fmt.Sprintf("%s-%d", tx.TransactionID, index)
		utxoSet[key] = UTXO{
			TransactionID: tx.TransactionID,
			OutputIndex:   index,
			Owner:         tx.Receiver, // Use tx.Receiver directly
			Amount:        tx.Amount,   // Use tx.Amount directly
		}
	}
}

// CreateSnapshot creates a point-in-time snapshot of the UTXO pool state
func (pool *UTXOPool) CreateSnapshot(height uint64, blockHash string) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	snapshot := StateSnapshot{
		Height:     height,
		UTXOStates: make(map[string]UTXO),
		BlockHash:  blockHash,
		Timestamp:  time.Now().Unix(),
	}

	// Deep copy current state
	for k, v := range pool.utxos {
		snapshot.UTXOStates[k] = v
	}

	pool.snapshots = append(pool.snapshots, snapshot)

	// Keep only last 100 snapshots
	if len(pool.snapshots) > 100 {
		pool.snapshots = pool.snapshots[1:]
	}
}

// RestoreSnapshot restores the UTXO pool state from a snapshot
func (pool *UTXOPool) RestoreSnapshot(snapshot *StateSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("cannot restore nil snapshot")
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Verify snapshot integrity
	tempPool := &UTXOPool{utxos: snapshot.UTXOStates}
	calculatedRoot := tempPool.CalculateMerkleRoot()
	if calculatedRoot != snapshot.BlockHash {
		return fmt.Errorf("snapshot integrity check failed: merkle root mismatch")
	}

	// Restore state
	pool.utxos = make(map[string]UTXO)
	for k, v := range snapshot.UTXOStates {
		pool.utxos[k] = v
	}
	pool.merkleRoot = snapshot.BlockHash

	log.Printf("[INFO] Restored UTXO pool state from snapshot at timestamp %d", snapshot.Timestamp)
	return nil
}

// GetStateRoot returns the current state root (merkle root + metadata hash)
func (pool *UTXOPool) GetStateRoot() string {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	if len(pool.utxos) == 0 {
		return ""
	}

	// Combine merkle root with additional state metadata
	metadata := fmt.Sprintf("%d-%d", len(pool.utxos), time.Now().Unix())
	combinedData := pool.merkleRoot + metadata
	hash := sha256.Sum256([]byte(combinedData))

	stateRoot := hex.EncodeToString(hash[:])
	log.Printf("[INFO] Generated state root: %s", stateRoot)
	return stateRoot
}

// GetStateChunks splits the UTXO set into chunks with Merkle proofs
func (pool *UTXOPool) GetStateChunks(chunkSize int) []StateChunk {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Get sorted keys for deterministic chunking
	keys := make([]string, 0, len(pool.utxos))
	for k := range pool.utxos {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Calculate total chunks needed
	totalChunks := (len(keys) + chunkSize - 1) / chunkSize
	chunks := make([]StateChunk, 0, totalChunks)

	// Generate chunks with Merkle proofs
	for i := 0; i < len(keys); i += chunkSize {
		end := min(i+chunkSize, len(keys))
		chunkKeys := keys[i:end]

		// Create chunk UTXOs map
		chunkUTXOs := make(map[string]UTXO)
		for _, key := range chunkKeys {
			chunkUTXOs[key] = pool.utxos[key]
		}

		// Generate Merkle proof for this chunk
		proof := pool.generateMerkleProofForChunk(chunkKeys, keys)

		chunks = append(chunks, StateChunk{
			ChunkID:     i / chunkSize,
			UTXOs:       chunkUTXOs,
			MerkleProof: proof,
			Total:       totalChunks,
		})
	}

	return chunks
}

// generateMerkleProofForChunk generates a Merkle proof for a specific chunk
func (pool *UTXOPool) generateMerkleProofForChunk(chunkKeys, allKeys []string) []string {
	// Get all leaf hashes
	leaves := make([]string, len(allKeys))
	for i, key := range allKeys {
		utxo := pool.utxos[key]
		data := fmt.Sprintf("%s-%d-%s-%.8f",
			utxo.TransactionID,
			utxo.OutputIndex,
			utxo.Owner,
			utxo.Amount,
		)
		hash := sha256.Sum256([]byte(data))
		leaves[i] = hex.EncodeToString(hash[:])
	}

	// Create a map of chunk keys for quick lookup
	chunkKeyMap := make(map[string]bool)
	for _, key := range chunkKeys {
		chunkKeyMap[key] = true
	}

	// Build Merkle tree and collect proof
	proof := make([]string, 0)
	currentLevel := leaves

	for len(currentLevel) > 1 {
		nextLevel := make([]string, (len(currentLevel)+1)/2)
		for i := 0; i < len(currentLevel); i += 2 {
			var combined string
			if i+1 < len(currentLevel) {
				combined = currentLevel[i] + currentLevel[i+1]
			} else {
				combined = currentLevel[i] + currentLevel[i] // Duplicate last hash if odd number
			}
			hash := sha256.Sum256([]byte(combined))
			nextLevel[i/2] = hex.EncodeToString(hash[:])

			// Add sibling to proof if this node contains any of our chunk's leaves
			isRelevant := false
			for j := i; j < min(i+2, len(currentLevel)); j++ {
				keyIndex := j
				if keyIndex < len(allKeys) && chunkKeyMap[allKeys[keyIndex]] {
					isRelevant = true
					break
				}
			}
			if isRelevant {
				if i+1 < len(currentLevel) {
					proof = append(proof, currentLevel[i+1])
				} else {
					proof = append(proof, currentLevel[i])
				}
			}
		}
		currentLevel = nextLevel
	}

	return proof
}

// VerifyStateChunk verifies a state chunk against the current state root
func (pool *UTXOPool) VerifyStateChunk(chunk StateChunk) error {
	// Create leaf nodes for chunk UTXOs
	nodes := make([]string, len(chunk.UTXOs))
	i := 0
	for key, utxo := range chunk.UTXOs {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%v", key, utxo)))
		nodes[i] = hex.EncodeToString(hash[:])
		i++
	}

	// Verify Merkle proof
	currentLevel := nodes
	proofIndex := 0

	for len(currentLevel) > 1 {
		nextLevel := make([]string, (len(currentLevel)+1)/2)
		for i := 0; i < len(currentLevel); i += 2 {
			var left, right string
			if i+1 < len(currentLevel) {
				left = currentLevel[i]
				right = currentLevel[i+1]
			} else {
				left = currentLevel[i]
				if proofIndex < len(chunk.MerkleProof) {
					right = chunk.MerkleProof[proofIndex]
					proofIndex++
				} else {
					right = left
				}
			}
			nextLevel[i/2] = hashPair(left, right)
		}
		currentLevel = nextLevel

		// If we have an odd number at this level and more proof elements,
		// use the next proof element
		if len(currentLevel) > 1 && proofIndex < len(chunk.MerkleProof) {
			currentLevel = append(currentLevel, chunk.MerkleProof[proofIndex])
			proofIndex++
		}
	}

	if currentLevel[0] != pool.merkleRoot {
		return fmt.Errorf("invalid merkle proof for chunk %d", chunk.ChunkID)
	}

	return nil
}

// Helper function to hash two nodes together
func hashPair(left, right string) string {
	if left > right {
		left, right = right, left
	}
	hash := sha256.Sum256([]byte(left + right))
	return hex.EncodeToString(hash[:])
}

// ApplyStateChunk applies a verified state chunk
func (pool *UTXOPool) ApplyStateChunk(chunk StateChunk) error {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Apply UTXOs from chunk
	for key, utxo := range chunk.UTXOs {
		pool.utxos[key] = utxo
	}

	// Recalculate Merkle root
	pool.merkleRoot = pool.CalculateMerkleRoot()
	return nil
}

// generateMerkleProof generates a Merkle proof for the given chunk keys
func (pool *UTXOPool) generateMerkleProof(chunkKeys []string) []string {
	// Get all UTXO keys and sort them
	allKeys := make([]string, 0, len(pool.utxos))
	for key := range pool.utxos {
		allKeys = append(allKeys, key)
	}
	sort.Strings(allKeys)

	// Create initial leaf nodes
	nodes := make([]string, len(allKeys))
	for i, key := range allKeys {
		nodes[i] = pool.utxos[key].Hash()
	}

	// Track which nodes need proofs
	needProof := make(map[int]bool)
	for _, key := range chunkKeys {
		for i, k := range allKeys {
			if k == key {
				needProof[i] = true
				break
			}
		}
	}

	var proof []string
	level := 0

	// Build tree and collect proof nodes
	for len(nodes) > 1 {
		nextLevel := make([]string, (len(nodes)+1)/2)
		for i := 0; i < len(nodes); i += 2 {
			var left, right string
			left = nodes[i]
			if i+1 < len(nodes) {
				right = nodes[i+1]
			} else {
				right = left
			}

			// Add sibling to proof if this node needs proof
			if needProof[i] {
				if i+1 < len(nodes) {
					proof = append(proof, right)
				}
			}
			if i+1 < len(nodes) && needProof[i+1] {
				proof = append(proof, left)
			}

			// Calculate parent hash
			parentHash := sha256.Sum256([]byte(left + right))
			nextLevel[i/2] = hex.EncodeToString(parentHash[:])

			// Track which parent nodes need proofs
			if needProof[i] || (i+1 < len(nodes) && needProof[i+1]) {
				needProof[i/2] = true
			}
		}

		nodes = nextLevel
		level++
	}

	return proof
}

// verifyMerkleProof verifies a Merkle proof for the given chunk
func (pool *UTXOPool) verifyMerkleProof(chunk StateChunk, stateRoot string) bool {
	// Get chunk keys and their hashes
	chunkHashes := make([]string, 0, len(chunk.UTXOs))
	for _, utxo := range chunk.UTXOs {
		chunkHashes = append(chunkHashes, utxo.Hash())
	}

	nodes := chunkHashes

	// Rebuild tree using proof
	for len(nodes) > 1 || len(nodes) == 1 && nodes[0] != stateRoot {
		nextLevel := make([]string, (len(nodes)+1)/2)

		for i := 0; i < len(nodes); i += 2 {
			var left, right string

			if i+1 < len(nodes) {
				// Two nodes available
				left = nodes[i]
				right = nodes[i+1]
			} else {
				// Single node, duplicate it
				left = nodes[i]
				right = left
			}

			// Calculate parent hash
			parentHash := sha256.Sum256([]byte(left + right))
			nextLevel[i/2] = hex.EncodeToString(parentHash[:])
		}

		nodes = nextLevel
	}

	// Final verification
	return len(nodes) == 1 && nodes[0] == stateRoot
}

// Helper function for array bounds
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (pool *UTXOPool) verifyUTXO(key string, utxo UTXO) error {
	// Verify individual UTXO integrity
	if utxo.TransactionID == "" || utxo.Amount <= 0 {
		return fmt.Errorf("invalid UTXO data")
	}
	return nil
}

// VerifyDeltaUpdate verifies an incremental state update
func (pool *UTXOPool) VerifyDeltaUpdate(delta *DeltaUpdate) error {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Create temporary pool to verify delta
	tempPool := NewUTXOPool(pool.db)
	for k, v := range pool.utxos {
		tempPool.utxos[k] = v
	}

	// Apply updates to temp pool
	for key, utxo := range delta.UTXOUpdates {
		if err := tempPool.verifyUTXO(key, utxo); err != nil {
			return fmt.Errorf("invalid UTXO in delta: %v", err)
		}
		tempPool.utxos[key] = utxo
	}

	// Remove deleted UTXOs
	for _, key := range delta.UTXODeletions {
		delete(tempPool.utxos, key)
	}

	// Verify state root matches
	calculatedRoot := tempPool.CalculateMerkleRoot()
	if calculatedRoot != delta.StateRoot {
		return fmt.Errorf("state root mismatch after delta application")
	}

	return nil
}

// GetDeltaUpdates returns the delta updates since the last sync
func (pool *UTXOPool) GetDeltaUpdates(lastSyncTime int64) *DeltaUpdate {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	delta := &DeltaUpdate{
		LastSyncTime:    lastSyncTime,
		UTXOUpdates:     make(map[string]UTXO),
		UTXODeletions:   make([]string, 0, len(pool.deletions)),
		UpdateTimestamp: time.Now().Unix(),
	}

	// Get updates since last sync
	for key, utxo := range pool.updates {
		if pool.lastUpdateTime > lastSyncTime {
			delta.UTXOUpdates[key] = utxo
		}
	}

	// Use append with ellipsis for efficient slice copy
	delta.UTXODeletions = append(delta.UTXODeletions, pool.deletions...)

	delta.StateRoot = pool.CalculateMerkleRoot()
	return delta
}

// ApplyDeltaUpdate applies a verified delta update
func (pool *UTXOPool) ApplyDeltaUpdate(delta *DeltaUpdate) error {
	if err := pool.VerifyDeltaUpdate(delta); err != nil {
		return fmt.Errorf("delta verification failed: %v", err)
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Apply updates
	for key, utxo := range delta.UTXOUpdates {
		pool.utxos[key] = utxo
	}

	// Apply deletions
	for _, key := range delta.UTXODeletions {
		delete(pool.utxos, key)
	}

	// Update state
	pool.lastUpdateTime = delta.UpdateTimestamp
	pool.merkleRoot = pool.CalculateMerkleRoot()

	// Persist changes
	pool.saveState()

	return nil
}

// Add this method to the UTXO struct
func (u UTXO) Hash() string {
	data := fmt.Sprintf("%s-%d-%s-%.8f",
		u.TransactionID,
		u.OutputIndex,
		u.Owner,
		u.Amount,
	)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// Add these methods to UTXOPool
func (u *UTXOPool) GetUTXOsForAddress(address string) []UTXO {
	u.mu.RLock()
	defer u.mu.RUnlock()

	var addressUTXOs []UTXO
	for _, utxo := range u.utxos {
		if utxo.Owner == address {
			addressUTXOs = append(addressUTXOs, utxo)
		}
	}
	return addressUTXOs
}

func (u *UTXOPool) GetUTXOs() map[string]UTXO {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.utxos
}

// Account state management
func (u *UTXOPool) GetBalance(address string) float64 {
	u.mu.RLock()
	defer u.mu.RUnlock()

	balance := 0.0
	for _, utxo := range u.utxos {
		if utxo.Owner == address && !utxo.Spent {
			balance += utxo.Amount
		}
	}
	return balance
}

// Handle chain reorganization
func (u *UTXOPool) HandleReorg(oldChain, newChain []*Block) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Restore state from last common block
	commonHeight := u.findLastCommonBlock(oldChain, newChain)
	if err := u.restoreSnapshot(commonHeight); err != nil {
		return err
	}

	// Apply new chain's transactions
	for _, block := range newChain[commonHeight+1:] {
		for _, tx := range block.Body.Transactions.GetAllTransactions() {
			u.AddUTXO(&tx, block.Header.BlockNumber)
		}
	}

	return nil
}

// State persistence
func (u *UTXOPool) saveState() error {
	data, err := json.Marshal(u.utxos)
	if err != nil {
		return err
	}
	return u.db.Put([]byte("utxo_state"), data)
}

func (u *UTXOPool) loadState() error {
	data, err := u.db.Get([]byte("utxo_state"))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &u.utxos)
}

// Helper functions
func (u *UTXOPool) findLastCommonBlock(oldChain, newChain []*Block) uint64 {
	minLen := len(oldChain)
	if len(newChain) < minLen {
		minLen = len(newChain)
	}

	for i := minLen - 1; i >= 0; i-- {
		if oldChain[i].Hash() == newChain[i].Hash() {
			return uint64(i)
		}
	}
	return 0
}

func (u *UTXOPool) restoreSnapshot(height uint64) error {
	for i := len(u.snapshots) - 1; i >= 0; i-- {
		if u.snapshots[i].Height <= height {
			u.utxos = u.snapshots[i].UTXOStates
			return nil
		}
	}
	return fmt.Errorf("no snapshot found for height %d", height)
}
