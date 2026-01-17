// Package server implements the LLM proxy HTTP server
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cloud-shuttle/drover/internal/llmproxy"
	"github.com/cloud-shuttle/drover/internal/llmproxy/provider"
	"github.com/gorilla/mux"
)

// Server is the LLM proxy HTTP server
type Server struct {
	config       *llmproxy.Config
	providers    map[llmproxy.ProviderType]llmproxy.Provider
	server       *http.Server
	costTracker  *CostTracker
	logger       *RequestLogger
	rateLimiter  *RateLimiter
	requestCount atomic.Int64
}

// New creates a new LLM proxy server
func New(cfg *llmproxy.Config) (*Server, error) {
	// Create providers from config
	providers, err := provider.CreateAllProviders(cfg.Providers)
	if err != nil {
		return nil, fmt.Errorf("failed to create providers: %w", err)
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no enabled providers found in configuration")
	}

	s := &Server{
		config:      cfg,
		providers:   providers,
		costTracker: NewCostTracker(cfg.CostBudget),
		logger:      NewRequestLogger(cfg.LogRequests, cfg.LogResponses, cfg.RedactPII),
		rateLimiter: NewRateLimiter(cfg.RateLimit),
	}

	return s, nil
}

// Start starts the HTTP server
func (s *Server) Start() error {
	router := mux.NewRouter()

	// API routes - OpenAI-compatible endpoints
	router.HandleFunc("/v1/chat/completions", s.handleChatCompletions).Methods("POST")
	router.HandleFunc("/v1/models", s.handleModels).Methods("GET")

	// Proxy-specific routes
	router.HandleFunc("/health", s.handleHealth).Methods("GET")
	router.HandleFunc("/metrics", s.handleMetrics).Methods("GET")
	router.HandleFunc("/providers", s.handleProviders).Methods("GET")

	// Apply middleware
	var handler http.Handler = router
	handler = s.rateLimiter.Middleware(handler)
	handler = s.loggingMiddleware(handler)
	handler = s.corsMiddleware(handler)

	s.server = &http.Server{
		Addr:         s.config.ListenAddr,
		Handler:      handler,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  1 * time.Minute,
	}

	enabledProviders := make([]string, 0, len(s.providers))
	for typ := range s.providers {
		enabledProviders = append(enabledProviders, string(typ))
	}

	log.Printf("LLM Proxy server starting on %s", s.config.ListenAddr)
	log.Printf("Enabled providers: %v", enabledProviders)
	log.Printf("Cost budget enabled: %v", s.config.CostBudget.Enabled)

	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

// GetProvider returns a provider by type, or the default if not specified
func (s *Server) GetProvider(typ llmproxy.ProviderType) (llmproxy.Provider, error) {
	if typ != "" {
		p, ok := s.providers[typ]
		if !ok {
			return nil, fmt.Errorf("provider %s not found or not enabled", typ)
		}
		return p, nil
	}

	// Return first available provider as default
	for _, p := range s.providers {
		return p, nil
	}

	return nil, fmt.Errorf("no providers available")
}

// GetProviderByModel returns the appropriate provider for a given model
func (s *Server) GetProviderByModel(model string) (llmproxy.ProviderType, error) {
	// Check explicit model mapping
	if typ, ok := s.config.ModelMapping[model]; ok {
		if _, exists := s.providers[typ]; exists {
			return typ, nil
		}
	}

	// Check each provider's models
	for typ, p := range s.providers {
		for _, m := range p.GetModels() {
			if m.ID == model || m.Name == model {
				return typ, nil
			}
		}
	}

	// Use default provider
	return "", nil
}

// handleHealth returns the server health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"uptime":  time.Since(time.Now()).String(),
		"version": "0.1.0",
	})
}

// handleMetrics returns usage metrics
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	stats := s.costTracker.GetStats()
	stats["request_count"] = s.requestCount.Load()

	json.NewEncoder(w).Encode(stats)
}

// handleProviders returns information about available providers
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	type ProviderInfo struct {
		Type    llmproxy.ProviderType `json:"type"`
		Models  []llmproxy.Model      `json:"models"`
		Enabled bool                  `json:"enabled"`
	}

	providers := make([]ProviderInfo, 0, len(s.providers))
	for typ, p := range s.providers {
		providers = append(providers, ProviderInfo{
			Type:    typ,
			Models:  p.GetModels(),
			Enabled: true,
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"providers": providers,
	})
}

// handleModels returns available models (OpenAI-compatible endpoint)
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var allModels []llmproxy.Model
	for _, p := range s.providers {
		allModels = append(allModels, p.GetModels()...)
	}

	type ModelResponse struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
			Created int64 `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}

	response := ModelResponse{
		Object: "list",
	}

	for _, m := range allModels {
		response.Data = append(response.Data, struct {
			ID     string `json:"id"`
			Object string `json:"object"`
			Created int64 `json:"created"`
			OwnedBy string `json:"owned_by"`
		}{
			ID:     m.ID,
			Object: "model",
			Created: 0,
			OwnedBy: string(m.Provider),
		})
	}

	json.NewEncoder(w).Encode(response)
}

// handleChatCompletions handles chat completion requests
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.requestCount.Add(1)

	startTime := time.Now()

	// Parse request
	var req llmproxy.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Determine provider for this model
	providerType, err := s.GetProviderByModel(req.Model)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, fmt.Errorf("failed to determine provider for model %s: %w", req.Model, err))
		return
	}
	req.Provider = providerType

	// Get provider
	p, err := s.GetProvider(providerType)
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, fmt.Errorf("provider unavailable: %w", err))
		return
	}

	// Set API key if available from request (for multi-tenant scenarios)
	if req.ProviderAPIKey != "" {
		// Override provider config for this request
		// This would require provider to support per-request API keys
		// For now, we'll use the server-configured API key
	}

	ctx := r.Context()

	// Check budget before processing
	if s.config.CostBudget.Enabled && !s.costTracker.CheckBudget(ctx) {
		s.respondError(w, http.StatusForbidden, fmt.Errorf("cost budget exceeded"))
		return
	}

	// Log request
	requestID := generateRequestID()
	if s.config.LogRequests {
		s.logger.LogRequest(requestID, &req, r)
	}

	// Execute request
	if req.Stream {
		s.handleStreamingChat(w, r, p, &req, requestID, startTime)
	} else {
		s.handleNonStreamingChat(w, r, p, &req, requestID, startTime)
	}
}

// handleNonStreamingChat handles non-streaming chat completions
func (s *Server) handleNonStreamingChat(w http.ResponseWriter, r *http.Request, p llmproxy.Provider, req *llmproxy.ChatRequest, requestID string, startTime time.Time) {
	resp, err := p.Chat(r.Context(), req)
	if err != nil {
		if s.config.LogRequests {
			s.logger.LogError(requestID, req, err)
		}
		s.respondError(w, http.StatusInternalServerError, fmt.Errorf("chat completion failed: %w", err))
		return
	}

	// Calculate and track cost
	if s.config.CostBudget.Enabled {
		cost, err := p.CalculateCost(req.Model, &resp.Usage)
		if err == nil {
			s.costTracker.RecordCost(cost)
		}
	}

	// Log response
	duration := time.Since(startTime)
	if s.config.LogResponses {
		s.logger.LogResponse(requestID, req, resp, duration)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleStreamingChat handles streaming chat completions
func (s *Server) handleStreamingChat(w http.ResponseWriter, r *http.Request, p llmproxy.Provider, req *llmproxy.ChatRequest, requestID string, startTime time.Time) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.respondError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported"))
		return
	}

	// Start streaming
	chunkCh, err := p.StreamChat(r.Context(), req)
	if err != nil {
		if s.config.LogRequests {
			s.logger.LogError(requestID, req, err)
		}
		s.respondError(w, http.StatusInternalServerError, fmt.Errorf("stream start failed: %w", err))
		return
	}

	// Stream chunks
	var totalTokens int
	for chunk := range chunkCh {
		// Accumulate token count (rough estimate)
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			totalTokens += len(chunk.Choices[0].Delta.Content) / 4
		}

		// Send SSE event
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Send final done event
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	// Log completion
	duration := time.Since(startTime)
	if s.config.LogRequests {
		s.logger.LogStreamingComplete(requestID, req, totalTokens, duration)
	}

	// Track cost estimate
	if s.config.CostBudget.Enabled {
		usage := &llmproxy.Usage{
			PromptTokens:     len(req.Messages) * 100,
			CompletionTokens: totalTokens,
			TotalTokens:      len(req.Messages)*100 + totalTokens,
		}
		cost, _ := p.CalculateCost(req.Model, usage)
		s.costTracker.RecordCost(cost)
	}
}

// respondError sends an error response
func (s *Server) respondError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	type ErrorResponse struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	resp := ErrorResponse{}
	resp.Error.Message = err.Error()
	resp.Error.Type = "api_error"
	resp.Error.Code = fmt.Sprintf("%d", status)

	json.NewEncoder(w).Encode(resp)
}

// Middleware

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		log.Printf("[%s] %s %s", r.Method, r.URL.Path, r.RemoteAddr)

		next.ServeHTTP(w, r)

		log.Printf("[%s] %s completed in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}
