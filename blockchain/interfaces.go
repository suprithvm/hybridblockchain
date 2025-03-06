package blockchain

// SyncCapable defines the interface for nodes that can sync
type SyncCapable interface {
	StartSync() error
}

// Shutdownable defines the interface for nodes that can shutdown
type Shutdownable interface {
	Shutdown() error
}
