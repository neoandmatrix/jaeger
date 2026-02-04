package ai_assistant

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/ollama"
	"github.com/tmc/langchaingo/llms/openai"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.uber.org/zap"

	"github.com/jaegertracing/jaeger/cmd/jaeger/internal/extension/jaegerquery"
	"github.com/jaegertracing/jaeger/cmd/jaeger/internal/extension/jaegerquery/querysvc"
)

var _ extension.Extension = (*aiExtension)(nil)

type aiExtension struct {
	config   *Config
	logger   *zap.Logger
	server   *http.Server
	llm      llms.Model
	queryAPI *querysvc.QueryService
}

func newAIExtension(config *Config, logger *zap.Logger) *aiExtension {
	return &aiExtension{
		config: config,
		logger: logger,
	}
}

// Dependencies ensures this extension starts after jaegerquery
func (e *aiExtension) Dependencies() []component.ID {
	return []component.ID{jaegerquery.ID}
}

// Start is called when the Collector is starting.
func (e *aiExtension) Start(ctx context.Context, host component.Host) error {
	e.logger.Info("Starting AI Assistant Extension", zap.String("model", e.config.ModelName), zap.String("provider", e.config.LLMProvider))

	// 1. Get QueryService dependency (MCP pattern)
	queryExt, err := jaegerquery.GetExtension(host)
	if err != nil {
		return fmt.Errorf("cannot get %s extension: %w", jaegerquery.ID, err)
	}
	e.queryAPI = queryExt.QueryService()
	e.logger.Info("AI Assistant successfully connected to Jaeger Query Service")

	var llmClient llms.Model

	if e.config.LLMProvider == "openai" {
		// OpenAI Compatible (like llama.cpp server)
		// llama.cpp server usually needs a dummy token, and base URL ending in /v1 maybe?
		// We trust the user provided APIEndpoint is correct (e.g. http://localhost:8080/v1)
		llmClient, err = openai.New(
			openai.WithModel(e.config.ModelName),
			openai.WithBaseURL(e.config.APIEndpoint),
			openai.WithToken("dummy-token"), // llama.cpp doesn't care, but library might
		)
	} else {
		// Default to Ollama
		llmClient, err = ollama.New(
			ollama.WithModel(e.config.ModelName),
			ollama.WithServerURL(e.config.APIEndpoint),
			ollama.WithFormat("json"),
		)
	}

	if err != nil {
		return err
	}
	e.llm = llmClient

	// 2. Configure HTTP Router
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ai/parse_search", e.handleParseSearch)
	mux.HandleFunc("/api/ai/explain_trace", e.handleExplainTrace)
	mux.HandleFunc("/api/ai/filter_spans", e.handleFilterSpans)

	e.server = &http.Server{
		Addr:    e.config.Server.Endpoint,
		Handler: enableCORS(mux),
	}

	// 3. Start Server in Goroutine
	go func() {
		if err := e.server.ListenAndServe(); err != http.ErrServerClosed {
			e.logger.Error("AI Assistant HTTP server failed", zap.Error(err))
		}
	}()

	return nil
}

// Shutdown is called when the Collector is stopping.
func (e *aiExtension) Shutdown(ctx context.Context) error {
	if e.server != nil {
		// Create a timeout context for graceful shutdown
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return e.server.Shutdown(ctx)
	}
	return nil
}

func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
		if r.Method == "OPTIONS" {
			return
		}
		next.ServeHTTP(w, r)
	})
}
