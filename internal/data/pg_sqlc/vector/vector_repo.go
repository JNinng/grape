package vector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"
	pgsqlc "grape/internal/data/pg_sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// 向量存储设施（biz.VectorStore 的 PostgreSQL 实现），存取 agent.agent_document。
// 一表承载多嵌入空间：embedding 为裸 vector，embedder/embed_dim 逐行声明
// 身份，检索命中对应维度的表达式部分 HNSW 索引。SQL 手写参数化，不进 sqlc
// 生成。表无 RLS，租户隔离由应用层强制；空间模型与探针契约见
// internal/data/README.md「向量介质」。
const (
	// vectorSchema 是 agent 域设施的固定 schema（与框架会话表同域）。
	vectorSchema = "agent"
	// vectorDefaultTable 是 conf.vector.table_name 留空时的默认表名，
	// 与 agent_vector.sql 的建表一致。
	vectorDefaultTable = "agent_document"
	// vectorTablePrefix 是 agent 域对象的统一表名前缀，
	// conf.vector.table_name 在构造期强制。
	vectorTablePrefix = "agent_"
	// vectorDefaultLimit 是 Search 未指定 Limit 时的默认结果数，与框架
	// pgvector 实现的默认一致（适配层语义不漂移）。
	vectorDefaultLimit = 10

	vectorColumns = `id, tenant_id, user_id, embedder, embed_dim, name, content, embedding, metadata, created_at, updated_at`

	// vectorInsertColumns 是写入列清单：created_at/updated_at 由列默认值
	// 生成，created_by/updated_by 复用 $3（认证用户）。
	vectorInsertColumns = `id, tenant_id, user_id, embedder, embed_dim, name, content, embedding, metadata, created_by, updated_by`

	vectorInsertSQL = `INSERT INTO %s (` + vectorInsertColumns + `)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $3, $3)`

	vectorSelectSQL = `SELECT ` + vectorColumns + `
FROM %s WHERE tenant_id = $1 AND id = $2`

	vectorUpdateSQL = `UPDATE %s SET embedder = $3, embed_dim = $4, name = $5, content = $6, embedding = $7, metadata = $8, updated_by = $9, updated_at = CURRENT_TIMESTAMP
WHERE tenant_id = $1 AND id = $2`

	vectorDeleteSQL = `DELETE FROM %s WHERE tenant_id = $1 AND id = $2`

	vectorDeleteByFilterSQL = `WITH deleted AS (DELETE FROM %s WHERE tenant_id = $1%s RETURNING 1)
SELECT count(*) FROM deleted`

	// UpdateMetadata 只合并 metadata（jsonb ||），不触碰正文与向量列。
	vectorUpdateMetadataSQL = `WITH updated AS (
    UPDATE %s SET metadata = metadata || $2::jsonb, updated_by = $3, updated_at = CURRENT_TIMESTAMP
    WHERE tenant_id = $1%s RETURNING 1)
SELECT count(*) FROM updated`

	vectorCountSQL = `SELECT count(*) FROM %s WHERE tenant_id = $1%s`

	// GetMetadata 的排序键：created_at 是 timestamp(0) 秒级精度，同秒
	// 写入必须以 id tie-break，否则 LIMIT/OFFSET 翻页不确定；排序键与
	// agent_document_tenant_created_idx（租户前缀）对齐。
	vectorMetadataSQL = `SELECT id, metadata FROM %s WHERE tenant_id = $1%s ORDER BY created_at, id`

	// vectorColumnsProbeSQL 读结构列：embedding 必须是裸 vector（无
	// typmod，维度随行），embedder/embed_dim 必须存在——三者构成多空间
	// 表形状，任一缺失即部署不符。
	vectorColumnsProbeSQL = `SELECT a.attname, format_type(a.atttypid, a.atttypmod)
FROM pg_attribute a
WHERE a.attrelid = to_regclass($1) AND a.attname = ANY($2) AND a.attisdropped = false`

	// vectorIndexesProbeSQL 读表上全部索引定义：每个 conf 声明的维度
	// 必须有匹配的表达式部分 HNSW 索引（vector(N) cast + embed_dim
	// 谓词 + cosine ops），缺失即检索会退化为顺序扫描，装配期失败。
	vectorIndexesProbeSQL = `SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2`
)

// vectorTableNamePattern 是表名标识符白名单：小写字母开头的下划线数字
// 组合，63 字符内（PostgreSQL 标识符上限）。表名是唯一拼进 SQL 文本的
// 配置值，白名单在构造期拒绝注入形态。
var vectorTableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// vectorIndexDefDimensionPattern 从 pg_indexes.indexdef 提取表达式索引的
// 维度：`::vector(N)) vector_cosine_ops` 是表达式部分 HNSW 索引入驻的
// 固定形态（(列)::vector(N) 的显式 cast）。闭括号锚定数字边界。
var vectorIndexDefDimensionPattern = regexp.MustCompile(`::vector\((\d+)\)\) vector_cosine_ops`)

// vectorStore 是向量存储的介质实现。
type vectorStore struct {
	data   *pgsqlc.Data
	table  string             // 已过白名单校验的表名（agent schema 内）
	spaces map[string]int32   // 空间键 → 声明维度（conf.VectorSpaces 单一解析源）
	dims   map[int32]struct{} // 注册表内去重维度集合（启动探针逐维度核索引）
	indexed map[int32]struct{} // 启动探针从 pg_indexes 发现的索引维度集合
}

var _ biz.VectorStore = (*vectorStore)(nil)

// NewVectorStore 构造向量存储设施。conf.vector.enabled=false 时返回禁用
// 占位实现（不触库）；启用时 fail-fast 校验空间注册表、表名白名单与部署
// 探针（多空间形状 + 每声明维度的表达式部分 HNSW 索引）。
func NewVectorStore(c *conf.Vector, d *pgsqlc.Data) (biz.VectorStore, func(), error) {
	if !c.GetEnabled() {
		return &disabledVectorStore{}, func() {}, nil
	}
	spaces, _, err := conf.VectorSpaces(c)
	if err != nil {
		return nil, nil, err
	}
	table := c.GetTableName()
	if table == "" {
		table = vectorDefaultTable
	}
	if !vectorTableNamePattern.MatchString(table) {
		return nil, nil, fmt.Errorf("pg_sqlc: vector table name %q must match %s", table, vectorTableNamePattern)
	}
	// agent_ 前缀强制（agent 域对象统一前缀，与 conf.proto
	// 注释承诺一致），前缀缺失在构造期拒绝。
	if !strings.HasPrefix(table, vectorTablePrefix) {
		return nil, nil, fmt.Errorf("pg_sqlc: vector table name %q must have the %s prefix", table, vectorTablePrefix)
	}
	dims := make(map[int32]struct{}, len(spaces))
	for _, dim := range spaces {
		dims[dim] = struct{}{}
	}
	r := &vectorStore{data: d, table: table, spaces: spaces, dims: dims, indexed: map[int32]struct{}{}}
	if err := r.probeDeployment(context.Background()); err != nil {
		return nil, nil, err
	}
	return r, func() {}, nil
}

// probeDeployment 启动探针：部署表必须存在且为多空间形状（裸 vector、
// embedder/embed_dim 列齐全），且每个声明维度存在匹配的表达式部分
// HNSW 索引。池直连只读 pg_catalog（无业务数据、无租户上下文），配置
// 错表/部署缺索引在装配期失败，不带病启动。
func (r *vectorStore) probeDeployment(ctx context.Context) error {
	if r.data == nil || r.data.Pool() == nil {
		return errors.New("pg_sqlc: vector probe requires an initialized database handle")
	}
	qualified := vectorSchema + "." + r.table
	columns, err := r.probeColumns(ctx, qualified)
	if err != nil {
		return err
	}
	declared, ok := columns["embedding"]
	if !ok {
		return fmt.Errorf("pg_sqlc: vector table %s is not deployed (embedding column missing); run agent_vector.sql first", qualified)
	}
	if declared != "vector" {
		return fmt.Errorf("pg_sqlc: vector table %s embedding column type is %s, expected bare vector (no typmod); the multi-space shape requires dimension-per-row via embed_dim, realign agent_vector.sql",
			qualified, declared)
	}
	for _, name := range []string{"embedder", "embed_dim"} {
		if _, ok := columns[name]; !ok {
			return fmt.Errorf("pg_sqlc: vector table %s is missing the %s column; the multi-space shape requires it, realign agent_vector.sql", qualified, name)
		}
	}
	return r.probeIndexes(ctx, qualified)
}

// probeColumns 读结构列 → 列名 → 声明类型。
func (r *vectorStore) probeColumns(ctx context.Context, qualified string) (map[string]string, error) {
	rows, err := r.data.Pool().Query(ctx, vectorColumnsProbeSQL, qualified,
		[]string{"embedding", "embedder", "embed_dim"})
	if err != nil {
		return nil, fmt.Errorf("pg_sqlc: probe vector table %s: %w", qualified, err)
	}
	defer rows.Close()
	columns := map[string]string{}
	for rows.Next() {
		var name, declared string
		if err := rows.Scan(&name, &declared); err != nil {
			return nil, fmt.Errorf("pg_sqlc: probe vector table %s: %w", qualified, err)
		}
		columns[name] = declared
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg_sqlc: probe vector table %s: %w", qualified, err)
	}
	return columns, nil
}

// probeIndexes 核对每个声明维度的表达式部分 HNSW 索引，并从 pg_indexes
// 发现已建索引的维度集合（供 resolveSpace 校验动态模型目录产生的空间）。
// 索引定义须同时含 `::vector(N)) vector_cosine_ops` 与 `embed_dim = N)`，
// cast 与谓词是检索 SQL 命中索引的前提，缺失即装配期拒绝。conf 声明的
// 每个维度仍必须有索引（保持 fail-fast）；索引集合是后续运行期维度校验
// 的唯一依据。索引表达式细节见 internal/data/README.md「向量介质」。
func (r *vectorStore) probeIndexes(ctx context.Context, qualified string) error {
	parts := strings.SplitN(qualified, ".", 2)
	rows, err := r.data.Pool().Query(ctx, vectorIndexesProbeSQL, parts[0], parts[1])
	if err != nil {
		return fmt.Errorf("pg_sqlc: probe vector table %s indexes: %w", qualified, err)
	}
	defer rows.Close()
	var defs []string
	for rows.Next() {
		var def string
		if err := rows.Scan(&def); err != nil {
			return fmt.Errorf("pg_sqlc: probe vector table %s indexes: %w", qualified, err)
		}
		defs = append(defs, def)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("pg_sqlc: probe vector table %s indexes: %w", qualified, err)
	}
	r.indexed = discoverIndexedDimensions(defs)
	for dim := range r.dims {
		expr := fmt.Sprintf("::vector(%d)) vector_cosine_ops", dim)
		predicate := fmt.Sprintf("embed_dim = %d)", dim)
		if !containsAny(defs, expr, predicate) {
			return fmt.Errorf("pg_sqlc: vector table %s has no expression partial hnsw index for dimension %d (expected %s with %s); add it to agent_vector.sql or align conf.vector.embedders",
				qualified, dim, expr, predicate)
		}
	}
	return nil
}

// discoverIndexedDimensions 从索引定义集合提取「cast 维度与 embed_dim 谓词
// 一致」的维度：同一索引定义里 `::vector(N)) vector_cosine_ops` 与
// `embed_dim = N)` 必须成对出现，否则不计入（避免部分/错配索引被误认为
// 可检索维度）。
func discoverIndexedDimensions(defs []string) map[int32]struct{} {
	out := make(map[int32]struct{})
	for _, def := range defs {
		for _, match := range vectorIndexDefDimensionPattern.FindAllStringSubmatch(def, -1) {
			dim, err := strconv.Atoi(match[1])
			if err != nil || dim <= 0 {
				continue
			}
			if strings.Contains(def, fmt.Sprintf("embed_dim = %d)", dim)) {
				out[int32(dim)] = struct{}{}
			}
		}
	}
	return out
}

// containsAny 判断 defs 中存在同时含两段片段的定义。
func containsAny(defs []string, expr, predicate string) bool {
	for _, def := range defs {
		if strings.Contains(def, expr) && strings.Contains(def, predicate) {
			return true
		}
	}
	return false
}

// vectorIdentity 从认证 ctx 提取身份：缺失或非法（空租户/非正用户）一律
// 拒绝。身份只来自服务端认证中间件，永不受请求侧输入影响。
func vectorIdentity(ctx context.Context) (pgsqlc.Identity, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return pgsqlc.Identity{}, errors.New("pg_sqlc: vector store requires an authenticated identity")
	}
	return pgsqlc.NewIdentity(id)
}

// vectorMetadata 把认证身份与嵌入空间身份写进文档 metadata 副本：
// tenant_id/user_id/embedder 以服务端产出为准覆盖同名键（请求侧伪造
// 无效），调用方自定义键保留。列与 metadata 双写：列供 SQL 强制过滤，
// metadata 供框架侧过滤语义回读。
func vectorMetadata(metadata map[string]any, auth pgsqlc.Identity, embedder string, dimension int) map[string]any {
	out := make(map[string]any, len(metadata)+3)
	for k, v := range metadata {
		out[k] = v
	}
	out["tenant_id"] = auth.TenantID
	out["user_id"] = auth.UserID
	out["embedder"] = embedder
	out["embed_dim"] = dimension
	return out
}

// marshalVectorMetadata 序列化写入列的 metadata；不可序列化值在此显式
// 报错，不静默丢键。
func marshalVectorMetadata(metadata map[string]any) ([]byte, error) {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("pg_sqlc: marshal vector metadata: %w", err)
	}
	return raw, nil
}

// resolveSpace 对账嵌入空间声明：键必填、维度必填且向量长度等于维度。
// 空间键若在 conf 注册表中声明，维度必须与声明一致；未在 conf 声明但
// 维度存在表达式部分 HNSW 索引时放行——动态模型目录（sys_model）产生的
// 空间键在装配期未知，运行期以索引维度集合为唯一校验依据（空间是否
// 存在与端点是否可用由 EmbedderResolver / Embedder 在更上层 fail-closed）。
// 维度无对应索引即拒绝（检索会退化为顺序扫描，且不能落库）。返回落库
// embed_dim 列的维度值。
func (r *vectorStore) resolveSpace(embedder string, dimension, length int, subject string) (int32, error) {
	if embedder == "" {
		return 0, fmt.Errorf("pg_sqlc: %s must declare an embedder space", subject)
	}
	if dimension <= 0 {
		return 0, fmt.Errorf("pg_sqlc: %s must declare a positive dimension", subject)
	}
	if declared, ok := r.spaces[embedder]; ok && dimension != int(declared) {
		return 0, fmt.Errorf("pg_sqlc: %s declares dimension %d for space %s, registry declares %d", subject, dimension, embedder, declared)
	}
	if length != dimension {
		return 0, fmt.Errorf("pg_sqlc: %s embedding length %d does not match declared dimension %d for space %s",
			subject, length, dimension, embedder)
	}
	if _, ok := r.indexed[int32(dimension)]; !ok {
		return 0, fmt.Errorf("%w: space %s declares dimension %d with no expression hnsw index (add it to agent_vector.sql)",
			biz.ErrEmbedderUnknown, embedder, dimension)
	}
	return int32(dimension), nil
}

// Add 写入文档与向量。身份取认证 ctx，空间声明过注册表对账；重复 ID
// 报唯一约束冲突。
func (r *vectorStore) Add(ctx context.Context, doc *biz.VectorDocument) error {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return err
	}
	dim, err := r.checkDocument(doc)
	if err != nil {
		return err
	}
	metadata, err := marshalVectorMetadata(vectorMetadata(doc.Metadata, auth, doc.Embedder, int(dim)))
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		_, err := s.Tx().Exec(ctx,
			fmt.Sprintf(vectorInsertSQL, r.qualifiedTable()),
			doc.ID, auth.TenantID, auth.UserID, doc.Embedder, dim, doc.Name, doc.Content,
			pgvector.NewVector(doc.Embedding), metadata)
		return err
	})
}

// Get 按 ID 取文档（含向量、嵌入空间身份与 metadata）。租户过滤在
// WHERE 内强制：跨租户与不存在同样落到无行，统一 ErrVectorDocNotFound
// （不泄露存在性）。
func (r *vectorStore) Get(ctx context.Context, id string) (*biz.VectorDocument, error) {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return nil, err
	}
	var row vectorDocRow
	err = r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			fmt.Sprintf(vectorSelectSQL, r.qualifiedTable()),
			auth.TenantID, id).Scan(row.fields()...)
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return row.toBiz()
}

// Update 整体覆盖文档可变部分（含嵌入空间——空间变更即显式重嵌入）；
// 0 行（不存在或跨租户）报 ErrVectorDocNotFound。ID 与归属不可借本
// 方法变更。
func (r *vectorStore) Update(ctx context.Context, doc *biz.VectorDocument) error {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return err
	}
	dim, err := r.checkDocument(doc)
	if err != nil {
		return err
	}
	metadata, err := marshalVectorMetadata(vectorMetadata(doc.Metadata, auth, doc.Embedder, int(dim)))
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		tag, err := s.Tx().Exec(ctx,
			fmt.Sprintf(vectorUpdateSQL, r.qualifiedTable()),
			auth.TenantID, doc.ID, doc.Embedder, dim, doc.Name, doc.Content,
			pgvector.NewVector(doc.Embedding), metadata, auth.UserID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return biz.ErrVectorDocNotFound
		}
		return nil
	})
}

// Delete 按 ID 删除；0 行（不存在或跨租户）报 ErrVectorDocNotFound。
func (r *vectorStore) Delete(ctx context.Context, id string) error {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		tag, err := s.Tx().Exec(ctx,
			fmt.Sprintf(vectorDeleteSQL, r.qualifiedTable()),
			auth.TenantID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return biz.ErrVectorDocNotFound
		}
		return nil
	})
}

// Search 在 filter.Embedder 声明的空间内余弦相似检索：声明与查询向量
// 长度、注册表三方对账后，维度的 vector(N) cast 以内联常量进 SQL
// （与索引表达式逐字一致才命中部分索引），分数与排序同源（ORDER BY
// 走 HNSW 索引），过滤条件与租户边界取交集。Limit 非正取默认页大小。
func (r *vectorStore) Search(ctx context.Context, embedding []float32, filter *biz.VectorFilter) ([]*biz.ScoredVectorDocument, error) {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if filter == nil {
		return nil, errors.New("pg_sqlc: search filter is required (the embedder space must be declared)")
	}
	dim, err := r.resolveSpace(filter.Embedder, filter.Dimension, len(embedding), "search filter")
	if err != nil {
		return nil, err
	}
	limit := vectorDefaultLimit
	if filter.Limit > 0 {
		limit = filter.Limit
	}

	// 过滤计划跳过 embedder 子句：检索的空间等值已由手写条件承担
	//（$3，必填且经 resolveSpace 对账），plan 只翻译 IDs/Metadata。
	stripped := *filter
	stripped.Embedder = ""
	plan, err := planVectorFilter(4, &stripped)
	if err != nil {
		return nil, err
	}
	args := make([]any, 0, len(plan.args)+4)
	args = append(args, pgvector.NewVector(embedding), auth.TenantID, filter.Embedder)
	args = append(args, plan.args...)
	where := " WHERE tenant_id = $2 AND embedder = $3 AND embed_dim = " + strconv.Itoa(int(dim)) + plan.where()
	score := vectorScoreExpr(dim)
	if filter.MinScore > 0 {
		args = append(args, filter.MinScore)
		where += fmt.Sprintf(" AND %s >= $%d", score, len(args))
	}
	args = append(args, limit)
	sql := fmt.Sprintf("SELECT %s, %s AS score FROM %s%s ORDER BY %s LIMIT $%d",
		vectorColumns, score, r.qualifiedTable(), where, vectorOrderExpr(dim), len(args))

	var out []*biz.ScoredVectorDocument
	err = r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		rows, err := s.Tx().Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row vectorDocRow
			var score float64
			if err := rows.Scan(append(row.fields(), &score)...); err != nil {
				return err
			}
			doc, err := row.toBiz()
			if err != nil {
				return err
			}
			out = append(out, &biz.ScoredVectorDocument{Document: doc, Score: score})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteByFilter 按 IDs/Embedder/Metadata 交集删除并返回行数；所有条件
// 均空时拒绝执行（租户级全量清理是 super 管理口径，不设普通入口）。
func (r *vectorStore) DeleteByFilter(ctx context.Context, filter *biz.VectorFilter) (int64, error) {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return 0, err
	}
	plan, err := planVectorFilter(2, filter)
	if err != nil {
		return 0, err
	}
	if len(plan.clauses) == 0 {
		return 0, errors.New("pg_sqlc: unfiltered vector delete is refused; tenant-wide purge belongs to the super management path")
	}
	args := append([]any{auth.TenantID}, plan.args...)
	var deleted int64
	err = r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			fmt.Sprintf(vectorDeleteByFilterSQL, r.qualifiedTable(), plan.where()),
			args...).Scan(&deleted)
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// UpdateMetadata 按 IDs/Embedder/Metadata/Conditions 交集把 patch 合并进
// 命中行的 metadata（jsonb ||），返回更新行数；只改 metadata 列。所有
// 条件均空时拒绝执行（租户级全量改写不设普通入口）。目录状态/有效期
// 变更依赖本方法同步 chunk metadata，失败必须让上层事务回滚。
func (r *vectorStore) UpdateMetadata(ctx context.Context, filter *biz.VectorFilter, patch map[string]any) (int64, error) {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return 0, err
	}
	if len(patch) == 0 {
		return 0, errors.New("pg_sqlc: vector metadata patch is empty")
	}
	plan, err := planVectorFilter(4, filter)
	if err != nil {
		return 0, err
	}
	if len(plan.clauses) == 0 {
		return 0, errors.New("pg_sqlc: unfiltered vector metadata update is refused; tenant-wide rewrite belongs to the super management path")
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return 0, fmt.Errorf("pg_sqlc: marshal vector metadata patch: %w", err)
	}
	args := []any{auth.TenantID, raw, auth.UserID}
	args = append(args, plan.args...)
	var updated int64
	err = r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			fmt.Sprintf(vectorUpdateMetadataSQL, r.qualifiedTable(), plan.where()),
			args...).Scan(&updated)
	})
	if err != nil {
		return 0, err
	}
	return updated, nil
}

// Count 统计租户内满足过滤条件的文档数；条件为空即租户全量，Embedder
// 非空则限该空间。
func (r *vectorStore) Count(ctx context.Context, filter *biz.VectorFilter) (int64, error) {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return 0, err
	}
	plan, err := planVectorFilter(2, filter)
	if err != nil {
		return 0, err
	}
	args := append([]any{auth.TenantID}, plan.args...)
	var total int64
	err = r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			fmt.Sprintf(vectorCountSQL, r.qualifiedTable(), plan.where()),
			args...).Scan(&total)
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// GetMetadata 按 IDs/Embedder/Metadata 过滤枚举 metadata，写入时间升序
// 分页；Limit 非正为不限量，此时 Offset 必须为 0（半开分页非法组合
// 显式拒绝）。
func (r *vectorStore) GetMetadata(ctx context.Context, filter *biz.VectorFilter) (map[string]map[string]any, error) {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := planVectorFilter(2, filter)
	if err != nil {
		return nil, err
	}
	limit, offset := 0, 0
	if filter != nil {
		limit, offset = filter.Limit, filter.Offset
	}
	if limit <= 0 && offset > 0 {
		return nil, errors.New("pg_sqlc: vector metadata offset requires a positive limit")
	}
	args := append([]any{auth.TenantID}, plan.args...)
	sql := fmt.Sprintf(vectorMetadataSQL, r.qualifiedTable(), plan.where())
	if limit > 0 {
		args = append(args, limit)
		sql += fmt.Sprintf(" LIMIT $%d", len(args))
		if offset > 0 {
			args = append(args, offset)
			sql += fmt.Sprintf(" OFFSET $%d", len(args))
		}
	}

	out := map[string]map[string]any{}
	err = r.data.IdentityTx(ctx, auth, func(s *pgsqlc.IdentityTx) error {
		rows, err := s.Tx().Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				return err
			}
			var metadata map[string]any
			if err := json.Unmarshal(raw, &metadata); err != nil {
				return fmt.Errorf("pg_sqlc: unmarshal vector metadata: %w", err)
			}
			out[id] = metadata
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mapErr 把无行统一映射为 ErrVectorDocNotFound（不存在与跨租户不可见
// 不区分，与 permission.md 的 NotFound 口径一致）。
func (r *vectorStore) mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.ErrVectorDocNotFound
	}
	return err
}

// qualifiedTable 是拼进 SQL 的完整表名：schema 固定 agent，表名构造期
// 已过白名单。
func (r *vectorStore) qualifiedTable() string {
	return vectorSchema + "." + r.table
}

// vectorScoreExpr 是归一化相似度：余弦距离（<=>）映射到 [0,1]，与
// 框架 pgvector 实现的分数语义一致；维度内联进 vector(N) cast（必须与
// 索引表达式逐字一致才命中部分索引），$1 是查询向量。
func vectorScoreExpr(dim int32) string {
	return fmt.Sprintf("1.0 - ((embedding::vector(%d)) <=> $1::vector(%d)) / 2.0", dim, dim)
}

// vectorOrderExpr 是检索排序键：与索引表达式逐字一致的距离算子。
func vectorOrderExpr(dim int32) string {
	return fmt.Sprintf("(embedding::vector(%d)) <=> $1::vector(%d)", dim, dim)
}

// vectorDocRow 是 agent_document 的行载体：fields 供 pgx Scan 展开参数。
type vectorDocRow struct {
	ID        string
	TenantID  string
	UserID    int64
	Embedder  string
	Dim       int32
	Name      *string
	Content   string
	Embedding pgvector.Vector
	Metadata  []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (row *vectorDocRow) fields() []any {
	return []any{&row.ID, &row.TenantID, &row.UserID, &row.Embedder, &row.Dim, &row.Name, &row.Content,
		&row.Embedding, &row.Metadata, &row.CreatedAt, &row.UpdatedAt}
}

// toBiz 转换为领域文档。metadata 里携带仓储写入的 tenant_id/user_id/
// embedder/embed_dim，一并原样返回给调用方（框架侧过滤语义可回读）。
func (row *vectorDocRow) toBiz() (*biz.VectorDocument, error) {
	metadata := map[string]any{}
	if len(row.Metadata) > 0 {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
			return nil, fmt.Errorf("pg_sqlc: unmarshal vector metadata: %w", err)
		}
	}
	var name string
	if row.Name != nil {
		name = *row.Name
	}
	return &biz.VectorDocument{
		ID:        row.ID,
		Embedder:  row.Embedder,
		Dimension: int(row.Dim),
		Name:      name,
		Content:   row.Content,
		Embedding: row.Embedding.Slice(),
		Metadata:  metadata,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// vectorFilterPlan 是过滤条件的参数化翻译：片段以 AND 起头拼进 WHERE，
// 值全部走占位符，过滤键不进 SQL 文本（metadata 条件经 jsonb 容器运算
// 符整值下发，键名/值都不参与拼接；embedder 走等值占位）。
type vectorFilterPlan struct {
	clauses []string
	args    []any
}

// planVectorFilter 把 biz.VectorFilter 翻译为 WHERE 片段；startArg 是首个
// 可用占位符序号（调用方已占用的参数数）。
func planVectorFilter(startArg int, filter *biz.VectorFilter) (*vectorFilterPlan, error) {
	plan := &vectorFilterPlan{}
	if filter == nil {
		return plan, nil
	}
	if len(filter.IDs) > 0 {
		plan.clauses = append(plan.clauses, fmt.Sprintf("id = ANY($%d)", startArg+len(plan.args)))
		plan.args = append(plan.args, filter.IDs)
	}
	if filter.Embedder != "" {
		plan.clauses = append(plan.clauses, fmt.Sprintf("embedder = $%d", startArg+len(plan.args)))
		plan.args = append(plan.args, filter.Embedder)
	}
	if len(filter.Metadata) > 0 {
		raw, err := json.Marshal(filter.Metadata)
		if err != nil {
			return nil, fmt.Errorf("pg_sqlc: marshal vector metadata filter: %w", err)
		}
		plan.clauses = append(plan.clauses, fmt.Sprintf("metadata @> $%d::jsonb", startArg+len(plan.args)))
		plan.args = append(plan.args, raw)
	}
	for i, cond := range filter.Conditions {
		clause, args, err := planMetadataCondition(startArg+len(plan.args), cond)
		if err != nil {
			return nil, fmt.Errorf("pg_sqlc: metadata condition %d: %w", i, err)
		}
		plan.clauses = append(plan.clauses, clause)
		plan.args = append(plan.args, args...)
	}
	return plan, nil
}

// planMetadataCondition 把单条 metadata 条件翻译为参数化 SQL 片段：键名
// 与比较值都走占位符，操作符只从封闭集合选择（键与值都不拼进 SQL 文本）。
// eq/ne/in 按文本比较（metadata->> 的返回值即 JSON 标量的文本形态）；
// lt/lte/gt/gte 显式 ::bigint 后做数值比较。
func planMetadataCondition(startArg int, cond biz.MetadataCondition) (string, []any, error) {
	key := strings.TrimSpace(cond.Key)
	if key == "" {
		return "", nil, errors.New("metadata condition key is required")
	}
	if len(cond.Values) == 0 {
		return "", nil, fmt.Errorf("metadata condition %s requires a value", cond.Op)
	}
	keyArg := startArg
	valueArg := startArg + 1
	valueRef := fmt.Sprintf("$%d", valueArg)
	keyRef := fmt.Sprintf("$%d", keyArg)
	switch cond.Op {
	case biz.VectorMetadataEq:
		text, err := metadataTextValue(cond.Values[0])
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("metadata->>%s = %s", keyRef, valueRef), []any{key, text}, nil
	case biz.VectorMetadataNe:
		text, err := metadataTextValue(cond.Values[0])
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("metadata->>%s <> %s", keyRef, valueRef), []any{key, text}, nil
	case biz.VectorMetadataIn:
		texts := make([]string, 0, len(cond.Values))
		for _, v := range cond.Values {
			text, err := metadataTextValue(v)
			if err != nil {
				return "", nil, err
			}
			texts = append(texts, text)
		}
		return fmt.Sprintf("metadata->>%s = ANY(%s)", keyRef, valueRef), []any{key, texts}, nil
	case biz.VectorMetadataLt, biz.VectorMetadataLte, biz.VectorMetadataGt, biz.VectorMetadataGte:
		num, err := metadataIntValue(cond.Values[0])
		if err != nil {
			return "", nil, err
		}
		op := map[biz.VectorMetadataOp]string{
			biz.VectorMetadataLt: "<", biz.VectorMetadataLte: "<=",
			biz.VectorMetadataGt: ">", biz.VectorMetadataGte: ">=",
		}[cond.Op]
		return fmt.Sprintf("(metadata->>%s)::bigint %s %s::bigint", keyRef, op, valueRef), []any{key, num}, nil
	default:
		return "", nil, fmt.Errorf("unsupported metadata condition op %q", cond.Op)
	}
}

// metadataTextValue 把条件值转为文本比较值（metadata->> 的返回形态）：
// 字符串原样，整数与布尔按其十进制/字面文本，浮点仅接受整数值。
func metadataTextValue(v any) (string, error) {
	switch value := v.(type) {
	case string:
		return value, nil
	case bool:
		return strconv.FormatBool(value), nil
	case int:
		return strconv.Itoa(value), nil
	case int8:
		return strconv.FormatInt(int64(value), 10), nil
	case int16:
		return strconv.FormatInt(int64(value), 10), nil
	case int32:
		return strconv.FormatInt(int64(value), 10), nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	case uint:
		return strconv.FormatUint(uint64(value), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(value), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(value), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(value), 10), nil
	case uint64:
		return strconv.FormatUint(value, 10), nil
	case float32:
		return metadataTextValueFloat(float64(value))
	case float64:
		return metadataTextValueFloat(value)
	default:
		return "", fmt.Errorf("unsupported metadata condition value type %T", v)
	}
}

func metadataTextValueFloat(v float64) (string, error) {
	if v != float64(int64(v)) {
		return "", fmt.Errorf("metadata text comparison requires an integral value, got %v", v)
	}
	return strconv.FormatInt(int64(v), 10), nil
}

// metadataIntValue 把条件值转为 bigint 比较值：整数类型直转，浮点仅接受
// 整数值。
func metadataIntValue(v any) (int64, error) {
	switch value := v.(type) {
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case int64:
		return value, nil
	case uint:
		return int64(value), nil
	case uint8:
		return int64(value), nil
	case uint16:
		return int64(value), nil
	case uint32:
		return int64(value), nil
	case uint64:
		if value > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("metadata numeric value %d overflows int64", value)
		}
		return int64(value), nil
	case float32:
		return metadataIntValueFloat(float64(value))
	case float64:
		return metadataIntValueFloat(value)
	default:
		return 0, fmt.Errorf("metadata numeric comparison requires an integer, got %T", v)
	}
}

func metadataIntValueFloat(v float64) (int64, error) {
	if v != float64(int64(v)) {
		return 0, fmt.Errorf("metadata numeric comparison requires an integer, got %v", v)
	}
	return int64(v), nil
}

func (p *vectorFilterPlan) where() string {
	if len(p.clauses) == 0 {
		return ""
	}
	return " AND " + strings.Join(p.clauses, " AND ")
}

// checkDocument 校验写侧入参：ID 必填，嵌入空间声明经注册表对账
// （先于事务给出明确错误，不落到数据库的 CHECK 断言），返回注册表
// 维度供落库 embed_dim 列。
func (r *vectorStore) checkDocument(doc *biz.VectorDocument) (int32, error) {
	if doc == nil {
		return 0, errors.New("pg_sqlc: vector document is required")
	}
	if strings.TrimSpace(doc.ID) == "" {
		return 0, errors.New("pg_sqlc: vector document id is required")
	}
	return r.resolveSpace(doc.Embedder, doc.Dimension, len(doc.Embedding), "vector document "+doc.ID)
}

// disabledVectorStore 是设施未启用时的占位实现：所有调用按设施缺失
// 拒绝（fail-closed），消费方拿到的是可用接口值而非 nil，调用错误语义
// 明确、不会 panic。
type disabledVectorStore struct{}

var _ biz.VectorStore = (*disabledVectorStore)(nil)

func (*disabledVectorStore) Add(context.Context, *biz.VectorDocument) error {
	return biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) Get(context.Context, string) (*biz.VectorDocument, error) {
	return nil, biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) Update(context.Context, *biz.VectorDocument) error {
	return biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) Delete(context.Context, string) error {
	return biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) Search(context.Context, []float32, *biz.VectorFilter) ([]*biz.ScoredVectorDocument, error) {
	return nil, biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) DeleteByFilter(context.Context, *biz.VectorFilter) (int64, error) {
	return 0, biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) UpdateMetadata(context.Context, *biz.VectorFilter, map[string]any) (int64, error) {
	return 0, biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) Count(context.Context, *biz.VectorFilter) (int64, error) {
	return 0, biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) GetMetadata(context.Context, *biz.VectorFilter) (map[string]map[string]any, error) {
	return nil, biz.ErrVectorStoreDisabled
}
