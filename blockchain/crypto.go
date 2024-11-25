package blockchain

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"crypto/rand"
	"log"
)

// SignMessage signs a message using the wallet's private key
// SignMessage signs a message using the wallet's private key
func SignMessage(privateKey *ecdsa.PrivateKey, message string) (string, error) {
    // Hash the message
    hash := sha256.Sum256([]byte(message))
    log.Printf("[DEBUG] Hash to Sign: %x", hash)

    // Sign the hash
    r, s, err := ecdsa.Sign(rand.Reader, privateKey, hash[:])
    if err != nil {
        return "", err
    }

    // Concatenate r and s into a single signature
    signature := append(r.Bytes(), s.Bytes()...)
    log.Printf("[DEBUG] Signature Generated: %x", signature)
    return hex.EncodeToString(signature), nil
}


// VerifySignature verifies a signature against a message and public key
// VerifySignature verifies a signature against a message and public key
func VerifySignature(publicKey *ecdsa.PublicKey, message, signature string) bool {
    // Decode the signature
    sigBytes, err := hex.DecodeString(signature)
    if err != nil {
        log.Printf("[DEBUG] Failed to decode signature: %v", err)
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
    log.Printf("[DEBUG] Hash for Verification: %x", hash)

    isValid := ecdsa.Verify(publicKey, hash[:], &r, &s)
    log.Printf("[DEBUG] Signature Valid: %v", isValid)
    return isValid
}

