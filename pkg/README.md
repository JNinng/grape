# pkg

与业务无关的通用包，不依赖 `internal/`，供各层直接引用。新增包须保持
这条边界：一旦引入业务概念或存储依赖，就应下沉到 `internal/`。

## 关键约束

- redact 是脱敏的单一代码点，日志、审计表与复现视图共用同一注册表。
- 脱敏器确定性：同一实例对同一输入恒产生同一输出；哈希形态以注入盐做
  keyed 哈希（同盐同输出、跨进程可复算），包内禁止随机与时间等不确定源。
- 注册表未登记类别原样返回并报 `false`，调用方据此决定放行或拒绝，不做
  静默假脱敏；key-filter 对非 JSON 与标量 JSON 原样返回，超深嵌套子树
  保留不脱敏，任何输入都不丢内容、不 panic。
- 实例 ID 的契约是进程内稳定：首次计算后不随环境变化重算，即使 IP 事后
  恢复可用也不换身份（身份漂移比持续缺 IP 危害更大）。
- `NewStandardRegistry` 只提供四类骨架默认值，面向「不出错」而非「最
  合适」；各消费面应在装配期按需 `Register` 覆盖（如 token 换 keyed
  哈希以支持关联）。
- version 的构建字段只经 `-ldflags -X` 注入，未注入时保留 `dev` /
  `unknown` 占位，据此区分开发构建与正式构建。
- secret 的密钥材料只来自装配期注册表（启动期固化、改配置需重启），不随
  密文传播：密文首段自描述「基础名@版本」，解密按它自动选材料，无版本或
  跨组回退；AAD 绑定完整键名与算法，防止密文跨业务键/跨版本/跨算法挪用；
  加解密失败统一为 `ErrUnavailable`，调用方不得按错误差异分支。轮换须登记
  新版本号并切换 `write` 指针，复用版本号换材料会使旧密文不可解。
- 算法标识是完整算法名（当前仅 `AES-256-GCM`），`SupportedKinds` 与登记表
  校验、密钥生成共用同一支持集合；不接受 `AES` 这类家族名。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [utils/ip.go](utils/ip.go) | `ResolveLocalIP` 按 `INSTANCE_IP` → `POD_IP` → 网卡顺序取非回环 IPv4，失败时汇总各来源原因。 |
| [utils/id.go](utils/id.go) | `InstanceId` 进程内恒定（首次计算含失败退化，不重试）；`NewTraceID` / `NewSpanID` 产合法 otel 标识。 |
| [utils/safe.go](utils/safe.go) | 日志体按 4 KiB 上限在 rune 边界截断；`MaskString` 系列按 rune 保留首尾、掩码中段。 |
| [version/version.go](version/version.go) | 经 ldflags 注入的版本、提交与构建时间单点声明，`Info()` 输出单行摘要（含运行时 Go 版本）。 |
| [redact/redact.go](redact/redact.go) | 确定性脱敏器与按类别注册的注册表：固定掩码、keyed 哈希、rune 截断、JSON 键过滤四种形态。 |
| [secret/secret.go](secret/secret.go) | 算法标识与密钥材料的单一事实源：支持集合、材料结构、装配期字段/长度校验口径、`Generate` 随机密钥。 |
| [secret/codec.go](secret/codec.go) | 只读 `Registry`（装配期固化材料与 `write` 指针）与自描述密文 Codec：`Sealer` 写入、单参数 `Open` 读取、统一 `ErrUnavailable`。 |

## 测试

- 工具函数契约：IP 来源优先级与全失败原因汇总、实例 ID 的形态与进程内
  恒定、日志截断的 rune 边界、掩码首尾保留语义、版本摘要格式。
- 脱敏器边界与确定性：空值、超长、多字节字符、嵌套 JSON、非字符串值；
  同盐同输出、跨调用确定，非 JSON 与标量 fail-open 原样返回。
- 注册表行为：登记、重复登记替换、未登记类别的放行语义，并发读写
  （`-race`）。
- 标准注册表：四类默认覆盖与各自形态（口令/令牌掩码、键过滤、检索
  截断）。
- 密文 Codec：三段自描述格式与随机 nonce、按首段业务键/版本选材料、
  AAD 业务键绑定、篡改与非法格式统一哨兵、拒绝式实例、材料校验矩阵、
  生成产物可装配、并发 Seal/Open（`-race`）。
