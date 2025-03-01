package blockchain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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
func NewWallet() (*Wallet, error) {
	// Generate mnemonic
	mnemonic, err := GenerateMnemonic(12)
	if err != nil {
		return nil, fmt.Errorf("failed to generate mnemonic: %v", err)
	}

	// Recover private key from the mnemonic
	privateKey, err := RecoverFromMnemonic(mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to recover private key: %v", err)
	}

	// Create the wallet
	wallet, err := NewWalletFromPrivateKeyAndMnemonic(privateKey, mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to create wallet: %v", err)
	}

	return wallet, nil
}

// NewWalletFromPrivateKeyAndMnemonic creates a wallet from an existing private key and mnemonic
func NewWalletFromPrivateKeyAndMnemonic(privateKey *ecdsa.PrivateKey, mnemonic string) (*Wallet, error) {
	// Validate inputs
	if privateKey == nil || len(mnemonic) == 0 {
		return nil, fmt.Errorf("invalid private key or mnemonic")
	}

	// Derive public key
	publicKey := &privateKey.PublicKey

	// Serialize keys
	privateKeyBytes, publicKeyBytes := serializeKeys(privateKey, publicKey)

	// Generate blockchain address
	address := generateAddress(publicKey)

	// Return the wallet object
	return &Wallet{
		PrivateKey:      privateKey,
		PublicKey:       publicKey,
		Address:         address,
		PrivateKeyBytes: privateKeyBytes,
		PublicKeyBytes:  publicKeyBytes,
		Mnemonic:        mnemonic,
	}, nil
}

// generateAddress derives a unique blockchain address from the public key
func generateAddress(publicKey *ecdsa.PublicKey) string {
	// Concatenate public key coordinates
	pubKeyBytes := append(publicKey.X.Bytes(), publicKey.Y.Bytes()...)

	// Hash the concatenated bytes
	hash := sha256.Sum256(pubKeyBytes)

	// Create a checksum from the hash
	checksum := sha256.Sum256(hash[:10])

	// Combine the prefix, hash, and checksum
	fullAddress := fmt.Sprintf("sup%s%s", hex.EncodeToString(hash[:10]), hex.EncodeToString(checksum[:4]))

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

func ValidateAddress(address string) bool {
	if len(address) < 30 || len(address) > 36 {
		return false
	}

	if address[:3] != "sup" {
		return false
	}

	// Extract the hash and checksum
	hashPart := address[3 : len(address)-8]
	expectedChecksum := address[len(address)-8:]

	// Recalculate the hash
	hashBytes, err := hex.DecodeString(hashPart)
	if err != nil {
		log.Print("[ERROR] Failed to decode hash part: ", err)
		return false
	}

	recalculatedChecksum := sha256.Sum256(hashBytes)
	computedChecksum := hex.EncodeToString(recalculatedChecksum[:4])
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

func RecoverWallet(mnemonic string) (*Wallet, error) {
	privateKey, err := RecoverFromMnemonic(mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to recover wallet from mnemonic: %v", err)
	}

	address := generateAddress(&privateKey.PublicKey)
	return &Wallet{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		Address:    address,
		Mnemonic:   mnemonic,
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

// SignTransaction signs a transaction using the wallet's private key
func (w *Wallet) SignTransaction(tx *Transaction) error {
	txHash := tx.Hash()
	signature, err := SignMessage(w.PrivateKey, txHash)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %v", err)
	}
	tx.Signature = signature
	return nil
}

// VerifyTransaction verifies a transaction's signature using the wallet's public key
func (w *Wallet) VerifyTransaction(tx *Transaction) bool {
	return VerifySignature(w.PublicKey, tx.Hash(), tx.Signature)
}

// Add this method to save wallet to file
func (w *Wallet) SaveToFile(filename string) error {
	// Marshal wallet to JSON
	data, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("failed to serialize wallet: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filename, data, 0600); err != nil {
		return fmt.Errorf("failed to write wallet file: %w", err)
	}

	return nil
}
