package blockchain

import (
	"fmt"
	"log"
	"strings"
)

// NodeConfig holds configuration for a blockchain node
type NodeConfig struct {
	Role               string
	ListenPort         int
	DataDir            string
	Database           *DatabaseConfig
	BootNodes          []string
	EnableNAT          bool
	NetworkID          string
	IsBootstrap        bool
	PublicIP           string
	EnablePeerExchange bool
}

// DefaultNodeConfig returns default configuration
func DefaultNodeConfig() *NodeConfig {
	return &NodeConfig{
		ListenPort:         50505,
		DataDir:            "./node_data",
		IsBootstrap:        false,
		EnableNAT:          true,
		EnablePeerExchange: true,
	}
}

// BootstrapConfig holds configuration specific to bootstrap nodes
type BootstrapConfig struct {
	ListenPort         int
	PublicIP           string
	DataDir            string
	EnableNAT          bool
	EnablePeerExchange bool
}

// NetworkConfig holds network-wide configuration
type NetworkConfig struct {
	// Listen address (e.g., "0.0.0.0" for all interfaces)
	ListenHost string
	// Listen port
	ListenPort int
	// Bootstrap nodes (e.g., ["ip:port", "ip:port"])
	BootstrapNodes []string
	// External IP (optional, for NAT traversal)
	ExternalIP string
	// Enable DHT server mode
	DHTServerMode bool
	// NAT configuration
	NATEnabled bool
	// UPnP port mapping
	UPnPEnabled bool
	// STUN server addresses
	STUNServers []string
	// TURN server configuration
	TURNServers []TURNConfig
	// Blockchain components
	Blockchain *Blockchain
	// Stake pool
	StakePool *StakePool
	// Mempool
	Mempool *Mempool
	// UTXO pool
	UTXOPool *UTXOPool
	// Network ID
	NetworkID string
	// Chain ID
	ChainID uint64
	// Max peers
	MaxPeers int
	// Dial ratio
	DialRatio int
	// The actual fields from your blockchain package
	P2PPort     int
	RPCPort     int
	NetworkPath string
	// Add database configuration
	Database *DatabaseConfig
}

// TURNConfig holds TURN server configuration
type TURNConfig struct {
	Address  string
	Username string
	Password string
}

type NetworkPorts struct {
	BootstrapNodeBasePort int
	P2PBasePort           int
	RPCBasePort           int
}

var DefaultNetworkPorts = NetworkPorts{
	BootstrapNodeBasePort: 50500, // Base port for bootstrap nodes
	P2PBasePort:           51500, // Base port for peer-to-peer communication
	RPCBasePort:           52500, // Base port for RPC services
}

var DefaultBootnodeConfig = NetworkConfig{
	ListenHost:     "0.0.0.0",
	ListenPort:     50505, // Set to the fixed port for bootnode
	BootstrapNodes: []string{},
	ExternalIP:     "49.204.110.41", // Set to the provided public IP
	DHTServerMode:  true,
	NATEnabled:     true,
	UPnPEnabled:    true,
	STUNServers:    []string{},
	TURNServers:    []TURNConfig{},
}

var DefaultNetworkConfig = NetworkConfig{
	ListenHost:     "0.0.0.0",
	ListenPort:     9000,
	BootstrapNodes: []string{},
	DHTServerMode:  true,
	NATEnabled:     true,
	UPnPEnabled:    true,
	STUNServers: []string{
		"stun.l.google.com:19302",
		"stun1.l.google.com:19302",
	},
	TURNServers: []TURNConfig{},
}

func (np NetworkPorts) GetBootstrapNodePort(instanceID int) int {
	return np.BootstrapNodeBasePort + instanceID
}

func (np NetworkPorts) GetP2PPort(instanceID int) int {
	return np.P2PBasePort + instanceID
}

func (np NetworkPorts) GetRPCPort(instanceID int) int {
	return np.RPCBasePort + instanceID
}

func NewDefaultConfig() *NetworkConfig {
	return &NetworkConfig{
		ListenHost:     "0.0.0.0",
		ListenPort:     9000,
		BootstrapNodes: []string{},
		DHTServerMode:  true,
		NATEnabled:     true,
		UPnPEnabled:    true,
		STUNServers: []string{
			"stun.l.google.com:19302",
			"stun1.l.google.com:19302",
		},
		TURNServers: []TURNConfig{},
	}
}

func (c *NetworkConfig) GetMultiaddr() string {
	return fmt.Sprintf("/ip4/%s/tcp/%d", c.ListenHost, c.ListenPort)
}

func (c *NetworkConfig) ValidateConfig() error {
	if c.P2PPort <= 0 {
		return fmt.Errorf("invalid P2P port")
	}
	if c.Blockchain == nil {
		return fmt.Errorf("blockchain not initialized")
	}
	return nil
}

func isValidHostname(hostname string) bool {
	if len(hostname) > 255 {
		return false
	}
	for _, part := range strings.Split(hostname, ".") {
		if len(part) > 63 {
			return false
		}
	}
	return true
}

// DatabaseConfig holds database-specific configuration
type DatabaseConfig struct {
	Type         string
	Path         string
	CacheSize    uint64
	MaxOpenFiles int
	Compression  bool
}

func DefaultDatabaseConfig() *DatabaseConfig {
	log.Printf("Creating default database config")
	return &DatabaseConfig{
		Type:         "leveldb",
		Path:         "blockchain_data",
		CacheSize:    256,
		MaxOpenFiles: 64,
		Compression:  true,
	}
}
