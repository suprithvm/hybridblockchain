package blockchain

import (
	"context"
	"fmt"
	"log"	
	"sync"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/pion/stun"
	"github.com/pion/turn/v2"
	upnp "github.com/huin/goupnp/dcps/internetgateway2"
)

// NATType represents different types of NAT
type NATType int

const (
	NATUnknown NATType = iota
	NATOpen
	NATFull
	NATSymmetric
	NATRestricted
)

// NATManager handles NAT traversal functionality
type NATManager struct {
	host         host.Host
	config       *NetworkConfig
	mutex        sync.RWMutex
	natType      NATType
	externalIP   string
	externalPort int
	mappedPort   int
	stunClient   *stun.Client
	turnClient   *turn.Client
	upnpEnabled  bool
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewNATManager creates a new NAT manager instance
func NewNATManager(h host.Host, config *NetworkConfig) *NATManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &NATManager{
		host:        h,
		config:      config,
		upnpEnabled: config.UPnPEnabled,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start initializes NAT traversal mechanisms
// In nat_traversal.go
func (nm *NATManager) Start() error {
    if !nm.config.NATEnabled {
        log.Println("NAT traversal disabled")
        return nil
    }

    var errs []error

    // STUN initialization
    if err := nm.initSTUN(); err != nil {
        errs = append(errs, fmt.Errorf("STUN initialization error: %w", err))
    }

    // TURN initialization
    if len(nm.config.TURNServers) > 0 {
        if err := nm.initTURN(); err != nil {
            errs = append(errs, fmt.Errorf("TURN initialization error: %w", err))
        }
    }

    // UPnP setup
    if nm.config.UPnPEnabled {
        if err := nm.setupUPnP(); err != nil {
            errs = append(errs, fmt.Errorf("UPnP setup error: %w", err))
        }
    }

    // Combine errors
    if len(errs) > 0 {
        return fmt.Errorf("NAT traversal initialization errors: %v", errs)
    }

    return nil
}

// initSTUN initializes STUN client and discovers external IP
func (nm *NATManager) initSTUN() error {
	if len(nm.config.STUNServers) == 0 {
		return fmt.Errorf("no STUN servers configured")
	}

	// Try each STUN server until one works
	var lastErr error
	for _, server := range nm.config.STUNServers {
		c, err := stun.Dial("udp", server)
		if err != nil {
			lastErr = err
			continue
		}
		nm.stunClient = c
		
		// Create message
		message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
		
		// Start transaction
		err = c.Do(message, func(res stun.Event) {
			if res.Error != nil {
				lastErr = res.Error
				return
			}

			// Get external IP
			var xorAddr stun.XORMappedAddress
			if err := xorAddr.GetFrom(res.Message); err != nil {
				lastErr = err
				return
			}

			nm.mutex.Lock()
			nm.externalIP = xorAddr.IP.String()
			nm.externalPort = xorAddr.Port
			nm.mutex.Unlock()
		})

		if err == nil && nm.externalIP != "" {
			return nil
		}
		lastErr = err
	}

	return fmt.Errorf("failed to initialize STUN: %v", lastErr)
}

// initTURN initializes TURN client
func (nm *NATManager) initTURN() error {
	if len(nm.config.TURNServers) == 0 {
		return fmt.Errorf("no TURN servers configured")
	}

	// Use the first configured TURN server
	turnConfig := nm.config.TURNServers[0]
	
	// Configure TURN client
	cfg := &turn.ClientConfig{
		STUNServerAddr: turnConfig.Address,
		TURNServerAddr: turnConfig.Address,
		Username:       turnConfig.Username,
		Password:       turnConfig.Password,
		Realm:         "blockchain",
	}

	client, err := turn.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create TURN client: %v", err)
	}

	nm.turnClient = client
	return nil
}

// setupUPnP attempts to set up UPnP port mapping
func (nm *NATManager) setupUPnP() error {
	clients, _, err := upnp.NewWANIPConnection2Clients()
	if err != nil {
		return fmt.Errorf("failed to discover UPnP devices: %v", err)
	}

	if len(clients) == 0 {
		return fmt.Errorf("no UPnP devices found")
	}

	// Try to map port on all discovered devices
	for _, client := range clients {
		err := client.AddPortMapping(
			"",                     // remoteHost (empty for all hosts)
			uint16(nm.config.ListenPort),  // external port
			"TCP",                  // protocol
			uint16(nm.config.ListenPort),  // internal port
			nm.config.ListenHost,   // internal client
			true,                   // enabled flag
			"Blockchain P2P",       // description
			uint32(86400),          // lease duration in seconds (24 hours)
		)

		if err == nil {
			nm.mappedPort = nm.config.ListenPort
			log.Printf("Successfully mapped port %d via UPnP", nm.config.ListenPort)
			return nil
		} else {
			log.Printf("Failed to map port via UPnP: %v", err)
		}
	}

	return fmt.Errorf("failed to set up UPnP port mapping on any device")
}

// detectNATType attempts to determine the type of NAT
func (nm *NATManager) detectNATType() {
	if nm.stunClient == nil {
		nm.natType = NATUnknown
		return
	}

	// Send binding request to STUN server
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	
	err := nm.stunClient.Do(message, func(res stun.Event) {
		if res.Error != nil {
			nm.natType = NATUnknown
			return
		}

		var xorAddr stun.XORMappedAddress
		if err := xorAddr.GetFrom(res.Message); err != nil {
			nm.natType = NATUnknown
			return
		}

		// Compare mapped address with local address
		if xorAddr.IP.String() == nm.config.ListenHost {
			nm.natType = NATOpen
		} else {
			// Additional tests needed to determine exact NAT type
			nm.natType = NATFull
		}
	})

	if err != nil {
		nm.natType = NATUnknown
	}
}

// GetExternalAddress returns the external IP and port
func (nm *NATManager) GetExternalAddress() (string, int) {
	nm.mutex.RLock()
	defer nm.mutex.RUnlock()
	return nm.externalIP, nm.externalPort
}

// GetNATType returns the detected NAT type
func (nm *NATManager) GetNATType() NATType {
	nm.mutex.RLock()
	defer nm.mutex.RUnlock()
	return nm.natType
}

// Close cleans up NAT manager resources
func (nm *NATManager) Close() error {
	nm.cancel()
	
	if nm.stunClient != nil {
		nm.stunClient.Close()
	}
	
	if nm.turnClient != nil {
		nm.turnClient.Close()
	}

	// Remove UPnP port mapping if it was set
	if nm.mappedPort > 0 {
		clients, _, _ := upnp.NewWANIPConnection2Clients()
		for _, client := range clients {
			client.DeletePortMapping("", uint16(nm.mappedPort), "TCP")
		}
	}

	return nil
}
