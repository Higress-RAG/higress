package rag

import (
	"errors"
	"fmt"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-session/common"
	"github.com/mark3labs/mcp-go/mcp"
)

const Version = "1.0.0"

type RAGConfig struct {
	config *config.Config
}

func init() {
	common.GlobalRegistry.RegisterServer("rag", &RAGConfig{
		config: &config.Config{
			RAG: config.RAGConfig{
				Splitter: config.SplitterConfig{
					Provider:     "recursive",
					ChunkSize:    500,
					ChunkOverlap: 50,
				},
				Threshold: 0.5,
				TopK:      10,
			},
			LLM: config.LLMConfig{
				Provider:    "",
				APIKey:      "",
				BaseURL:     "",
				Model:       "gpt-4o",
				Temperature: 0.5,
				MaxTokens:   2048,
			},
            Embedding: config.EmbeddingConfig{
                Provider:   "",
                APIKey:     "",
                BaseURL:    "",
                Model:      "",
                Dimensions: 0,
            },
            VectorDB: config.VectorDBConfig{
                Provider:   "",
                Host:       "",
                Port:       0,
                Database:   "",
                Collection: "",
				Username:   "",
				Password:   "",
				Mapping: config.MappingConfig{
					Fields: []config.FieldMapping{
						{
							StandardName: "id",
							RawName:      "id",
							Properties: map[string]interface{}{
								"max_length": 256,
								"auto_id":    false,
							},
						},
						{
							StandardName: "content",
							RawName:      "content",
							Properties: map[string]interface{}{
								"max_length": 8192,
							},
						},
						{
							StandardName: "vector",
							RawName:      "vector",
							Properties:   make(map[string]interface{}),
						},
						{
							StandardName: "metadata",
							RawName:      "metadata",
							Properties:   make(map[string]interface{}),
						},
						{
							StandardName: "created_at",
							RawName:      "created_at",
							Properties:   make(map[string]interface{}),
						},
					},
					Index: config.IndexConfig{
						IndexType: "HNSW",
						Params:    map[string]interface{}{"M": 8, "efConstruction": 64},
					},
					Search: config.SearchConfig{
						MetricType: "IP",
						Params:     make(map[string]interface{}),
					},
				},
			},
		},
	})
}

func (c *RAGConfig) ParseConfig(cfg map[string]any) error {
	// Parse RAG configuration
	if ragConfig, ok := cfg["rag"].(map[string]any); ok {
		if splitter, exists := ragConfig["splitter"].(map[string]any); exists {
			if splitterType, exists := splitter["provider"].(string); exists {
				c.config.RAG.Splitter.Provider = splitterType
			}
			if chunkSize, exists := splitter["chunk_size"].(float64); exists {
				c.config.RAG.Splitter.ChunkSize = int(chunkSize)
			}
			if chunkOverlap, exists := splitter["chunk_overlap"].(float64); exists {
				c.config.RAG.Splitter.ChunkOverlap = int(chunkOverlap)
			}
		}
		if threshold, exists := ragConfig["threshold"].(float64); exists {
			c.config.RAG.Threshold = threshold
		}
		if topK, exists := ragConfig["top_k"].(float64); exists {
			c.config.RAG.TopK = int(topK)
		}
	}

	// Parse Embedding configuration
	if embeddingConfig, ok := cfg["embedding"].(map[string]any); ok {
		if provider, exists := embeddingConfig["provider"].(string); exists {
			c.config.Embedding.Provider = provider
		} else {
			return errors.New("missing embedding provider")
		}

		if apiKey, exists := embeddingConfig["api_key"].(string); exists {
			c.config.Embedding.APIKey = apiKey
		}
		if baseURL, exists := embeddingConfig["base_url"].(string); exists {
			c.config.Embedding.BaseURL = baseURL
		}
		if model, exists := embeddingConfig["model"].(string); exists {
			c.config.Embedding.Model = model
		}
		if dimensions, exists := embeddingConfig["dimensions"].(float64); exists {
			c.config.Embedding.Dimensions = int(dimensions)
		}
	}

	// Parse llm configuration
	if llmConfig, ok := cfg["llm"].(map[string]any); ok {
		if provider, exists := llmConfig["provider"].(string); exists {
			c.config.LLM.Provider = provider
		}
		if apiKey, exists := llmConfig["api_key"].(string); exists {
			c.config.LLM.APIKey = apiKey
		}
		if baseURL, exists := llmConfig["base_url"].(string); exists {
			c.config.LLM.BaseURL = baseURL
		}
		if model, exists := llmConfig["model"].(string); exists {
			c.config.LLM.Model = model
		}
		if temperature, exists := llmConfig["temperature"].(float64); exists {
			c.config.LLM.Temperature = temperature
		}
		if maxTokens, exists := llmConfig["max_tokens"].(float64); exists {
			c.config.LLM.MaxTokens = int(maxTokens)
		}
	}

	// Parse VectorDB configuration
	if vectordbConfig, ok := cfg["vectordb"].(map[string]any); ok {
		if provider, exists := vectordbConfig["provider"].(string); exists {
			c.config.VectorDB.Provider = provider
		} else {
			return errors.New("missing vectordb provider")
		}
		if host, exists := vectordbConfig["host"].(string); exists {
			c.config.VectorDB.Host = host
		}
		if port, exists := vectordbConfig["port"].(float64); exists {
			c.config.VectorDB.Port = int(port)
		}
		if dbName, exists := vectordbConfig["database"].(string); exists {
			c.config.VectorDB.Database = dbName
		}
		if collection, exists := vectordbConfig["collection"].(string); exists {
			c.config.VectorDB.Collection = collection
		}
		if username, exists := vectordbConfig["username"].(string); exists {
			c.config.VectorDB.Username = username
		}
		if password, exists := vectordbConfig["password"].(string); exists {
			c.config.VectorDB.Password = password
		}

		// Parse mapping here
		if mapping, exists := vectordbConfig["mapping"].(map[string]any); exists {
			// Parse field mappings
			if fields, ok := mapping["fields"].([]any); ok {
				c.config.VectorDB.Mapping.Fields = []config.FieldMapping{}
				for _, field := range fields {
					if fieldMap, ok := field.(map[string]any); ok {
						fieldMapping := config.FieldMapping{
							Properties: make(map[string]interface{}),
						}
						if standardName, ok := fieldMap["standard_name"].(string); ok {
							fieldMapping.StandardName = standardName
						}

						if rawName, ok := fieldMap["raw_name"].(string); ok {
							fieldMapping.RawName = rawName
						}
						// Parse properties
						if properties, ok := fieldMap["properties"].(map[string]any); ok {
							for key, value := range properties {
								fieldMapping.Properties[key] = value
							}
						}
						c.config.VectorDB.Mapping.Fields = append(c.config.VectorDB.Mapping.Fields, fieldMapping)
					}
				}
			}

			// Parse index configuration
			if index, ok := mapping["index"].(map[string]any); ok {
				if indexType, ok := index["index_type"].(string); ok {
					c.config.VectorDB.Mapping.Index.IndexType = indexType
				}

				// Parse index parameters
				if params, ok := index["params"].(map[string]any); ok {
					c.config.VectorDB.Mapping.Index.Params = params
				}
			}

			// Parse search configuration
			if search, ok := mapping["search"].(map[string]any); ok {
				if metricType, ok := search["metric_type"].(string); ok {
					c.config.VectorDB.Mapping.Search.MetricType = metricType
				}
				// Parse search parameters
				if params, ok := search["params"].(map[string]any); ok {
					c.config.VectorDB.Mapping.Search.Params = params
				}
			}
		}
	}

	// Optional: parse enhanced pipeline configuration
	if pipelineConfig, ok := cfg["pipeline"].(map[string]any); ok {
		pc := &config.PipelineConfig{}
		if v, ok := pipelineConfig["enable_pre"].(bool); ok { pc.EnablePre = v }
		if v, ok := pipelineConfig["enable_hybrid"].(bool); ok { pc.EnableHybrid = v }
		if v, ok := pipelineConfig["enable_post"].(bool); ok { pc.EnablePost = v }
		if v, ok := pipelineConfig["enable_crag"].(bool); ok { pc.EnableCRAG = v }
		if v, ok := pipelineConfig["rrf_k"].(float64); ok { pc.RRFK = int(v) }

		// pre
		if pre, ok := pipelineConfig["pre"].(map[string]any); ok {
			pc.Pre = &config.PreConfig{}
			if cls, ok := pre["classifier"].(map[string]any); ok {
				if b, ok := cls["enable_rules"].(bool); ok { pc.Pre.Classifier.EnableRules = b }
				if b, ok := cls["enable_model"].(bool); ok { pc.Pre.Classifier.EnableModel = b }
			}
			if rw, ok := pre["rewrite"].(map[string]any); ok {
				if b, ok := rw["enable"].(bool); ok { pc.Pre.Rewrite.Enable = b }
				if arr, ok := rw["variants"].([]any); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok { pc.Pre.Rewrite.Variants = append(pc.Pre.Rewrite.Variants, s) }
					}
				}
			}
			if de, ok := pre["decompose"].(map[string]any); ok {
				if b, ok := de["enable"].(bool); ok { pc.Pre.Decompose.Enable = b }
			}
		}

		// retrievers
		if rets, ok := pipelineConfig["retrievers"].([]any); ok {
			for _, it := range rets {
				if m, ok := it.(map[string]any); ok {
					rc := config.RetrieverConfig{}
					if s, ok := m["type"].(string); ok { rc.Type = s }
					if s, ok := m["provider"].(string); ok { rc.Provider = s }
					if p, ok := m["params"].(map[string]any); ok {
						rc.Params = map[string]string{}
						for k, v := range p {
							if sv, ok := v.(string); ok { rc.Params[k] = sv }
						}
					}
					pc.Retrievers = append(pc.Retrievers, rc)
				}
			}
		}

		// post
		if post, ok := pipelineConfig["post"].(map[string]any); ok {
			pc.Post = &config.PostConfig{}
			if rr, ok := post["rerank"].(map[string]any); ok {
				if b, ok := rr["enable"].(bool); ok { pc.Post.Rerank.Enable = b }
				if s, ok := rr["provider"].(string); ok { pc.Post.Rerank.Provider = s }
				if s, ok := rr["endpoint"].(string); ok { pc.Post.Rerank.Endpoint = s }
				if v, ok := rr["top_n"].(float64); ok { pc.Post.Rerank.TopN = int(v) }
			}
			if cmp, ok := post["compress"].(map[string]any); ok {
				if b, ok := cmp["enable"].(bool); ok { pc.Post.Compress.Enable = b }
				if s, ok := cmp["method"].(string); ok { pc.Post.Compress.Method = s }
				if f, ok := cmp["target_ratio"].(float64); ok { pc.Post.Compress.TargetRatio = f }
			}
		}

		// crag
		if crag, ok := pipelineConfig["crag"].(map[string]any); ok {
			pc.CRAG = &config.CRAGConfig{}
			if ev, ok := crag["evaluator"].(map[string]any); ok {
				if s, ok := ev["provider"].(string); ok { pc.CRAG.Evaluator.Provider = s }
				if s, ok := ev["endpoint"].(string); ok { pc.CRAG.Evaluator.Endpoint = s }
				if f, ok := ev["correct"].(float64); ok { pc.CRAG.Evaluator.Correct = f }
				if f, ok := ev["incorrect"].(float64); ok { pc.CRAG.Evaluator.Incorrect = f }
			}
			if b, ok := crag["strict"].(bool); ok { pc.CRAG.Strict = b }
			if s, ok := crag["fail_mode"].(string); ok { pc.CRAG.FailMode = s }
			if v, ok := crag["max_iters"].(float64); ok { pc.CRAG.MaxIters = int(v) }
		}

		// session
		if sess, ok := pipelineConfig["session"].(map[string]any); ok {
			pc.Session = &config.SessionConfig{}
			if s, ok := sess["store"].(string); ok { pc.Session.Store = s }
			if v, ok := sess["ttl_seconds"].(float64); ok { pc.Session.TTLSeconds = int(v) }
			if r, ok := sess["redis"].(map[string]any); ok {
				pc.Session.Redis = map[string]interface{}{}
				for k, v := range r { pc.Session.Redis[k] = v }
			}
		}

		// http defaults
		if httpCfg, ok := pipelineConfig["http"].(map[string]any); ok {
			pc.HTTP = &config.HTTPClientConfig{}
			if v, ok := httpCfg["timeout_ms"].(float64); ok { pc.HTTP.TimeoutMs = int(v) }
			if v, ok := httpCfg["retry"].(float64); ok { pc.HTTP.Retry = int(v) }
			if v, ok := httpCfg["backoff_min_ms"].(float64); ok { pc.HTTP.BackoffMinMs = int(v) }
			if v, ok := httpCfg["backoff_max_ms"].(float64); ok { pc.HTTP.BackoffMaxMs = int(v) }
			if v, ok := httpCfg["max_consecutive_failures"].(float64); ok { pc.HTTP.MaxConsecutiveFailures = int(v) }
			if v, ok := httpCfg["circuit_open_seconds"].(float64); ok { pc.HTTP.CircuitOpenSeconds = int(v) }
			if arr, ok := httpCfg["host_allowlist"].([]any); ok {
				for _, a := range arr { if s, ok := a.(string); ok { pc.HTTP.HostAllowlist = append(pc.HTTP.HostAllowlist, s) } }
			}
		}

		c.config.Pipeline = pc
	}
	return nil
}

func (c *RAGConfig) NewServer(serverName string) (*common.MCPServer, error) {
	mcpServer := common.NewMCPServer(
		serverName,
		Version,
		common.WithInstructions("This is a RAG (Retrieval-Augmented Generation) server for knowledge management and intelligent Q&A"),
	)

	// Initialize RAG client with configuration
	ragClient, err := NewRAGClient(c.config)
	if err != nil {
		return nil, fmt.Errorf("create rag client failed, err: %w", err)
	}

	// Knowledge Base Management Tools
	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("create-chunks-from-text", "Process and segment input text into semantic chunks for knowledge base ingestion", GetCreateChunkFromTextSchema()),
		HandleCreateChunkFromText(ragClient),
	)

	// Chunk Management Tools
	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("list-chunks", "Retrieve and display all knowledge chunks in the database", GetListChunksSchema()),
		HandleListChunks(ragClient),
	)
	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("delete-chunk", "Remove a specific knowledge chunk from the database using its unique identifier", GetDeleteChunkSchema()),
		HandleDeleteChunk(ragClient),
	)

	// Semantic Search Tool
	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("search-chunks", "Perform semantic search across knowledge chunks using natural language query", GetSearchSchema()),
		HandleSearch(ragClient),
	)

	// Intelligent Q&A Tool
	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("chat", "Answer user questions by retrieving relevant knowledge from the database and generating responses using RAG-enhanced LLM", GetChatSchema()),
		HandleChat(ragClient),
	)

	return mcpServer, nil
}
