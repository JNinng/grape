# biz

领域层：只认 DO。定义 DO、用例、仓储接口（依赖反转的接缝）、类型化错误与选项类型
（如 `SessionOptions`）；领域规则（必填、缺省值、跨字段规则）只在这里落地。动态 List
以 `*dynq.Query` 作为查询协议随仓储接口跨界，biz 只透传、不解析。

依赖方向恒为 service/task → biz → data：biz 声明仓储接口，data 实现；对 `api/...` 的
唯一依赖是错误 reason 枚举，不感知 DTO、PO 与存储标签。分层图、跨层对象形态与新增
资源速查见 [internal/README.md](../README.md)。

## 关键约束

- 租户边界与行级数据范围由存储层强制（RLS + `@scope`/写前函数），biz 不重复判定；
  功能权限码由入口层判定。三层模型与落点见 [permission.md](../../permission.md)。

- `super` 方法仅用于身份已成立、接口有意绕过租户/范围约束的写面（菜单、全局配置
  经受控写函数）；租户实体管理面的 `super` 是对象粒度约定，约束照常生效。语义与
  例外清单见 [permission.md](../../permission.md)。

- 仓储契约的身份入口分两类：关系与授权仓储收 `Identity`（租户边界与审计操作者）；
  向量/审计/留痕契约不接收身份，实现从认证 ctx 提取并 fail-closed。身份成立前的
  凭据/令牌查询与登录日志追加属文档化例外（以租户 ID 或令牌哈希为定位事实），
  不使用 `super`，见 [permission.md](../../permission.md)。

- 同一 DO 贯穿 service/task → biz → data，禁止中途换形；流向图与跨层对象形态见
  [internal/README.md](../README.md)「分层与数据流向」。

- 口令哈希与令牌哈希只在 biz 单点生成/校验，仓储出入参只见哈希，明文不进日志；
  登录点查对「不存在」与「口令错误」同因返回（防枚举）。

- 全局配置写路径经 `PlatformTenantGuard` 强制平台根租户；其余配置族为租户表，
  走 RLS 与普通 DML。边界规则见 [permission.md](../../permission.md)。

- 受控写面（菜单、全局配置）由部署面 SECURITY DEFINER 函数承担，运行角色无直写
  权限；部署顺序见 [deploy_db.md](../../deploy_db.md)，SQL 对象索引见
  [sqlc/README.md](../data/pg_sqlc/sqlc/README.md)。

- Agent 工具调用/授权确认审计与检索留痕的证据面、写入点与关联键见
  [replay.md](../../replay.md)。

- 工具授权：`FindActive*` 未命中以 `(nil, nil)` 表达正常控制流，`Resolve` 会话级先于
  全局级，`Create` 同键幂等；吊销对不存在与跨租户统一 404，无码调用方对他人授权
  得 403（同租户内 id 存在性可枚举，id 无内容泄露）。

- 向量设施的嵌入空间以 `Embedder` + `Dimension` 成对声明，实现按注册表对账；默认
  空间的有效优先级为请求显式 > 租户配置 > 全局 conf。介质语义见
  [data/README.md](../data/README.md)。

## 关键文件导览

| 文件 | 职责 |
| --- | --- |
| [user.go](user.go) | 用户 DO 与仓储/用例：创建缺省归属人与状态、个人中心本人读写与改密、角色分配/归属转移入口。 |
| [assignment.go](assignment.go) | 分配选择器与角色整组替换：候选查询归一化、版本 hash、`managed`/`locked` 分区的入参侧。 |
| [dept.go](dept.go) | 部门与组织树：创建/移动的父级与防环校验、树装配；闭包表是数据库派生数据。 |
| [usergroup.go](usergroup.go) | 用户组与成员映射：批量增删的去重排序、成员分页归一；组软删后映射残留由活跃过滤排除。 |
| [role.go](role.go) | 角色与数据范围/菜单授权整组替换；创建强制租户自定义类型与仅本人范围，预置角色不可删。 |
| [menu.go](menu.go) | 菜单树用例：父级/防环/软删校验与树装配，写方法带 `Super`。 |
| [dict.go](dict.go) | 字典类型与数据两级共用仓储；`value_type` 白名单在用例、宿主存在性在仓储事务内判定。 |
| [config.go](config.go) | 四类配置族（全局/租户/功能开关/翻译）的 DO、仓储与用例。 |
| [tenant.go](tenant.go) | 平台级租户实体管理：租户树创建规则、自身租户守卫、停用/恢复与软删。 |
| [auditlog.go](auditlog.go) | 登录/操作日志的只读查询与 append-only 追加入口。 |
| [auth.go](auth.go) | 登录编排：租户回落、防枚举、口令通过后判禁用、透明重哈希、委托会话签发；登录日志 best-effort。 |
| [session.go](session.go) | 会话与令牌生命周期（签发/校验/撤销/登出/刷新）及治理面 List；令牌只以哈希跨界。 |
| [password.go](password.go) | 口令哈希单点：argon2id 写入，按自描述标识校验历史算法并给出重哈希信号。 |
| [identity.go](identity.go) | 服务端身份事实与私有 ctx 载体；仅认证中间件注入，缺失由调用方 fail-closed。 |
| [permission.go](permission.go) | 功能权限码常量、入口判定用例与权限码枚举；判定每请求库内直查、应用内无缓存。 |
| [grant.go](grant.go) | Agent 工具授权：许可解析与本人/管理面吊销；管理面权限码在用例层判定。 |
| [audit.go](audit.go) | Agent 工具调用/授权确认审计：入参校验、决定词表配对、摘要脱敏与截断。 |
| [retrieval_log.go](retrieval_log.go) | 检索留痕查询用例与 ctx 事实载体；写入由 data 层装饰器直写、不经用例。 |
| [embedder.go](embedder.go) | 文本向量化与默认空间解析契约；嵌入空间经 ctx 载体传给框架适配器。 |
| [document.go](document.go) | 摄取管线：区分串切段（缺省段落空行，空串关闭）+ 超长段 rune 窗口二次切分、逐块 embed、按来源覆盖写入；重摄即迁移路径。 |
| [vectorstore.go](vectorstore.go) | 向量设施契约：文档/过滤/评分形状与读写方法面（多嵌入空间、metadata 条件与批量更新、枚举读面 `List`/`GetMetadata`）。 |
| [knowledge.go](knowledge.go) | 知识库与条目用例：库/条目校验、切分参数（大小/重叠/区分串）与嵌入模型选择、chunk metadata 同步、删除清理与重建索引、条目实际分片枚举（按分片序，取自向量存储）、可见库集合解析（`VisibleLibraries`，检索与列库工具共用；条目读面随所属库可见性收窄）。 |
| [knowledge_search.go](knowledge_search.go) | 知识检索用例（前端与 agent 的统一入口）：入参校验与 top_k 钳制、可见库集合现场解析（`library_id` 不可见/不存在统一 `ErrLibraryNotFound`）、默认空间向量化、固定检索条件（状态/有效期 + `kb_id IN` 可见库集合）、来源缺省 console 留痕声明（调用方已声明时保留）。 |
| [model.go](model.go) | 文本模型目录与密钥加解密契约；嵌入空间形状与模型类别/维度常量。 |
| [model_manage.go](model_manage.go) | 模型目录用例：租户/平台行 CRUD、密钥只写不读、embedding 维度 1024 与 model_key 白名单校验。 |
| [maintenance.go](maintenance.go) | 设施无关的诊断/维护契约；ops 与 task 只认契约。 |
| [metrics.go](metrics.go) | Agent 运行观测契约（运行数/限流拒绝/工具终态），由 ops 实现。 |
| [query.go](query.go) | 动态查询能力出口：资源 List schema 转发，不触库、无租户边界。 |
| [provider.go](provider.go) | Wire `ProviderSet`：用例构造函数与共用脱敏注册表。 |

## 测试

- 用例层以 mock 仓储验证领域规则：必填/列宽/状态哨兵、缺省值与归一化、树防环、
  去重排序、权限码与平台守卫、版本冲突与错误透传（覆盖组织、RBAC、字典、配置族、
  租户、用户与分配）。
- 安全单点：口令多算法校验与重哈希、登录防枚举与登录日志 best-effort、会话令牌
  哈希/元数据截断/登出幂等、身份 ctx 往返。
- Agent 面：审计词表配对与脱敏截断、授权会话优先/幂等/权限码边界、检索留痕分页
  与 fail-closed、摄取切分（固定窗口与区分串）/维度对账/覆盖幂等/中途失败、知识检索的
  校验/钳制/固定条件/设施缺失透传。
