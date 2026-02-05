// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

// Package aihandler provides an AI-powered natural language interface to Jaeger.
// This handler is part of the Jaeger Query Service and uses MCP to execute trace queries.
package aihandler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
	"go.uber.org/zap"

	"github.com/jaegertracing/jaeger/cmd/jaeger/internal/extension/jaegerquery/querysvc"
)

// Config holds the AI handler configuration.
type Config struct {
	Enabled      bool   `mapstructure:"enabled"`
	LLMProvider  string `mapstructure:"llm_provider"`
	ModelName    string `mapstructure:"model_name"`
	APIEndpoint  string `mapstructure:"api_endpoint"`
	MCPServerURL string `mapstructure:"mcp_server_url"`
}

// Handler provides AI-powered natural language search for Jaeger.
type Handler struct {
	logger       *zap.Logger
	llm          llms.Model
	queryService *querysvc.QueryService
	mcpServerURL string
}

// NewHandler creates a new AI handler.
func NewHandler(
	cfg Config,
	queryService *querysvc.QueryService,
	logger *zap.Logger,
) (*Handler, error) {
	// Initialize LLM client
	llmClient, err := openai.New(
		openai.WithModel(cfg.ModelName),
		openai.WithBaseURL(cfg.APIEndpoint),
		openai.WithToken("dummy-token"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM: %w", err)
	}

	return &Handler{
		logger:       logger,
		llm:          llmClient,
		queryService: queryService,
		mcpServerURL: cfg.MCPServerURL,
	}, nil
}

// RegisterRoutes registers the AI handler routes on the given router.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/ai/search", h.handleSearch)
}

// MCPToolInput represents the input for an MCP tool call.
type MCPToolInput struct {
	ServiceName  string `json:"service_name"`
	StartTimeMin string `json:"start_time_min,omitempty"`
	StartTimeMax string `json:"start_time_max,omitempty"`
	SpanName     string `json:"span_name,omitempty"`
	WithErrors   bool   `json:"with_errors,omitempty"`
	DurationMin  string `json:"duration_min,omitempty"`
	DurationMax  string `json:"duration_max,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

// handleSearch processes natural language queries and executes them via MCP.
func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Parse request
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 2. Fetch available services from QueryService (for prompt context)
	services, err := h.queryService.GetServices(r.Context())
	if err != nil {
		h.logger.Warn("Failed to fetch services", zap.Error(err))
		services = []string{}
	}
	sort.Strings(services)

	// 3. Generate MCP tool call using LLM
	mcpInput, err := h.generateMCPInput(r.Context(), req.Query, services)
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
		return
	}

	// 4. Execute MCP search_traces tool
	result, err := h.executeMCPTool(r.Context(), "search_traces", mcpInput)
	if err != nil {
		http.Error(w, fmt.Sprintf("MCP error: %v", err), http.StatusInternalServerError)
		return
	}

	// 5. Return MCP result to client
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"query":      req.Query,
		"mcp_input":  mcpInput,
		"mcp_result": result,
	})
}

// generateMCPInput uses the LLM to convert natural language to MCP tool input.
func (h *Handler) generateMCPInput(ctx context.Context, query string, services []string) (*MCPToolInput, error) {
	systemPrompt := fmt.Sprintf(`You are a query parser for the Jaeger Distributed Tracing system.
Convert the user's natural language query into a JSON object that matches the MCP search_traces tool input.

Available Services: %v

Output Schema (JSON only, no explanations):
{
  "service_name": "string (REQUIRED - must be one of Available Services)",
  "start_time_min": "string (e.g., '-1h', '-30m', RFC3339). Default: '-1h'",
  "start_time_max": "string (e.g., 'now'). Default: 'now'",
  "span_name": "string or null",
  "with_errors": "boolean. Set to true if user mentions errors/failures.",
  "duration_min": "string (e.g., '100ms', '2s'). Set if user mentions slow.",
  "duration_max": "string or null",
  "limit": "integer. Default: 20"
}

Rules:
1. service_name is REQUIRED. Match user input to the closest service in Available Services.
2. If user says "errors" or "failed", set with_errors=true.
3. If user says "slow", set duration_min="500ms".
4. Output ONLY valid JSON, no markdown.`, services)

	response, err := h.llm.Call(ctx, systemPrompt+"\n\nUser Query: "+query)
	if err != nil {
		return nil, err
	}

	// Parse LLM response as JSON
	var result MCPToolInput
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w, response: %s", err, response)
	}

	// Set defaults if not provided
	if result.StartTimeMin == "" {
		result.StartTimeMin = "-1h"
	}
	if result.Limit == 0 {
		result.Limit = 20
	}

	return &result, nil
}

// executeMCPTool calls the MCP server to execute a tool.
func (h *Handler) executeMCPTool(ctx context.Context, toolName string, input any) (map[string]any, error) {
	// Build MCP tool call request
	mcpRequest := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": input,
		},
	}

	body, err := json.Marshal(mcpRequest)
	if err != nil {
		return nil, err
	}

	// Call MCP server
	req, err := http.NewRequestWithContext(ctx, "POST", h.mcpServerURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("MCP server request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MCP server returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse MCP response
	var mcpResponse map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&mcpResponse); err != nil {
		return nil, err
	}

	return mcpResponse, nil
}
