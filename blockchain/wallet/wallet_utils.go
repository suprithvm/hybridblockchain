package blockchain

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/sha3"
	"math/big"
	"strings"
)

// GenerateMnemonic creates a new mnemonic phrase
func GenerateMnemonic() (string, error) {
	entropy := make([]byte, 16) // 128-bit entropy
	_, err := rand.Read(entropy)
	if err != nil {
		return "", err
	}
	hash := sha3.Sum256(entropy)
	mnemonic := hex.EncodeToString(hash[:])
	return mnemonic, nil
}

// RecoverFromMnemonic recovers a wallet using a mnemonic phrase
func RecoverFromMnemonic(mnemonic string) (*ecdsa.PrivateKey, error) {
	if len(mnemonic) == 0 {
		return nil, errors.New("mnemonic cannot be empty")
	}

	// Deterministic seed derived from the mnemonic
	hash := sha256.Sum256([]byte(mnemonic))

	// Use the hash as the seed for private key generation
	curve := elliptic.P256()
	privateKey := new(ecdsa.PrivateKey)
	privateKey.D = new(big.Int).SetBytes(hash[:])
	privateKey.PublicKey.Curve = curve
	privateKey.PublicKey.X, privateKey.PublicKey.Y = curve.ScalarBaseMult(hash[:])

	return privateKey, nil
}

// DeriveChildKey derives a child key from a master key
func DeriveChildKey(masterKey *ecdsa.PrivateKey, index int) (*ecdsa.PrivateKey, error) {
	seed := fmt.Sprintf("%s-%d", masterKey.D.String(), index)
	hash := sha3.Sum256([]byte(seed))

	childKey, err := ecdsa.GenerateKey(elliptic.P256(), strings.NewReader(hex.EncodeToString(hash[:])))
	if err != nil {
		return nil, err
	}
	return childKey, nil
}
