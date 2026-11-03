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

// 向量存储设施（biz.VectorStore 的 PostgreSQL 实现）。存取对象是
// agent.agent_document（见 sqlc/agent_vector.sql）：pgvector 类型与相似
// 度算子超出 sqlc catalog 的支持面，SQL 以手写参数化常量维护于此，
// 不进 sqlc 生成（治理口径见 sqlc/README.md）。
//
// 空间模型：一表承载多嵌入空间——embedding 列无固定维度（裸 vector），
// embedder/embed_dim 列逐行声明归属（DB 层 CHECK 把维度列与实际向量
// 长度绑定），检索按空间键等值 + 维度谓词命中对应维度的表达式部分
// HNSW 索引。空间注册表（键→维度）经 conf.VectorSpaces 单一解析源
// 构建：写侧与检索侧的声明都要过注册表对账（调用方声明不可伪造），
// 检索 SQL 的 vector(N) cast 以校验后的维度内联（typmod 不可参数化，
// 与表名白名单同纪律：进 SQL 文本的只有服务端产出的整数）。
//
// 租户隔离（应用层强制执行点）：全部方法从认证 ctx 提取身份，缺身份
// 直接拒绝；写入侧身份落租户列并冗余进 metadata，读侧每条 SQL 强制
// tenant_id 过滤（表无 RLS，隔离不依赖数据库策略存在）。过滤值一律走
// 占位符参数，唯一进入 SQL 文本的配置值是表名与校验后的维度常量
// （构造期按标识符白名单校验）。
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

// vectorStore 是向量存储的介质实现。
type vectorStore struct {
	data   *pgsqlc.Data
	table  string              // 已过白名单校验的表名（agent schema 内）
	spaces map[string]int32    // 空间键 → 声明维度（conf.VectorSpaces 单一解析源）
	dims   map[int32]struct{}  // 注册表内去重维度集合（启动探针逐维度核索引）
}

var _ biz.VectorStore = (*vectorStore)(nil)

// NewVectorStore 构造向量存储设施。conf.vector.enabled=false 时返回禁用
// 占位实现（所有调用按设施缺失拒绝，不触库）；启用时执行装配期校验并
// fail-fast：空间注册表合法（键唯一、维度合法、default 引用已声明键）、
// 表名过标识符白名单、启动探针确认部署表为多空间形状（裸 vector 列 +
// embedder/embed_dim 列）且每个声明维度存在匹配的表达式部分 HNSW
// 索引——配置与部署失配启动即失败。
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
	r := &vectorStore{data: d, table: table, spaces: spaces, dims: dims}
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

// probeIndexes 核对每个声明维度的表达式部分 HNSW 索引：indexdef 须含
// `::vector(N)) vector_cosine_ops`（cast 尾段，闭括号锚定数字边界，维度
// 4 不会被维度 40 的定义误配——pg_get_indexdef 以 `(列)::vector(N)` 形态
// 渲染表达式）与 `embed_dim = N)`（部分谓词）——cast 与谓词是检索 SQL
// 命中索引的前提（查询侧重复同一 cast 表达式），缺失即检索退化为顺序
// 扫描，装配期拒绝。维度超过 2000 时 vector ops 不可索引（pgvector
// 上限），需 halfvec 索引变体（见 agent_vector.sql）。
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

// resolveSpace 对账嵌入空间声明：键必填且已声明、维度必填且等于注册
// 表声明、向量长度等于维度——三方一致返回注册表维度（int32，落库
// embed_dim 列）。检索侧复用（查询向量长度即 length）。
func (r *vectorStore) resolveSpace(embedder string, dimension, length int, subject string) (int32, error) {
	if embedder == "" {
		return 0, fmt.Errorf("pg_sqlc: %s must declare an embedder space", subject)
	}
	declared, ok := r.spaces[embedder]
	if !ok {
		return 0, fmt.Errorf("%w: %q is not declared in conf.vector.embedders", biz.ErrEmbedderUnknown, embedder)
	}
	if dimension <= 0 {
		return 0, fmt.Errorf("pg_sqlc: %s must declare a positive dimension", subject)
	}
	if dimension != int(declared) {
		return 0, fmt.Errorf("pg_sqlc: %s declares dimension %d for space %s, registry declares %d", subject, dimension, embedder, declared)
	}
	if length != dimension {
		return 0, fmt.Errorf("pg_sqlc: %s embedding length %d does not match declared dimension %d for space %s",
			subject, length, dimension, embedder)
	}
	return declared, nil
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

	plan, err := planVectorFilter(4, filter)
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
// embedder，一并原样返回给调用方（框架侧过滤语义可回读）。
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
	return plan, nil
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

func (*disabledVectorStore) Count(context.Context, *biz.VectorFilter) (int64, error) {
	return 0, biz.ErrVectorStoreDisabled
}

func (*disabledVectorStore) GetMetadata(context.Context, *biz.VectorFilter) (map[string]map[string]any, error) {
	return nil, biz.ErrVectorStoreDisabled
}
