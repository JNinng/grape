# Grape

Go 后端项目。

## 存储体系

关系存储（pg_sqlc）承载多租户 RBAC 数据权限体系（schema 按业务域拆分声明、
RLS 租户隔离、@scope 数据范围守卫、写前判断函数、权限查询判定函数、种子
数据）。权限约束的落点（租户边界 / 行级数据范围 / 功能权限码）、读写形态
与错误语义见 [permission.md](permission.md)；SQL 对象索引与三层权限模型见
[internal/data/pg_sqlc/sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)，
部署执行顺序见 [deploy_db.md](deploy_db.md)。

查询代码由 sqlc 依据 `internal/data/pg_sqlc/sqlc/`（schema/ 九个域文件 +
rls/operate/expose/queries/seed）生成，该目录是单一事实源；`internal/data/pg_sqlc/gen/`
下的生成文件禁止手改。

```powershell
# 在 internal/data/pg_sqlc/sqlc 目录执行（或项目根 make sqlc）
sqlc generate -f sqlc.yaml
```

## 生成内容

| 产物                                                                                 | 来源块              | 说明                                                                                  |
| ------------------------------------------------------------------------------------ | ------------------- | -------------------------------------------------------------------------------------- |
| `gen/models.go` `gen/db.go` `gen/querier.go` `gen/<查询文件>.sql.go`（如 `operate`/`basic`/`perm`/`sys_user`/`sys_session`） | sqlc-gen-go（块 1） | PO 模型与固定形态查询（权限判断、角色映射写入、锁行读取、权限查询接口、资源 CRUD 等命名查询），按查询文件逐一产出 |
| `gen/<表名>_dynamic_gen.go`（按表生成，仅声明了 `@dynamic` 的表，如 `basic`/`sys_user`）                       | sqlc_gen_dynamic 插件（块 2） | 动态 List/Count：Parse / List / Count，能力由 schema 列注释 DSL 声明，@scope 守卫 fail-closed |

修改 sqlc 输入（`schema/`、`rls/operate/expose`、`queries/`）后必须重新执行
`sqlc generate -f sqlc.yaml` 并提交生成物；`seed.sql` 不参与生成，改动按部署流程执行。
