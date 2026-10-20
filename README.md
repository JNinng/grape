# Grape

Go 后端项目。

## 数据层生成命令

关系存储（pg_sqlc）的 schema 与查询代码由 sqlc 生成，schema.sql（DDL + 注释 DSL）为单一事实源，`internal/data/pg_sqlc/gen/` 下的生成文件禁止手改。

```powershell
# 在 internal/data/pg_sqlc/sqlc 目录执行
sqlc generate -f sqlc.yaml
```

## 生成内容

| 产物                                                              | 来源块          | 说明                                       |
| ----------------------------------------------------------------- | --------------- | ------------------------------------------ |
| `gen/models.go` `gen/db.go` `gen/queries.sql.go` `gen/querier.go` | sqlc-gen-go（块 1） | PO 模型与固定形态 CRUD（Get/Create/Update/Delete…） |
| `gen/users_dynamic_gen.go` `gen/orders_dynamic_gen.go`            | sqlc_gen_dynamic 插件（块 2） | 动态 List/Count：Parse / List / Count，能力由 schema.sql 列注释 DSL 声明 |

修改 `internal/data/pg_sqlc/sqlc/schema.sql` 或 `internal/data/pg_sqlc/sqlc/queries.sql` 后必须重新执行 `sqlc generate -f sqlc.yaml` 并提交生成物。
