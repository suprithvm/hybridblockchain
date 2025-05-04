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
	ScriptPubKey  string  `json:"script_pub_key"`
}

// StateSnapshot represents a point-in-time snapshot of the UTXO pool state
type StateSnapshot struct {
	Height     uint64
	UTXOStates map[string]UTXO
	BlockHash  string
	Timestamp  int64
}

// UTXOPool represents a pool of unspent transaction outputs
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

// UtxoSnapshot represents a point-in-time snapshot of the UTXO set
type UtxoSnapshot struct {
	BlockHeight uint64          `json:"block_height"`
	BlockHash   string          `json:"block_hash"`
	UTXOs       map[string]UTXO `json:"utxos"`
	MerkleRoot  string          `json:"merkle_root"`
	Timestamp   int64           `json:"timestamp"`
}

// NewUTXOPool creates a new UTXO pool
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

	// Check if this is a system transaction (coinbase or validator reward)
	isSystemTx := false
	if tx.TxType == TX_COINBASE || tx.TxType == TX_VALIDATOR_REWARD {
		isSystemTx = true
		if tx.TxType == TX_COINBASE {
			log.Printf("🌱 Processing coinbase transaction %s for block #%d", tx.TransactionID, blockHeight)
		} else if tx.TxType == TX_VALIDATOR_REWARD {
			log.Printf("💸 Processing validator reward transaction %s for block #%d", tx.TransactionID, blockHeight)
		}
	}

	// Add new UTXOs from transaction outputs
	for i, output := range tx.Outputs {
		utxoKey := fmt.Sprintf("%s-%d", tx.TransactionID, i)

		// For system transactions, the Owner is always the Receiver
		owner := output.Receiver

		utxo := UTXO{
			TransactionID: tx.TransactionID,
			OutputIndex:   i,
			Amount:        output.Amount,
			Owner:         owner,
			BlockHeight:   blockHeight,
			Timestamp:     tx.Timestamp,
			ScriptPubKey:  output.ScriptPubKey,
		}

		pool.utxos[utxoKey] = utxo

		if isSystemTx {
			if tx.TxType == TX_COINBASE {
				log.Printf("💎 Created UTXO from coinbase: %s", utxoKey)
			} else if tx.TxType == TX_VALIDATOR_REWARD {
				log.Printf("💰 Created UTXO from validator reward: %s", utxoKey)
			}
			log.Printf("   • Amount: %.8f", output.Amount)
			log.Printf("   • Owner: %s", owner)
		}
	}

	// Only mark inputs as spent for non-system transactions
	if !isSystemTx {
		// Mark spent inputs
		for _, input := range tx.Inputs {
			inputKey := fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)
			if utxo, exists := pool.utxos[inputKey]; exists {
				utxo.Spent = true
				pool.utxos[inputKey] = utxo
			}
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

	// Update account states only if node and accountManager are available
	if pool.node != nil && pool.node.accountManager != nil {
		accountStates := make(map[string]*AccountState)
		for _, output := range tx.Outputs {
			state, err := pool.node.accountManager.GetAccountState(output.Receiver)
			if err != nil || state == nil {
				// Create a new account state if none exists
				log.Printf("Creating new account state for %s", output.Receiver)
				state = &AccountState{
					Address: output.Receiver,
					Balance: output.Amount,
					Nonce:   0,
				}
			} else {
				state.Balance += output.Amount
			}
			accountStates[output.Receiver] = state
		}
		pool.node.accountManager.BatchUpdateAccounts(accountStates)
	} else {
		log.Printf("⚠️ Skipping account state update: node or accountManager not initialized")
	}
}

// RemoveUTXO removes a UTXO from the pool
func (pool *UTXOPool) RemoveUTXO(txID string, outputIndex int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	key := fmt.Sprintf("%s-%d", txID, outputIndex)
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
		key := fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)
		utxo, exists := pool.utxos[key]
		if !exists || utxo.Owner != tx.Sender {
			return false
		}
		totalInput += utxo.Amount
	}
	return totalInput >= tx.Amount
}

func UpdateUTXOSet(tx Transaction, utxoSet map[string]UTXO) {
	// Check if this is a system transaction (coinbase or validator reward)
	isSystemTx := tx.TxType == TX_COINBASE || tx.TxType == TX_VALIDATOR_REWARD

	// Only remove spent UTXOs for non-system transactions
	if !isSystemTx {
		// Remove spent UTXOs
		for _, input := range tx.Inputs {
			key := fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)
			delete(utxoSet, key)
		}
	}

	// Add new UTXOs
	for index, output := range tx.Outputs {
		key := fmt.Sprintf("%s-%d", tx.TransactionID, index)
		utxoSet[key] = UTXO{
			TransactionID: tx.TransactionID,
			OutputIndex:   index,
			Owner:         output.Receiver,
			Amount:        output.Amount,
			ScriptPubKey:  output.ScriptPubKey,
		}
	}
}

// CreateSnapshot creates a snapshot of the current UTXO state at a specific block height
func (pool *UTXOPool) CreateSnapshot(blockHeight uint64, blockHash string) error {
	// Create a snapshot of the current UTXO state
	snapshot := &UtxoSnapshot{
		BlockHeight: blockHeight,
		BlockHash:   blockHash,
		UTXOs:       make(map[string]UTXO),
		MerkleRoot:  pool.merkleRoot,
		Timestamp:   time.Now().Unix(),
	}

	// Only store unspent UTXOs to save space
	for key, utxo := range pool.utxos {
		if !utxo.Spent {
			snapshot.UTXOs[key] = utxo
		}
	}

	err := pool.saveSnapshot(snapshot)
	if err != nil {
		log.Printf("⚠️ Error saving UTXO snapshot: %v", err)
		return err
	}
	log.Printf("📸 Created UTXO snapshot at block height %d with %d unspent outputs",
		blockHeight, len(snapshot.UTXOs))

	return nil
}

// saveSnapshot persists a UTXO snapshot to database
func (pool *UTXOPool) saveSnapshot(snapshot *UtxoSnapshot) error {
	if pool.db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Generate a key for the snapshot
	snapshotKey := fmt.Sprintf("utxo_snapshot_%d", snapshot.BlockHeight)

	// Serialize the snapshot
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to serialize UTXO snapshot: %v", err)
	}

	// Store in the database
	err = pool.db.Put([]byte(snapshotKey), data)
	if err != nil {
		return fmt.Errorf("failed to store UTXO snapshot: %v", err)
	}

	return nil
}

// LoadSnapshotAtHeight loads a UTXO snapshot from a specific block height
func (pool *UTXOPool) LoadSnapshotAtHeight(blockHeight uint64) error {
	if pool.db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Generate the snapshot key
	snapshotKey := fmt.Sprintf("utxo_snapshot_%d", blockHeight)

	// Retrieve from database
	data, err := pool.db.Get([]byte(snapshotKey))
	if err != nil {
		return fmt.Errorf("failed to retrieve UTXO snapshot: %v", err)
	}

	// Deserialize the snapshot
	var snapshot UtxoSnapshot
	err = json.Unmarshal(data, &snapshot)
	if err != nil {
		return fmt.Errorf("failed to deserialize UTXO snapshot: %v", err)
	}

	// Apply the snapshot to the current state
	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Reset the current state
	pool.utxos = snapshot.UTXOs
	pool.merkleRoot = snapshot.MerkleRoot
	pool.lastUpdateTime = snapshot.Timestamp

	log.Printf("📥 Loaded UTXO snapshot from block height %d with %d unspent outputs",
		snapshot.BlockHeight, len(snapshot.UTXOs))

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

// GetUTXOs returns the map of UTXOs
func (up *UTXOPool) GetUTXOs() map[string]UTXO {
	up.mu.RLock()
	defer up.mu.RUnlock()
	return up.utxos
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
func (u *UTXOPool) GetAllUTXOs() map[string]UTXO {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.utxos
}

// Clone creates a deep copy of the UTXOPool
func (pool *UTXOPool) Clone() *UTXOPool {
	pool.mu.RLock()
	defer pool.mu.RUnlock()

	clonedPool := &UTXOPool{
		utxos:             make(map[string]UTXO),
		merkleRoot:        pool.merkleRoot,
		lastVerifiedState: pool.lastVerifiedState,
		lastUpdateTime:    pool.lastUpdateTime,
		updates:           make(map[string]UTXO),
		db:                pool.db,
		node:              pool.node,
	}

	// Deep copy the UTXOs
	for key, utxo := range pool.utxos {
		clonedPool.utxos[key] = utxo
	}

	// Deep copy the updates
	if pool.updates != nil {
		for key, update := range pool.updates {
			clonedPool.updates[key] = update
		}
	}

	// Deep copy the deletions slice
	if pool.deletions != nil {
		clonedPool.deletions = make([]string, len(pool.deletions))
		copy(clonedPool.deletions, pool.deletions)
	}

	// Copy snapshots if needed
	if len(pool.snapshots) > 0 {
		clonedPool.snapshots = make([]StateSnapshot, len(pool.snapshots))
		for i, snapshot := range pool.snapshots {
			// Deep copy each snapshot
			clonedSnapshot := StateSnapshot{
				Height:     snapshot.Height,
				BlockHash:  snapshot.BlockHash,
				Timestamp:  snapshot.Timestamp,
				UTXOStates: make(map[string]UTXO),
			}

			// Copy snapshot UTXO states
			for key, utxo := range snapshot.UTXOStates {
				clonedSnapshot.UTXOStates[key] = utxo
			}

			clonedPool.snapshots[i] = clonedSnapshot
		}
	}

	return clonedPool
}

// ProcessBlockTransactions processes all transactions in a block and updates the UTXO state
// This method is designed to be called when a node receives a new block (whether through sync or broadcast)
func (pool *UTXOPool) ProcessBlockTransactions(block *Block) error {
	if block == nil {
		return fmt.Errorf("cannot process transactions for nil block")
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	log.Printf("🔄 Processing %d transactions for block #%d",
		len(block.Body.Transactions.GetAllTransactions()),
		block.Header.BlockNumber)

	// Collect account updates for batch processing
	accountUpdates := make(map[string]*AccountState)

	// Process all transactions in the block
	for _, tx := range block.Body.Transactions.GetAllTransactions() {
		// Process inputs (mark UTXOs as spent)
		if tx.TxType != TX_COINBASE && tx.TxType != TX_VALIDATOR_REWARD {
			senderBalance := 0.0
			for _, input := range tx.Inputs {
				key := fmt.Sprintf("%s-%d", input.TransactionID, input.OutputIndex)
				if utxo, exists := pool.utxos[key]; exists {
					utxo.Spent = true
					pool.utxos[key] = utxo
					senderBalance += utxo.Amount

					if pool.updates == nil {
						pool.updates = make(map[string]UTXO)
					}
					pool.updates[key] = utxo
				} else {
					log.Printf("⚠️ Warning: Input UTXO %s not found in pool during block processing", key)
				}
			}

			// Update sender account state for regular transactions
			if pool.node != nil && pool.node.accountManager != nil && tx.Sender != "" {
				state, err := pool.node.accountManager.GetAccountState(tx.Sender)
				if err == nil && state != nil {
					// We subtract the total transaction amount from the sender's balance
					state.Balance -= (tx.Amount + tx.GasFee)
					state.LastActivity = time.Now().Unix()

					// Store for batch update
					accountUpdates[tx.Sender] = state
				}
			}
		}

		// Process outputs (create new UTXOs)
		for i, output := range tx.Outputs {
			utxoKey := fmt.Sprintf("%s-%d", tx.TransactionID, i)

			// Set owner based on transaction type
			owner := output.Receiver
			if tx.TxType == TX_COINBASE || tx.TxType == TX_VALIDATOR_REWARD {
				owner = output.Receiver // Ensure the receiver is set correctly for system transactions
			}

			utxo := UTXO{
				TransactionID: tx.TransactionID,
				OutputIndex:   i,
				Amount:        output.Amount,
				Owner:         owner,
				BlockHeight:   block.Header.BlockNumber,
				Timestamp:     tx.Timestamp,
				ScriptPubKey:  output.ScriptPubKey,
				Spent:         false,
			}

			pool.utxos[utxoKey] = utxo

			if pool.updates == nil {
				pool.updates = make(map[string]UTXO)
			}
			pool.updates[utxoKey] = utxo

			// Update account states if possible
			if pool.node != nil && pool.node.accountManager != nil {
				// Get existing state or create new one
				state, err := pool.node.accountManager.GetAccountState(output.Receiver)
				if err != nil {
					// Create a new account state if none exists
					state = &AccountState{
						Address:      output.Receiver,
						Balance:      output.Amount,
						Nonce:        0,
						LastActivity: time.Now().Unix(),
						UTXOs:        make(map[string]string),
						PendingTxs:   make(map[string]bool),
					}
				} else {
					// Update existing balance
					state.Balance += output.Amount
					state.LastActivity = time.Now().Unix()

					// Add UTXO reference to the account state
					if state.UTXOs == nil {
						state.UTXOs = make(map[string]string)
					}
				}

				// Track UTXO in account state
				state.UTXOs[utxoKey] = tx.TransactionID

				// Store for batch update
				accountUpdates[output.Receiver] = state
			}
		}
	}

	// Batch update account states
	if pool.node != nil && pool.node.accountManager != nil && len(accountUpdates) > 0 {
		err := pool.node.accountManager.BatchUpdateAccounts(accountUpdates)
		if err != nil {
			log.Printf("⚠️ Error updating account states: %v", err)
		} else {
			log.Printf("✓ Updated %d account states", len(accountUpdates))
		}
	}

	// Update the merkle root
	pool.merkleRoot = pool.CalculateMerkleRoot()
	pool.lastUpdateTime = time.Now().Unix()

	// Create a state snapshot at this block height if we're at a snapshot interval
	if block.Header.BlockNumber%100 == 0 || block.Header.BlockNumber == 0 {
		pool.CreateSnapshot(block.Header.BlockNumber, block.Hash())
	}

	// Persist changes
	return pool.saveState()
}
