package blockchain

import (
    "context"
    "log"
    "sync"
    "time"
)

const (
    ValidatorHeartbeatInterval = 30 * time.Second
    ValidatorTimeoutDuration  = 90 * time.Second
    ValidatorSyncInterval     = 5 * time.Minute
)

// ValidatorProtocol manages validator communication
type ValidatorProtocol struct {
    node           *Node
    validators     map[string]*ValidatorState
    heartbeats    map[string]time.Time
    mu            sync.RWMutex
    ctx           context.Context
    cancel        context.CancelFunc
}

type ValidatorState struct {
    Address     string
    LastSeen    time.Time
    IsActive    bool
    Heartbeats  uint64
    Timeouts    uint64
}

// NewValidatorProtocol creates a new validator protocol instance
func NewValidatorProtocol(node *Node) *ValidatorProtocol {
    ctx, cancel := context.WithCancel(context.Background())
    return &ValidatorProtocol{
        node:        node,
        validators:  make(map[string]*ValidatorState),
        heartbeats: make(map[string]time.Time),
        ctx:        ctx,
        cancel:     cancel,
    }
}

// Start begins the validator protocol
func (vp *ValidatorProtocol) Start() {
    go vp.heartbeatMonitor()
    go vp.validatorSync()
}

// Stop stops the validator protocol
func (vp *ValidatorProtocol) Stop() {
    vp.cancel()
}

// Monitor validator heartbeats
func (vp *ValidatorProtocol) heartbeatMonitor() {
    ticker := time.NewTicker(ValidatorHeartbeatInterval)
    defer ticker.Stop()

    for {
        select {
        case <-vp.ctx.Done():
            return
        case <-ticker.C:
            vp.checkValidatorHeartbeats()
        }
    }
}

// Synchronize validator states
func (vp *ValidatorProtocol) validatorSync() {
    ticker := time.NewTicker(ValidatorSyncInterval)
    defer ticker.Stop()

    for {
        select {
        case <-vp.ctx.Done():
            return
        case <-ticker.C:
            vp.syncValidatorStates()
        }
    }
}

// Handle validator heartbeat
func (vp *ValidatorProtocol) HandleHeartbeat(validatorAddr string) {
    vp.mu.Lock()
    defer vp.mu.Unlock()

    vp.heartbeats[validatorAddr] = time.Now()
    if state, exists := vp.validators[validatorAddr]; exists {
        state.LastSeen = time.Now()
        state.Heartbeats++
    }
}

// Check validator heartbeats and handle timeouts
func (vp *ValidatorProtocol) checkValidatorHeartbeats() {
    vp.mu.Lock()
    defer vp.mu.Unlock()

    now := time.Now()
    for addr, lastBeat := range vp.heartbeats {
        if now.Sub(lastBeat) > ValidatorTimeoutDuration {
            vp.handleValidatorTimeout(addr)
        }
    }
}

// Handle validator timeout
func (vp *ValidatorProtocol) handleValidatorTimeout(addr string) {
    if state, exists := vp.validators[addr]; exists {
        state.IsActive = false
        state.Timeouts++
        log.Printf("⚠️ Validator %s timed out (timeouts: %d)", addr, state.Timeouts)
        
        // Notify network of validator timeout
        vp.node.BroadcastValidatorTimeout(addr)
    }
}

// Sync validator states with network
func (vp *ValidatorProtocol) syncValidatorStates() {
    vp.mu.RLock()
    activeValidators := make([]string, 0)
    for addr, state := range vp.validators {
        if state.IsActive {
            activeValidators = append(activeValidators, addr)
        }
    }
    vp.mu.RUnlock()

    // Broadcast validator set update
    vp.node.BroadcastValidatorSetUpdate(activeValidators)
} 