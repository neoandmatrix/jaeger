package ai_assistant

import (
	"encoding/json"
	"fmt"
	"net/http"
	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

type FilterRequest struct {
	Spans []SpanStart `json:"spans"`
}

type SpanStart struct {
	ID        string `json:"id"`
	Operation string `json:"op"`
	Service   string `json:"svc"`
}

type FilterResponse struct {
	NoiseIDs []string `json:"noise_ids"`
}

func (e *aiExtension) handleFilterSpans(w http.ResponseWriter, r *http.Request) {
	var req FilterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	// We only send a subset to the LLM to classify noise
	// Ideally, we would combine CPA (Critical Path) with this.
	// But the API defined by user takes a list of spans and returns noise IDs.
	// We'll proceed with LLM classification.

	inputBytes, _ := json.Marshal(req.Spans)
	prompt := fmt.Sprintf(`Given this list of microservice operations, classify each as "NOISE" (irrelevant background task) or "CONTEXT" (relevant business logic).
Criteria for NOISE:
- Health checks (/health, /status)
- Feature flag lookups
- Periodic metrics flushing
- Empty generic middleware spans

Input:
%s

Output JSON:
{"noise_ids": ["id1", "id2"]}`, string(inputBytes))

	resp, err := e.llm.Call(r.Context(), prompt, llms.WithTemperature(0))
	if err != nil {
		e.logger.Error("LLM inference failed", zap.Error(err))
		http.Error(w, "Inference failed", http.StatusInternalServerError)
		return
	}

	var filterResp FilterResponse
	// Try to unmarshal. If LLM wraps in markdown code blocks, we might need basics cleaning,
	// but using 'json' format in Ollama usually avoids this.
	if err := json.Unmarshal([]byte(resp), &filterResp); err != nil {
		// Fallback: try to find the JSON object in the string
		e.logger.Error("Failed to parse filter response", zap.String("response", resp))
		http.Error(w, "Failed to parse model output", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filterResp)
}


