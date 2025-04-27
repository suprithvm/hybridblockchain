package blockchain

// Gas conversion constants and utilities
const (
	// GasToTokenConversionFactor is the conversion rate from gas units to tokens
	// 100,000,000 gas units = 1 token (similar to Satoshi to BTC)
	GasToTokenConversionFactor = 1e8
)

// ConvertGasToTokens converts a gas amount to token amount
func ConvertGasToTokens(gasAmount uint64) float64 {
	return float64(gasAmount) / GasToTokenConversionFactor
}

// ConvertTokensToGas converts a token amount to gas units
func ConvertTokensToGas(tokenAmount float64) uint64 {
	return uint64(tokenAmount * GasToTokenConversionFactor)
}
