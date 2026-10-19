# Grape

Go 后端项目。

## 数据层生成命令

schema 与查询代码由 sqlc 生成，schema.sql（DDL + 注释 DSL）为单一事实源，`internal/data/db_sqlc/` 下的生成文件禁止手改。

```powershell
# 在 internal/data 目录执行
sqlc generate -f sqlc.yaml
```

## 生成内容

| 产物                          | 来源块          | 说明                                       |
| ----------------------------- | --------------- | ------------------------------------------ |
| `db_sqlc/models.go` `db_sqlc/db.go` `db_sqlc/queries.sql.go` `db_sqlc/querier.go` | sqlc-gen-go（块 1） | PO 模型与固定形态 CRUD（Get/Create/Update/Delete…） |
| `db_sqlc/users_dynamic_gen.go` `db_sqlc/orders_dynamic_gen.go` | sqlc_gen_dynamic 插件（块 2） | 动态 List/Count：Parse / List / Count，能力由 schema.sql 列注释 DSL 声明 |

修改 `internal/data/schema.sql` 或 `internal/data/queries.sql` 后必须重新执行 `sqlc generate -f sqlc.yaml` 并提交生成物。
