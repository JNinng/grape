# 测试指南

测试与被测代码同目录（`*_test.go` / `*.test.ts`），按层隔离：`service` 用假
仓储隔离传输适配，`biz` mock 仓储接口只验证用例，`data` 以真实 PostgreSQL
集成测试验证存储边界。集成测试由 `GRAPE_*_TEST_*` 环境变量门控，未设置时
自动跳过。

## 测试库机制

- 集成测试库由模板库 `grape_test_tpl` 统一派生：全量部署清单只在模板库
  装载一次（指纹 = 清单内容 + 顺序 + PG 版本，任一变化自动重建）。
- 每个测试从模板克隆独立库（`grape_t_<测试名哈希>`），测试并行运行、
  结束即删库；SQL 变更无须手工清理模板。
- 要求：PostgreSQL 13+（`DROP DATABASE ... WITH (FORCE)`），管理员 DSN
  具备建库权限，`pgvector` 扩展可用（模板装载含向量设施）。
- 前提：同一集群不并发运行不同代码版本的测试套件。

## 前置环境变量

管理员 DSN 指向具备建库权限的超级用户连接（维护连接的库名由测试改写为
postgres / 模板库 / 专属库，DSN 内库名不参与）；测试 DSN 以运行角色
`grape_u` 连接（库名不重要，测试会自动改写为克隆库）。各集成测试面共用
同一集群时，管理员与测试 DSN 各设一份即可复用。

| 变量 | 用途 |
|---|---|
| `GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN` | 根包（会话/用户/租户/RBAC/配置等） |
| `GRAPE_VECTOR_TEST_ADMIN_DSN` / `GRAPE_VECTOR_TEST_DSN` | 向量介质与词法/重摄 |
| `GRAPE_AUDIT_TEST_ADMIN_DSN` / `GRAPE_AUDIT_TEST_DSN` | Agent 审计 |
| `GRAPE_RETRIEVAL_TEST_ADMIN_DSN` / `GRAPE_RETRIEVAL_TEST_DSN` | 检索留痕 |
| `GRAPE_MODEL_CALL_TEST_ADMIN_DSN` / `GRAPE_MODEL_CALL_TEST_DSN` | 模型调用留痕 |
| `GRAPE_OSS_TEST_ADMIN_DSN` / `GRAPE_OSS_TEST_DSN` | 内嵌 OSS |
| `GRAPE_SESSION_TEST_ADMIN_DSN` | Agent 会话存储（专属库 `grape_agent_session_test`） |
| `GRAPE_CONFIRM_TEST_ADMIN_DSN` | Agent 确认恢复（专属库 `grape_agent_confirm_test`） |
| `GRAPE_PERMISSION_TEST_ADMIN_DSN` | Agent 权限审计（专属库 `grape_agent_permission_test`） |
| `GRAPE_GRANT_TEST_ADMIN_DSN` | Agent 授权 API（专属库 `grape_grant_test`） |
| `GRAPE_SEARCH_TEST_ADMIN_DSN` | Agent 文档检索工具（专属库 `grape_search_test`） |

成对出现的六个变量走模板克隆路径，测试库结束即删；仅设管理员 DSN 的五个
Agent 侧套件经 testsupport.SuiteDatabase 维护专属库：grape_u 连接串由
管理员 DSN 改写派生（须 URL 形态），每轮重装全清单、不走模板克隆。

角色与口令约定（role.sql 占位符由测试装载时替换为代码内固定口令）：

- `grape_u` 测试口令固定为 `grape_u_pwd`：干净集群上角色不存在时按该
  口令自动创建；已有同名角色时 role.sql 不改写口令。集群口令不一致时，
  模板路径可改测试 DSN 的口令段适配，Agent 侧套件固定派生该口令、无法
  绕过——统一 `ALTER ROLE grape_u PASSWORD 'grape_u_pwd'` 对齐。
- `test_u` 不被测试连接，仅随清单创建（固定口令 `test_u_pwd`，已存在
  则跳过）。
- 自查（管理员连接执行）：`SELECT rolname FROM pg_roles WHERE rolname
  IN ('grape_u', 'test_u')` 确认角色就绪；`SELECT name FROM
  pg_available_extensions WHERE name = 'vector'` 确认扩展可用。

## Windows（PowerShell）

```powershell
# 环境变量（按本地集群实际值修改）
$admin = 'postgres://root:<密码>@127.0.0.1:5432/postgres'
$user  = 'postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape'
$env:GRAPE_TEST_ADMIN_DSN           = $admin; $env:GRAPE_TEST_DSN           = $user
$env:GRAPE_VECTOR_TEST_ADMIN_DSN    = $admin; $env:GRAPE_VECTOR_TEST_DSN    = $user
$env:GRAPE_AUDIT_TEST_ADMIN_DSN     = $admin; $env:GRAPE_AUDIT_TEST_DSN     = $user
$env:GRAPE_RETRIEVAL_TEST_ADMIN_DSN = $admin; $env:GRAPE_RETRIEVAL_TEST_DSN = $user
$env:GRAPE_MODEL_CALL_TEST_ADMIN_DSN = $admin; $env:GRAPE_MODEL_CALL_TEST_DSN = $user
$env:GRAPE_OSS_TEST_ADMIN_DSN       = $admin; $env:GRAPE_OSS_TEST_DSN       = $user
$env:GRAPE_SESSION_TEST_ADMIN_DSN   = $admin
$env:GRAPE_CONFIRM_TEST_ADMIN_DSN   = $admin
$env:GRAPE_PERMISSION_TEST_ADMIN_DSN = $admin
$env:GRAPE_GRANT_TEST_ADMIN_DSN     = $admin
$env:GRAPE_SEARCH_TEST_ADMIN_DSN    = $admin

# 交付前检查
go vet ./...
go test -count=1 ./...

# 仅 data 层集成测试（约 3 分钟，并行）
go test -count=1 ./internal/data/...

# 只跑某个包 / 某个用例
go test -count=1 ./internal/data/pg_sqlc/deploy
go test -count=1 -run TestSessionDiscipline ./internal/data/pg_sqlc

# PG 连接压力大时下调并行度（默认跟随 GOMAXPROCS）
go test -count=1 -parallel 8 ./internal/data/...

# 前端
cd web; npm test; npm run lint; npm run build
```

## Linux（bash）

```bash
# 环境变量（按本地集群实际值修改）
export GRAPE_TEST_ADMIN_DSN='postgres://root:<密码>@127.0.0.1:5432/postgres'
export GRAPE_TEST_DSN='postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape'
export GRAPE_VECTOR_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_VECTOR_TEST_DSN="$GRAPE_TEST_DSN"
export GRAPE_AUDIT_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_AUDIT_TEST_DSN="$GRAPE_TEST_DSN"
export GRAPE_RETRIEVAL_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_RETRIEVAL_TEST_DSN="$GRAPE_TEST_DSN"
export GRAPE_MODEL_CALL_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_MODEL_CALL_TEST_DSN="$GRAPE_TEST_DSN"
export GRAPE_OSS_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_OSS_TEST_DSN="$GRAPE_TEST_DSN"
export GRAPE_SESSION_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_CONFIRM_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_PERMISSION_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_GRANT_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"
export GRAPE_SEARCH_TEST_ADMIN_DSN="$GRAPE_TEST_ADMIN_DSN"

# 交付前检查
go vet ./...
go test -count=1 ./...

# 仅 data 层集成测试
go test -count=1 ./internal/data/...

# 只跑某个包 / 某个用例
go test -count=1 ./internal/data/pg_sqlc/deploy
go test -count=1 -run TestSessionDiscipline ./internal/data/pg_sqlc

# 下调并行度
go test -count=1 -parallel 8 ./internal/data/...

# 前端
(cd web && npm test && npm run lint && npm run build)
```

## 说明

- 未设置对应环境变量时集成测试打印 skip 信息后跳过，纯单测不受影响；
  因此 `go test ./...` 通过不代表集成面全部执行，交付前须确认上表变量
  均已设置。
- 模板克隆路径的测试库结束即删；模板库 `grape_test_tpl` 保留供复用，
  集群上可用 `shobj_description(oid,'pg_database')` 查看其失效指纹。
  Agent 侧专属库保留到下一轮重装，无须手工清理。
- 测试中断（进程被杀）可能遗留 `grape_t_*` 库：下次模板失效重建时按
  前缀自动清理，或手动 `DROP DATABASE grape_t_xxx WITH (FORCE)`。
- CI 无本地集群时，可在 job 内用 service 容器起 PostgreSQL 并注入上述
  变量；未注入则集成测试全部跳过，须注意这不等价于完整验证。
