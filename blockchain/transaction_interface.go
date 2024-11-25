package blockchain

import "crypto/ecdsa"

// TransactionInterface defines common behaviors for all transaction types.
type TransactionInterface interface {
    Hash() string
    ValidateSignatures(wallet interface{}, publicKeyMap map[string]*ecdsa.PublicKey) bool
}