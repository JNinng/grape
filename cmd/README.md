# cmd

可执行程序入口：每个子目录一个独立命令，只做装配与入口参数处理，
业务实现收敛在 [internal/](../internal/README.md)。命令之间共享的事实
（SQL 部署顺序、升级步骤、分层流向）不在本目录复述，按文末链接取用。

## 关键约束

- `server/wire_gen.go` 是 Wire 生成产物，改依赖注入后执行
  `make generate` 刷新，禁止手改。
- configinit 的模板是配置结构的单一事实源，与
  [internal/conf/conf.proto](../internal/conf/conf.proto) 对应；模板正文
  须与 [configs/config.yaml](../configs/config.yaml) 逐字节一致（模板仅多
  头部说明注释），conf 变更时同批更新两者。
- 模板必须留在 `cmd/configinit/template/`，**勿移入 `configs/`**：该目录
  会被 Kratos file source 整体加载，模板与运行配置必须分离。
- dbdeploy 不宣称全量幂等：角色（判存）与种子
  （`ON CONFLICT DO NOTHING`）可重放，结构 DDL 为裸创建、对已部署库重复
  执行报错；全量重建须先手动 `DROP DATABASE`。角色是集群级共享对象，
  并发部署互斥。
- 部署口令只经 `GRAPE_DEPLOY_*` 环境变量或 flag 传入，占位符在内存中
  替换，仓库、日志与错误文本均不落明文。
- 实例身份的映射（`auth.default_tenant_id`）收在装配点，biz 与 service
  不导入 `conf`。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [server/main.go](server/main.go) | 服务主程序：装 protobuf JSON codec、加载配置、构建日志，经 Wire 装配后运行。 |
| [server/wire.go](server/wire.go) | Wire 注入桩，`wireinject` 构建标签使其不参与最终编译。 |
| [server/wire_gen.go](server/wire_gen.go) | Wire 生成产物，只经 `make generate` 刷新。 |
| [configinit/main.go](configinit/main.go) | 配置初始化命令：内嵌模板按字节写入目标目录，不展开 `${ENV:default}` 占位。 |
| [configinit/template/config.yaml](configinit/template/config.yaml) | 配置模板单一事实源，与 conf.proto 对应。 |
| [dbdeploy/main.go](dbdeploy/main.go) | 数据库部署命令入口：解析参数并执行 Deploy。 |
| [dbdeploy/options.go](dbdeploy/options.go) | 部署参数定义与 flag/env 解析校验。 |
| [dbdeploy/deploy.go](dbdeploy/deploy.go) | 部署流程：校验清单、幂等建库、单事务按清单执行并补 CONNECT 授权。 |

补充要点：

- 配置加载先于日志装配：日志落盘路径来自 `log.file`，未配置时只输出
  标准输出；配置加载期无日志依赖，失败直接 panic。
- configinit 以 `O_EXCL` 实现「已存在即拒绝」，检查与创建之间无竞态；
  `-force` 才覆盖。
- dbdeploy 建库阶段固定连接 `postgres` 库，DSN 中的库名部分被忽略
  （`CREATE DATABASE` 不能入事务）；建库完成后再连目标库、在单事务内
  执行清单。管理员 DSN 与两角色口令缺失或库名非法时报错退出。

## 用法

- 初始化配置：`make config-init ARGS="-dir <目录>"`，或
  `go run ./cmd/configinit -dir <目录> [-force]`。
- 运行服务：`go run ./cmd/server -conf <目录>`。
- 数据库部署：`make dbdeploy ARGS="--database <库名>"`。执行顺序、
  幂等边界与升级步骤见 [deploy_db.md](../deploy_db.md)。

## 测试

- configinit：模板经与 server 相同的加载路径可解析出必需配置段；默认
  关闭的模型可靠性段保持零包装；不覆盖/覆盖/逐字节输出/自动建目录等
  文件语义。
- 模板同步：`cmd/configinit/template/config.yaml` 正文须与
  `configs/config.yaml` 一致，偏离时测试报错，防止两处配置漂移。
- dbdeploy 参数：flag 优先于环境变量、库名格式校验、缺项报错文案。
- 部署清单校验：真实清单通过、缺文件与 `CREATE DATABASE` /
  `CONCURRENTLY` 语句被拒、schema 序号连续与关键顺序约束。
- 真实库部署（`GRAPE_TEST_ADMIN_DSN` 门控）：全新库往返、生产种子只含
  平台自举数据、角色/种子重放幂等与结构 DDL 重复执行失败、口令不泄漏。
  本套件与 pg_sqlc 集成套件都创建集群级角色，不得并行运行。
