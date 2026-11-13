# web

Grape 前端：Vite + React + TypeScript + Tailwind CSS v4 + shadcn/ui
本地改写版（`new-york-v4`）+ `@ag-ui/client`。构建产物经
[embed.go](embed.go) 的 `go:embed` 收进 Go 二进制，由 `internal/server`
托管静态资源与 SPA fallback，最终单二进制交付。

## 构建与嵌入

`go:embed` 在 Go 编译期收录 `dist/`，顺序固定：先前端构建，再
`go build`。只改 Go 无需重跑 npm；只改前端必须重新编译二进制。

在仓库根依次执行：

```bash
make web                # 等价 cd web && npm ci && npm run build
go build ./cmd/server
```

仓库恒有极简的 `dist/index.html` 占位页（提示先构建），未执行前端构建
时 `go build` 依然通过。[.gitignore](.gitignore) 以 `dist/*` +
`!dist/index.html` 收口：构建产物整体不入库，占位页是唯一入库文件。
npm 构建会覆盖占位页，提交前用
`git checkout -- web/dist/index.html` 恢复。发布顺序与产物口径见
[../operations.md](../operations.md#构建与发布)。

## 开发期代理

`npm run dev` 启动 Vite 开发服务器；[vite.config.ts](vite.config.ts)
把 `/v1`（业务 API）与 `/agui`（AG-UI / ack，SSE 长流）代理到
`http://127.0.0.1:8000`（`configs/config.yaml` 的 `server.http.addr`）。
生产期前后端同源，无需代理。SSE 超时与前置代理要求归
[../operations.md](../operations.md#sse-与超时治理)，本目录不重复配置。

## 关键约束

- **身份与权限**：前端只做界面收敛，真实判定在入口层与数据库（见
  [../permission.md](../permission.md#功能权限码)）。权限码拉取失败时
  界面按全量展示，越权请求由 API 拒绝兜底。
- **序列化口径**：全部按 protojson——字段小驼峰、int64 为字符串、时间为
  RFC3339、optional 未设置即省略（显式 null 同义）。写请求省略 undefined
  键即未设置。
- **不可信文本**：模型文本与工具参数/结果一律不可信，全部以 React 文本
  节点渲染（等价 `textContent`），不引入 `innerHTML` / markdown 注入路径。
  将来引入 markdown 渲染必须先过 sanitizer（如 dompurify）并另行评审。
- **旧内核兼容**：CSS 禁用 oklch、透明度修饰符与独立 transform 属性；
  组件改写规则与 jsdom 垫片见
  [src/components/ui/README.md](src/components/ui/README.md)。

## 目录导览

| 路径 | 职责 |
| ---- | ---- |
| [embed.go](embed.go) | `//go:embed all:dist` 导出 `fs.FS`；必须与 `dist` 同级，`server` 只消费该导出。 |
| [vite.config.ts](vite.config.ts) | Vite / Vitest 配置：React 与 Tailwind 插件、开发代理、jsdom 环境。 |
| [index.html](index.html) | Vite 入口；头部脚本在样式加载前预挂 `.light`/`.dark` 与 `data-accent`，防刷新闪白或闪错色。 |
| [src/index.css](src/index.css) | Tailwind 入口与设计 token 单一事实源：语义色、品牌/状态衍生面、色系覆盖、字体与页面过渡。 |
| [src/main.tsx](src/main.tsx) | 装配 `ThemeProvider` → `BrowserRouter` → `App`。 |
| [src/App.tsx](src/App.tsx) | 路由表与全局装配：会话失效回登录、`Toaster`、每路由 `PageTransition`。 |
| [src/api/](src/api/) | 各后端服务的 HTTP 客户端；`http.ts` 统一附 Bearer 并执行 401 单飞刷新重放，其余按业务域分文件。 |
| [src/auth/](src/auth/) | 会话态与权限：令牌存取、单飞刷新、`ProtectedRoute`、权限码 Provider 与路由守卫。 |
| [src/agui/](src/agui/) | AG-UI 接入：Agent 工厂、纯函数事件 reducer、历史快照、确认决定、取消与运行 ID。 |
| [src/components/](src/components/) | 通用交互层：`ui/` 本地改写组件、`manage/` 管理页共享件、主题提供者与两个切换器、换页过渡。 |
| [src/pages/](src/pages/) | 页面与页面私有拆件：登录、对话、复现；`settings/` 为管理面。 |
| [src/ui/icons.tsx](src/ui/icons.tsx) | 仅保留 `GrapeMark` 品牌标；通用图标一律用 lucide-react。 |
| [src/test/setup.ts](src/test/setup.ts) | Vitest 的 jsdom 垫片与 jest-dom 装载。 |

## 登录与令牌策略

- 契约：`POST /v1/auth/login`（`tenantId` 可空，留空由服务端回落默认
  租户）、`POST /v1/auth/refresh`（轮换一对新令牌）、
  `POST /v1/auth/logout`（凭 `Authorization` 头撤销当前会话，幂等）。
- access token 只存模块内存（刷新即失效）；refresh token 存
  localStorage（跨标签页共享同一会话，浏览器重启后仍可静默续期）。
  XSS 的真正缓解是 CSP 与输出转义，存储介质只决定暴露时长与范围；
  refresh token 单次有效、服务端轮换即作废，泄漏窗口有限。
- 刷新轮换必须单飞：并发 401 只发一次 refresh，其余调用共享同一结果，
  避免旧刷新令牌被并发轮换互相作废；失败传播给全部等待方，清态后经
  `auth/callbacks.ts` 通知 `App` 跳转登录页。
- `ProtectedRoute` 在无 access token 时用 refresh token 静默续期一次，
  覆盖页面刷新后的会话恢复；等待期渲染占位，避免闪跳登录页。
- 登出不依赖后端结果：无论撤销成败（含网络失败）都清本地态并回登录页。

## 对话页

- 流式问答：`agui/agent.ts` 构造 `HttpAgent` 并注入 `authFetch` 作为
  传输（`EventSource` 不能带 Authorization，fetch 可以）；运行事件经
  `agui/events.ts` 的纯函数 reducer 折叠为文本增量、思考增量、工具调用
  生命周期、步骤与错误横幅。
- 思考内容：`REASONING_MESSAGE_START/CONTENT/END`（及缩写
  `REASONING_MESSAGE_CHUNK`）折叠为独立思考项，与正文同处流内时序；渲染
  为默认收起的虚线折叠块。思考框与回答同属助手回合（`groupChatUnits`），
  共用「助手」标签、宽度收敛到气泡列——思考在上、回答在下。摘要行在增量
  中显示「正在思考…」载入态。载入提示另有两处：正文流式时气泡末尾闪烁
  竖线；运行中且无任何流式项、无在跑工具时，列表尾部显示「正在思考…」
  ——待确认（运行挂起等人决定）不显示该提示。
- 历史回放：切换会话时 `POST /agui/history` 拉一次 messages snapshot
  （`agui/history.ts` 手解 SSE data 行）。拿到首份 `MESSAGES_SNAPSHOT` 即
  `reader.cancel()` 返回，不等快照运行走完；不跟 follow 流——上游 track
  follow 有同毫秒事件被跳过的已知缺陷，快照即回放。
- 思考强度（输入框左侧档位触发器）：触发器只显示图标与当前档位文字，
  悬浮（或聚焦后回车）弹出档位单选：关闭 / 低 / 中 / 高。缺省中档，偏好
  存 localStorage `grape.thinking`。值经 AG-UI `forwardedProps.thinking`
  （档位字符串）随每次运行下发，后端据此覆盖本次运行的模型生成配置
  （`agui/thinking.ts` 是这一前端契约的单一代码点）。provider 不支持的
  档位由服务端归并（如 DeepSeek v4 把低/中映射为高）。切换只影响后续
  运行，在途运行保持启动时的取值（运行时触发器禁用）；恢复路径续跑不带
  forwardedProps，按后端缺省配置执行。
- 消息流跟随：仅在用户停留底部（32px 容差）时贴底滚动流式增量与步骤，
  向上翻阅历史后不再抢滚动位置；发送消息、切换会话与滚回底部恢复跟随。
  消息滚动区与会话侧栏常显细滚动条轨道（`overflow-y-scroll`），避免内容
  从不满到溢出时滚动条显隐挤动宽度。
- 确认卡：流内 `tool_confirmation` 自定义事件（载荷与 ack `Payload`
  字段一字对齐）出卡；决定 `POST /agui/ack/confirmation`，404 视为已
  失效收敛。待确认列表 `GET /agui/ack/confirmations?threadId=` 在加载
  会话、运行结束与 30s 低频轮询兜底，服务端列表为对账基准；该查询的
  404 按空列表收敛——归属守卫对尚未建档的新会话就回 404（语义是
  「对你不可见/还没有」），新会话首次运行前轮询必然命中。
- 决定后收敛：一次决定落定后按 1s/2s/2s/3s/3s 先密后疏重拉历史快照
  （续跑产物要等服务端写回快照），整表替换 items；相邻两轮一致或达
  轮次上限即停。会话切走即停，运行中跳过该轮，单轮失败静默。
- 取消：运行中 `POST /agui/cancel`（`RunAgentInput` 形状，threadId +
  runId 定位）；404 为运行已结束的正常竞态。
- 会话列表：`GET /agui/sessions` 按更新时间降序；切换会话重置对话态并
  拉历史，新建会话生成新 threadId。归属由服务端认证身份决定。

## 管理页（/settings）

对话页头部设置图标进入；`SettingsLayout` 为左侧模块导航 + 右侧子路由
内容区，模块注册与权限要求见 `pages/settings/SettingsLayout.tsx`，
子路由由 `App.tsx` 用 `RequirePermission` 包裹。索引路由等权限码加载
完成后重定向到声明顺序中首个有权限的管理模块，无管理权限时回落到个人
中心。各页具体交互以代码为准，以下只保留跨页契约与差异。

### 权限与导航

- 权限数据源：`PermissionProvider`（`auth/permissions.tsx`）挂载时经
  `GET /v1/permissions/mine` 拉取一次；导航、路由守卫与页面按钮共用。
  加载完成前导航留空，避免先展示无权模块再移除的闪烁。
- 导航过滤：列表/树页要求「页面码 + Query 码」同时持有；审计、配置等
  多视图页要求「页面码 + 任一视图码」。缺少任一要求不展示，避免进入后
 列表请求 403。码表见 `auth/permissionCodes.ts`，页面/视图码来自种子
  菜单，操作级码与后端权限判定对齐；前端显隐只做界面收敛。
- 路由守卫：直接输入无权 URL 重定向到个人中心（无码模块）。
- 按钮级显隐：新建、行操作与数据维护动作按操作权限码显隐；多视图标签
  按视图 Query 码显隐；无任何行操作时省略整列，无可用视图时给空状态。

### 共享组件与数据形态

- `manage/data-table.tsx` 消费 query.v1 动态查询协议：`where` 恒 AND、
  `sort`、分页信封（`total` 为字符串型 int64）。列集/顺序/表头/排序/
  显隐/页大小上限全部响应 ListSchema；表头点击循环 asc → desc → 回落
  `defaultSort`；筛选或写操作后回第 1 页；schema 加载失败给可重试横幅，
  不静默降级。
- `manage/query-filter.tsx` 为渐进式筛选输入：条件以 chip 串展示、落条
  即查（无查询按钮），操作符子集与值编辑器由字段 schema 推导。
- `manage/option-select.tsx` 为远程搜索选择器（cmdk + Popover）：250ms
  防抖、滚动/按钮加载更多；调用方声明的当前值固定保留在候选里，取消
  勾选或「清除选择」后仍可重新选回；无标签时回显
  `ID {value}（不可见）`。
- `manage/row-actions.tsx` 为行内操作悬停弹框：portal 渲染不被 sticky
  列裁剪；动作集为空时不渲染触发钮。
- ListSchema 消费模式：各页经 `api/querySchema.ts` 拉取
  `GET /v1/query/schema?resource=`，据此生成列定义与筛选字段；默认排序
  取 schema 的 `defaultSort`。资源的 List 能力是服务端确定性产出，前端
  不自行猜测可排序性与显隐。
- 写面校验与唯一性：手填 ID 输入一律正整数形态的字符串；配置与字典的
  值类型取自 `string/integer/boolean/float/json` 白名单；翻译 `rowId` 为
  int64 范围内正整数、`locale` 为 BCP-47 基本形态；租户默认语言同
  BCP-47、最大用户数为正整数；新建用户初始密码由服务端哈希、状态可在
  新建时选择启停。编码、字典值等唯一性由后端约束，前端只映射冲突提示。
- 动作反馈惯例：成功 toast，表单失败保留弹窗并内联错误，删除二次确认，
  写成功后重查当前页/当前树。
- 错误映射：带词表的页面把后端 reason 消息/文案映射为可读提示（配置与
  租户优先用稳定 reason 码兜底）；无词表的直接透传原始消息，不吞未知
  错误。

### 各页差异契约

| 页面 | 差异契约 |
| ---- | ---- |
| 个人中心 | 维护本人资料与口令，目标恒取认证身份；改密先校验当前口令，不依赖管理面权限码。 |
| 用户 | 角色整组替换携带 version，409 冲突只重载不重试；归属三元组整组替换，清空落 NULL；范围外 locked 角色只提示数量。 |
| 部门 | 主视图为无分页组织树；新建时部门管理员必选；编辑/移动的上级下拉排除自身子树，选根即不发送 `parentId`；有子部门时拒绝删除。 |
| 用户组 | 组编码创建后不可变且不经更新面上送；成员批量添加按逗号/空白去重、幂等跳过已有成员并提示实际新增数，任一用户不可用则整批拒绝。 |
| 角色 | 编码与类型创建后不可变；数据范围整组替换，ID 明细只替换当前规则类型；菜单授权整组替换，提交完整勾选集合，空集即收回全部，树与回显未就绪前禁止保存。 |
| 菜单 | 主视图为无分页权限码树；权限编码与类型创建后不可变；移动下拉排除自身整棵子树；有子菜单时拒绝删除。 |
| 字典 | 类型编码创建后不可变；数据面板按 `dictTypeId` 维护，字典值与值类型创建后不可改；类型下仍有数据时拒绝删除。 |
| 审计日志 | 操作/登录两个只读视图；操作日志详情按 id 点查并展示前后 JSON 快照，旧值缺失时显示「（无）」；页面无写动作。 |
| 会话 | 只读列表；撤销幂等（不存在/已撤销/已清理均按成功处理），令牌哈希不进入 DTO。 |
| 配置 | 四视图；功能开关覆盖语义须同时上送 `status` 与 `isEnabled`；翻译身份列只读；值列由 UI manifest 声明为 optional。 |
| 租户 | 新建不发送 `parentId`，固定挂在当前登录租户下；编码创建后不可变；当前登录租户的停用/恢复/删除禁用；到期时间按日期输入、提交转当日 UTC RFC3339，留空即清空。 |
| 知识库 | 左侧库列表 + 右侧库内条目固定分页（宿主 id 在路径参数，规避动态协议数值精度）；库级嵌入模型/切分参数建库时可选，区分串以转义形态编辑（`\n` 换行、`\t` 制表，留空即不按区分串切分，提交恒显式上送）；条目新建可选 text / markdown（markdown 按标题层级切分，编辑态类型只读）；条目状态与有效期合成「启用/停用/未生效/已过期」展示；点击条目标题打开详情（目录事实 + 向量存储中的实际分片，分片按序可点击展开正文）；列表不含正文，编辑条目按 id 点查详情回填正文，加载完成前禁用正文与保存（正文未改动时服务端按摘要判定不重新向量化）；重建索引入行操作；删除条目/库同时清理 chunk。检索面板复用已加载库列表做库过滤，检索条件一变即清空旧结果（迟到的旧响应丢弃），命中正文与条目原文按文本渲染，「查看原文」经命中 `sourceId` 点查条目。 |
| 模型 | 平台行只读，持有 `sys:model:platform:*` 时可编辑；租户行覆盖同名平台模型；密钥 `type=password` 只写不读，展示「已配置」并提供清除开关；嵌入模型维度固定 1024。 |
| 模型调用 | 只读列表（动态查询协议）；场景按封闭词表出中文标签、状态出徽标、token 未上报出占位不以 0 冒充；行内「详情」弹窗原样渲染落库的输入/输出摘要与错误（`<pre>` 纯文本），页面无写动作。 |

## 运维复现视图

`/replay` 受保护路由（对话页头部有入口）：选择会话后同屏呈现对话流
回放、工具调用审计、授权确认记录、检索留痕四面会话关联证据。模型调用
留痕是管线侧证据面（无会话事实，不随会话选择联动），已移至系统设置的
独立管理页（见「管理页」的模型调用条目）。
证据面、关联键与写入机制见 [../replay.md](../replay.md#证据面)，本目录只
描述页面行为。

- 对话回放复用 `/agui/history` 快照，快照即回放；「刷新快照」按钮兜底
  上游 track follow 的同毫秒缺陷。无 track 事件或事件已过保留期的会话，
  快照运行可能 500，回放面按空态提示降级（`historyUnavailable`），审计/
  授权/检索三面照常装载——不以一面失败盖掉整页。
- 快照中的思考消息（role=reasoning）与回答同属助手回合：共用角色标签，
  思考卡片默认收起、置于回答之上，摘要行含起始时间与相对本轮用户消息的
  用时，展开后以纯文本显示思考原文；空内容段落不造空块。思考项参与 trace
  高亮的按段归属（与助手回复同段），不带独立的模型用量事实，避免同一次
  调用重复计数。
- 工具审计、确认审计、检索留痕分别经 `/v1/agent/audit/tool-calls`、
  `/v1/agent/audit/confirmations`、`/v1/agent/retrieval-logs` 查询；返回
  行是落库时已脱敏的形态，前端直接渲染、不回传原文。工具重试次数后端
  恒记 0，前端不声明也不呈现。
- 检索留痕行默认收起、头部可点开（button + `aria-expanded`，键盘可达；
  「追踪」按钮与展开触发器平级，点追踪只切链路）。展开后显示命中列表与
  阶段时间线「向量召回 / 词法召回 → 融合 → 重排 → 回包」：阶段行默认
  收起，摘要含查询变体、重排模型、上限、候选数、单元按原因分组计数
  （入包 / 截断 / 收编 / 冗余）、耗时（0 ms 照常显示）与失败摘要；点开
  阶段显示候选表（名次 / 分片 ID / 分数修约 4 位 / 锚点）与回包单元表
  （聚合粒度 / 文档 ID / 成员分片 / 章节 / token / 结果）。未入包原因按
  后端 `reason` 映射，同源收编与冗余不写成截断；单路直调行无 stages，
  不渲染也不伪造。命中列表标注分数来源（重排阶段成功即无 error 才取重
  排分，否则有融合取融合分，单路直调取相似度；重排失败降级仍保留融合
  序分数，按融合分呈现）并按同一精度修约。阶段数据经
  `parseRetrievalStages` 防御性解析（损坏元素逐项丢弃，文本按节点渲染、
  不走 `innerHTML`）。
- 分片 ID 与单元文档 ID 可点开浮层（`Popover` 复用 `components/ui`）：
  分片走 `GET /v1/knowledge/documents/chunks/{chunk_id}` 取原文、序号、
  锚点、嵌入空间与退役标记（非空时醒目提示「已重摄淘汰，保留期内可反
  查」）及条目摘要；文档 ID 只点查条目摘要。请求经 `detailCache` 按 ID
  去重（同一 ID 一次），失败不缓存、浮层内可重试；刷新快照与切换会话时
  全部失效。
- trace 关联：三证据面的非空 trace_id 按出现频次降序汇总为可搜索
  下拉。选中链路只做高亮对比、不隐藏数据：命中行高亮、未命中行灰显；
  回放流经 `relatedToolCallIds`（审计与确认两面并集）桥接，命中项所在
  的「以用户消息为界」整段保持原样，段外降透明度（无命中项时不降）。
  凡携带 trace_id 的证据行都有「追踪」按钮，再点同值反选清除。
- 回放流的时间：快照 `rawEvent`（后端开启源元数据）按 messageId /
  toolCallId 索引起始时间，`agui/history.ts` 折叠消息时回填到回放项。
  消息与工具摘要行显示「始于 时间」；工具与回复另显示相对本轮用户消息
  起点的「用时」（段内以用户消息为界，无锚点则不计）。来源索引缺失时
  只显示占位，不伪造时间，也不用本地时钟冒充起始时间。工具真实耗时仍
  以审计行 `durationMs` 为准。
- 模型调用事实：`/agui/session-usage` 按 threadId 返回每次模型调用的
  模型型号、token 用量（输入/输出/合计，含缓存与思考细分 token；缓存率
  由前端按 `缓存 ÷ 输入` 计算并保留两位小数）。事实按 `messageId` 索引
  ——助手回复取自身 ID，工具调用取其 `parentMessageId`（决策轮通常没有
  正文，助手消息不单独成项），因此工具卡片把模型与 token 事实放在折叠区
  之外，收起时也可见。provider 未上报的字段不展示，不以 0 冒充；用量端点
  缺失（旧会话或会话不可见）时回放照常，只是不显示这些事实。首字与思考
  时长是流程级事实（框架只在每次运行的首条调用测量并复制到后续调用），
  只在该运行首条调用上标注「本轮」，不逐条重复。
- live 路径的确认行与工具行 trace 不同，跨 trace 对账以
  `toolCallId`/`confirmationId` 为准，不按 trace 强行合并。
  确认行配色按决定方向约定：放行方向绿、拒绝方向红，词表外 fail-closed
  显红，词表由后端把门（见
  [../replay.md](../replay.md#授权确认审计)）。
- 压缩占位符识别（`replay/compaction.ts`）按 trpc-agent-go
  `context_compact` 的字面形状：elided 整体替换块带行式恢复引用；方括号
  截断标记带内联 `event_id=` 引用（含紧凑变体）；无引用的裸截断标记与
  策略占位符不可恢复。识别以形状特征为准，不假设必然出现。
- 带 event_id 的占位符经 `/agui/event-window` 懒加载会话事件原文并排
  对照，失败可重试；无引用的只标注「无恢复引用，原文不可反查」。会话
  事件流保留全部原文，压缩只作用于请求投影副本。
- 数据组装（trace 汇总/过滤/高亮/hits 解析）与占位符识别均为纯函数，
  畸形输入安全降级不抛错；渲染延续纯文本 sanitize 基线。

## 主题与样式

- `components/theme-provider.tsx` 为零依赖主题提供者：`theme`
  （dark/light/system）存 localStorage，system 经 `matchMedia` 实时
  跟随；`index.html` 头部脚本预挂 `.light`/`.dark` 防刷新闪白；跨标签经
  `storage` 事件同步；切换时临时禁用过渡。
- 主题色系（accent）是明暗之外的第二维度：`accent` 存 localStorage，
  经 `html[data-accent]` 触发 [src/index.css](src/index.css) 的覆盖块；
  `violet` 为默认（不落属性），另有 `blue`、`green`、`amber`、`safe`。
  色盲友好系用 Okabe-Ito 蓝主色，并把状态软色对从红/绿换成橙/青绿；
  语义仍以文字标签为主。
- 页面禁止硬编码品牌/状态 tint：会话选中、链路高亮、追踪按钮、审批与
  执行芯片一律走 `--brand-*` / `--success-*` / `--danger-*`。新增色阶
  或 token 须补齐浅深两套主题，新色系另加 `data-accent` 覆盖块。
- 色板与 token 一律字面 hex/rgba：oklch 需 Chrome 111+，透明度修饰符
  编译为 `color-mix()`，旧内核解析失败会使颜色整体失效；弱化/半透明
  统一走 `--overlay`、`--destructive-soft`、`--soft` 等 token。
- 切换器 `mode-toggle.tsx` 与 `accent-picker.tsx` 位于登录页与对话页
  头部。组件层的本地改写差异与兼容改写规则见
  [src/components/ui/README.md](src/components/ui/README.md)。

## 测试

- Vitest + jsdom，垫片与 jest-dom 在 [src/test/setup.ts](src/test/setup.ts)
  装载；渲染主题切换器的页面测试需包 `ThemeProvider`（登录、对话页头部
  有 `ModeToggle`）。
- radix 交互走「打开 → 点选选项」，不用原生 `selectOptions`；异步动作
  以行为与可观察结果断言，不绑定内部实现。
- 纯函数优先单测：事件 reducer、历史快照折叠、trace 关联/高亮、压缩
  占位符识别、查询条件组装均可脱离网络断言。
- 网络层以替身注入：401 单飞刷新、DTO 归一（items 缺省补空数组、
  int64 字符串）、schema 驱动表格与筛选。
- 页面测试 mock 对应 `api/*` 模块；后端集成测试与权限/租户语义不在
  本层重复。

## 命令

| 命令 | 说明 |
| ---- | ---- |
| `npm run dev` | 开发服务器（代理后端）。 |
| `npm run build` | 类型检查 + 产物构建到 `dist/`。 |
| `npm test` | Vitest 单元测试（jsdom）。 |
| `npm run lint` | ESLint（typescript-eslint 推荐集，从简口径）。 |
