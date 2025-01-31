package api

import (
	"github.com/gorilla/mux"
)

// Router handles all API routes
type Router struct {
	*mux.Router
	gasHandlers *GasHandlers
}

// NewRouter creates a new API router
func NewRouter() *Router {
	r := &Router{
		Router:      mux.NewRouter(),
		gasHandlers: NewGasHandlers(),
	}
	r.setupRoutes()
	return r
}

// setupRoutes configures all API routes
func (r *Router) setupRoutes() {
	// Gas routes
	r.HandleFunc("/gas/current-price", r.gasHandlers.HandleGetCurrentPrice).Methods("GET")
	r.HandleFunc("/gas/estimate", r.gasHandlers.HandleEstimateFees).Methods("POST")
	r.HandleFunc("/gas/network-status", r.gasHandlers.HandleGetNetworkStatus).Methods("GET")
	r.HandleFunc("/gas/raw-fees", r.gasHandlers.HandleGetRawGasFees).Methods("GET")
} 