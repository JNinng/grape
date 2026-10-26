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

## 用法

- 初始化配置：`make config-init`（默认输出到当前目录），或 `go run ./cmd/configinit --dir <目录> [--force]`。
- 运行服务：`go run ./cmd/server -conf <目录>`，数据源等敏感项在输出的 config.yaml 中以 `${ENV:default}` 占位，由 Kratos 加载时展开（env source 取 `KRATOS_` 前缀环境变量，如 `KRATOS_DATABASE_SOURCE`；未注入时回落占位内默认值）。
