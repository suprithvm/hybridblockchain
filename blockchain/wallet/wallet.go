package blockchain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
)

// Wallet represents a blockchain wallet
type Wallet struct {
	PrivateKey      *ecdsa.PrivateKey `json:"-"`
	PublicKey       *ecdsa.PublicKey  `json:"-"`
	Address         string            `json:"address"`
	PrivateKeyBytes []byte            `json:"private_key"` // Serialized Private Key
	PublicKeyBytes  []byte            `json:"public_key"`  // Serialized Public Key
	Mnemonic        string            `json:"mnemonic"`    // Mnemonic phrase for recovery
}

// NewWallet creates a new wallet by generating a key pair
func NewWallet() *Wallet {
	// Generate mnemonic
	mnemonic, err := GenerateMnemonic()
	if err != nil {
		panic(fmt.Errorf("failed to generate mnemonic: %v", err))
	}

	// Create wallet from mnemonic
	privateKey, err := RecoverFromMnemonic(mnemonic)
	if err != nil {
		panic(fmt.Errorf("failed to recover wallet from mnemonic: %v", err))
	}

	return NewWalletFromPrivateKeyAndMnemonic(privateKey, mnemonic)
}

// NewWalletFromPrivateKeyAndMnemonic creates a wallet from an existing private key and mnemonic
func NewWalletFromPrivateKeyAndMnemonic(privateKey *ecdsa.PrivateKey, mnemonic string) *Wallet {
	// Derive public key
	publicKey := &privateKey.PublicKey

	// Serialize keys
	privateKeyBytes, publicKeyBytes := serializeKeys(privateKey, publicKey)

	// Generate blockchain address
	address := generateAddress(publicKey)

	return &Wallet{
		PrivateKey:      privateKey,
		PublicKey:       publicKey,
		Address:         address,
		PrivateKeyBytes: privateKeyBytes,
		PublicKeyBytes:  publicKeyBytes,
		Mnemonic:        mnemonic,
	}
}

// generateAddress derives a unique blockchain address from the public key
func generateAddress(publicKey *ecdsa.PublicKey) string {
	// Concatenate public key coordinates
	pubKeyBytes := append(publicKey.X.Bytes(), publicKey.Y.Bytes()...)

	// Hash the concatenated bytes
	hash := sha256.Sum256(pubKeyBytes)

	// Create a checksum from the hash
	checksum := sha256.Sum256(hash[:])

	// Combine the prefix, hash, and checksum
	fullAddress := fmt.Sprintf("sup%s%s", hex.EncodeToString(hash[:10]), hex.EncodeToString(checksum[:4]))

	// Ensure the address length is between 30-36 characters
	if len(fullAddress) > 36 {
		return fullAddress[:36]
	}
	return fullAddress
}

// SerializeKeys serializes the private and public keys
func serializeKeys(privateKey *ecdsa.PrivateKey, publicKey *ecdsa.PublicKey) ([]byte, []byte) {
	privateKeyBytes := privateKey.D.Bytes()
	if len(privateKeyBytes) < 32 {
		padding := make([]byte, 32-len(privateKeyBytes))
		privateKeyBytes = append(padding, privateKeyBytes...)
	}

	publicKeyBytes := append(
		make([]byte, 32-len(publicKey.X.Bytes())), publicKey.X.Bytes()...,
	)
	publicKeyBytes = append(publicKeyBytes, append(
		make([]byte, 32-len(publicKey.Y.Bytes())), publicKey.Y.Bytes()...,
	)...)

	return privateKeyBytes, publicKeyBytes
}

// DeserializeKeys deserializes private and public keys from bytes
func DeserializeKeys(privateKeyBytes, publicKeyBytes []byte) (*ecdsa.PrivateKey, *ecdsa.PublicKey, error) {
	curve := elliptic.P256()

	privateKey := new(ecdsa.PrivateKey)
	privateKey.D = new(big.Int).SetBytes(privateKeyBytes)
	privateKey.PublicKey.Curve = curve
	privateKey.PublicKey.X = new(big.Int).SetBytes(publicKeyBytes[:32])
	privateKey.PublicKey.Y = new(big.Int).SetBytes(publicKeyBytes[32:])

	if !curve.IsOnCurve(privateKey.PublicKey.X, privateKey.PublicKey.Y) {
		return nil, nil, fmt.Errorf("deserialized public key is not on the curve")
	}

	publicKey := &ecdsa.PublicKey{
		Curve: curve,
		X:     privateKey.PublicKey.X,
		Y:     privateKey.PublicKey.Y,
	}

	return privateKey, publicKey, nil
}

// EncodeAddress generates a human-readable address
func EncodeAddress(publicKey string) string {
	hash := sha256.Sum256([]byte(publicKey))
	checksum := hash[:4] // First 4 bytes for checksum
	address := fmt.Sprintf("sup%s%s", hex.EncodeToString(hash[:10]), hex.EncodeToString(checksum))
	return address[:36] // Ensure length between 30-36
}

// ValidateAddress ensures the address is valid
func ValidateAddress(address string) bool {
	if len(address) < 30 || len(address) > 36 {
		return false
	}

	if address[:3] != "sup" {
		return false
	}

	// Extract and verify checksum
	addressBody := address[3:29]
	expectedChecksum := address[29:]
	hash := sha256.Sum256([]byte(addressBody))
	computedChecksum := hex.EncodeToString(hash[:4])

	return computedChecksum == expectedChecksum
}

// RecoverWalletFromMnemonic recovers a wallet using a mnemonic phrase
func RecoverWalletFromMnemonic(mnemonic string) (*Wallet, error) {
	privateKey, err := RecoverFromMnemonic(mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to recover wallet from mnemonic: %v", err)
	}

	publicKey := &privateKey.PublicKey
	privateKeyBytes, publicKeyBytes := serializeKeys(privateKey, publicKey)
	address := generateAddress(publicKey)

	return &Wallet{
		PrivateKey:      privateKey,
		PublicKey:       publicKey,
		Address:         address,
		PrivateKeyBytes: privateKeyBytes,
		PublicKeyBytes:  publicKeyBytes,
		Mnemonic:        mnemonic,
	}, nil
}

// LoadWalletFromFile loads a wallet from a file
func LoadWalletFromFile(filename string) (*Wallet, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read wallet file: %w", err)
	}

	var wallet Wallet
	err = json.Unmarshal(data, &wallet)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize wallet: %w", err)
	}

	privateKey, publicKey, err := DeserializeKeys(wallet.PrivateKeyBytes, wallet.PublicKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize keys: %w", err)
	}

	wallet.PrivateKey = privateKey
	wallet.PublicKey = publicKey

	return &wallet, nil
}

// NewWalletFromPrivateKey creates a wallet from an existing private key
func NewWalletFromPrivateKey(privateKey *ecdsa.PrivateKey) *Wallet {
	// Derive the public key from the private key
	publicKey := &privateKey.PublicKey

	// Serialize keys
	privateKeyBytes, publicKeyBytes := serializeKeys(privateKey, publicKey)

	// Generate blockchain address
	address := generateAddress(publicKey)

	return &Wallet{
		PrivateKey:      privateKey,
		PublicKey:       publicKey,
		Address:         address,
		PrivateKeyBytes: privateKeyBytes,
		PublicKeyBytes:  publicKeyBytes,
	}
}
