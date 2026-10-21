# Grape

Go 后端项目。

## 存储体系

关系存储（pg_sqlc）承载多租户 RBAC 数据权限体系（schema 按业务域拆分声明、
RLS 租户隔离、@scope 数据范围守卫、写前判断函数、权限查询判定函数、种子
数据），SQL 对象索引、三层权限模型与部署执行顺序见
[internal/data/pg_sqlc/sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)。

schema 与查询代码由 sqlc 生成，`internal/data/pg_sqlc/sqlc/`（schema/ 九个
域文件 + rls/operate/expose/queries）为单一事实源，`internal/data/pg_sqlc/gen/`
下的生成文件禁止手改。

```powershell
# 在 internal/data/pg_sqlc/sqlc 目录执行（或项目根 make sqlc）
sqlc generate -f sqlc.yaml
```

## 生成内容

| 产物                                                                                 | 来源块              | 说明                                                                                  |
| ------------------------------------------------------------------------------------ | ------------------- | -------------------------------------------------------------------------------------- |
| `gen/models.go` `gen/db.go` `gen/querier.go` `gen/<查询文件>.sql.go`（如 `operate`/`basic`/`perm`/`sys_user`） | sqlc-gen-go（块 1） | PO 模型与固定形态查询（权限判断、角色映射写入、锁行读取、权限查询接口、资源 CRUD 等命名查询），按查询文件逐一产出 |
| `gen/<表名>_dynamic_gen.go`（逐表一个，如 `basic` `sys_user`）                       | sqlc_gen_dynamic 插件（块 2） | 动态 List/Count：Parse / List / Count，能力由 schema 列注释 DSL 声明，@scope 守卫 fail-closed |

修改 `internal/data/pg_sqlc/sqlc/` 下任一 SQL 文件后必须重新执行
`sqlc generate -f sqlc.yaml` 并提交生成物。
