# CRAG (Corrective Retrieval-Augmented Generation)

This package implements Corrective Retrieval-Augmented Generation (CRAG) for the RAG system, which intelligently evaluates retrieved documents and takes corrective actions based on relevance.

## Components

### 1. Evaluators

#### LLMEvaluator (`evaluator_llm.go`)
Uses an LLM to evaluate document relevance on a scale of 0-1.

**Features:**
- Evaluates (query, context) relevance using LLM prompts
- Configurable thresholds for Correct/Incorrect/Ambiguous verdicts
- Robust score parsing with fallback to default values

**Configuration:**
```go
evaluator := &crag.LLMEvaluator{
    Provider:    llmProvider,     // LLM provider instance
    CorrectTh:   0.7,             // Threshold for "correct" verdict (default: 0.7)
    IncorrectTh: 0.3,             // Threshold for "incorrect" verdict (default: 0.3)
}
```

**Verdict Logic:**
- `score >= CorrectTh` → VerdictCorrect (high relevance)
- `score < IncorrectTh` → VerdictIncorrect (low relevance)  
- Otherwise → VerdictAmbiguous (medium relevance)

#### HTTPEvaluator (`evaluator_http.go`)
Calls an external HTTP service for evaluation.

**Request Format:**
```json
{
    "query": "user query",
    "context": "retrieved documents"
}
```

**Response Format:**
```json
{
    "score": 0.85,
    "verdict": "correct"
}
```

### 2. Web Search (`web_search.go`)

Performs web searches to retrieve external knowledge when internal documents are insufficient.

**Supported Providers:**
- DuckDuckGo (default, no API key required)
- Bing Web Search API (requires API key)

**Usage:**
```go
searcher := &crag.WebSearcher{
    Provider: "duckduckgo",  // or "bing"
    Endpoint: "",            // optional custom endpoint
    APIKey:   "",            // required for Bing
}

results, err := searcher.Search(ctx, "query", 3)
```

### 3. Query Processing (`utils.go`)

#### QueryRewriter
Rewrites queries to be more suitable for web search engines.

```go
rewriter := &crag.QueryRewriter{
    Provider: llmProvider,
}

rewritten, err := rewriter.Rewrite(ctx, "original query")
```

#### KnowledgeRefiner
Extracts and refines key information from text into bullet points.

```go
refiner := &crag.KnowledgeRefiner{
    Provider: llmProvider,
}

refined, err := refiner.Refine(ctx, rawText)
```

### 4. Corrective Actions (`action.go`)

Based on the evaluation verdict, CRAG takes different actions:

#### CorrectAction (High Relevance, score >= 0.7)
- Documents are highly relevant
- Use documents directly
- Optionally refine content for better quality

#### IncorrectAction (Low Relevance, score < 0.3)
- Documents are not relevant
- Perform web search for external knowledge
- Rewrite query for better search results
- Refine web search results

#### AmbiguousAction (Medium Relevance, 0.3 <= score < 0.7)
- Documents are partially relevant
- Combine internal documents with web search results
- Refine both internal and external knowledge
- Return merged results

**ActionContext:**
```go
actionCtx := &crag.ActionContext{
    Query:         query,
    Context:       ctx,
    WebSearcher:   webSearcher,     // optional
    QueryRewriter: queryRewriter,   // optional
    Refiner:       refiner,          // optional
}

// Use in actions
results := crag.CorrectAction(actionCtx, candidates)
results := crag.IncorrectAction(actionCtx)
results := crag.AmbiguousAction(actionCtx, internal, external)
```

## Integration

### In RAG Client (`rag_client.go`)

```go
// Initialize CRAG components
if ragclient.config.Pipeline.CRAG != nil {
    cragCfg := ragclient.config.Pipeline.CRAG
    
    // Initialize evaluator
    if cragCfg.Evaluator.Provider == "llm" && ragclient.llmProvider != nil {
        ev = &crag.LLMEvaluator{
            Provider:    ragclient.llmProvider,
            CorrectTh:   cragCfg.Evaluator.Correct,
            IncorrectTh: cragCfg.Evaluator.Incorrect,
        }
    }
    
    // Initialize web searcher
    webSearcher = &crag.WebSearcher{
        Provider: "duckduckgo",
        Endpoint: "",
        APIKey:   "",
    }
    
    // Initialize query rewriter and refiner
    if ragclient.llmProvider != nil {
        queryRewriter = &crag.QueryRewriter{Provider: ragclient.llmProvider}
        refiner = &crag.KnowledgeRefiner{Provider: ragclient.llmProvider}
    }
}
```

### In Orchestrator (`orchestrator/orchestrator.go`)

```go
// CRAG evaluation and correction
if pc.EnableCRAG && o.Evaluator != nil {
    // Evaluate relevance
    score, verdict, err := o.Evaluator.Evaluate(ctx, query, concatenatedDocs)
    
    // Build action context
    actionCtx := &crag.ActionContext{
        Query:         query,
        Context:       ctx,
        WebSearcher:   o.WebSearcher,
        QueryRewriter: o.QueryRewriter,
        Refiner:       o.Refiner,
    }
    
    // Execute corrective action
    switch verdict {
    case crag.VerdictCorrect:
        fused = crag.CorrectAction(actionCtx, fused)
    case crag.VerdictIncorrect:
        fused = crag.IncorrectAction(actionCtx)
    case crag.VerdictAmbiguous:
        fused = crag.AmbiguousAction(actionCtx, fused, nil)
    }
}
```

## Configuration Example

```yaml
pipeline:
  enable_crag: true
  crag:
    evaluator:
      provider: llm        # "llm" or "http"
      correct: 0.7         # threshold for high relevance
      incorrect: 0.3       # threshold for low relevance
    fail_mode: open        # "open" (keep results) or "closed" (return error)
    strict: false          # require evaluator or allow fallback
  
  retrievers:
    - type: web
      provider: duckduckgo
      params:
        endpoint: ""      # optional
        api_key: ""       # required for Bing

llm:
  provider: openai
  api_key: your-api-key
  model: gpt-4o
  temperature: 0.3
```

## Workflow

```
1. Initial Retrieval
   ↓
2. CRAG Evaluation (Evaluator)
   ↓
3. Decision based on score:
   
   ├─ High (>= 0.7): CorrectAction
   │  └─ Use documents + optional refinement
   │
   ├─ Low (< 0.3): IncorrectAction
   │  └─ Query rewrite → Web search → Refinement
   │
   └─ Medium (0.3-0.7): AmbiguousAction
      └─ Combine documents + web search → Refinement
   
4. Return corrected results
```

## Benefits

1. **Self-Correcting**: Automatically detects and corrects poor retrieval results
2. **External Knowledge**: Falls back to web search when internal knowledge is insufficient
3. **Quality Enhancement**: Refines knowledge for better LLM consumption
4. **Flexible**: Works with different evaluators and search providers
5. **Configurable**: Adjustable thresholds and components

## References

- Original CRAG paper: Corrective Retrieval Augmented Generation
- Python reference implementation provided by user

