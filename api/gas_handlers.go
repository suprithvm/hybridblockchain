package api

import (
	"blockchain-core/blockchain/gas"
	"encoding/json"
	"net/http"
)

// GasHandlers contains all gas-related HTTP handlers
type GasHandlers struct {
	model     *gas.GasModel
	estimator *gas.GasEstimator
	preview   *gas.FeePreview
}

// NewGasHandlers creates a new instance of gas handlers
func NewGasHandlers() *GasHandlers {
	model := gas.NewGasModel(gas.MinGasPrice, 15_000_000)
	estimator := gas.NewGasEstimator(model)
	preview := gas.NewFeePreview(estimator)

	return &GasHandlers{
		model:     model,
		estimator: estimator,
		preview:   preview,
	}
}

// HandleGetCurrentPrice handles /gas/current-price endpoint
func (h *GasHandlers) HandleGetCurrentPrice(w http.ResponseWriter, r *http.Request) {
	response := map[string]uint64{
		"low":    h.model.GetCurrentGasPrice() / 2,
		"normal": h.model.GetCurrentGasPrice(),
		"high":   h.model.GetCurrentGasPrice() * 2,
	}

	json.NewEncoder(w).Encode(response)
}

// HandleEstimateFees handles /gas/estimate endpoint
func (h *GasHandlers) HandleEstimateFees(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxSize int `json:"tx_size"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	options := h.preview.GetTransactionFeePreview(req.TxSize)
	recommended := h.preview.GetRecommendedFee(req.TxSize)

	response := struct {
		Options     map[string]*gas.FeeDetails `json:"options"`
		Recommended *gas.FeeDetails            `json:"recommended"`
	}{
		Options:     options,
		Recommended: recommended,
	}

	json.NewEncoder(w).Encode(response)
}

// HandleGetNetworkStatus handles /gas/network-status endpoint
func (h *GasHandlers) HandleGetNetworkStatus(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{
		"status": h.preview.GetNetworkCongestion(),
	}

	json.NewEncoder(w).Encode(response)
}

// HandleGetRawGasFees handles /gas/raw-fees endpoint
func (h *GasHandlers) HandleGetRawGasFees(w http.ResponseWriter, r *http.Request) {
	currentPrice := h.model.GetCurrentGasPrice()

	response := map[string]uint64{
		"low":    currentPrice / 2,
		"normal": currentPrice,
		"high":   currentPrice * 2,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
