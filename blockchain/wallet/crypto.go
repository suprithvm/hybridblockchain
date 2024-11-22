package blockchain

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"crypto/rand"
)

// SignMessage signs a message using the wallet's private key
func SignMessage(privateKey *ecdsa.PrivateKey, message string) (string, error) {
	// Hash the message
	hash := sha256.Sum256([]byte(message))

	// Sign the hash
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, hash[:])
	if err != nil {
		return "", err
	}

	// Concatenate r and s into a single signature
	signature := append(r.Bytes(), s.Bytes()...)
	return hex.EncodeToString(signature), nil
}

// VerifySignature verifies a signature against a message and public key
func VerifySignature(publicKey *ecdsa.PublicKey, message, signature string) bool {
	// Decode the signature
	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	// Split the signature into r and s components
	r := big.Int{}
	s := big.Int{}
	sigLen := len(sigBytes) / 2
	r.SetBytes(sigBytes[:sigLen])
	s.SetBytes(sigBytes[sigLen:])

	// Hash the message
	hash := sha256.Sum256([]byte(message))

	// Verify the signature
	return ecdsa.Verify(publicKey, hash[:], &r, &s)
}
