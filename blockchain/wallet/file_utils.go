package blockchain

import (
	"encoding/json"
	"fmt"
	"os"
)

// SaveWallet saves a wallet to a file in JSON format
func SaveWallet(wallet *Wallet, filename string) error {
	data, err := json.Marshal(wallet)
	if err != nil {
		return fmt.Errorf("failed to serialize wallet: %w", err)
	}

	err = os.WriteFile(filename, data, 0600) // Restrict permissions
	if err != nil {
		return fmt.Errorf("failed to write wallet file: %w", err)
	}
	return nil
}

// LoadWallet loads a wallet from a JSON file
func LoadWallet(filename string) (*Wallet, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read wallet file: %w", err)
	}

	var tempWallet Wallet
	err = json.Unmarshal(data, &tempWallet)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize wallet: %w", err)
	}

	privateKey, publicKey, err := DeserializeKeys(tempWallet.PrivateKeyBytes, tempWallet.PublicKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize keys: %w", err)
	}

	tempWallet.PrivateKey = privateKey
	tempWallet.PublicKey = publicKey

	return &tempWallet, nil
}
