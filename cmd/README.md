# cmd

可执行程序入口：`server` 是唯一服务二进制，默认运行业务服务，并以子命令
形式附带 `version` / `configinit` / `secretctl` 运维命令（实现收在
[internal/cmd/](../internal/cmd/README.md)，本目录只留分发与装配）；
`dbdeploy` 是独立的数据库部署工具。入口目录只做装配与入口参数处理，
业务实现收敛在 [internal/](../internal/README.md)。命令之间共享的事实
（SQL 部署顺序、分层流向）不在本目录复述，按文末链接取用。

## 关键约束

- `server/wire_gen.go` 是 Wire 生成产物，改依赖注入后执行
  `make generate` 刷新，禁止手改。
- 子命令分发收在 `server/main.go` 的 `run`：无参数或首参为 flag（如
  `-conf`）时按 serve 处理，历史用法 `go run ./cmd/server -conf <目录>`
  保持不变；serve 路径的配置/装配失败直接 panic（fail-fast），子命令以
  退出码与 stderr 文本表达失败。
- configinit 的模板是配置结构的单一事实源，与
  [internal/conf/conf.proto](../internal/conf/conf.proto) 对应；模板正文
  须与 [configs/config.yaml](../configs/config.yaml) 逐字节一致（模板仅多
  头部说明注释），conf 变更时同批更新两者。
- dbdeploy 不宣称全量幂等：角色（判存）与种子
  （`ON CONFLICT DO NOTHING`）可重放，结构 DDL 为裸创建、对已部署库重复
  执行报错；全量重建须先手动 `DROP DATABASE`。角色是集群级共享对象，
  并发部署互斥。
- 部署口令只经 `GRAPE_DEPLOY_*` 环境变量或 flag 传入，占位符在内存中
  替换，仓库、日志与错误文本均不落明文。
- 实例身份的映射（`auth.default_tenant_id`）收在装配点，biz 与 service
  不导入 `conf`。
- 版本号、commit、构建时间经 `go build -ldflags -X` 注入
  [pkg/version](../pkg/version/version.go)（契约见该包），`version` 子命令
  与 Kratos 元信息统一取自该包；`make build` 负责注入。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [server/main.go](server/main.go) | 服务主程序：子命令分发（serve 为默认，实现见 internal/cmd/），装配 protobuf JSON codec、加载配置、构建日志后经 Wire 装配运行。 |
| [server/wire.go](server/wire.go) | Wire 注入桩，`wireinject` 构建标签使其不参与最终编译。 |
| [server/wire_gen.go](server/wire_gen.go) | Wire 生成产物，只经 `make generate` 刷新。 |
| [dbdeploy/main.go](dbdeploy/main.go) | 数据库部署命令入口：解析参数并执行 Deploy。 |
| [dbdeploy/options.go](dbdeploy/options.go) | 部署参数定义与 flag/env 解析校验。 |
| [dbdeploy/deploy.go](dbdeploy/deploy.go) | 部署流程：校验清单、幂等建库、单事务按清单执行并补 CONNECT 授权。 |

补充要点：

- 配置加载先于日志装配：日志落盘路径来自 `log.file`，未配置时只输出
  标准输出；配置加载期无日志依赖，失败直接 panic。
- GOMAXPROCS 的容器配额修正（automaxprocs）只在 serve 路径执行，
  `version` / `configinit` / `secretctl` 短命子命令不校正也不输出
  maxprocs 日志。
- dbdeploy 建库阶段固定连接 `postgres` 库，DSN 中的库名部分被忽略
  （`CREATE DATABASE` 不能入事务）；建库完成后再连目标库、在单事务内
  执行清单。管理员 DSN 与两角色口令缺失或库名非法时报错退出。

## 用法

- 运行服务：`go run ./cmd/server -conf <目录>`（默认即 serve，也可显式
  `go run ./cmd/server serve -conf <目录>`）。
- 初始化配置：`make config-init ARGS="-dir <目录>"`，或
  `go run ./cmd/server configinit -dir <目录> [-force]`。
- 密钥运维：`make secretctl ARGS="algorithms"` 列出算法标识；
  `make secretctl ARGS="keygen AES-256-GCM"` 生成登记表材料。密钥只写
  stdout，不落日志、不写文件；未知算法标识报错退出且不输出密钥。
- 构建信息：`make version`，或 `go run ./cmd/server version`。
- 数据库部署：`make dbdeploy ARGS="--database <库名>"`。执行顺序与
  幂等边界见 [deploy_db.md](../deploy_db.md)。

## 测试

- 子命令分发（server/main_test.go）：未知子命令拒绝退出、help 输出用法、
  version 输出包含全部构建字段、configinit / secretctl 正确路由、首参为
  flag 时回落 serve 且非法 flag 在参数解析期被拒。
- 子命令实现（configinit / secretctl / version）的语义测试见
  [internal/cmd/README.md](../internal/cmd/README.md) 的「测试」一节。
- dbdeploy 参数：flag 优先于环境变量、库名格式校验、缺项报错文案。
- 部署清单校验：真实清单通过、缺文件与 `CREATE DATABASE` /
  `CONCURRENTLY` 语句被拒、schema 序号连续与关键顺序约束。
- 真实库部署（`GRAPE_TEST_ADMIN_DSN` 门控）：全新库往返、生产种子只含
  平台自举数据、角色/种子重放幂等与结构 DDL 重复执行失败、口令不泄漏。
  本套件与 pg_sqlc 集成套件都创建集群级角色，不得并行运行。
