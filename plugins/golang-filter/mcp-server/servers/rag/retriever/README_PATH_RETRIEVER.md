# Path Retriever - 双路径稀疏检索

## 概述

Path Retriever 实现了基于文档路径的稀疏检索功能，与 BM25 Retriever 配合使用可以实现双路径稀疏检索（BM25 + Path Retriever），通过融合机制提升检索效果。

## 架构设计

### 1. Path Retriever 实现

Path Retriever 继承自统一的 `Retriever` 接口，与 BM25 Retriever 保持一致的架构：

- **类型标识**: `"path"`
- **检索目标**: 文档的路径字段（如 `know_path`, `file_path` 等）
- **检索策略**: 使用 BM25 算法，但针对路径字段进行优化加权

### 2. 双路径融合机制

系统通过现有的 Fusion 机制自动处理双路径检索结果：

- **RRF (Reciprocal Rank Fusion)**: 默认融合策略，对 BM25 和 Path 检索结果进行融合（类似 EasyRAG 的 `reciprocal_rank_fusion`）
- **Simple Fusion**: 简单融合策略，保留每个文档的最高分数（类似 EasyRAG 的 `HybridRetriever.fusion`）
- **Weighted Strategy**: 支持为不同检索器设置权重
- **自动去重**: 基于文档 ID 自动去重和合并

### 3. 配置方式

在 `PipelineConfig.Retrievers` 中配置 Path Retriever：

```yaml
pipeline:
  retrievers:
    # BM25 主检索器（内容检索）
    - type: bm25
      provider: elasticsearch
      params:
        endpoint: "http://es:9200"
        index: "rag_bm25"
        top_k: "10"
        name: "bm25_main"
    
    # Path 路径检索器（路径检索）
    - type: path
      provider: elasticsearch
      params:
        endpoint: "http://es:9200"
        index: "rag_bm25"
        path_field: "know_path"  # 路径字段名，可选值: know_path, file_path, path, document_path
        top_k: "10"
        name: "path_retriever"
```

### 4. Retrieval Profile 配置

在 Retrieval Profile 中指定使用双路径检索：

```yaml
retrieval_profiles:
  - name: "dual_sparse_profile"
    retrievers:
      - "bm25_main"
      - "path_retriever"
    top_k: 10
    per_retriever_top_k: 10
    variant_budgets:
      sparse: 20  # 为稀疏检索（包括BM25和Path）分配预算
```

### 5. 工作原理

1. **并行检索**: BM25 和 Path Retriever 并行执行检索
2. **结果融合**: 使用配置的 Fusion 策略（默认 RRF）合并结果
3. **去重排序**: 基于文档 ID 去重，按融合分数排序
4. **返回结果**: 返回 TopK 个融合后的结果

### 6. 路径字段支持

Path Retriever 支持以下路径字段：

- `know_path`: 知识路径（默认）
- `file_path`: 文件路径
- `path`: 通用路径
- `document_path`: 文档路径
- `metadata.*`: 元数据中的任意路径字段

### 7. 查询优化

Path Retriever 的查询策略：

1. **路径字段优先**: 对路径字段设置更高的 boost（2.0）
2. **元数据路径**: 支持 `metadata.path_field` 格式（boost 1.5）
3. **内容回退**: 如果路径不匹配，回退到内容检索（boost 0.5）

### 8. 与 EasyRAG 的对比

参考 EasyRAG 的实现方式：

```python
# EasyRAG 中的实现
self.sparse_retriever = BM25Retriever.from_defaults(
    nodes=self.nodes,
    embed_type=f_embed_type_2,  # 内容检索
    ...
)
self.path_retriever = BM25Retriever.from_defaults(
    nodes=self.nodes,
    embed_type=5,  # 路径检索 (know_path)
    ...
)
node_with_scores = HybridRetriever.fusion([
    node_with_scores,
    node_with_scores_path,
])
```

我们的实现：

- **架构一致性**: 复用现有的 Retriever 接口和 Fusion 机制
- **配置驱动**: 通过配置文件灵活控制，无需代码修改
- **可扩展性**: 支持多个 Path Retriever 实例，针对不同路径字段

## 使用示例

### 完整配置示例

```yaml
pipeline:
  enable_hybrid: true
  rrf_k: 60
  retrievers:
    - type: bm25
      provider: elasticsearch
      params:
        endpoint: "http://elasticsearch:9200"
        index: "knowledge_base"
        top_k: "10"
        name: "bm25_content"
    
    - type: path
      provider: elasticsearch
      params:
        endpoint: "http://elasticsearch:9200"
        index: "knowledge_base"
        path_field: "know_path"
        top_k: "10"
        name: "path_knowledge"
  
  retrieval_profiles:
    - name: "dual_sparse"
      retrievers:
        - "bm25_content"
        - "path_knowledge"
      top_k: 10
      per_retriever_top_k: 10
      variant_budgets:
        sparse: 20
```

### 预期效果

- **内容匹配**: BM25 Retriever 负责内容语义匹配
- **路径匹配**: Path Retriever 负责文档结构/路径匹配
- **融合提升**: 两者融合后能够同时捕获内容和结构信息，提升检索准确性

## 扩展性

### 添加新的路径字段

只需在配置中指定不同的 `path_field` 参数：

```yaml
- type: path
  params:
    path_field: "custom_path_field"
```

### 多路径检索器

可以配置多个 Path Retriever，针对不同路径字段：

```yaml
retrievers:
  - type: path
    params:
      path_field: "know_path"
      name: "path_knowledge"
  - type: path
    params:
      path_field: "file_path"
      name: "path_file"
```

然后在 Profile 中同时使用：

```yaml
retrievers:
  - "bm25_content"
  - "path_knowledge"
  - "path_file"
```

## 注意事项

1. **索引要求**: Elasticsearch 索引需要包含路径字段，并建立相应的索引
2. **字段映射**: 确保路径字段在 Elasticsearch 中正确映射
3. **性能考虑**: 双路径检索会增加检索时间，建议合理设置 `top_k` 和 `per_retriever_top_k`
4. **融合策略**: 根据实际效果调整 RRF 参数或使用 Weighted Strategy

