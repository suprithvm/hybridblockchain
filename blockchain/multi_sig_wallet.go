package blockchain

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// MultiSigwWallet represents a multi-signature wallet
type MultiSigwWallet struct {
	Owners        []string             // List of wallet owners
	RequiredSigs  int                  // Number of required signatures
	Balance       float64              // Wallet balance
	PublicKeyMap  map[string]*ecdsa.PublicKey // Owner -> Public key mapping
	Address       string               // Unique multi-sig wallet address
}

// GenerateMultiSigAddress generates a unique address for the multi-signature wallet
func GenerateMultiSigAddress(publicKeyMap map[string]*ecdsa.PublicKey) string {
    // Extract and sort the owners to ensure deterministic order
    owners := make([]string, 0, len(publicKeyMap))
    for owner := range publicKeyMap {
        owners = append(owners, owner)
    }
    sort.Strings(owners) // Sort to ensure consistent ordering

    // Concatenate sorted public keys
    var combinedKeys string
    for _, owner := range owners {
        pubKey := publicKeyMap[owner]
        combinedKeys += owner + pubKey.X.Text(16) + pubKey.Y.Text(16)
    }

    // Hash the combined keys to create a unique identifier
    hash := sha256.Sum256([]byte(combinedKeys))

    // Convert the hash to a hexadecimal string
    address := fmt.Sprintf("sup%s", hex.EncodeToString(hash[:28])) // Use exactly 28 characters after 'sup'

    return address[:31]
}




// NewMultiSigWallet creates a new multi-signature wallet
func NewMultiSigWallet(owners []string, requiredSigs int, publicKeyMap map[string]*ecdsa.PublicKey) (*MultiSigwWallet, error) {
	if len(owners) == 0 {
		return nil, errors.New("owners list cannot be empty")
	}
	if requiredSigs <= 0 || requiredSigs > len(owners) {
		return nil, errors.New("invalid number of required signatures")
	}

	// Generate a unique address for the multi-sig wallet
	address := GenerateMultiSigAddress(publicKeyMap)

	return &MultiSigwWallet{
		Owners:       owners,
		RequiredSigs: requiredSigs,
		Balance:      0,
		PublicKeyMap: publicKeyMap,
		Address:      address,
	}, nil
}

// Deposit adds funds to the wallet
func (msw *MultiSigwWallet) AddFunds(amount float64) {
	msw.Balance += amount
}

// Withdraw removes funds from the wallet
func (msw *MultiSigwWallet) DeductFunds(amount float64) error {
	if msw.Balance < amount {
		return errors.New("insufficient funds")
	}
	msw.Balance -= amount
	return nil
}

// AdjustSignatureThreshold changes the number of required signatures
func (msw *MultiSigwWallet) AdjustSignatureThreshold(newThreshold int) error {
    if newThreshold <= 0 || newThreshold > len(msw.Owners) {
        return fmt.Errorf("invalid signature threshold")
    }
    msw.RequiredSigs = newThreshold
    return nil
}



// ReplaceOwnerKey replaces a lost owner's public key with a new one
func (msw *MultiSigwWallet) ReplaceOwnerKey(oldOwner string, mnemonic string, newKey *ecdsa.PublicKey) error {
    // Ensure old owner exists
    _, exists := msw.PublicKeyMap[oldOwner]
    if !exists {
        return fmt.Errorf("owner %s not found", oldOwner)
    }

    // Recover private key using mnemonic
    masterKey, err := RecoverFromMnemonic(mnemonic)
    if err != nil {
        return fmt.Errorf("failed to recover private key from mnemonic: %v", err)
    }

    // Verify recovered key corresponds to the old owner
    recoveredAddress := generateAddress(&masterKey.PublicKey)
    if recoveredAddress != oldOwner {
        return fmt.Errorf("recovered key does not match owner address")
    }

    // Replace the key
    msw.PublicKeyMap[oldOwner] = newKey
    return nil
}


// ExecuteTransaction validates and executes a multi-signature transaction
func (msw *MultiSigwWallet) ExecuteTransaction(tx *MultiSigTransaction, publicKeyMap map[string]*ecdsa.PublicKey) error {
	// Validate the multi-signature transaction
	if !tx.ValidateSignatures(msw, publicKeyMap) {
		return fmt.Errorf("transaction validation failed due to insufficient or invalid signatures")
	}

	// Deduct funds and update the UTXO set
	if err := msw.DeductFunds(tx.Amount); err != nil {
		return fmt.Errorf("failed to deduct funds: %v", err)
	}

	return nil
}
