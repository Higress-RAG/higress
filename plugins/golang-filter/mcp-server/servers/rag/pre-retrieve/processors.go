package pre_retrieve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/embedding"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/llm"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-session/common"
)

// =============================================================================
// Memory Intake Processor - 记忆采集
// =============================================================================

// MemoryIntakeProcessor 记忆采集处理器接口
type MemoryIntakeProcessor interface {
	Process(ctx context.Context, rawQuery string, sessionID string) (*QueryContext, error)
}

// SessionStore 会话存储接口
type SessionStore interface {
	GetLastNRounds(ctx context.Context, sessionID string, n int) ([]ConversationRound, error)
	GetDocIDs(ctx context.Context, sessionID string) ([]string, error)
	SaveRound(ctx context.Context, sessionID string, round ConversationRound) error
}

// ExternalMemoryStore 外部记忆存储接口
type ExternalMemoryStore interface {
	GetRelevantMemories(ctx context.Context, query string) ([]string, error)
}

// DefaultMemoryIntakeProcessor 默认记忆采集处理器
type DefaultMemoryIntakeProcessor struct {
	config        *config.MemoryConfig
	sessionStore  SessionStore
	externalStore ExternalMemoryStore
}

func NewMemoryIntakeProcessor(cfg *config.MemoryConfig, sessionStore SessionStore, externalStore ExternalMemoryStore) MemoryIntakeProcessor {
	return &DefaultMemoryIntakeProcessor{
		config:        cfg,
		sessionStore:  sessionStore,
		externalStore: externalStore,
	}
}

func (p *DefaultMemoryIntakeProcessor) Process(ctx context.Context, rawQuery string, sessionID string) (*QueryContext, error) {
	queryCtx := &QueryContext{
		Query:     rawQuery,
		SessionID: sessionID,
	}

	if !p.config.Enabled {
		return queryCtx, nil
	}

	if p.config.LastNRounds > 0 && p.sessionStore != nil {
		rounds, err := p.sessionStore.GetLastNRounds(ctx, sessionID, p.config.LastNRounds)
		if err == nil {
			queryCtx.LastNRounds = rounds
		}
	}

	if p.config.EnableDocIDs && p.sessionStore != nil {
		docIDs, err := p.sessionStore.GetDocIDs(ctx, sessionID)
		if err == nil {
			queryCtx.DocIDs = docIDs
		}
	}

	return queryCtx, nil
}

// RedisSessionStore Redis 会话存储实现
type RedisSessionStore struct {
	redisClient      *common.RedisClient
	keyPrefix        string
	sessionExpiry    time.Duration
	maxHistoryRounds int
}

func NewRedisSessionStore(redisClient *common.RedisClient, keyPrefix string, sessionExpiry time.Duration, maxHistoryRounds int) SessionStore {
	if keyPrefix == "" {
		keyPrefix = "pre-retrieve:session:"
	}
	if sessionExpiry == 0 {
		sessionExpiry = 24 * time.Hour
	}
	if maxHistoryRounds == 0 {
		maxHistoryRounds = 10
	}
	return &RedisSessionStore{
		redisClient:      redisClient,
		keyPrefix:        keyPrefix,
		sessionExpiry:    sessionExpiry,
		maxHistoryRounds: maxHistoryRounds,
	}
}

func (s *RedisSessionStore) GetLastNRounds(ctx context.Context, sessionID string, n int) ([]ConversationRound, error) {
	key := s.keyPrefix + sessionID
	value, err := s.redisClient.Get(key)
	if err != nil {
		return []ConversationRound{}, nil
	}

	var rounds []ConversationRound
	if err := json.Unmarshal([]byte(value), &rounds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session data: %w", err)
	}

	if len(rounds) <= n {
		return rounds, nil
	}
	return rounds[len(rounds)-n:], nil
}

func (s *RedisSessionStore) GetDocIDs(ctx context.Context, sessionID string) ([]string, error) {
	key := s.keyPrefix + sessionID + ":docs"
	value, err := s.redisClient.Get(key)
	if err != nil {
		return []string{}, nil
	}

	var docIDs []string
	if err := json.Unmarshal([]byte(value), &docIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal doc IDs: %w", err)
	}
	return docIDs, nil
}

func (s *RedisSessionStore) SaveRound(ctx context.Context, sessionID string, round ConversationRound) error {
	key := s.keyPrefix + sessionID
	rounds, _ := s.GetLastNRounds(ctx, sessionID, s.maxHistoryRounds)
	rounds = append(rounds, round)
	if len(rounds) > s.maxHistoryRounds {
		rounds = rounds[len(rounds)-s.maxHistoryRounds:]
	}

	data, err := json.Marshal(rounds)
	if err != nil {
		return fmt.Errorf("failed to marshal rounds: %w", err)
	}
	return s.redisClient.Set(key, string(data), s.sessionExpiry)
}

// InMemorySessionStore 内存会话存储（测试用）
type InMemorySessionStore struct {
	sessions  map[string][]ConversationRound
	docIDs    map[string][]string
	maxRounds int
}

func NewInMemorySessionStore(maxRounds int) SessionStore {
	if maxRounds == 0 {
		maxRounds = 10
	}
	return &InMemorySessionStore{
		sessions:  make(map[string][]ConversationRound),
		docIDs:    make(map[string][]string),
		maxRounds: maxRounds,
	}
}

func (s *InMemorySessionStore) GetLastNRounds(ctx context.Context, sessionID string, n int) ([]ConversationRound, error) {
	rounds := s.sessions[sessionID]
	if len(rounds) <= n {
		return rounds, nil
	}
	return rounds[len(rounds)-n:], nil
}

func (s *InMemorySessionStore) GetDocIDs(ctx context.Context, sessionID string) ([]string, error) {
	return s.docIDs[sessionID], nil
}

func (s *InMemorySessionStore) SaveRound(ctx context.Context, sessionID string, round ConversationRound) error {
	rounds := s.sessions[sessionID]
	rounds = append(rounds, round)
	if len(rounds) > s.maxRounds {
		rounds = rounds[len(rounds)-s.maxRounds:]
	}
	s.sessions[sessionID] = rounds
	return nil
}

// =============================================================================
// Context Alignment Processor - 上下文对齐
// =============================================================================

// ContextAlignmentProcessor 上下文对齐处理器接口
type ContextAlignmentProcessor interface {
	Process(ctx context.Context, queryCtx *QueryContext) (*AlignedQuery, error)
}

// AnchorCandidateRetriever 锚点候选检索器接口
type AnchorCandidateRetriever interface {
	RetrieveCandidates(ctx context.Context, queryCtx *QueryContext) ([]Anchor, error)
}

// DefaultContextAlignmentProcessor 默认上下文对齐处理器
type DefaultContextAlignmentProcessor struct {
	config                   *config.ContextAlignmentConfig
	llmProvider              llm.Provider
	anchorCandidateRetriever AnchorCandidateRetriever
}

func NewContextAlignmentProcessor(cfg *config.ContextAlignmentConfig, llmProvider llm.Provider, anchorRetriever AnchorCandidateRetriever) ContextAlignmentProcessor {
	return &DefaultContextAlignmentProcessor{
		config:                   cfg,
		llmProvider:              llmProvider,
		anchorCandidateRetriever: anchorRetriever,
	}
}

func (p *DefaultContextAlignmentProcessor) Process(ctx context.Context, queryCtx *QueryContext) (*AlignedQuery, error) {
	if !p.config.Enabled {
		return &AlignedQuery{Query: queryCtx.Query}, nil
	}

	alignedQuery := &AlignedQuery{Query: queryCtx.Query, AlignmentOps: []string{}}

	// 多轮整合：代词消解和时间归一化
	integratedQuery, ops, err := p.integrateContext(ctx, queryCtx)
	if err != nil {
		return nil, fmt.Errorf("integrate context failed: %w", err)
	}
	alignedQuery.Query = integratedQuery
	alignedQuery.AlignmentOps = append(alignedQuery.AlignmentOps, ops...)

	// 锚点候选和裁决
	if p.config.EnableAnchor && p.anchorCandidateRetriever != nil {
		anchors, err := p.retrieveAndDecideAnchors(ctx, queryCtx, integratedQuery)
		if err == nil {
			alignedQuery.Anchors = anchors
		}
	}

	return alignedQuery, nil
}

func (p *DefaultContextAlignmentProcessor) integrateContext(ctx context.Context, queryCtx *QueryContext) (string, []string, error) {
	ops := []string{}
	query := queryCtx.Query

	if len(queryCtx.LastNRounds) == 0 {
		return query, ops, nil
	}

	if p.config.EnablePronouns && p.llmProvider != nil {
		resolvedQuery, err := p.resolvePronounsWithLLM(ctx, queryCtx)
		if err == nil && resolvedQuery != query {
			query = resolvedQuery
			ops = append(ops, "pronoun_resolution")
		}
	}

	if p.config.EnableTimeNorm && p.llmProvider != nil {
		normalizedQuery, err := p.normalizeTimeWithLLM(ctx, query)
		if err == nil && normalizedQuery != query {
			query = normalizedQuery
			ops = append(ops, "time_normalization")
		}
	}

	return query, ops, nil
}

func (p *DefaultContextAlignmentProcessor) resolvePronounsWithLLM(ctx context.Context, queryCtx *QueryContext) (string, error) {
	history := strings.Builder{}
	for i, round := range queryCtx.LastNRounds {
		history.WriteString(fmt.Sprintf("Q%d: %s\nA%d: %s\n", i+1, round.Question, i+1, round.Answer))
	}

	prompt := fmt.Sprintf(`Based on the conversation history, resolve any pronouns or ambiguous references in the current query to make it self-contained.

Conversation History:
%s

Current Query: %s

Please rewrite the query to be self-contained without pronouns or unclear references. Only output the rewritten query, no explanations.

Rewritten Query:`, history.String(), queryCtx.Query)

	resolved, err := p.llmProvider.GenerateCompletion(ctx, prompt)
	if err != nil {
		return queryCtx.Query, err
	}
	return strings.TrimSpace(resolved), nil
}

func (p *DefaultContextAlignmentProcessor) normalizeTimeWithLLM(ctx context.Context, query string) (string, error) {
	prompt := fmt.Sprintf(`Normalize any relative time expressions in the query to absolute or standardized forms.

Query: %s

If there are relative time expressions (like "yesterday", "last week", "recently"), convert them to more specific or absolute forms. If there are no time expressions, return the original query unchanged.

Only output the normalized query, no explanations.

Normalized Query:`, query)

	normalized, err := p.llmProvider.GenerateCompletion(ctx, prompt)
	if err != nil {
		return query, err
	}
	return strings.TrimSpace(normalized), nil
}

func (p *DefaultContextAlignmentProcessor) retrieveAndDecideAnchors(ctx context.Context, queryCtx *QueryContext, alignedQuery string) ([]Anchor, error) {
	candidates, err := p.anchorCandidateRetriever.RetrieveCandidates(ctx, queryCtx)
	if err != nil {
		return []Anchor{}, err
	}

	filtered := []Anchor{}
	for _, anchor := range candidates {
		if anchor.Score >= p.config.AnchorScoreThreshold {
			filtered = append(filtered, anchor)
		}
	}

	maxAnchors := p.config.MaxAnchors
	if maxAnchors <= 0 {
		maxAnchors = 2
	}
	if len(filtered) > maxAnchors {
		filtered = filtered[:maxAnchors]
	}

	return filtered, nil
}

// DefaultAnchorCandidateRetriever 默认锚点候选检索器
type DefaultAnchorCandidateRetriever struct{}

func NewDefaultAnchorCandidateRetriever() AnchorCandidateRetriever {
	return &DefaultAnchorCandidateRetriever{}
}

func (r *DefaultAnchorCandidateRetriever) RetrieveCandidates(ctx context.Context, queryCtx *QueryContext) ([]Anchor, error) {
	anchors := []Anchor{}
	for _, docID := range queryCtx.DocIDs {
		anchors = append(anchors, Anchor{
			ID:       docID,
			Score:    0.8,
			Type:     "document",
			Content:  docID,
			MustKeep: []string{},
		})
	}
	return anchors, nil
}

// =============================================================================
// PreQRAG Planner - 统一规划器
// =============================================================================

// PreQRAGPlanner PreQRAG 规划器接口
type PreQRAGPlanner interface {
	Plan(ctx context.Context, alignedQuery *AlignedQuery) (*PreQRAGPlan, error)
}

// DefaultPreQRAGPlanner 默认 PreQRAG 规划器
type DefaultPreQRAGPlanner struct {
	config      *config.PreQRAGPlanningConfig
	llmProvider llm.Provider
}

func NewPreQRAGPlanner(cfg *config.PreQRAGPlanningConfig, llmProvider llm.Provider) PreQRAGPlanner {
	return &DefaultPreQRAGPlanner{
		config:      cfg,
		llmProvider: llmProvider,
	}
}

func (p *DefaultPreQRAGPlanner) Plan(ctx context.Context, alignedQuery *AlignedQuery) (*PreQRAGPlan, error) {
	if !p.config.Enabled {
		return p.createSimplePlan(alignedQuery), nil
	}

	plan := &PreQRAGPlan{
		Nodes:            []QueryNode{},
		JoinStrategy:     "union",
		CardinalityPrior: CardinalityUnknown,
	}

	// 1. 规范化
	normalizedQuery := alignedQuery.Query
	normalizations := []string{}
	if p.config.EnableNormalization && p.llmProvider != nil {
		normalized, norms, err := p.normalize(ctx, alignedQuery)
		if err == nil {
			normalizedQuery = normalized
			normalizations = norms
		}
	}

	// 2. 单/多文档先验判定
	if p.config.EnableCardinalityPrior && p.llmProvider != nil {
		cardinality, err := p.determineCardinality(ctx, normalizedQuery, alignedQuery)
		if err == nil {
			plan.CardinalityPrior = cardinality
		}
	}

	// 3. 子问题分解
	var subQueries []string
	if p.config.EnableDecomposition && plan.CardinalityPrior == CardinalityMulti && p.llmProvider != nil {
		decomposed, err := p.decomposeQuery(ctx, normalizedQuery, alignedQuery)
		if err == nil && len(decomposed) > 0 {
			subQueries = decomposed
			if p.config.MaxSubQueries > 0 && len(subQueries) > p.config.MaxSubQueries {
				subQueries = subQueries[:p.config.MaxSubQueries]
			}
		}
	}

	if len(subQueries) == 0 {
		subQueries = []string{normalizedQuery}
		plan.CardinalityPrior = CardinalitySingle
	}

	// 4. 通道感知重写
	for i, subQuery := range subQueries {
		nodeID := fmt.Sprintf("node_%d", i)
		node := QueryNode{
			ID:             nodeID,
			Query:          subQuery,
			SparseRewrite:  subQuery,
			DenseRewrite:   subQuery,
			Normalizations: normalizations,
		}

		if p.config.EnableChannelRewrite && p.llmProvider != nil {
			sparse, dense, err := p.channelRewrite(ctx, subQuery, alignedQuery)
			if err == nil {
				node.SparseRewrite = sparse
				node.DenseRewrite = dense
			}
		}

		plan.Nodes = append(plan.Nodes, node)
	}

	// 5. 构建 DAG 边
	if len(plan.Nodes) > 1 {
		for i := 0; i < len(plan.Nodes)-1; i++ {
			plan.Edges = append(plan.Edges, PlanEdge{
				From: plan.Nodes[i].ID,
				To:   plan.Nodes[i+1].ID,
				Type: "parallel",
			})
		}
	}

	return plan, nil
}

func (p *DefaultPreQRAGPlanner) createSimplePlan(alignedQuery *AlignedQuery) *PreQRAGPlan {
	return &PreQRAGPlan{
		Nodes: []QueryNode{{
			ID:            "node_0",
			Query:         alignedQuery.Query,
			SparseRewrite: alignedQuery.Query,
			DenseRewrite:  alignedQuery.Query,
		}},
		JoinStrategy:     "union",
		CardinalityPrior: CardinalitySingle,
	}
}

func (p *DefaultPreQRAGPlanner) normalize(ctx context.Context, alignedQuery *AlignedQuery) (string, []string, error) {
	mustKeepTerms := []string{}
	for _, anchor := range alignedQuery.Anchors {
		mustKeepTerms = append(mustKeepTerms, anchor.MustKeep...)
	}

	mustKeepStr := ""
	if len(mustKeepTerms) > 0 {
		mustKeepStr = fmt.Sprintf("\nIMPORTANT: Must preserve these terms exactly: %s", strings.Join(mustKeepTerms, ", "))
	}

	prompt := fmt.Sprintf(`Normalize the query by:
1. Standardizing terminology and units
2. Normalizing time expressions
3. Correcting negations
4. Fixing typos and grammar
%s

Query: %s

Output only the normalized query, no explanations.

Normalized Query:`, mustKeepStr, alignedQuery.Query)

	normalized, err := p.llmProvider.GenerateCompletion(ctx, prompt)
	if err != nil {
		return alignedQuery.Query, []string{}, err
	}

	return strings.TrimSpace(normalized), []string{"terminology", "time", "negation"}, nil
}

func (p *DefaultPreQRAGPlanner) determineCardinality(ctx context.Context, query string, alignedQuery *AlignedQuery) (CardinalityType, error) {
	prompt := fmt.Sprintf(`Analyze the query and determine if it requires information from a single document or multiple documents.

Query: %s

Consider:
- Does it contain conjunctions like "and", "or", "compare"?
- Does it ask for multiple entities or concepts?
- Is it a comparison question?

Answer with only one word: "single" or "multi"

Answer:`, query)

	response, err := p.llmProvider.GenerateCompletion(ctx, prompt)
	if err != nil {
		return CardinalityUnknown, err
	}

	response = strings.ToLower(strings.TrimSpace(response))
	if strings.Contains(response, "multi") {
		return CardinalityMulti, nil
	}
	if strings.Contains(response, "single") {
		return CardinalitySingle, nil
	}
	return CardinalityUnknown, nil
}

func (p *DefaultPreQRAGPlanner) decomposeQuery(ctx context.Context, query string, alignedQuery *AlignedQuery) ([]string, error) {
	prompt := fmt.Sprintf(`Decompose the complex query into 1-3 independent sub-queries that can be searched separately.

Query: %s

Requirements:
- Each sub-query should be self-contained
- Sub-queries should be independent and can be executed in parallel
- If the query is simple and cannot be decomposed, return only the original query

Output format (one sub-query per line):
1. [first sub-query]
2. [second sub-query]
3. [third sub-query]

Sub-queries:`, query)

	response, err := p.llmProvider.GenerateCompletion(ctx, prompt)
	if err != nil {
		return []string{query}, err
	}

	subQueries := []string{}
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 3 && line[0] >= '0' && line[0] <= '9' && line[1] == '.' {
			line = strings.TrimSpace(line[2:])
		}
		if line != "" {
			subQueries = append(subQueries, line)
		}
	}

	if len(subQueries) == 0 {
		return []string{query}, nil
	}
	return subQueries, nil
}

func (p *DefaultPreQRAGPlanner) channelRewrite(ctx context.Context, query string, alignedQuery *AlignedQuery) (string, string, error) {
	sparsePrompt := fmt.Sprintf(`Rewrite the query for sparse retrieval (BM25/keyword search):
- Use explicit keywords and terms
- Expand abbreviations
- Include synonyms where appropriate
- Make it keyword-rich for lexical matching

Original Query: %s

Sparse Rewrite:`, query)

	sparseRewrite, err := p.llmProvider.GenerateCompletion(ctx, sparsePrompt)
	if err != nil {
		sparseRewrite = query
	} else {
		sparseRewrite = strings.TrimSpace(sparseRewrite)
	}

	densePrompt := fmt.Sprintf(`Rewrite the query for dense retrieval (semantic search):
- Make it semantically clear and concise
- Focus on the core intent
- Remove redundant words
- Optimize for semantic similarity

Original Query: %s

Dense Rewrite:`, query)

	denseRewrite, err := p.llmProvider.GenerateCompletion(ctx, densePrompt)
	if err != nil {
		denseRewrite = query
	} else {
		denseRewrite = strings.TrimSpace(denseRewrite)
	}

	return sparseRewrite, denseRewrite, nil
}

// =============================================================================
// Expansion Processor - 扩写
// =============================================================================

// ExpansionProcessor 扩写处理器接口
type ExpansionProcessor interface {
	Expand(ctx context.Context, plan *PreQRAGPlan, alignedQuery *AlignedQuery) (map[string]QueryExpansion, error)
}

// TaxonomyProvider 分类体系提供者接口
type TaxonomyProvider interface {
	GetRelatedTerms(ctx context.Context, term string) ([]string, error)
	GetSynonyms(ctx context.Context, term string) ([]string, error)
}

// DefaultExpansionProcessor 默认扩写处理器
type DefaultExpansionProcessor struct {
	config           *config.ExpansionConfig
	llmProvider      llm.Provider
	taxonomyProvider TaxonomyProvider
}

func NewExpansionProcessor(cfg *config.ExpansionConfig, llmProvider llm.Provider, taxonomyProvider TaxonomyProvider) ExpansionProcessor {
	return &DefaultExpansionProcessor{
		config:           cfg,
		llmProvider:      llmProvider,
		taxonomyProvider: taxonomyProvider,
	}
}

func (p *DefaultExpansionProcessor) Expand(ctx context.Context, plan *PreQRAGPlan, alignedQuery *AlignedQuery) (map[string]QueryExpansion, error) {
	if !p.config.Enabled {
		return map[string]QueryExpansion{}, nil
	}

	expansions := make(map[string]QueryExpansion)

	for _, node := range plan.Nodes {
		expansion := QueryExpansion{NodeID: node.ID, Terms: []ExpansionTerm{}}

		// 1. 从锚点提取必须保留的词项
		for _, anchor := range alignedQuery.Anchors {
			for _, term := range anchor.MustKeep {
				expansion.Terms = append(expansion.Terms, ExpansionTerm{
					Term:   term,
					Weight: 1.5,
					Facet:  "anchor",
					Source: "anchor",
				})
			}
		}

		// 2. 使用 LLM 生成扩展词项
		if p.llmProvider != nil {
			llmTerms, err := p.generateExpansionWithLLM(ctx, node)
			if err == nil {
				expansion.Terms = append(expansion.Terms, llmTerms...)
			}
		}

		// 3. 从分类体系获取相关术语
		if p.config.EnableTaxonomy && p.taxonomyProvider != nil {
			taxonomyTerms, err := p.getFromTaxonomy(ctx, node.Query)
			if err == nil {
				expansion.Terms = append(expansion.Terms, taxonomyTerms...)
			}
		}

		// 4. 获取同义词
		if p.config.EnableSynonyms && p.taxonomyProvider != nil {
			synonymTerms, err := p.getSynonyms(ctx, node.Query)
			if err == nil {
				expansion.Terms = append(expansion.Terms, synonymTerms...)
			}
		}

		// 限制扩展词数量
		if p.config.MaxTerms > 0 && len(expansion.Terms) > p.config.MaxTerms {
			expansion.Terms = expansion.Terms[:p.config.MaxTerms]
		}

		expansions[node.ID] = expansion
	}

	return expansions, nil
}

func (p *DefaultExpansionProcessor) generateExpansionWithLLM(ctx context.Context, node QueryNode) ([]ExpansionTerm, error) {
	prompt := fmt.Sprintf(`Generate 3-6 expansion terms for sparse retrieval (BM25) of the following query.

Query: %s

Requirements:
- Include related keywords and terminology
- Include domain-specific terms
- Include potential synonyms or variants
- Avoid stopwords and overly generic terms

Output format (one term per line with weight 0.5-1.0):
term1 | weight | facet
term2 | weight | facet

Example:
Kubernetes | 0.9 | technology
container orchestration | 0.8 | concept

Expansion Terms:`, node.SparseRewrite)

	response, err := p.llmProvider.GenerateCompletion(ctx, prompt)
	if err != nil {
		return []ExpansionTerm{}, err
	}

	terms := []ExpansionTerm{}
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) >= 2 {
			term := strings.TrimSpace(parts[0])
			weight := 0.7
			facet := ""

			if len(parts) >= 2 {
				fmt.Sscanf(strings.TrimSpace(parts[1]), "%f", &weight)
			}
			if len(parts) >= 3 {
				facet = strings.TrimSpace(parts[2])
			}

			if term != "" {
				terms = append(terms, ExpansionTerm{
					Term:   term,
					Weight: weight,
					Facet:  facet,
					Source: "llm",
				})
			}
		}
	}

	return terms, nil
}

func (p *DefaultExpansionProcessor) getFromTaxonomy(ctx context.Context, query string) ([]ExpansionTerm, error) {
	words := strings.Fields(query)
	allTerms := []ExpansionTerm{}

	for _, word := range words {
		relatedTerms, err := p.taxonomyProvider.GetRelatedTerms(ctx, word)
		if err == nil {
			for _, term := range relatedTerms {
				allTerms = append(allTerms, ExpansionTerm{
					Term:   term,
					Weight: 0.6,
					Facet:  "taxonomy",
					Source: "taxonomy",
				})
			}
		}
	}

	return allTerms, nil
}

func (p *DefaultExpansionProcessor) getSynonyms(ctx context.Context, query string) ([]ExpansionTerm, error) {
	words := strings.Fields(query)
	allTerms := []ExpansionTerm{}

	for _, word := range words {
		synonyms, err := p.taxonomyProvider.GetSynonyms(ctx, word)
		if err == nil {
			for _, syn := range synonyms {
				allTerms = append(allTerms, ExpansionTerm{
					Term:   syn,
					Weight: 0.8,
					Facet:  "synonym",
					Source: "synonym",
				})
			}
		}
	}

	return allTerms, nil
}

// DefaultTaxonomyProvider 默认分类体系提供者
type DefaultTaxonomyProvider struct {
	relatedTerms map[string][]string
	synonyms     map[string][]string
}

func NewDefaultTaxonomyProvider() TaxonomyProvider {
	return &DefaultTaxonomyProvider{
		relatedTerms: map[string][]string{
			"kubernetes": {"k8s", "container", "orchestration", "pod", "deployment"},
			"database":   {"sql", "nosql", "data", "storage", "query"},
		},
		synonyms: map[string][]string{
			"kubernetes": {"k8s"},
			"k8s":        {"kubernetes"},
			"database":   {"db", "datastore"},
		},
	}
}

func (p *DefaultTaxonomyProvider) GetRelatedTerms(ctx context.Context, term string) ([]string, error) {
	term = strings.ToLower(term)
	if terms, ok := p.relatedTerms[term]; ok {
		return terms, nil
	}
	return []string{}, nil
}

func (p *DefaultTaxonomyProvider) GetSynonyms(ctx context.Context, term string) ([]string, error) {
	term = strings.ToLower(term)
	if syns, ok := p.synonyms[term]; ok {
		return syns, nil
	}
	return []string{}, nil
}

// =============================================================================
// HyDE Processor - 假设文档嵌入
// =============================================================================

// HyDEProcessor HyDE 处理器接口
type HyDEProcessor interface {
	Generate(ctx context.Context, plan *PreQRAGPlan, alignedQuery *AlignedQuery) (map[string]HyDEVector, error)
}

// DefaultHyDEProcessor 默认 HyDE 处理器
type DefaultHyDEProcessor struct {
	config            *config.HyDEConfig
	llmProvider       llm.Provider
	embeddingProvider embedding.Provider
}

func NewHyDEProcessor(cfg *config.HyDEConfig, llmProvider llm.Provider, embeddingProvider embedding.Provider) HyDEProcessor {
	return &DefaultHyDEProcessor{
		config:            cfg,
		llmProvider:       llmProvider,
		embeddingProvider: embeddingProvider,
	}
}

func (p *DefaultHyDEProcessor) Generate(ctx context.Context, plan *PreQRAGPlan, alignedQuery *AlignedQuery) (map[string]HyDEVector, error) {
	if !p.config.Enabled || p.llmProvider == nil || p.embeddingProvider == nil {
		return map[string]HyDEVector{}, nil
	}

	hydeVectors := make(map[string]HyDEVector)

	for _, node := range plan.Nodes {
		if !p.shouldGenerateHyDE(node) {
			continue
		}

		hypotheticalDoc, err := p.generateHypotheticalDocument(ctx, node)
		if err != nil {
			continue
		}

		vector, err := p.embeddingProvider.GetEmbedding(ctx, hypotheticalDoc)
		if err != nil {
			continue
		}

		qualityScore := p.calculateQualityScore(ctx, hypotheticalDoc, node.Query)
		if !p.passGuardrails(ctx, hypotheticalDoc, node.Query, qualityScore) {
			continue
		}

		hydeVectors[node.ID] = HyDEVector{
			NodeID:          node.ID,
			HypotheticalDoc: hypotheticalDoc,
			Vector:          vector,
			QualityScore:    qualityScore,
		}
	}

	return hydeVectors, nil
}

func (p *DefaultHyDEProcessor) shouldGenerateHyDE(node QueryNode) bool {
	if len(node.Query) < p.config.MinQueryLength {
		return true
	}
	words := strings.Fields(node.Query)
	if len(words) < 5 {
		return true
	}
	return false
}

func (p *DefaultHyDEProcessor) generateHypotheticalDocument(ctx context.Context, node QueryNode) (string, error) {
	targetLength := p.config.GeneratedDocLength
	if targetLength == 0 {
		targetLength = 120
	}

	prompt := fmt.Sprintf(`Generate a hypothetical document passage that would be highly relevant to answering the following query.

Query: %s

Requirements:
- The passage should be %d-150 words
- Write as if it's an excerpt from a relevant document
- Include specific details and terminology
- Make it informative and directly relevant to the query
- Do not include phrases like "This document discusses..." - write the content directly

Hypothetical Document:`, node.DenseRewrite, targetLength)

	doc, err := p.llmProvider.GenerateCompletion(ctx, prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(doc), nil
}

func (p *DefaultHyDEProcessor) calculateQualityScore(ctx context.Context, hypotheticalDoc string, originalQuery string) float64 {
	score := 0.5
	words := strings.Fields(hypotheticalDoc)
	if len(words) >= 50 && len(words) <= 200 {
		score += 0.2
	}

	queryWords := strings.Fields(strings.ToLower(originalQuery))
	docLower := strings.ToLower(hypotheticalDoc)
	matchCount := 0
	for _, word := range queryWords {
		if len(word) > 3 && strings.Contains(docLower, word) {
			matchCount++
		}
	}
	if len(queryWords) > 0 {
		coverage := float64(matchCount) / float64(len(queryWords))
		score += coverage * 0.3
	}

	return score
}

func (p *DefaultHyDEProcessor) passGuardrails(ctx context.Context, hypotheticalDoc string, originalQuery string, qualityScore float64) bool {
	if p.config.EnablePerplexityCheck && qualityScore < 0.4 {
		return false
	}

	if p.config.EnableNLIGuardrail {
		words := strings.Fields(hypotheticalDoc)
		if len(words) < 30 || len(words) > 300 {
			return false
		}
	}

	return true
}
