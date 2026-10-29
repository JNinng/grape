# web

Grape 前端：Vite + React + TypeScript + Tailwind CSS + `@ag-ui/client`。构建产物经
[embed.go](embed.go) 的 `go:embed` 进 Go 二进制，由 `internal/server` 挂载静态托管
与 SPA fallback，最终单二进制交付。

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
| [src/index.css](src/index.css) | Tailwind 入口与全局设计基调：品牌紫主色、字体栈、细滚动条基础层，以及按钮（btn）、输入框（field）、卡片（card）等跨页面组件类；页面样式一律写工具类，不另散落 CSS。色板以字面 hex 覆盖默认 oklch（旧内核解析不了 oklch 会使颜色全失效），并避免颜色透明度修饰符（color-mix 同因），新增色阶须在 @theme 补齐。 |
| [src/api/](src/api/) | 会话 API 与带 401 状态机的 fetch 封装：`session.ts`（登录 / 登出）、`http.ts`（附 Bearer 头、401 → 单飞刷新 → 重放一次）、`agentSessions.ts`（agent 会话列表）、`agentAudit.ts`（工具调用/授权确认审计）、`agentRetrievalLogs.ts`（检索留痕）、`agentEventWindow.ts`（会话事件窗口）、`toApiError.ts`、`types.ts`。 |
| [src/auth/](src/auth/) | 会话态：`store.ts`（令牌存取）、`refresh.ts` + `singleFlight.ts`（单飞刷新轮换）、`ProtectedRoute.tsx`（受保护路由）、`callbacks.ts`（会话失效回调接缝）。 |
| [src/agui/](src/agui/) | AG-UI 对话域：`agent.ts`（`HttpAgent` 工厂，fetch 注入 authFetch）、`events.ts`（事件 reducer 与历史快照折叠，纯函数）、`history.ts`（/agui/history 快照回放）、`confirmation.ts`（ack 决定与待确认查询）、`cancel.ts`（/agui/cancel）、`ids.ts`（运行侧 ID）。 |
| [src/ui/](src/ui/) | 共享纯视觉件：`icons.tsx` 内联 SVG 图标集（品牌标与线性图标），全部 aria-hidden 不携带可访问名，语义由周边文本承载。 |
| [src/pages/](src/pages/) | `LoginPage` 登录页、`ChatPage` 对话页（装配会话侧栏 / 消息流 / 确认卡 / 取消）、`ReplayPage` 运维复现视图；`chat/` 子目录为对话页拆件：`MessageList.tsx`（纯文本渲染）、`ConfirmationCard.tsx`（确认卡）；`replay/` 子目录为复现视图拆件：`compaction.ts`（压缩占位符识别纯函数）、`model.ts`（数据组装与 trace_id 关联纯函数）、`CompactionRefPanel.tsx`（占位徽标与原文对照展开）。 |

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
  `replay/model.ts` 纯函数）。选中链路只做高亮对比、不隐藏数据：三个证据
  面全量渲染，命中行品牌紫高亮、未命中行灰显（`matchesTraceId` 谓词区分
  两种呈现），保住证据间的上下文；回放流经 `relatedToolCallIds` 桥接
  （toolCallId 取审计与确认两面并集，live 路径两面 trace 不同），命中项所
  在的「用户消息为界」整段经 `relatedChatItemIds` 邻近归属保持原样，段外
  项降透明度（无任何命中项时不降，避免整流变灰而无所指）；表内 trace 值
  可点击反选。工具调用项摘要行常显审计/确认关联数徽标，展开后在参数与
  结果原文下内嵌同一 toolCallId 的审计行与授权确认行。确认行与工具行的
  trace 在 live 路径不同，跨 trace 的对账以 toolCallId/confirmationId 为准；
  下拉按 trace 分组，不自动把两行并到一组。
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
