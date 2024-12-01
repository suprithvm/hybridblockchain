package blockchain

import (
    "fmt"
    "net"
)

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
    // Blockchain components
    Blockchain *Blockchain
	// Stake pool	
    StakePool  *StakePool
	// Mempool
    Mempool    *Mempool
	// UTXO pool
    UTXOPool   *UTXOPool

}

func NewDefaultConfig() *NetworkConfig {
    return &NetworkConfig{
        ListenHost:     "0.0.0.0",
        ListenPort:     9000,
        BootstrapNodes: []string{},
        DHTServerMode:  true,
    }
}

func (c *NetworkConfig) GetMultiaddr() string {
    return fmt.Sprintf("/ip4/%s/tcp/%d", c.ListenHost, c.ListenPort)
}

func (c *NetworkConfig) ValidateConfig() error {
    // Validate listen host
    if c.ListenHost != "0.0.0.0" {
        if net.ParseIP(c.ListenHost) == nil {
            return fmt.Errorf("invalid listen host: %s", c.ListenHost)
        }
    }

    // Validate port (allow 0 for testing - system will assign random port)
    if c.ListenPort < 0 || (c.ListenPort > 65535) || (c.ListenPort > 0 && c.ListenPort < 1024) {
        return fmt.Errorf("invalid port number: %d", c.ListenPort)
    }

    // Validate bootstrap nodes
    for _, node := range c.BootstrapNodes {
        host, _, err := net.SplitHostPort(node)
        if err != nil {
            return fmt.Errorf("invalid bootstrap node address: %s", node)
        }
        if net.ParseIP(host) == nil {
            return fmt.Errorf("invalid bootstrap node IP: %s", host)
        }
    }

    return nil
} 