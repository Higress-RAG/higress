# Reranking Module

This module provides multiple reranking strategies to improve search result quality in RAG systems.

## Overview

Reranking is a critical post-processing step that reorders initial search results based on relevance to the query. This module implements four different reranking strategies:

1. **HTTP Reranker** - Delegates to an external HTTP service
2. **LLM Reranker** - Uses LLM to score document relevance
3. **Keyword Reranker** - Simple keyword matching and positioning
4. **Model Reranker** - Uses dedicated reranking models (e.g., BGE-reranker, Cohere rerank)

## Reranking Strategies

### 1. HTTP Reranker

Calls an external HTTP service for reranking. Useful for integrating with existing reranking services.

**Configuration:**
```yaml
pipeline:
  enable_post: true
  post:
    rerank:
      enable: true
      provider: http
      endpoint: "http://localhost:8081/rerank"
      top_n: 5
```

**Expected Request:**
```json
{
  "query": "user query",
  "candidates": [
    {"id": "doc1", "text": "document content"},
    {"id": "doc2", "text": "another document"}
  ],
  "top_n": 5
}
```

**Expected Response:**
```json
{
  "ranking": [
    {"id": "doc2", "score": 0.95},
    {"id": "doc1", "score": 0.82}
  ]
}
```

### 2. LLM Reranker

Uses an LLM to evaluate each document's relevance on a scale of 0-10.

**Features:**
- Scores each document individually using LLM
- Detailed relevance guidelines in prompt
- Progress logging every 5 documents
- Fallback to original scores on errors

**Configuration:**
```yaml
pipeline:
  enable_post: true
  post:
    rerank:
      enable: true
      provider: llm
      model: gpt-3.5-turbo  # optional, uses default LLM config
      top_n: 5

llm:
  provider: openai
  api_key: your-api-key
  model: gpt-3.5-turbo
  temperature: 0
```

**Scoring Guidelines:**
- 0-2: Completely irrelevant
- 3-5: Some relevant information but doesn't directly answer
- 6-8: Relevant and partially answers
- 9-10: Highly relevant and directly answers

**Example Usage:**
```go
reranker := &post.LLMReranker{
    Provider: llmProvider,
    Model:    "gpt-3.5-turbo",
}

reranked, err := reranker.Rerank(ctx, query, candidates, 5)
```

### 3. Keyword Reranker

Fast, lightweight reranking based on keyword matching and positioning.

**Features:**
- Extracts keywords from query (words > 3 characters)
- Base score from original similarity
- Bonus for keyword matches
- Position bonus for keywords in first quarter
- Frequency bonus for multiple occurrences

**Configuration:**
```yaml
pipeline:
  enable_post: true
  post:
    rerank:
      enable: true
      provider: keyword
      top_n: 5
```

**Scoring Formula:**
```
final_score = (original_score * 0.5) + keyword_score

keyword_score = 
  + 0.1 per keyword match
  + 0.1 if keyword in first quarter
  + min(0.05 * frequency, 0.2) for repetitions
```

**Example Usage:**
```go
reranker := &post.KeywordReranker{
    MinKeywordLength: 3,    // Default
    BaseScoreWeight:  0.5,  // Default
}

reranked, err := reranker.Rerank(ctx, query, candidates, 5)
```

**Best For:**
- Fast reranking without external calls
- Keyword-heavy queries
- When LLM calls are expensive
- Real-time applications

### 4. Model Reranker

Uses dedicated reranking models like BGE-reranker, Cohere rerank, or custom cross-encoders.

**Configuration:**
```yaml
pipeline:
  enable_post: true
  post:
    rerank:
      enable: true
      provider: model
      endpoint: "https://api.example.com/rerank"
      model: "bge-reranker-large"
      api_key: your-api-key  # optional
      top_n: 5
```

**Request Format:**
```json
{
  "query": "user query",
  "documents": [
    "document 1 content",
    "document 2 content"
  ],
  "model": "bge-reranker-large",
  "top_n": 5
}
```

**Response Format:**
```json
{
  "results": [
    {
      "index": 1,
      "relevance_score": 0.95,
      "document": "document 2 content"
    },
    {
      "index": 0,
      "relevance_score": 0.82,
      "document": "document 1 content"
    }
  ]
}
```

**Supported Models:**
- BGE-reranker (BAAI)
- Cohere rerank
- Custom cross-encoder models

**Example Usage:**
```go
reranker := &post.ModelReranker{
    Endpoint: "https://api.jina.ai/v1/rerank",
    Model:    "bge-reranker-large",
    APIKey:   "your-api-key",
}

reranked, err := reranker.Rerank(ctx, query, candidates, 5)
```

## Performance Comparison

| Strategy | Speed | Accuracy | Cost | Use Case |
|----------|-------|----------|------|----------|
| Keyword | ⚡⚡⚡ Fast | Medium | Free | Real-time, keyword queries |
| HTTP | ⚡⚡ Medium | High | Medium | Existing services |
| Model | ⚡⚡ Medium | Very High | Low-Medium | Production, best accuracy |
| LLM | ⚡ Slow | High | High | Complex queries, when accuracy is critical |

## Integration

### In rag_client.go

The reranker is automatically initialized based on configuration:

```go
var rr post.Reranker
if ragclient.config.Pipeline.Post != nil && ragclient.config.Pipeline.Post.Rerank.Enable {
    rerankCfg := ragclient.config.Pipeline.Post.Rerank
    switch rerankCfg.Provider {
    case "llm":
        rr = &post.LLMReranker{
            Provider: ragclient.llmProvider,
            Model:    rerankCfg.Model,
        }
    case "keyword":
        rr = &post.KeywordReranker{
            MinKeywordLength: 3,
            BaseScoreWeight:  0.5,
        }
    case "model":
        rr = &post.ModelReranker{
            Endpoint: rerankCfg.Endpoint,
            Model:    rerankCfg.Model,
            APIKey:   rerankCfg.APIKey,
        }
    default:
        rr = post.NewHTTPReranker(rerankCfg.Endpoint)
    }
}
```

### In orchestrator.go

Reranking happens after fusion and before CRAG:

```go
// Post-processing
if pc.EnablePost && o.Reranker != nil && o.Cfg.Pipeline.Post != nil && o.Cfg.Pipeline.Post.Rerank.Enable {
    topN := o.Cfg.Pipeline.Post.Rerank.TopN
    rr, _ := o.Reranker.Rerank(ctx, query, fused, topN)
    fused = rr
}
```

## Best Practices

### 1. Choose the Right Strategy

- **Keyword Reranker**: Use for real-time applications with keyword-heavy queries
- **Model Reranker**: Best for production with good accuracy/cost balance
- **LLM Reranker**: Use when accuracy is paramount and cost is not a concern
- **HTTP Reranker**: Use when you have an existing reranking service

### 2. Optimal top_n Values

```yaml
# Initial retrieval: get more documents
rag:
  top_k: 20

# Reranking: narrow down to most relevant
post:
  rerank:
    top_n: 5  # Typically 3-10 depending on use case
```

### 3. Error Handling

All rerankers gracefully fallback to original scores on errors:
- Network failures
- Parsing errors
- LLM errors

### 4. Performance Tuning

**For LLM Reranker:**
```yaml
llm:
  temperature: 0      # Deterministic scoring
  model: gpt-3.5-turbo  # Faster and cheaper than gpt-4
```

**For Keyword Reranker:**
```go
reranker := &KeywordReranker{
    MinKeywordLength: 4,    // Increase for more selective matching
    BaseScoreWeight:  0.3,  // Lower if you trust keyword matching more
}
```

## Examples

### Complete Configuration

```yaml
# Full pipeline with LLM reranking
pipeline:
  enable_hybrid: true
  enable_post: true
  enable_crag: true
  
  post:
    rerank:
      enable: true
      provider: llm
      top_n: 5
    
    compress:
      enable: true
      target_ratio: 0.7

llm:
  provider: openai
  api_key: your-api-key
  model: gpt-3.5-turbo
  temperature: 0
```

### Mixed Strategy

You can combine strategies by using different rerankers in a pipeline:

```go
// First pass: Keyword reranking (fast)
keywordReranker := &post.KeywordReranker{}
intermediate, _ := keywordReranker.Rerank(ctx, query, candidates, 10)

// Second pass: Model reranking (accurate)
modelReranker := &post.ModelReranker{
    Endpoint: "...",
    Model:    "bge-reranker-large",
}
final, _ := modelReranker.Rerank(ctx, query, intermediate, 5)
```

## Testing

Run tests with:
```bash
cd plugins/golang-filter/mcp-server/servers/rag/post
go test -v -run TestRerank
```

## References

- [CRAG: Corrective Retrieval Augmented Generation](https://arxiv.org/abs/2401.15884)
- [BGE Reranker](https://github.com/FlagOpen/FlagEmbedding)
- [Cohere Rerank API](https://docs.cohere.com/docs/reranking)

## Contributing

When adding new reranking strategies:

1. Implement the `Reranker` interface
2. Add configuration support in `config/pipeline.go`
3. Update `rag_client.go` initialization logic
4. Add tests in `rerank_advanced_test.go`
5. Update this documentation

