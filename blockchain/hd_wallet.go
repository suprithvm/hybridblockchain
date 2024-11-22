package blockchain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// HDWallet represents a hierarchical deterministic wallet
type HDWallet struct {
	MasterKey      *ecdsa.PrivateKey   // Master private key
	MasterPublicKey *ecdsa.PublicKey  // Master public key
	Mnemonic       string             // Mnemonic phrase for recovery
	ChildKeys      []*ecdsa.PrivateKey // Derived child keys
	Addresses      []string           // Blockchain addresses for child keys
}

// GenerateMasterKey creates a master key from a mnemonic
func GenerateMasterKey(mnemonic string) (*ecdsa.PrivateKey, error) {
	return RecoverFromMnemonic(mnemonic)
}


// DeriveChildKey generates a child key from the master key
func DeriveChildKeyFromMaster(masterKey *ecdsa.PrivateKey, index uint32) (*ecdsa.PrivateKey, error) {
	// Create a deterministic seed based on master key and index
	seed := append(masterKey.D.Bytes(), byte(index>>24), byte(index>>16), byte(index>>8), byte(index))
	hash := sha256.Sum256(seed)

	// Generate child key
	childKey := new(ecdsa.PrivateKey)
	childKey.D = new(big.Int).SetBytes(hash[:])
	childKey.PublicKey.Curve = elliptic.P256()
	childKey.PublicKey.X, childKey.PublicKey.Y = childKey.PublicKey.Curve.ScalarBaseMult(hash[:])

	return childKey, nil
}



// GenerateAddressFromPublicKey creates an address from a public key
func GenerateAddressFromPublicKey(publicKey *ecdsa.PublicKey) string {
	// Concatenate public key coordinates
	pubKeyBytes := append(publicKey.X.Bytes(), publicKey.Y.Bytes()...)

	// Hash the concatenated bytes
	hash := sha256.Sum256(pubKeyBytes)

	// Create a checksum from the hash
	checksum := sha256.Sum256(hash[:])

	// Combine the prefix, hash, and checksum
	fullAddress := fmt.Sprintf("sup%s%s", hex.EncodeToString(hash[:10]), hex.EncodeToString(checksum[:4]))

	// Ensure the address length is between 30-36 characters
	if len(fullAddress) < 30 {
		padding := strings.Repeat("x", 30-len(fullAddress))
		fullAddress = fullAddress + padding
	} else if len(fullAddress) > 36 {
		fullAddress = fullAddress[:36]
	}

	return fullAddress
}


// CreateHDWallet initializes an HD wallet with child keys
func CreateHDWallet(mnemonic string, numKeys int) (*HDWallet, error) {
	masterKey, err := GenerateMasterKey(mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to generate master key: %v", err)
	}

	wallet := &HDWallet{
		MasterKey:      masterKey,
		MasterPublicKey: &masterKey.PublicKey,
		Mnemonic:       mnemonic,
	}

	for i := 0; i < numKeys; i++ {
		childKey, err := DeriveChildKeyFromMaster(masterKey, uint32(i))
		if err != nil {
			return nil, fmt.Errorf("failed to derive child key: %v", err)
		}
		wallet.ChildKeys = append(wallet.ChildKeys, childKey)

		address := GenerateAddressFromPublicKey(&childKey.PublicKey)
		wallet.Addresses = append(wallet.Addresses, address)
	}

	return wallet, nil
}

// ListAddresses retrieves all derived addresses in the wallet
func (w *HDWallet) ListAddresses() []string {
	return w.Addresses
}

// SignTransactionWithChildKey signs a transaction using a specific child key
func (w *HDWallet) SignTransactionWithChildKey(tx *Transaction, index int) error {
	if index >= len(w.ChildKeys) {
		return fmt.Errorf("invalid child key index")
	}

	childKey := w.ChildKeys[index]
	txHash := tx.Hash()
	signature, err := SignMessage(childKey, txHash)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature
	return nil
}

