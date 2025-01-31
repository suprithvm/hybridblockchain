package blockchain

import "crypto/ecdsa"

// TransactionInterface defines common behaviors for all transaction types.
type TransactionInterface interface {
    Hash() string
    ValidateSignatures(wallet interface{}, publicKeyMap map[string]*ecdsa.PublicKey) bool
	GetData() []byte
	GetSize() int
	Validate() error
	Sign(privateKey []byte) error
	VerifySignature() bool
}