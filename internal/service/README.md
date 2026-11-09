# service

传输层适配器：proto 生成的资源服务与少量手写端点都在此落地。边界上只做
DTO ↔ DO 转换与 `query.v1` → `dynq.Query` 协议转换，然后委托 [biz](../biz/)
用例；业务规则、入参校验与权限判定都不在本层，身份只从认证上下文读取。

## 关键约束

- **只转换与委托**：资源服务嵌入 proto 的 `Unimplemented*Server`，方法内
  映射字段、转换查询、调用用例；不拼 SQL、不判断权限、不实现业务规则。
- **身份 fail-closed**：受保护端点只认认证中间件注入的身份，缺失按未认证
  拒绝；令牌校验不在本层重复实现（生成端点经 `authOf` 读取）。
- **更新即裁剪**：更新端点只映射方法显式列出的可变字段，不可变列（如
  用户归属三元组、角色 `code`/`role_type`/`data_scope`、租户 `code`）
  不映射进 DO；必填的 `status` 缺失即 INVALID_ARGUMENT。
- **状态与可选字段归一**：创建时缺失状态交用例落缺省，显式 0（禁用）原样
  保留。可选文本的空串经 `optionalString` 折为未设置，唯配置 `value`
  例外——显式空串与纯空白是有效值。
- **查询协议**：List 方法复用 `convertQuery`；`page` 与 `after` 互斥，
  转换错误经 `invalidQueryError` 包装为资源自身的 INVALID_ARGUMENT，
  响应页码由 `pageOf` 回填。`enumAliases` 是预留的枚举名归一能力，
  当前没有资源传入别名。
- **手写端点**：无 proto 契约的 Agent 路由与审计/检索查询端点也在本层
  注册；归属、`ctx.Middleware` 纪律与中间件链顺序见
  [internal/README.md](../README.md#手写端点归属)。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [session.go](session.go) | `SessionService`：登录映射为 `biz.LoginInput`，刷新轮换令牌，登出免认证自取 Bearer 头按哈希撤销（重复登出幂等）；登录来源缺省回落服务端观测的 IP 与 User-Agent。 |
| [permission.go](permission.go) | `PermissionService`：`GetMyPermissions` 返回认证身份去重升序的权限码集合（前端导航过滤的数据源）；用户无效返回空集（正常响应），端点本身不设权限码。 |
| [query.go](query.go) | `QueryService` 与查询转换辅助：`GetListSchema` 输出前端查询组件契约（内容 hash 可作 ETag，端点不设功能权限码）；`convertQuery` / `normalizeEnumValue` / `invalidQueryError` / `pageOf` 供各资源 List 复用。 |
| [user.go](user.go) | `UserService`：用户 CRUD、个人中心（目标恒取认证身份）、归属转移、角色分配与归属候选（Option.value 渲染为十进制字符串）；`authOf` 与 `optionalString` / `tsPBPtr` 等辅助定义在此。 |
| [dept.go](dept.go) | `DeptService`：部门 CRUD、平铺 List、树查询与移动；`parent_id` 未设置即移根，`owner_user_group_id` 未设置即清空。 |
| [usergroup.go](usergroup.go) | `UserGroupService`：用户组 CRUD 与成员三端点；成员列表固定 offset 分页，`has_more` 由归一化页码×页大小与 total 比较得出。 |
| [role.go](role.go) | `RoleService`：角色 CRUD；数据范围与菜单授权是两个随角色读写的整组替换面。 |
| [menu.go](menu.go) | `MenuService`：菜单树/详情只读，写端点方法名带 `Super`（受控写面）；结构变更独立走 `MoveMenuSuper`。 |
| [dict.go](dict.go) | `DictService`：字典类型与数据两级 CRUD 与动态 List；更新输入丢弃 `code` 与值语义/归属列。 |
| [config.go](config.go) | 配置族四个适配器：全局配置（写面带 `Super`，平台根租户限制由用例强制）、租户配置、功能开关、通用翻译；全局/租户配置的 `value` 仅 nil 折叠，显式空串与纯空白原样保留。 |
| [tenant.go](tenant.go) | `TenantService`：平台级租户实体管理；`Super` 只表达对象粒度（租户树边界仍由 RLS 承担），`parent_id` 省略或等于调用方租户，`code` 创建后不可变，`expired_at` 更新为覆盖语义。 |
| [audit.go](audit.go) | `AuditService`：登录/操作日志只读查询（两表 append-only，无写端点）；操作日志的 JSON 快照解码为 `structpb.Struct`，非对象脏数据降级为字段未设置而非整行失败。 |
| [session_admin.go](session_admin.go) | `SessionAdminService`：租户内会话列表与幂等撤销；DTO 不携带令牌哈希与租户 ID。 |
| [agent_http.go](agent_http.go) | `AgentService`：AG-UI 与 ack 手写路由注册面，把骨架 `ServeMux` 经 `ctx.Middleware` 入链（SSE 由框架直写）；`BasePath` 供 server 的超时跳过名单使用。 |
| [agent_sessions.go](agent_sessions.go) | `AgentSessionsService`：会话列表手写路由注册面（handler 在 `agent.Stack`），按认证身份 userKey 列本人会话、跨用户不可见；必须先于 agent 前缀通配注册。 |
| [agent_event_window.go](agent_event_window.go) | `AgentEventWindowService`：会话事件窗口手写路由注册面，按 threadId + eventId 取锚点窗口（before/after 各上限 50）、跨用户 404；与会话列表同序，先于前缀通配。 |
| [agent_audit.go](agent_audit.go) | `AgentAuditService`：工具调用与授权确认审计查询（列表/点查互斥，默认 20、上限 200；摘要为落库时已脱敏形态）。 |
| [agent_retrieval.go](agent_retrieval.go) | `AgentRetrievalService`：检索留痕查询（会话/来源过滤，默认 20、上限 200），证据字段按落库形态透传。 |
| [knowledge.go](knowledge.go) | `KnowledgeService` 同时实现 `LibraryService` 与 `DocumentService`：库/条目 DTO 与动态 List、库内固定分页、模型选项与重建索引；`SearchDocuments` 委托知识检索用例，设施未启用映射 503 `SEARCH_UNAVAILABLE`，其余执行故障固定文案 500 `SEARCH_FAILED`、细节只进服务端日志；`ListDocumentChunks` 返回条目实际分片（同类映射：503 `DOCUMENT_CHUNKS_UNAVAILABLE`、500 `DOCUMENT_CHUNKS_FAILED`）。 |
| [model.go](model.go) | `ModelService`：模型目录租户 CRUD 与平台 super 写面；密钥只写不读，响应只回 `hasApiKey`。 |
| [provider.go](provider.go) | `ProviderSet` 聚合全部服务构造函数，由 cmd 的 Wire 统一装配。 |

## 测试

- 身份与 fail-closed：各服务从 ctx 取身份，缺失即未认证；个人中心与
  权限码查询的目标恒取认证身份，归属候选按认证身份推导可见集合。
- 映射与归一：optional 字段折 nil、可空时间戳/树形节点/int64→字符串
  渲染、创建状态缺省与显式 0 的区分。
- 更新输入裁剪：身份/归属/结构列被丢弃，必填 `status` 缺失即
  INVALID_ARGUMENT。
- 查询协议与分页：page/after 冲突、分页信封回填与非法查询的错误映射。
- 知识检索与知识库：检索入参透传、400/401/503/500 错误映射与固定文案；库
  区分串 optional 语义（创建省略取缺省、显式空串保留、更新省略保持原值）。
- 手写端点：审计与检索留痕的列表/点查互斥、分页上限与错误状态码映射；
  Agent 路由的入链与无令牌 401 由 [server 测试](../server/README.md#测试)
  覆盖。

## 相关文档

- 三层权限模型（租户边界 / 行级数据范围 / 功能权限码）见
  [permission.md](../../permission.md)。
- SSE 超时与生产治理口径见
  [operations.md](../../operations.md#sse-与超时治理)。
- Agent 审计写入面与关联键语义见
  [replay.md](../../replay.md#写入机制) 与
  [replay.md](../../replay.md#关联键与粒度)。
- 新增资源与手写端点的接入流程见
  [internal/README.md](../README.md#新增资源速查)。
