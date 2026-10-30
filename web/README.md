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
| [src/components/](src/components/) | shadcn/ui 组件层：`ui/` 下 18 个经 hex 兼容改写的官方组件（new-york-v4 口径，见「UI 组件与主题」节）、`theme-provider.tsx`（零依赖主题提供者：明暗 + 色系持久化、跟随系统、跨标签同步）、`mode-toggle.tsx`（浅色/深色/跟随系统切换器）、`accent-picker.tsx`（主题色系切换器）、`page-transition.tsx`（路由换页淡入过渡）。 |
| [src/api/](src/api/) | 会话 API 与带 401 状态机的 fetch 封装：`session.ts`（登录 / 登出）、`http.ts`（附 Bearer 头、401 → 单飞刷新 → 重放一次）、`agentSessions.ts`（agent 会话列表）、`agentAudit.ts`（工具调用/授权确认审计）、`agentRetrievalLogs.ts`（检索留痕）、`agentEventWindow.ts`（会话事件窗口）、`toApiError.ts`、`types.ts`。 |
| [src/auth/](src/auth/) | 会话态：`store.ts`（令牌存取）、`refresh.ts` + `singleFlight.ts`（单飞刷新轮换）、`ProtectedRoute.tsx`（受保护路由）、`callbacks.ts`（会话失效回调接缝）。 |
| [src/agui/](src/agui/) | AG-UI 对话域：`agent.ts`（`HttpAgent` 工厂，fetch 注入 authFetch）、`events.ts`（事件 reducer 与历史快照折叠，纯函数）、`history.ts`（/agui/history 快照回放）、`confirmation.ts`（ack 决定与待确认查询）、`cancel.ts`（/agui/cancel）、`ids.ts`（运行侧 ID）。 |
| [src/ui/](src/ui/) | 共享纯视觉件：`icons.tsx` 仅保留 GrapeMark 品牌标；通用线性图标一律用 lucide-react，加载态用 `ui/spinner`。 |
| [src/pages/](src/pages/) | `LoginPage` 登录页、`ChatPage` 对话页（装配会话侧栏 / 消息流 / 确认卡 / 取消）、`ReplayPage` 运维复现视图；`chat/` 子目录为对话页拆件：`MessageList.tsx`（纯文本渲染）、`ConfirmationCard.tsx`（确认卡）；`replay/` 子目录为复现视图拆件：`compaction.ts`（压缩占位符识别纯函数）、`model.ts`（数据组装与 trace_id 关联纯函数）、`CompactionRefPanel.tsx`（占位徽标与原文对照展开）。 |

## UI 组件与主题

组件层取自 shadcn/ui 官方仓库 `new-york-v4`（依赖统一包 `radix-ui`、`cn`、
`class-variance-authority`、`lucide-react`、`cmdk`、`tw-animate-css`），落地
18 个够用即止的组件到 `src/components/ui/`：alert、alert-dialog、badge、
button、card、collapsible、command、dropdown-menu、empty、input、label、
popover、select、separator、spinner、table、textarea、tooltip。
`components.json` 保留 shadcn
CLI 配置，未来 `npx shadcn add <x>` 拉取新组件后须做一次 hex 兼容改写再提交。

**hex 兼容改写规则**（官方组件默认依赖 oklch 与透明度修饰符，编译为
`color-mix()`，需 Chrome 111+；本项目坚持全内核可解析的字面色值）：

1. 语义 token（`--background`、`--primary`、`--border` 等）值一律字面
   hex；半透明（遮罩、弱化底色）用 rgba() 形式的专用 token
   （`--overlay`、`--destructive-soft`、`--soft`），rgba 全内核可解析。
2. 组件内 `/NN` 透明度修饰符全部消除：悬停加深 → `--primary-hover` /
   `--destructive-hover`；聚焦/校验环 → 实色 `ring-ring` /
   `ring-destructive-soft`；弱化悬停面 → `bg-soft`。
3. 组件内 `dark:` 分支若仅为补偿 alpha 删除，若为真实配色差异改写为
   语义 token（两套主题在 `:root` / `.dark` 各自给值）。
4. 定位不用 `translate-x-/-y-*` 工具类——Tailwind v4 将其编译为独立 CSS
   `translate` 属性（需 Chrome 104+），旧内核整条丢弃会使定位失效（如
   弹框偏右下）；居中一律用 `fixed inset-0 grid place-items-center` 容器
   （alert-dialog 已如此实现），动画的 transform 关键帧（tw-animate-css
   走传统 `transform`）不受影响。

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

**测试**：radix 组件在 jsdom 下需要 `src/test/setup.ts` 的无操作垫片
（pointer capture、scrollIntoView、ResizeObserver、matchMedia）；页面渲染
需包 `ThemeProvider`（页头有 ModeToggle）；radix Select / Popover+Command
的交互走「打开 → 点选选项」而非原生 selectOptions。

## 登录与令牌策略

- 契约：`POST /v1/auth/login`（tenant_id 可空，留空由服务端回落默认租户）、
  `POST /v1/auth/refresh`（轮换一对新令牌）、`POST /v1/auth/logout`（凭
  Authorization 头撤销当前会话，幂等）。字段名按 protojson 映射为 camelCase。
- accessToken 只存模块内存（页面刷新即失效）；refreshToken 存 sessionStorage
  （单标签页会话内有效，不跨标签共享、不跨浏览器重启残留）。XSS 的真正缓解是
  CSP 与输出转义，存储介质只决定暴露时长与范围；localStorage 禁用。
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
  UI 以「0（框架未暴露）」呈现避免误读。
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
  `internal/agent/eventwindow.go`）懒加载会话事件原文并排对照，失败可
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
