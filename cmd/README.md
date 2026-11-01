# cmd

可执行程序入口，每个子目录一个独立命令，只做装配与入口参数处理，业务实现收敛在 [internal/](../internal/)。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [server/main.go](server/main.go) | 服务主程序：安装 protobuf JSON codec、构建日志，经 Kratos config 加载配置（file source 目录 + env source、`${ENV:default}` 占位展开、Scan 进 `conf.Bootstrap`），由 Wire 装配应用并运行。 |
| [server/wire.go](server/wire.go) | Wire 注入桩（`wireinject` 构建标签，不参与编译）。 |
| [server/wire_gen.go](server/wire_gen.go) | Wire 生成产物，改依赖注入后经 `make generate` 刷新，禁止手改。 |
| [configinit/main.go](configinit/main.go) | 配置初始化命令：把内嵌模板按字节输出为 config.yaml（`--dir` 指定目录、缺目录自动创建、目标已存在默认拒绝、`--force` 覆盖），不解析、不替换 `${ENV:default}` 密钥占位。 |
| [configinit/template/config.yaml](configinit/template/config.yaml) | 配置模板单一事实源，与 [internal/conf/conf.proto](../internal/conf/conf.proto) 对应；conf 结构变更时须同批更新本模板。勿移入 `configs/`：该目录会被 Kratos file source 整体加载，模板与运行配置必须分离。 |
| [dbdeploy/main.go](dbdeploy/main.go) | 数据库部署命令入口：装配部署参数（flag 优先、`GRAPE_DEPLOY_*` 环境变量兜底）并执行 Deploy；用法与口径见根目录 [deploy_db.md](../deploy_db.md)。 |
| [dbdeploy/options.go](dbdeploy/options.go) | 部署参数（管理 DSN、目标库名、两角色口令）的定义、flag/env 解析与校验。 |
| [dbdeploy/deploy.go](dbdeploy/deploy.go) | 部署流程实现：清单校验、幂等建库、单事务按 `DeployFiles` 清单顺序执行、事务内补 `GRANT CONNECT`；口令占位符内存替换，不落日志。 |
| [dbdeploy/manifest_test.go](dbdeploy/manifest_test.go) | 部署清单校验的单元测试：真实清单可通过、缺文件与 CREATE DATABASE / CONCURRENTLY 语句报错、schema 序号连续与关键顺序约束（role 先于 rls、agent 域尾随 seed）。 |
| [dbdeploy/deploy_it_test.go](dbdeploy/deploy_it_test.go) | 部署全量集成测试（`GRAPE_TEST_ADMIN_DSN` 门控跳过）：真实库部署往返、重复执行的角色/种子幂等边界；与 pg_sqlc 集成套件不并行（角色是集群级共享对象）。 |

## 用法

- 初始化配置：`make config-init`（默认输出到当前目录），或 `go run ./cmd/configinit --dir <目录> [--force]`。
- 运行服务：`go run ./cmd/server -conf <目录>`，数据源等敏感项在输出的 config.yaml 中以 `${ENV:default}` 占位，由 Kratos 加载时展开（env source 取 `KRATOS_` 前缀环境变量，如 `KRATOS_DATABASE_SOURCE`；未注入时回落占位内默认值）。
- 数据库部署：`make dbdeploy ARGS="--database <库名>"`（或 `go run ./cmd/dbdeploy`），执行顺序、幂等边界与升级步骤见根目录 [deploy_db.md](../deploy_db.md)。
