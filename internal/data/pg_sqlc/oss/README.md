# oss — 内嵌对象存储设施（gofakes3，数据落 PG）

`biz.ObjectStore` 之外的另一半内嵌 OSS：服务内嵌 [rclone/gofakes3]
（SigV4 鉴权 fork；上游 johannesboyne 不校验签名，不可用于安全场景）以
库形态提供标准 S3 API，数据落 PostgreSQL `oss_object` 表
（[../sqlc/schema/93_oss.sql](../sqlc/schema/93_oss.sql)，设施面无 RLS，
bucket 即命名空间，查询走 sqlc 固定命名查询）。业务客户端只认 S3 协议
（[../../ossclient/](../../ossclient/)），切 MinIO / 阿里云 OSS / AWS 只
改 sys_config 的 `oss.*` 配置键。

## 设计要点

- **挂载与鉴权**：`NewHandler` 在 `conf.oss.enabled=true` 且 sys_config
  `oss.provider=local` 时产出挂载 Handler（其余返回 nil 不挂路由；
  enabled=true 而配置不齐在装配期拒绝启动）。`/s3/` 经 server 层
  `HandlePrefix` 链外直挂，不走 JWT 中间件链——SigV4 签名自证身份。
- **前缀与签名**：客户端 BaseEndpoint 含 `/s3` 前缀，签名覆盖含前缀的
  原始路径；外层 `signedPrefixHandler` 先以原始路径校验签名、通过后剥
  前缀交给内层 gofakes3 路由（path-style）。内层服务器不持鉴权键
  （gofakes3 鉴权以路径原文参与签名，剥前缀后会误判），外层是唯一鉴权点。
- **桶语义**：桶不单独登记，存在性以桶内对象行存在为准；首写经
  `WithAutoBucket` 的 CreateBucket 幂等放行；空桶列举按 NoSuchBucket。
- **限流与覆盖写**：PutObject 按 Content-Length 限流 ≤ 20MB（未知大小
  读到上限 +1 判定），超限 EntityTooLarge；20MB 上限远低于 PostgreSQL
  TOAST 单字段约 1GB 的承载上限，bytea 数据面无膨胀风险。同键覆盖写
  幂等，etag 为内容 MD5 十六进制，created_at 保持首次写入。本期只开简单
  上传，不实现 MultipartBackend（multipart 由 gofakes3 内存 uploader
  组装，complete 时整体走 PutObject，同样受限流约束）。
- **池直连口径**：oss_object 无 RLS、S3 请求经 SigV4 自证身份，Backend
  与装配期配置读取均以池直连（`Pool()` 的既定装配期/设施面用途），不经
  事务门面；grape_u 的 DML 授权在 93_oss.sql 文件内显式声明。
- **已知边界**：gofakes3 不支持版本控制、细粒度 ACL、lifecycle，业务不
  依赖；对象键应为 URL 安全 ASCII（含空格等转义差异字符的键在 gofakes3
  的签名口径下会校验失败，业务键形如 UUID/文件名不受影响）；suffix
  Range（`bytes=-N`，N 超过对象大小）gofakes3 按 ErrInvalidRange 返回
  416（N 恰等于对象大小时为全量 206），AWS S3 对 N ≥ 对象大小均返回
  整个对象（200）——库级限制，Backend 现有钩子无法优雅修复，业务不
  依赖越界 suffix Range。

## 文件导览

| 文件 | 内容 |
| --- | --- |
| [provider.go](provider.go) | Wire ProviderSet：`NewHandler`（disabled / 非 local 返回 nil） |
| [server.go](server.go) | `NewHandler` 挂载构造（装配期 sys_config 读取、决策矩阵）与 `signedPrefixHandler`（外层 SigV4 + 前缀剥离） |
| [backend.go](backend.go) | `gofakes3.Backend` 的 PG 实现：覆盖写、Range substr 切片、列举归并、桶生命周期、限流、复制与元数据 jsonb 编解码 |
| [backend_unit_test.go](backend_unit_test.go) | 纯单元测试：LIKE 转义、元数据编解码、配置值解码、etag 引号口径 |
| [oss_pg_test.go](oss_pg_test.go) | 集成测试（真实 PostgreSQL，`GRAPE_OSS_TEST_ADMIN_DSN` / `GRAPE_OSS_TEST_DSN` 门控，从模板库克隆独立测试库）：Backend 存储语义、/s3/ 回环的 SDK 语义与 SigV4 拒绝矩阵、挂载决策矩阵、grape_u 授权 |

## 测试

S3 SDK 语义（Put / Get Range 206 / Head / Delete、presigned GET+PUT、
篡改签名 SignatureDoesNotMatch、匿名与未知 AK 403、过期预签名）以内嵌
服务 httptest 回环验证；Backend 存储语义以 grape_u 连接直测（覆盖写
created_at 不变、Range 只取切片、marker/delimiter 列举、桶生命周期、
20MB 限流、复制与元数据漂移容忍）。
