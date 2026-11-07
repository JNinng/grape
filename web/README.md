# web

Grape 前端：Vite + React + TypeScript + Tailwind CSS + shadcn/ui（本地改写版）
+ `@ag-ui/client`。构建产物经 [embed.go](embed.go) 的 `go:embed` 进 Go 二进制，
由 `internal/server` 挂载静态托管与 SPA fallback，最终单二进制交付。

## 构建顺序（重要）

`go:embed` 在 **Go 编译期** 收录 `dist/` 内容，顺序固定为：**先 npm 构建，再
go build**。

```bash
# 1. 前端构建（或仓库根 make web）
cd web && npm ci && npm run build
# 2. 后端编译（嵌入最新产物）
go build ./cmd/server
```

只改 Go 不改前端时无需重新跑 npm；只改前端时必须重新编译二进制，产物才会更新。

## 占位策略

仓库内恒有一个极简的 `dist/index.html` 占位页（说明「前端未构建，先 npm build」）：

- 未执行前端构建时 `go build` 依然通过（embed 永远有内容可收录）；
- [`.gitignore`](.gitignore) 用 `dist/*` + `!dist/index.html` 收口：构建产物整体
  不入库，占位页是唯一入库文件；
- npm 构建后真实产物覆盖 `dist/`（含构建出的 `index.html`），**仅本地生效、不要
  把构建后的 `index.html` 提交入库**（用 `git checkout -- web/dist/index.html`
  恢复占位页）。

## 开发期代理

`npm run dev` 启动 Vite 开发服务器，`vite.config.ts` 把 `/v1`（业务 API）与
`/agui`（AG-UI / ack，SSE 长流）代理到 `http://127.0.0.1:8000`（即
`configs/config.yaml` 的 `server.http.addr`）。生产期前后端同源，无需代理。

## 目录导览

| 路径 | 职责 |
| ---- | ---- |
| [embed.go](embed.go) | `//go:embed all:dist` 导出 `fs.FS`；`go:embed` 不能引用父目录，故本包必须与 `dist` 同级，`server` 只导入该导出。 |
| [vite.config.ts](vite.config.ts) | Vite / Vitest 配置：React 与 Tailwind 插件、开发代理、jsdom 测试环境。 |
| [index.html](index.html) | Vite 入口（构建后变形为 `dist/index.html`，与仓库占位页是两回事）。 |
| [src/index.css](src/index.css) | Tailwind 入口与全局设计基调：语义 token（`:root` 浅色 / `.dark` 深色两套字面 hex，经 `@theme inline` 映射为工具类）、品牌衍生面与状态软色对（`--brand-*` / `--success-*` / `--danger-*`，随主题色系切换）、五个主题色系覆盖块（`html[data-accent]`，见「UI 组件与主题」节）、字体栈、细滚动条基础层与组件类；页面样式一律写工具类，不另散落 CSS。色板与 token 值一律字面 hex/rgba——oklch 需 Chrome 111+（旧内核解析不了会使颜色全失效），透明度修饰符（编译为 color-mix）同理禁用，弱化/半透明统一走 `--overlay`、`--destructive-soft` 等 rgba token，新增色阶或 token 须补齐两套主题（新色系另加 data-accent 覆盖块）。 |
| [src/components/](src/components/) | shadcn/ui 组件层：`ui/` 下 24 个经兼容改写的官方组件（new-york-v4 口径，组件清单、本地改写差异与兼容改写规则见 [ui/README.md](src/components/ui/README.md)）、`manage/data-table.tsx`（管理页通用表格：query.v1 动态查询 + 分页 + 表头排序，列显隐/可排序性/页大小上限按 ListSchema 门控，各模块复用）、`manage/query-filter.tsx`（渐进式筛选输入：条件以 chip 串展示、落条即查，筛选项动态响应 ListSchema）、`manage/option-select.tsx`（远程搜索选项选择器：cmdk + Popover，单/多选、250ms 防抖、滚动/按钮加载更多；已选值与调用方声明的当前值固定保留在候选列表里，取消勾选或「清除选择」后仍可重新选回；无标签时回显 `ID {value}（不可见）`）、`manage/row-actions.tsx`（行内操作悬停弹框，portal 渲染不被 sticky 列裁剪）、`theme-provider.tsx`（零依赖主题提供者：明暗 + 色系持久化、跟随系统、跨标签同步）、`mode-toggle.tsx`（浅色/深色/跟随系统切换器）、`accent-picker.tsx`（主题色系切换器）、`page-transition.tsx`（路由换页淡入过渡）。 |
| [src/api/](src/api/) | 会话 API 与带 401 状态机的 fetch 封装：`session.ts`（登录 / 登出）、`http.ts`（附 Bearer 头、401 → 单飞刷新 → 重放一次）、`query.ts`（query.v1 动态查询协议类型，各管理模块共用）、`querySchema.ts`（`GET /v1/query/schema` 客户端：资源 List 能力 schema，驱动筛选与列定义）、`users.ts`（UserService 管理端点，int64 走 protojson 字符串口径）、`assignOptions.ts`（用户分配选择器与角色整组替换：可分配角色/当前角色/设置角色与三个归属候选，选项 value 为 ID 字符串口径）、`depts.ts`（DeptService 管理端点：树/CRUD/移动，同 users 口径）、`userGroups.ts`（UserGroupService 管理端点：组 CRUD + 成员列表/添加/移除，同 users 口径）、`roles.ts`（RoleService 管理端点：CRUD + 数据范围配置 + 菜单授权，同 users 口径）、`menus.ts`（MenuService 管理端点：菜单树/CRUD/移动，同 users 口径）、`dicts.ts`（DictService 管理端点：字典类型与数据两级 CRUD + 动态 List，同 users 口径）、`configs.ts`（配置族管理端点：全局配置/租户配置/功能开关/翻译四组 CRUD + 动态 List，同 users 口径）、`tenants.ts`（TenantService 管理端点：租户实体 CRUD + 停用/恢复，字符串主键，同 users 口径）、`auditLogs.ts`（AuditService：登录/操作日志只读查询）、`sessionAdmin.ts`（SessionAdminService：会话列表 + 主动撤销）、`permissions.ts`（PermissionService：当前用户功能权限码查询，导航/路由守卫/按钮显隐的数据源）、`agentSessions.ts`（agent 会话列表）、`agentAudit.ts`（工具调用/授权确认审计）、`agentRetrievalLogs.ts`（检索留痕）、`agentEventWindow.ts`（会话事件窗口）、`toApiError.ts`、`types.ts`。 |
| [src/auth/](src/auth/) | 会话态与功能权限：`store.ts`（令牌存取）、`refresh.ts` + `singleFlight.ts`（单飞刷新轮换）、`ProtectedRoute.tsx`（受保护路由）、`callbacks.ts`（会话失效回调接缝）、`permissionCodes.ts`（页面/操作权限码常量，对齐生产种子）、`permissions.tsx`（`PermissionProvider` 单次拉取权限码 + `usePermissions` + `RequirePermission` 路由守卫）。 |
| [src/agui/](src/agui/) | AG-UI 对话域：`agent.ts`（`HttpAgent` 工厂，fetch 注入 authFetch）、`events.ts`（事件 reducer 与历史快照折叠，纯函数）、`history.ts`（/agui/history 快照回放）、`confirmation.ts`（ack 决定与待确认查询）、`cancel.ts`（/agui/cancel）、`ids.ts`（运行侧 ID）。 |
| [src/ui/](src/ui/) | 共享纯视觉件：`icons.tsx` 仅保留 GrapeMark 品牌标；通用线性图标一律用 lucide-react，加载态用 `ui/spinner`。 |
| [src/pages/](src/pages/) | `LoginPage` 登录页、`ChatPage` 对话页（装配会话侧栏 / 消息流 / 确认卡 / 取消，侧栏底部有系统设置入口与更多菜单（运维复现/登出））、`ReplayPage` 运维复现视图；`chat/` 子目录为对话页拆件：`MessageList.tsx`（纯文本渲染）、`ConfirmationCard.tsx`（确认卡）；`replay/` 子目录为复现视图拆件：`compaction.ts`（压缩占位符识别纯函数）、`model.ts`（数据组装与 trace_id 关联纯函数）、`CompactionRefPanel.tsx`（占位徽标与原文对照展开）；`settings/` 子目录为管理页：`SettingsLayout.tsx`（左侧模块导航 + 内容区布局，导航按当前用户权限码过滤、拉取失败回落全量，未开放模块保留禁用占位机制；侧栏底部「返回工作台」链接回到对话主界面 `/`，个人中心无权限码恒显示）、`ProfilePage.tsx`（个人中心：维护本人昵称/邮箱/手机号/备注/语言与当前口令校验改密，目标恒取认证身份）、`UserManagePage.tsx`（用户管理样板页：列表/筛选/新建（含归属三元组下拉，未选归属用户时服务端默认为创建人）/编辑/改密/删除/设置角色/转移归属（整组替换，清空即落 NULL））、`DeptManagePage.tsx`（部门管理页：组织树展示/新建/编辑/移动/删除）、`UserGroupManagePage.tsx`（用户组管理页：列表/筛选/新建/编辑/删除/成员管理）、`RoleManagePage.tsx`（角色管理页：列表/筛选/新建/编辑/删除/数据范围配置/菜单授权树）、`MenuManagePage.tsx`（菜单管理页：权限码树展示/新建/编辑/移动/删除）、`DictManagePage.tsx`（字典管理页：类型列表/筛选/新建/编辑/删除 + 数据维护面板的增改删）、`ConfigManagePage.tsx`（配置管理页：全局配置/租户配置/功能开关/翻译四视图标签切换，列表/筛选/新建/编辑/删除 + 功能开关快捷启停）、`TenantManagePage.tsx`（租户管理页：列表/筛选/新建/编辑/停用恢复/删除，自身租户的停用恢复与删除禁用）、`AuditLogManagePage.tsx`（审计日志页：操作/登录两视图标签切换，操作日志详情弹窗渲染前后 JSON 快照）、`SessionManagePage.tsx`（会话管理页：列表/筛选/撤销确认）。 |

## UI 组件与主题

组件层取自 shadcn/ui 官方仓库 `new-york-v4`，落地 24 个本地改写组件到
[src/components/ui/](src/components/ui/)。组件清单、各组件与官方版的行为
差异、radix 的 jsdom 测试垫片，以及新组件落地必过的**兼容改写规则**（hex
颜色与 transform 位移——oklch / `color-mix()` / CSS `translate` / `rotate`
独立属性均超出兼容内核能力）见 [ui/README.md](src/components/ui/README.md)。

**暗色模式**：`components/theme-provider.tsx`（移植自官方 vite-app 模板，
零依赖）以 `theme`（dark/light/system）存 localStorage 并解析为 html 上的
`.dark` 类；`index.html` 头部脚本在样式加载前预挂同类，避免暗色用户刷新
闪白；切换器 `mode-toggle.tsx`（三选一下拉）位于登录页与对话页头部。
浅色主题保持原视觉（slate 中性面 + violet-600 品牌主色），深色主题为
slate 深蓝黑面 + 提亮一档的主色；页面内所有硬编码色已改语义 token，状态
点缀色（emerald/amber/red）在 JSX 里以 `dark:` 深色阶成对出现。

**主题色系（accent）**：明暗之外的第二维度，经 `html[data-accent]` 触发
index.css 的覆盖块，`ThemeProvider` 以 localStorage 键 `accent` 持久化、
跨标签同步，`index.html` 头部脚本预挂载防闪；切换器为头部调色板下拉
（`accent-picker.tsx`）。五个色系：`violet` 品牌紫（默认，:root 本值不落
属性）、`blue` 蓝、`green` 绿、`amber` 琥珀、`safe` 色盲友好。色系覆盖
品牌相关 token（primary/ring/品牌衍生面），其中色盲友好系额外把状态
软色对从 红/绿 换成 橙/青绿（Okabe-Ito 蓝主色，红绿色觉障碍可分辨审批
方向；语义仍以文字标签为主）。页面禁止硬编码品牌/状态 tint（会话选中、
链路高亮、追踪按钮、审批与执行芯片一律走 `--brand-*` / `--success-*` /
`--danger-*`），新色系须补齐浅深两套覆盖块。

**测试**：页面渲染需包 `ThemeProvider`（页头有 ModeToggle）；radix
Select / Popover+Command 的交互走「打开 → 点选选项」而非原生
selectOptions。

## 登录与令牌策略

- 契约：`POST /v1/auth/login`（tenant_id 可空，留空由服务端回落默认租户）、
  `POST /v1/auth/refresh`（轮换一对新令牌）、`POST /v1/auth/logout`（凭
  Authorization 头撤销当前会话，幂等）。字段名按 protojson 映射为 camelCase。
- accessToken 只存模块内存（页面刷新即失效）；refreshToken 存 localStorage
  （跨标签页共享同一会话，浏览器重启后仍可静默续期）。XSS 的真正缓解是
  CSP 与输出转义，存储介质只决定暴露时长与范围；refresh token 单次有效、
  服务端轮换即作废，泄漏窗口有限。
- 刷新轮换必须单飞：并发 401 只发一次 refresh（`auth/singleFlight.ts`），其余
  调用共享同一结果，避免旧刷新令牌被并发轮换互相作废；失败传播给全部等待方，
  清态后经 `callbacks.ts` 通知 App 跳转登录页。
- 登出不依赖后端结果：无论撤销成败（含网络失败）都清本地态并回登录页。

## 对话页形态

- 流式问答：`agui/agent.ts` 的 `createChatAgent` 构造 `HttpAgent`，注入
  `authFetch` 作为传输（EventSource 不能带头，fetch 可带令牌）；
  每次运行发起时从 auth store 取当前令牌，401 时复用 `api/http.ts` 的单飞刷新
  状态机，刷新失败清态回登录页。运行事件经 `agui/events.ts` 的纯函数 reducer
  折叠为渲染态：文本增量（TEXT_MESSAGE_*）、工具调用生命周期（TOOL_CALL_*，
  参数/结果折叠展示）、步骤（STEP_*）与错误（RUN_ERROR → 错误横幅）。
- 历史（thread）回放：切换会话时 POST `/agui/history` 拉一次 messages snapshot
  （`agui/history.ts` 手解 SSE data 行）。拿到首份 MESSAGES_SNAPSHOT 即取消
  流（reader.cancel）返回——历史立即渲染，不等快照运行走完 run 生命周期
  关流。不跟 follow 流——上游 track follow 的同毫秒事件可能被跳过（上游已
  知缺陷），快照即回放，新消息由运行事件折叠。
- 确认卡：流内 `tool_confirmation` 自定义事件（载荷字段与 ack `Payload` 一字
  对齐）出卡；决定 POST `/agui/ack/confirmation`（approve/deny × remember
  session|global），404 视为已失效收敛；待确认列表 GET `/agui/ack/confirmations?threadId=`
  在加载会话、运行结束与 30s 低频轮询兜底，服务端列表为对账基准。
- 决定后收敛：一次决定落定（成功与 404 已失效共用 onDecided 回调）后，按
  1s/2s/2s/3s/3s 的先密后疏节奏重拉历史快照（续跑产物要等服务端写回快照
  才能拉到），整表替换 items；相邻两轮一致（快照已稳定）或达轮次上限即停。
  会话切走即停；运行进行中跳过该轮（流事件负责实时折叠）；单轮失败静默。
- 取消：运行中显示取消按钮，POST `/agui/cancel`（RunAgentInput 形状，
  threadId + runId 定位）；404 为运行已结束的正常竞态。
- 会话列表：GET `/agui/sessions`（后端见 `internal/service/agent_sessions.go`）
  列出当前用户会话元数据，按更新时间降序；点击切换 thread（重置对话态并拉
  历史），新建会话生成新 threadId。归属由服务端认证身份决定。
- sanitize 口径：模型文本与工具参数/结果一律不可信，全部以 React 文本节点
  渲染（等价 textContent），不引入 innerHTML / markdown 注入路径；将来引入
  markdown 渲染必须先过 sanitizer（如 dompurify）并另行评审。

## 管理页（/settings）

对话页头部设置图标进入；`ProtectedRoute` + `PageTransition` 包裹，
`SettingsLayout` 左侧模块导航 + 右侧子路由内容区（`/settings/users`、
`/settings/depts`、`/settings/user-groups`、`/settings/roles`、
`/settings/menus`、`/settings/dicts`、`/settings/audit-logs`、
`/settings/sessions`、`/settings/configs`、`/settings/tenants`，索引路由等
权限码加载完成后重定向到声明顺序中首个有权限的管理模块，无管理权限时
回落到个人中心；拉取失败沿用全量回落）。

- 权限数据源：`PermissionProvider` 挂载时经 `GET /v1/permissions/mine`
  （`api/permissions.ts`）拉取一次当前用户功能权限码，导航、路由守卫与
  页面按钮共用；加载完成前导航留空（避免先展示无权模块再移除的闪烁），
  拉取失败（含 403/网络错误）回落静态全量展示（越权由 API 拒绝兜底）。
- 导航按权限要求过滤：列表/树页要求「页面码 + Query 码」同时持有
  （用户 `sys:user` + `sys:user:query`、部门 `sys:dept` + `sys:dept:query`、
  用户组 `sys:user-group` + `sys:user-group:query`、角色 `sys:role` +
  `sys:role:query`、菜单 `sys:menu` + `sys:menu:query`、字典 `sys:dict` +
  `sys:dict:query`、会话 `sys:session` + `sys:session:query`、租户
  `sys:tenant` + `sys:tenant:query`）；多视图页要求「页面码 + 任一视图码」
  （审计 `sys:audit` + 任一审计日志码、配置 `sys:config` + 任一配置族
  Query 码）。缺少任一要求的模块不展示，避免进入后列表请求 403。
- 路由守卫：管理子路由经 `RequirePermission` 包裹，使用与导航相同的
  权限要求；直接输入无权 URL 时重定向到个人中心（无码模块）。
- 按钮级显隐：新建按钮、行操作菜单项、字典数据维护面板的增改删按操作
  权限码显隐，配置四视图与审计双视图的标签页按视图 Query 码显隐（码表见
  `auth/permissionCodes.ts`，操作级码与后端 `internal/biz/permission.go`
  对齐，页面/视图码来自种子菜单）；无任何行操作时省略整列，无可用视图时
  给空状态。

- 数据形态：List 端点走 query.v1 动态查询协议——`api/query.ts` 声明
  `DynamicQuery`（where 条件恒 AND / sort / pagination），`QuerySet` 分页
  信封的 total 为 protojson 字符串型 int64；各页挂载时经 `api/querySchema.ts`
  拉取资源的 List 能力 schema，筛选与列定义动态响应 schema（可排序性、列
  显隐与页大小上限以其为准，默认排序取 schema 的 default_sort，加载失败给
  可重试横幅、不静默降级）；`manage/data-table.tsx` 负责组装查询、表头排序
  （asc → desc → 回落默认）与分页交互，筛选产物由各页以 `where` 传入（变化
  即回第 1 页），增删改后递增 `reloadKey` 重查。
- int64 口径：DTO 的 id 与归属三元组一律字符串；optional 字段未设置省略，
  请求体省略 undefined 键即未设置（protojson 对显式 null 亦按未设置处理）。
- 用户管理页（样板页）：筛选与列定义动态响应 ListSchema、新建（初始密码
  服务端哈希，状态缺省启用）、编辑（status 更新必填，Switch 显式表达）、
  修改密码、设置角色（远程搜索多选：并行加载当前可管理角色与候选首页，
  当前角色固定列在候选列表并预勾选（可分配候选接口会排除已拥有角色），清除后仍可重新勾选；提交按「管理范围内整组替换」携带 version；范围外
  locked 角色只提示数量、原样保留；409 `USER_ROLE_SET_CHANGED` 提示并
  重载当前集合，不重试写入）、转移归属（三个单选选择器接各自的 owner
  options 端点，当前值不在候选内时回显 `ID {value}（不可见）`，整组替换、
  清除即落 NULL）、删除（软删除，二次确认）。动作失败反馈走 toast（成功）
  与弹窗内联错误（失败保弹窗），列表加载失败在表格内给出可重试横幅。
  选项契约见 `query.v1.OptionQuery` / `Option` / `OptionSet`：候选接口是
  用户分配动作的独立并行接口，不接入 query-filter 的
  `ListSchemaField.api` 远程选项来源（后者仍只声明字段级选项数据源）。
- 部门管理页：主视图为组织树（`GET /v1/depts/tree` 一次返回租户内全部未删
  除部门，无分页，不接 data-table；节点行缩进呈现、带子节点可展开/收起，
  缺省全展开）。新建（上级部门下拉可选任意部门或根，部门管理员必选——复用
  归属用户候选端点（可见范围内的活跃用户，按 `sys:user:transfer-owner` 设
  码）的远程单选选择器，归属用户组可选（归属用户组候选端点同码），状态缺
  省启用，「新建子部门」预选父级）、编辑（覆盖上级部门、名称、部门管理员、
  归属用户组与状态；上级部门下拉排除自身子树，部门管理员/归属用户组为带
  当前值回显的远程单选选择器；PUT 覆盖语义，状态必填）、移动（新上级部门
  下拉排除被移动部门自身及其子树，选「无（根部门）」移为根即不发送
  parentId；成环/未知父级由后端拒绝并透传提示）、删除（软删除，二次确认；
  后端 409 DEPT_HAS_CHILDREN 时提示先删子级）。动作成功后整树重查；错误按
  org/v1 的 reason 消息映射为可读英文提示，词表外透传原始消息；树加载失败
  给可重试横幅。
- 用户组管理页：data-table 列表（筛选与列定义动态响应 ListSchema）。新建（组编码/组名租户内唯一，409 冲突映射为
  可读提示；组长用户 ID 可选、状态缺省启用）、编辑（仅 name/remark/status，
  组编码只读展示且不上送，组长不经更新面）、删除（软删除，二次确认）、成
  员管理（行内动作弹出面板：成员分页列表按添加时间升序；批量添加以逗号/
  空白分隔的用户 ID 去重上送，幂等跳过已有成员并提示实际新增条数，任一
  用户未知/跨租户/已删除由后端整批拒绝、前端透传 reason 映射文案；逐行移
  除，非成员幂等；增删成功重查当前页；面板保持打开，全局 toast 位于
  aria-hidden 子树内，相关测试断言显式带 `hidden: true`）。错误词表与部门
  页同模式：org/v1 的 USER_GROUP_* reason 消息映射为可读英文提示，词表外
  透传原始消息。
- 角色管理页：data-table 列表（筛选与列定义动态响应 ListSchema）。
  新建（编码与角色名租户内唯一且创建后不可变，409 冲突映射为可读提示），
  编辑（仅 name/status/remark，status 更新语义必填；编码、类型与数据范围
  不经更新面），删除（软删除，二次确认；系统预置角色
  ROLE_PRESET_UNDELETABLE 映射为不可删提示）。数据范围配置：弹窗先
  `GET /v1/roles/{id}/data-scope` 加载配置再渲染表单（加载失败给可重试横
  幅）——数据范围规则下拉 1-7；规则为 4 指定人/5 指定部门/6 用户组时渲
  染对应类型的 ID 集合输入面（其余规则无 ID 面），规则切换时输入面随之
  重填；分配层级限定部门 ID 留空即 NULL（全租户可分配）；分发部门集合整
  组替换、留空即遵循通用分配逻辑。保存整组替换：ID 范围明细只替换当前规
  则对应类型的条目（其余类型的惰性条目原样透传），全部 ID 校验正整数口径
  与后端一致；未知引用由后端 ROLE_INVALID_ARGUMENT 拒绝并映射文案。菜单
  授权：弹窗并行加载菜单树（`GET /v1/menus/tree`）与当前有效授权集合
  （`GET /v1/roles/{id}/menus`，停用/软删菜单不在回显集合），带 checkbox
  的树渲染（停用菜单 checkbox 禁用），勾选目录级联勾选/取消整棵子树，父
  节点按部分子授权呈半选态；保存按勾选集合整组差量替换（空集合合法即收
  回全部授权），树未成功加载（或授权未回显）前保存按钮禁用——没有回显
  基线不能构造完整集合，防止误清空授权。错误词表同模式：perm/v1 的
  ROLE_* reason 消息映射为可读英文提示，词表外透传原始消息。
- 菜单管理页：主视图为权限码树（`GET /v1/menus/tree` 一次返回租户内全部
  未删除菜单，无分页，不接 data-table；节点行缩进呈现、带子节点可展开/
  收起，缺省全展开，行内尾随类型徽标、权限编码、排序号与状态）。新建
  （上级菜单下拉可选任意菜单或根；权限编码与菜单类型为创建后不可变列、
  仅在创建表单出现；排序号校验整数口径），编辑（仅可变字段 name/status/
  icon/routePath/component/permissionExpr/sortOrder/remark，权限编码与类
  型以只读文案展示），移动（新上级菜单下拉**排除被移动菜单自身及其整棵
  子树**，在前端挡掉明显成环的选项；未知父级与其余成环路径仍由后端
  MENU_INVALID_ARGUMENT 拒绝并透传提示；选「无（根菜单）」移为根即不发
  送 parentId；move 端点为 PUT），删除（软删除，二次确认；后端 409
  MENU_HAS_CHILDREN 时提示先删子级）。动作成功后整树重查；错误按 perm/v1
  的 MENU_* reason 消息映射为可读英文提示，词表外透传原始消息；树加载失
  败给可重试横幅。
- 字典管理页：data-table 类型列表（筛选与列定义动态响应 ListSchema）。新
  建（类型编码租户内唯一、创建后不可变，409 冲突映射为可读提示）、编辑
  （仅 name/remark/status）、删除（软删除，二次确认；该类型下仍有未删除
  数据时后端拒绝 DICT_TYPE_HAS_DATA 并提示先清空）。数据维护：行内动作打
  开面板，按 `dictTypeId` 过滤拉取该类型的数据项（sortOrder 升序、同序按
  id）；添加（标签/字典值/值类型/排序号，值类型下拉为
  string/integer/boolean/float/json 白名单）、行内编辑（仅
  label/sortOrder/status，字典值与值类型只读）、逐行删除；同类型下字典值
  唯一（409 冲突映射文案）；增删改后重查当前页。错误按 dict/v1 的 DICT_*
  reason 消息映射为可读英文提示，词表外透传原始消息。
- 审计日志页：操作日志/登录日志两个只读视图以标签切换（切换时清空筛选并
  重查；列与筛选分别动态响应 `sys_operation_log` / `sys_login_log` 的
  ListSchema，页面不设写动作——日志行由登录用例与入口操作审计中间件写入）。
  操作日志行内「详情」经 id 点查渲染全字段与操作
  前后 JSON 快照（新值取响应/请求载荷，密码类固定掩码、其余敏感键无盐哈希；
  旧值当前不在通用中间件采集，显示「（无）」）；登录日志以徽标呈现成功/失败。两个视图的
  加载失败都给可重试横幅。
- 会话管理页：data-table 会话列表（筛选与列定义动态响应 ListSchema；状态
  与撤销态以徽标呈现，令牌哈希不入 DTO）。行内「撤销」（二次确认，文案
  说明访问与刷新令牌一并失效）；撤销幂等——目标不存在、已撤销或已被清理
  均按成功处理，成功后重查列表，失败以 toast 呈现后端原始消息。
- 配置管理页：全局配置/租户配置/功能开关/翻译四个视图以标签切换（切换时
  清空筛选并重查；列与筛选分别动态响应 `sys_config` / `sys_tenant_config` /
  `sys_feature_flag` / `sys_translation` 的 ListSchema，打开某视图才拉取其
  schema 与列表）。全局与租户配置：新建（配置键唯一，值类型下拉为
  string/integer/boolean/float/json 白名单）、编辑（仅 name/value/remark/
  status，配置键与值类型只读且不上送）、删除（软删除，二次确认）。功能
  开关：新建/编辑（功能键创建后不可变）、行内「启用/停用」快捷切换——更新
  为覆盖语义，status 与 isEnabled 必须同时上送并带存量 name/description/
  remark，避免省略 isEnabled 落 false 或清空描述；删除（软删除，二次确认）。
  翻译：新建（来源表/来源行/来源列/语言代码/翻译文本，rowId 须为 int64
  范围内的正整数、locale 须为 BCP-47 基本形态如 zh-CN，提交前内联校验）、
  编辑（身份四列只读）、删除（软删除，二次确认）。列表的配置值列由 UI manifest
  声明为 optional（默认显示、可切换）；写动作成功出 toast，表单弹窗失败保留内联
  提示；删除失败以 toast 呈现。错误按 config/v1 的 CONFIG_* /
  TENANT_CONFIG_* / FEATURE_FLAG_* / TRANSLATION_* reason 码优先兜底、
  消息文案词表次之，映射为可读英文提示，两表都未命中则透传原始消息；四个
  视图的加载失败都给可重试横幅。
- 租户管理页：data-table 租户列表（筛选与列定义动态响应 `sys_tenant` 的
  ListSchema；列由 schemaColumns 生成，编码/名称/父级租户/状态徽标/默认
  语言/最大用户数/到期时间/更新时间等均可见，父级/最大用户数/到期时间由
  UI manifest 声明为 optional，空值出占位横线）。新建（id 服务端生成；parentId 不上送，
  固定挂在当前登录租户下；编码与名称未删除租户间唯一，编码创建后不可变；
  到期时间按 date 输入并在提交时转当日 UTC 的 RFC3339；最大用户数须为正
  整数；默认语言缺省 zh-CN 且按 BCP-47 基本形态校验）、编辑（编码只读，
  仅名称/联系人/联系电话/到期时间/最大用户数/默认语言/状态可改；不提交
  编码、父级与归属租户，到期时间与最大用户数留空即清空）、停用/恢复
  （二次确认，幂等目标状态写；停用仅置数据状态，当前版本不承诺阻断该
  租户的登录与访问）、删除（软删除，二次确认；存在子租户时后端返回
  TENANT_HAS_CHILDREN，提示先删子租户并保留弹窗）。当前登录租户
  （`getUser().tenantId` 与行 id 相同）的停用/恢复与删除动作禁用，菜单
  文字直接标注「（当前登录租户）」且页面有常驻可见说明；编辑弹窗的状态
  开关对自身租户禁用并说明原因。新建/编辑失败在弹窗内联提示；停用/恢复/
  删除失败在确认弹窗内联提示并同时 toast；错误按 tenant/v1 的消息专用
  文案优先（自身状态变更、caller 租户缺失等）、`TENANT_*` reason 码次之
  兜底，两表都未命中则透传原始消息。

## 运维复现视图

证据面、关联键与写入机制的完整口径见根目录 [replay.md](../replay.md)；
本节只描述页面行为。

`/replay` 受保护路由（ChatPage 头部有入口）：会话选择 → 四联证据同屏——
对话流回放、工具调用审计、授权确认记录、检索留痕。同一请求内的证据以
trace_id 串联；确认决定属独立 ack 请求，live 路径的确认行与工具行 trace
不同、recovery 路径相同，两者按 toolCallId/confirmationId 关联。验收
口径：任一回答可反查全部召回证据。

- 对话回放：复用对话页的 `/agui/history` 快照（`agui/history.ts`），快照即
  回放；上游 track follow 的同毫秒事件缺陷（上游已知缺陷）以「刷新
  快照」按钮手动兜底，不跟 follow 流。无 track 事件的会话（仅存 ack 待
  确认指针、或 track 事件已过保留期）快照运行上游 500，回放面按空态提示
  降级（`historyUnavailable` 分支），审计/授权/检索三面照常装载——回放
  只是四联证据之一，不以它的失败盖掉整页。
- 工具调用审计 / 授权确认记录：GET `/v1/agent/audit/tool-calls` 与
  `/confirmations`（`api/agentAudit.ts`）；检索留痕：GET
  `/v1/agent/retrieval-logs`（`api/agentRetrievalLogs.ts`）。按会话过滤
  分页沿用端点口径（offset/limit，每面默认 50），返回行是落库时已脱敏的
  形态，前端直接渲染、不回传原文。工具重试次数后端恒记 0（框架不暴露），
  前端不声明也不呈现该字段。
- trace_id 关联：三证据面的非空 trace_id 汇总为下拉（按出现频次降序，
  `replay/model.ts` 纯函数）。下拉为 Popover+Command 组合的可搜索
  Combobox：输入子串即时前端过滤候选（大小写不敏感，「全部链路」清除项
  恒在，无匹配给占位提示），并补齐方向键导航与 Enter 选中（cmdk 内建；
  锚点上的交互经 `onInteractOutside`/`onFocusOutside` 豁免，避免输入期间
  弹层被外部点击判定关闭）。选中链路只做高亮对比、不隐藏数据：三个证据
  面全量渲染，命中行品牌紫高亮、未命中行灰显（`matchesTraceId` 谓词区分
  两种呈现），保住证据间的上下文；回放流经 `relatedToolCallIds` 桥接
  （toolCallId 取审计与确认两面
  并集，live 路径两面 trace 不同），命中项所在的「用户消息为界」整段
  经 `relatedChatItemIds` 邻近归属保持原样，段外项降透明度（无任何命
  中项时不降，避免整流变灰而无所指）。凡携带 trace_id 的证据行（工具
  审计/授权确认/检索留痕，含回放工具项展开后的内嵌行）都以「追踪」
  按钮一键选中该链路（再点同值反选清除），与下拉选中同一效果。工具
  调用项摘要行常显审计/确认关联数徽标，展开后在参数与结果原文下内嵌
  同一 toolCallId 的审计行与授权确认行。确认行与工具行的 trace 在
  live 路径不同，跨 trace 的对账以 toolCallId/confirmationId 为准；
  下拉按 trace 分组，不自动把两行并到一组。
- 确认行配色按决定方向约定区分（`decisionChipClass`）：approved / auto /
  `*_auto` 绿（放行方向），denied / auto_deny / `*_deny` 红（拒绝方向，
  系统决定词表口径见 [replay.md](../replay.md) 授权确认审计节）；词表外
  取值 fail-closed 显红——后端 biz 模式校验把门决定词表，前端不再枚举
  白名单。
- 压缩占位与原文对照：`replay/compaction.ts` 按 trpc-agent-go
  context_compact 的字面形状识别占位符（elided 引用行、方括号截断标记
  含紧凑与裸变体、策略占位），提取 event_id 恢复引用；带引用的经
  GET `/agui/event-window`（`api/agentEventWindow.ts`，后端见
  `internal/agent/httpapi/eventwindow.go`）懒加载会话事件原文并排对照，失败可
  重试；无引用的只标注「已压缩（不可反查）」。识别以形状特征为准、不
  假设必然出现（占位符产生在请求投影副本，回放原文里通常不出现）。
- 数据组装（trace 汇总/过滤/高亮/hits 解析）与占位符识别均为纯函数，
  畸形输入（非字符串、坏 JSON、缺字段）安全降级不抛错；渲染延续对话页
  的纯文本 sanitize 基线。

## 命令

| 命令 | 说明 |
| ---- | ---- |
| `npm run dev` | 开发服务器（代理后端）。 |
| `npm run build` | 类型检查 + 产物构建到 `dist/`。 |
| `npm test` | Vitest 单元测试（jsdom）。 |
| `npm run lint` | ESLint（typescript-eslint 推荐集，从简口径）。 |
