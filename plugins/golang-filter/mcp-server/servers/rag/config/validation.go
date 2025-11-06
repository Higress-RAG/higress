package config

import (
	"fmt"
	"strings"
)

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("config validation error [%s]: %s", e.Field, e.Message)
}

// ValidationErrors is a collection of validation errors
type ValidationErrors []ValidationError

func (errs ValidationErrors) Error() string {
	if len(errs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("found %d configuration error(s):\n", len(errs)))
	for i, err := range errs {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, err.Message))
	}
	return b.String()
}

// Validate validates the complete configuration
func (c *Config) Validate() error {
	var errs ValidationErrors

	// Validate Embedding configuration
	if err := c.validateEmbedding(); err != nil {
		errs = append(errs, err...)
	}

	// Validate VectorDB configuration
	if err := c.validateVectorDB(); err != nil {
		errs = append(errs, err...)
	}

	// Validate RAG configuration
	if err := c.validateRAG(); err != nil {
		errs = append(errs, err...)
	}

	// Validate Pipeline configuration if present
	if c.Pipeline != nil {
		if err := c.validatePipeline(); err != nil {
			errs = append(errs, err...)
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// validateEmbedding validates embedding configuration
func (c *Config) validateEmbedding() ValidationErrors {
	var errs ValidationErrors

	if c.Embedding.Provider == "" {
		errs = append(errs, ValidationError{
			Field:   "embedding.provider",
			Message: "embedding provider is required",
		})
	}

	if c.Embedding.Model == "" {
		errs = append(errs, ValidationError{
			Field:   "embedding.model",
			Message: "embedding model is required",
		})
	}

	if c.Embedding.Dimensions <= 0 {
		errs = append(errs, ValidationError{
			Field:   "embedding.dimensions",
			Message: fmt.Sprintf("embedding dimensions must be positive, got %d", c.Embedding.Dimensions),
		})
	}

	// Validate dimensions are reasonable (typical range: 128-4096)
	if c.Embedding.Dimensions > 0 && (c.Embedding.Dimensions < 128 || c.Embedding.Dimensions > 4096) {
		errs = append(errs, ValidationError{
			Field:   "embedding.dimensions",
			Message: fmt.Sprintf("embedding dimensions %d is outside typical range [128, 4096]", c.Embedding.Dimensions),
		})
	}

	return errs
}

// validateVectorDB validates vector database configuration
func (c *Config) validateVectorDB() ValidationErrors {
	var errs ValidationErrors

	if c.VectorDB.Provider == "" {
		errs = append(errs, ValidationError{
			Field:   "vectordb.provider",
			Message: "vectordb provider is required",
		})
	}

	// Provider-specific validations
	switch strings.ToLower(c.VectorDB.Provider) {
	case "chroma", "milvus", "qdrant":
		if c.VectorDB.Host == "" {
			errs = append(errs, ValidationError{
				Field:   "vectordb.host",
				Message: fmt.Sprintf("vectordb host is required for %s provider", c.VectorDB.Provider),
			})
		}
		if c.VectorDB.Collection == "" {
			errs = append(errs, ValidationError{
				Field:   "vectordb.collection",
				Message: fmt.Sprintf("collection name is required for %s provider", c.VectorDB.Provider),
			})
		}
	case "sqlite":
		if c.VectorDB.Database == "" {
			errs = append(errs, ValidationError{
				Field:   "vectordb.database",
				Message: "database path is required for SQLite provider",
			})
		}
	}

	return errs
}

// validateRAG validates RAG configuration
func (c *Config) validateRAG() ValidationErrors {
	var errs ValidationErrors

	if c.RAG.TopK <= 0 {
		errs = append(errs, ValidationError{
			Field:   "rag.top_k",
			Message: fmt.Sprintf("rag.top_k must be positive, got %d", c.RAG.TopK),
		})
	}

	if c.RAG.TopK > 100 {
		errs = append(errs, ValidationError{
			Field:   "rag.top_k",
			Message: fmt.Sprintf("rag.top_k %d is too large (max recommended: 100)", c.RAG.TopK),
		})
	}

	if c.RAG.Threshold < 0 || c.RAG.Threshold > 1 {
		errs = append(errs, ValidationError{
			Field:   "rag.threshold",
			Message: fmt.Sprintf("rag.threshold must be in [0, 1], got %.2f", c.RAG.Threshold),
		})
	}

	return errs
}

// validatePipeline validates pipeline configuration
func (c *Config) validatePipeline() ValidationErrors {
	var errs ValidationErrors

	// Validate RRF parameter
	if c.Pipeline.RRFK < 0 {
		errs = append(errs, ValidationError{
			Field:   "pipeline.rrf_k",
			Message: fmt.Sprintf("pipeline.rrf_k must be non-negative, got %d", c.Pipeline.RRFK),
		})
	}

	// Validate retrieval profiles
	for i, prof := range c.Pipeline.RetrievalProfiles {
		if prof.Name == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.retrieval_profiles[%d].name", i),
				Message: "profile name is required",
			})
		}

		if prof.TopK < 0 {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.retrieval_profiles[%d].top_k", i),
				Message: fmt.Sprintf("top_k must be non-negative, got %d", prof.TopK),
			})
		}

		if prof.Threshold < 0 || prof.Threshold > 1 {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.retrieval_profiles[%d].threshold", i),
				Message: fmt.Sprintf("threshold must be in [0, 1], got %.2f", prof.Threshold),
			})
		}

		// Validate gating thresholds
		if prof.VectorGate < 0 || prof.VectorGate > 1 {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.retrieval_profiles[%d].vector_gate", i),
				Message: fmt.Sprintf("vector_gate must be in [0, 1], got %.2f", prof.VectorGate),
			})
		}

		if prof.VectorLowGate < 0 || prof.VectorLowGate > 1 {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.retrieval_profiles[%d].vector_low_gate", i),
				Message: fmt.Sprintf("vector_low_gate must be in [0, 1], got %.2f", prof.VectorLowGate),
			})
		}

		// Validate gate consistency
		if prof.VectorGate > 0 && prof.VectorLowGate > 0 && prof.VectorLowGate >= prof.VectorGate {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.retrieval_profiles[%d]", i),
				Message: fmt.Sprintf("vector_low_gate (%.2f) must be less than vector_gate (%.2f)", prof.VectorLowGate, prof.VectorGate),
			})
		}
	}

	// Validate Post configuration
	if c.Pipeline.Post != nil {
		if c.Pipeline.Post.Rerank.Enable {
			if c.Pipeline.Post.Rerank.Endpoint == "" {
				errs = append(errs, ValidationError{
					Field:   "pipeline.post.rerank.endpoint",
					Message: "rerank endpoint is required when rerank is enabled",
				})
			}
			if c.Pipeline.Post.Rerank.TopN < 0 {
				errs = append(errs, ValidationError{
					Field:   "pipeline.post.rerank.top_n",
					Message: fmt.Sprintf("rerank.top_n must be non-negative, got %d", c.Pipeline.Post.Rerank.TopN),
				})
			}
		}

		if c.Pipeline.Post.Compress.Enable {
			if c.Pipeline.Post.Compress.TargetRatio < 0 || c.Pipeline.Post.Compress.TargetRatio > 1 {
				errs = append(errs, ValidationError{
					Field:   "pipeline.post.compress.target_ratio",
					Message: fmt.Sprintf("compress.target_ratio must be in [0, 1], got %.2f", c.Pipeline.Post.Compress.TargetRatio),
				})
			}
		}
	}

	// Validate CRAG configuration
	if c.Pipeline.CRAG != nil && c.Pipeline.EnableCRAG {
		if c.Pipeline.CRAG.Evaluator.Provider == "http" && c.Pipeline.CRAG.Evaluator.Endpoint == "" {
			errs = append(errs, ValidationError{
				Field:   "pipeline.crag.evaluator.endpoint",
				Message: "CRAG evaluator endpoint is required when provider is http",
			})
		}

		if c.Pipeline.CRAG.Evaluator.Correct < 0 || c.Pipeline.CRAG.Evaluator.Correct > 1 {
			errs = append(errs, ValidationError{
				Field:   "pipeline.crag.evaluator.correct",
				Message: fmt.Sprintf("CRAG correct threshold must be in [0, 1], got %.2f", c.Pipeline.CRAG.Evaluator.Correct),
			})
		}

		if c.Pipeline.CRAG.Evaluator.Incorrect < 0 || c.Pipeline.CRAG.Evaluator.Incorrect > 1 {
			errs = append(errs, ValidationError{
				Field:   "pipeline.crag.evaluator.incorrect",
				Message: fmt.Sprintf("CRAG incorrect threshold must be in [0, 1], got %.2f", c.Pipeline.CRAG.Evaluator.Incorrect),
			})
		}
	}

	// Validate Retrievers
	for i, ret := range c.Pipeline.Retrievers {
		if ret.Type == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("pipeline.retrievers[%d].type", i),
				Message: "retriever type is required",
			})
		}

		// Type-specific validations
		switch ret.Type {
		case "bm25":
			if ret.Params["endpoint"] == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("pipeline.retrievers[%d].params.endpoint", i),
					Message: "BM25 retriever requires endpoint parameter",
				})
			}
		case "web":
			if ret.Params["endpoint"] == "" && ret.Provider == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("pipeline.retrievers[%d]", i),
					Message: "Web retriever requires either endpoint or provider",
				})
			}
		}
	}

	return errs
}
