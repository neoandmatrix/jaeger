package ai_assistant

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

type SearchParams struct {
	Service     string `json:"service"`
	Operation   string `json:"operation,omitempty"`
	Tags        string `json:"tags,omitempty"`
	MinDuration string `json:"minDuration,omitempty"`
	MaxDuration string `json:"maxDuration,omitempty"`
	Lookback    string `json:"lookback,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

func (e *aiExtension) handleParseSearch(w http.ResponseWriter, r *http.Request) {
	// 1. Parse Request Body
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	// 2. Construct Prompt
	prompt := fmt.Sprintf("USER QUERY: %s", req.Query)
	// We could prepend e.config.Prompts.SearchSystemPrompt here if LangChainGo supported strict system messages in simple Call API
	// or we can construct a unified prompt.
	if e.config.Prompts.SearchSystemPrompt != "" {
		prompt = fmt.Sprintf("%s\n\n%s", e.config.Prompts.SearchSystemPrompt, prompt)
	} else {
		// Default system prompt if none configured
		defaultSystemPrompt := `You are a query parser for the Jaeger Distributed Tracing system.
Your goal is to extract search parameters from the user's natural language input and return them as a strict JSON object.

Output Schema:
{
  "service": "The name of the microservice. (string or null)",
  "operation": "The specific endpoint or function name. (string or null)" default: null,
  "tags": "Logfmt string of tags. Default: null",
  "minDuration": "Execution time floor. Default: null",
  "maxDuration": "Execution time ceiling. Default: null",
  "lookback": "Time window. Valid values: 5m, 15m, 30m, 1h, 2h, 3h, 6h, 12h, 24h, 2d. Default: 1h",
  "limit": "Max number of traces. Default: 20"
}

Rules:
1. Service: If no service is specified, use "jaeger".
2. Tags: Return null unless "error", "failed", or "exception" is mentioned.
   - If mentioned, set to "error=true".
3. MinDuration: Return null unless "slow" or a duration is mentioned.
   - If "slow" is mentioned without duration, use "500ms".
4. MaxDuration: Return null unless "fast" or a specific "max" duration is mentioned.
5. Do NOT output invalid JSON.

Example:
Input: "Show me all traces"
Output:
{
  "service": "jaeger",
  "tags": null,
  "minDuration": null,
  "maxDuration": null,
  "lookback": "1h",
  "limit": 20
}
`
		prompt = fmt.Sprintf("%s\n\n%s", defaultSystemPrompt, prompt)
	}

	// 3. Call LLM (using context from request for cancellation)
	// Note: We assume the client was initialized with JSON format enforcement
	resp, err := e.llm.Call(r.Context(), prompt, llms.WithTemperature(0.1)) // Low temp for deterministic output
	if err != nil {
		e.logger.Error("LLM inference failed", zap.Error(err))
		http.Error(w, "Inference failed", http.StatusInternalServerError)
		return
	}

	// 4. Validate JSON (Unmarshal into struct)
	var params SearchParams
	if err := json.Unmarshal([]byte(resp), &params); err != nil {
		e.logger.Error("Model produced invalid JSON", zap.String("response", resp))
		// Optional: Retry logic could go here
		http.Error(w, "Failed to parse model output", http.StatusInternalServerError)
		return
	}

	// 5. Return JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(params)
}
