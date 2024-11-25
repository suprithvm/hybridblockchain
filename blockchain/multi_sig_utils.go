package blockchain

import (
	"crypto/ecdsa"
	"fmt"
	"log"
)

// PublicKeyDatabase simulates a storage for address-to-public-key mappings
var PublicKeyDatabase = make(map[string]*ecdsa.PublicKey)

// AddPublicKeyToDatabase adds a public key to the database for an address
func AddPublicKeyToDatabase(address string, publicKey *ecdsa.PublicKey) {
	PublicKeyDatabase[address] = publicKey
}

// GetPublicKeyForAddress retrieves the public key for the given address
func GetPublicKeyForAddress(address string) (*ecdsa.PublicKey, error) {
    // Special case for BLOCKCHAIN address
    if address == "BLOCKCHAIN" {
        return nil, nil // No public key required for BLOCKCHAIN
    }

    // Check if the address exists in the database
    publicKey, exists := PublicKeyDatabase[address]
    if !exists {
        return nil, fmt.Errorf("public key not found for address: %s", address)
    }
    log.Printf("[DEBUG] Public Key Retrieved for %s: %v", address, publicKey)
    return publicKey, nil
}
