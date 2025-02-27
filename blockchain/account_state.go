package blockchain

import (
	"blockchain-core/blockchain/db"
	"encoding/json"
	"sync"
	"time"
)

type AccountState struct {
	Address      string            `json:"address"`
	Nonce        uint64            `json:"nonce"`
	Balance      float64           `json:"balance"`
	LastActivity int64             `json:"last_activity"`
	UTXOs        map[string]string `json:"utxos"`       // UTXO ID -> Transaction Hash
	PendingTxs   map[string]bool   `json:"pending_txs"` // Transaction Hash -> status
	mu           sync.RWMutex
}

type AccountManager struct {
	accounts     map[string]*AccountState
	cache        *AccountCache
	db           db.Database
	mu           sync.RWMutex
	updateTicker *time.Ticker
}

type AccountCache struct {
	balances    map[string]float64
	lastUpdated map[string]int64
	maxAge      time.Duration
	mu          sync.RWMutex
}

func NewAccountManager(database db.Database) *AccountManager {
	am := &AccountManager{
		accounts: make(map[string]*AccountState),
		cache: &AccountCache{
			balances:    make(map[string]float64),
			lastUpdated: make(map[string]int64),
			maxAge:      time.Minute * 5,
		},
		db:           database,
		updateTicker: time.NewTicker(time.Minute),
	}

	// Start background tasks
	go am.periodicStateUpdate()
	go am.periodicCacheCleanup()

	return am
}

func (am *AccountManager) GetAccountState(address string) (*AccountState, error) {
	am.mu.RLock()
	if state, exists := am.accounts[address]; exists {
		am.mu.RUnlock()
		return state, nil
	}
	am.mu.RUnlock()

	// Load from database if not in memory
	state, err := am.loadAccountState(address)
	if err != nil {
		// Initialize new account if not found
		state = &AccountState{
			Address:    address,
			UTXOs:      make(map[string]string),
			PendingTxs: make(map[string]bool),
		}
		am.saveAccountState(state)
	}

	am.mu.Lock()
	am.accounts[address] = state
	am.mu.Unlock()

	return state, nil
}

func (am *AccountManager) UpdateNonce(address string) (uint64, error) {
	state, err := am.GetAccountState(address)
	if err != nil {
		return 0, err
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	state.Nonce++
	state.LastActivity = time.Now().Unix()

	// Persist state update
	if err := am.saveAccountState(state); err != nil {
		return 0, err
	}

	return state.Nonce, nil
}

func (am *AccountManager) ValidateNonce(address string, nonce uint64) bool {
	state, err := am.GetAccountState(address)
	if err != nil {
		return false
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	return nonce == state.Nonce+1
}

// Batch update handling
func (am *AccountManager) BatchUpdateAccounts(updates map[string]*AccountState) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	batch := am.db.NewBatch()
	for addr, state := range updates {
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		batch.Put([]byte("acc:"+addr), data)
		am.accounts[addr] = state

		// Update cache
		am.cache.mu.Lock()
		am.cache.balances[addr] = state.Balance
		am.cache.lastUpdated[addr] = time.Now().Unix()
		am.cache.mu.Unlock()
	}

	return batch.Write()
}

// Cache management
func (ac *AccountCache) GetBalance(address string) (float64, bool) {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	lastUpdate, exists := ac.lastUpdated[address]
	if !exists {
		return 0, false
	}

	if time.Since(time.Unix(lastUpdate, 0)) > ac.maxAge {
		return 0, false
	}

	balance, exists := ac.balances[address]
	return balance, exists
}

// Background tasks
func (am *AccountManager) periodicStateUpdate() {
	for range am.updateTicker.C {
		am.mu.RLock()
		for _, state := range am.accounts {
			if time.Since(time.Unix(state.LastActivity, 0)) > time.Hour {
				// Persist inactive accounts
				am.saveAccountState(state)
			}
		}
		am.mu.RUnlock()
	}
}

func (am *AccountManager) periodicCacheCleanup() {
	ticker := time.NewTicker(time.Minute * 10)
	for range ticker.C {
		am.cache.mu.Lock()
		now := time.Now().Unix()
		for addr, lastUpdate := range am.cache.lastUpdated {
			if now-lastUpdate > int64(am.cache.maxAge.Seconds()) {
				delete(am.cache.balances, addr)
				delete(am.cache.lastUpdated, addr)
			}
		}
		am.cache.mu.Unlock()
	}
}

// Database operations
func (am *AccountManager) saveAccountState(state *AccountState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return am.db.Put([]byte("acc:"+state.Address), data)
}

func (am *AccountManager) loadAccountState(address string) (*AccountState, error) {
	data, err := am.db.Get([]byte("acc:" + address))
	if err != nil {
		return nil, err
	}

	var state AccountState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
