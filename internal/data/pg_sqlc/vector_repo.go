package pg_sqlc

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

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// 向量存储设施（biz.VectorStore 的 PostgreSQL 实现）。存取对象是
// agent.agent_document（见 sqlc/agent_vector.sql）：pgvector 类型与相似
// 度算子超出 sqlc catalog 的支持面，SQL 以手写参数化常量维护于此，
// 不进 sqlc 生成（治理口径见 sqlc/README.md）。
//
// 租户隔离（决策 6 的强制执行点）：全部方法从认证 ctx 提取身份，缺身份
// 直接拒绝；写入侧身份落租户列并冗余进 metadata，读侧每条 SQL 强制
// tenant_id 过滤（表无 RLS，隔离不依赖数据库策略存在）。过滤值一律走
// 占位符参数，唯一进入 SQL 文本的配置值是表名（构造期按标识符白名单
// 校验）。embedding 维度在构造期与部署列探针对账，不一致拒绝启动。
const (
	// vectorSchema 是 agent 域设施的固定 schema（与框架会话表同域）。
	vectorSchema = "agent"
	// vectorDefaultTable 是 conf.vector.table_name 留空时的默认表名，
	// 与 agent_vector.sql 的建表一致。
	vectorDefaultTable = "agent_document"
	// vectorTablePrefix 是 agent 域对象的统一表名前缀（TODO 决策 3），
	// conf.vector.table_name 在构造期强制。
	vectorTablePrefix = "agent_"
	// vectorDefaultLimit 是 Search 未指定 Limit 时的默认结果数，与框架
	// pgvector 实现的默认一致（适配层语义不漂移）。
	vectorDefaultLimit = 10
	// vectorMaxDimension 是 pgvector 单列维度上限（类型系统硬上限）。
	vectorMaxDimension = 16000

	vectorColumns = `id, tenant_id, user_id, name, content, embedding, metadata, created_at, updated_at`

	// vectorInsertColumns 是写入列清单：created_at/updated_at 由列默认值
	// 生成，created_by/updated_by 复用 $3（认证用户）。
	vectorInsertColumns = `id, tenant_id, user_id, name, content, embedding, metadata, created_by, updated_by`

	vectorInsertSQL = `INSERT INTO %s (` + vectorInsertColumns + `)
VALUES ($1, $2, $3, $4, $5, $6, $7, $3, $3)`

	vectorSelectSQL = `SELECT ` + vectorColumns + `
FROM %s WHERE tenant_id = $1 AND id = $2`

	vectorUpdateSQL = `UPDATE %s SET name = $3, content = $4, embedding = $5, metadata = $6, updated_by = $7, updated_at = CURRENT_TIMESTAMP
WHERE tenant_id = $1 AND id = $2`

	vectorDeleteSQL = `DELETE FROM %s WHERE tenant_id = $1 AND id = $2`

	vectorDeleteByFilterSQL = `WITH deleted AS (DELETE FROM %s WHERE tenant_id = $1%s RETURNING 1)
SELECT count(*) FROM deleted`

	vectorCountSQL = `SELECT count(*) FROM %s WHERE tenant_id = $1%s`

	// GetMetadata 的排序键：created_at 是 timestamp(0) 秒级精度，同秒
	// 写入必须以 id tie-break，否则 LIMIT/OFFSET 翻页不确定；排序键与
	// agent_document_tenant_created_idx（租户前缀）对齐。
	vectorMetadataSQL = `SELECT id, metadata FROM %s WHERE tenant_id = $1%s ORDER BY created_at, id`

	// vectorScoreExpr 是归一化相似度：余弦距离（<=>）映射到 [0,1]，与
	// 框架 pgvector 实现的分数语义一致；占位符是查询向量。
	vectorScoreExpr = `1.0 - (embedding <=> $%d::vector) / 2.0`

	// vectorProbeSQL 读 embedding 列的声明类型：format_type 产出
	// "vector(N)"，维度随类型而非数据存在（空表可校验）。
	vectorProbeSQL = `SELECT format_type(a.atttypid, a.atttypmod)
FROM pg_attribute a
WHERE a.attrelid = to_regclass($1) AND a.attname = 'embedding' AND a.attisdropped = false`
)

// vectorTableNamePattern 是表名标识符白名单：小写字母开头的下划线数字
// 组合，63 字符内（PostgreSQL 标识符上限）。表名是唯一拼进 SQL 文本的
// 配置值，白名单在构造期拒绝注入形态。
var vectorTableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// vectorStore 是向量存储的介质实现。
type vectorStore struct {
	data      *Data
	table     string // 已过白名单校验的表名（agent schema 内）
	dimension int32
}

var _ biz.VectorStore = (*vectorStore)(nil)

// NewVectorStore 构造向量存储设施。conf.vector.enabled=false 时返回禁用
// 占位实现（所有调用按设施缺失拒绝，不触库）；启用时执行装配期校验并
// fail-fast：维度在 (0, 16000] 内、表名过标识符白名单、启动探针确认
// 部署表存在且 embedding 列维度与配置一致——维度配置错误启动即失败。
func NewVectorStore(c *conf.Vector, d *Data) (biz.VectorStore, func(), error) {
	if !c.GetEnabled() {
		return &disabledVectorStore{}, func() {}, nil
	}
	dimension := c.GetDimension()
	if dimension <= 0 || dimension > vectorMaxDimension {
		return nil, nil, fmt.Errorf("pg_sqlc: vector dimension must be in (0, %d], got %d", vectorMaxDimension, dimension)
	}
	table := c.GetTableName()
	if table == "" {
		table = vectorDefaultTable
	}
	if !vectorTableNamePattern.MatchString(table) {
		return nil, nil, fmt.Errorf("pg_sqlc: vector table name %q must match %s", table, vectorTableNamePattern)
	}
	// agent_ 前缀强制（TODO 决策 3：agent 域对象统一前缀，与 conf.proto
	// 注释承诺一致），前缀缺失在构造期拒绝。
	if !strings.HasPrefix(table, vectorTablePrefix) {
		return nil, nil, fmt.Errorf("pg_sqlc: vector table name %q must have the %s prefix", table, vectorTablePrefix)
	}
	r := &vectorStore{data: d, table: table, dimension: dimension}
	if err := r.probeDimension(context.Background()); err != nil {
		return nil, nil, err
	}
	return r, func() {}, nil
}

// probeDimension 启动探针：部署表必须存在且 embedding 列维度与配置一致。
// 池直连只读 pg_catalog（无业务数据、无租户上下文），配置错表/错维度
// 在装配期失败，不带病启动。
func (r *vectorStore) probeDimension(ctx context.Context) error {
	if r.data == nil || r.data.db == nil {
		return errors.New("pg_sqlc: vector probe requires an initialized database handle")
	}
	qualified := vectorSchema + "." + r.table
	var columnType *string
	err := r.data.db.QueryRow(ctx, vectorProbeSQL, qualified).Scan(&columnType)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("pg_sqlc: vector table %s is not deployed (embedding column missing); run agent_vector.sql first", qualified)
	}
	if err != nil {
		return fmt.Errorf("pg_sqlc: probe vector table %s: %w", qualified, err)
	}
	if columnType == nil {
		return fmt.Errorf("pg_sqlc: vector table %s is not deployed (embedding column missing); run agent_vector.sql first", qualified)
	}
	declared := *columnType
	dimension, err := vectorDeclaredDimension(declared)
	if err != nil {
		return fmt.Errorf("pg_sqlc: vector table %s: %w", qualified, err)
	}
	if dimension != r.dimension {
		return fmt.Errorf("pg_sqlc: vector dimension mismatch: conf %d, table %s embedding %d; align conf.vector.dimension with agent_vector.sql",
			r.dimension, qualified, dimension)
	}
	return nil
}

// vectorDeclaredDimension 从 format_type 产出（"vector(1024)"）解析维度。
func vectorDeclaredDimension(declared string) (int32, error) {
	if !strings.HasPrefix(declared, "vector(") || !strings.HasSuffix(declared, ")") {
		return 0, fmt.Errorf("unexpected embedding column type %s", declared)
	}
	dimension, err := strconv.ParseInt(declared[len("vector("):len(declared)-1], 10, 32)
	if err != nil || dimension <= 0 {
		return 0, fmt.Errorf("unexpected embedding column type %s", declared)
	}
	return int32(dimension), nil
}

// vectorIdentity 从认证 ctx 提取身份：缺失或非法（空租户/非正用户）一律
// 拒绝。身份只来自服务端认证中间件，永不受请求侧输入影响。
func vectorIdentity(ctx context.Context) (Identity, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return Identity{}, errors.New("pg_sqlc: vector store requires an authenticated identity")
	}
	auth := pgAuth(id)
	if err := auth.validate(); err != nil {
		return Identity{}, err
	}
	return auth, nil
}

// vectorMetadata 把认证身份写进文档 metadata 副本：tenant_id/user_id 以
// 服务端身份为准覆盖同名键（请求侧伪造无效），调用方自定义键保留。列
// 与 metadata 双写：列供 SQL 强制过滤，metadata 供框架侧过滤语义回读。
func vectorMetadata(metadata map[string]any, auth Identity) map[string]any {
	out := make(map[string]any, len(metadata)+2)
	for k, v := range metadata {
		out[k] = v
	}
	out["tenant_id"] = auth.TenantID
	out["user_id"] = auth.UserID
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

// checkDocument 校验写侧入参：ID 必填，embedding 非空且维度与部署列
// 一致（先于事务给出明确错误，不落到数据库的维度断言）。
func (r *vectorStore) checkDocument(doc *biz.VectorDocument) error {
	if doc == nil {
		return errors.New("pg_sqlc: vector document is required")
	}
	if strings.TrimSpace(doc.ID) == "" {
		return errors.New("pg_sqlc: vector document id is required")
	}
	switch {
	case len(doc.Embedding) == 0:
		return fmt.Errorf("pg_sqlc: vector document %s has an empty embedding", doc.ID)
	case int32(len(doc.Embedding)) != r.dimension:
		return fmt.Errorf("pg_sqlc: vector document %s embedding dimension %d does not match configured %d",
			doc.ID, len(doc.Embedding), r.dimension)
	}
	return nil
}

// Add 写入文档与向量。身份取认证 ctx；重复 ID 报唯一约束冲突。
func (r *vectorStore) Add(ctx context.Context, doc *biz.VectorDocument) error {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return err
	}
	if err := r.checkDocument(doc); err != nil {
		return err
	}
	metadata, err := marshalVectorMetadata(vectorMetadata(doc.Metadata, auth))
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		_, err := s.Tx().Exec(ctx,
			fmt.Sprintf(vectorInsertSQL, r.qualifiedTable()),
			doc.ID, auth.TenantID, auth.UserID, doc.Name, doc.Content,
			pgvector.NewVector(doc.Embedding), metadata)
		return err
	})
}

// Get 按 ID 取文档（含向量与 metadata）。租户过滤在 WHERE 内强制：跨
// 租户与不存在同样落到无行，统一 ErrVectorDocNotFound（不泄露存在性）。
func (r *vectorStore) Get(ctx context.Context, id string) (*biz.VectorDocument, error) {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return nil, err
	}
	var row vectorDocRow
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			fmt.Sprintf(vectorSelectSQL, r.qualifiedTable()),
			auth.TenantID, id).Scan(row.fields()...)
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return row.toBiz()
}

// Update 整体覆盖文档可变部分；0 行（不存在或跨租户）报
// ErrVectorDocNotFound。ID 与归属不可借本方法变更。
func (r *vectorStore) Update(ctx context.Context, doc *biz.VectorDocument) error {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return err
	}
	if err := r.checkDocument(doc); err != nil {
		return err
	}
	metadata, err := marshalVectorMetadata(vectorMetadata(doc.Metadata, auth))
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		tag, err := s.Tx().Exec(ctx,
			fmt.Sprintf(vectorUpdateSQL, r.qualifiedTable()),
			auth.TenantID, doc.ID, doc.Name, doc.Content,
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
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
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

// Search 余弦相似检索：分数与排序同源（ORDER BY 走 HNSW 索引），过滤
// 条件与租户边界取交集。Limit 非正取默认页大小。
func (r *vectorStore) Search(ctx context.Context, embedding []float32, filter *biz.VectorFilter) ([]*biz.ScoredVectorDocument, error) {
	auth, err := vectorIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if len(embedding) == 0 {
		return nil, errors.New("pg_sqlc: search embedding is empty")
	}
	if int32(len(embedding)) != r.dimension {
		return nil, fmt.Errorf("pg_sqlc: search embedding dimension %d does not match configured %d", len(embedding), r.dimension)
	}
	limit := vectorDefaultLimit
	if filter != nil && filter.Limit > 0 {
		limit = filter.Limit
	}
	minScore := 0.0
	if filter != nil {
		minScore = filter.MinScore
	}

	plan, err := planVectorFilter(3, filter)
	if err != nil {
		return nil, err
	}
	args := make([]any, 0, len(plan.args)+3)
	args = append(args, pgvector.NewVector(embedding), auth.TenantID)
	args = append(args, plan.args...)
	where := " WHERE tenant_id = $2" + plan.where()
	if minScore > 0 {
		args = append(args, minScore)
		where += fmt.Sprintf(" AND %s >= $%d", fmt.Sprintf(vectorScoreExpr, 1), len(args))
	}
	args = append(args, limit)
	sql := fmt.Sprintf("SELECT %s, %s AS score FROM %s%s ORDER BY embedding <=> $1::vector LIMIT $%d",
		vectorColumns, fmt.Sprintf(vectorScoreExpr, 1), r.qualifiedTable(), where, len(args))

	var out []*biz.ScoredVectorDocument
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
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

// DeleteByFilter 按 IDs/Metadata 交集删除并返回行数；两个条件均空时
// 拒绝执行（租户级全量清理是 super 管理口径，不设普通入口）。
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
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			fmt.Sprintf(vectorDeleteByFilterSQL, r.qualifiedTable(), plan.where()),
			args...).Scan(&deleted)
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// Count 统计租户内满足过滤条件的文档数；条件为空即租户全量。
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
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		return s.Tx().QueryRow(ctx,
			fmt.Sprintf(vectorCountSQL, r.qualifiedTable(), plan.where()),
			args...).Scan(&total)
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// GetMetadata 按 IDs/Metadata 过滤枚举 metadata，写入时间升序分页；
// Limit 非正为不限量，此时 Offset 必须为 0（半开分页非法组合显式拒绝）。
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
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
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

// vectorDocRow 是 agent_document 的行载体：fields 供 pgx Scan 展开参数。
type vectorDocRow struct {
	ID        string
	TenantID  string
	UserID    int64
	Name      *string
	Content   string
	Embedding pgvector.Vector
	Metadata  []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (row *vectorDocRow) fields() []any {
	return []any{&row.ID, &row.TenantID, &row.UserID, &row.Name, &row.Content,
		&row.Embedding, &row.Metadata, &row.CreatedAt, &row.UpdatedAt}
}

// toBiz 转换为领域文档。metadata 里携带仓储写入的 tenant_id/user_id，
// 一并原样返回给调用方（框架侧过滤语义可回读）。
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
// 符整值下发，键名/值都不参与拼接）。
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
