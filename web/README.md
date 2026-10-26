# web

Grape 前端（A1 起步）：Vite + React + TypeScript + `@ag-ui/client`。构建产物经
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
| [vite.config.ts](vite.config.ts) | Vite / Vitest 配置：React 插件、开发代理、jsdom 测试环境。 |
| [index.html](index.html) | Vite 入口（构建后变形为 `dist/index.html`，与仓库占位页是两回事）。 |
| [src/api/](src/api/) | 会话 API 与带 401 状态机的 fetch 封装：`session.ts`（登录 / 登出）、`http.ts`（附 Bearer 头、401 → 单飞刷新 → 重放一次）、`agentSessions.ts`（agent 会话列表）、`toApiError.ts`、`types.ts`。 |
| [src/auth/](src/auth/) | 会话态：`store.ts`（令牌存取）、`refresh.ts` + `singleFlight.ts`（单飞刷新轮换）、`ProtectedRoute.tsx`（受保护路由）、`callbacks.ts`（会话失效回调接缝）。 |
| [src/agui/](src/agui/) | AG-UI 对话域：`agent.ts`（`HttpAgent` 工厂，fetch 注入 authFetch）、`events.ts`（事件 reducer 与历史快照折叠，纯函数）、`history.ts`（/agui/history 快照回放）、`confirmation.ts`（ack 决定与待确认查询）、`cancel.ts`（/agui/cancel）、`ids.ts`（运行侧 ID）。 |
| [src/pages/](src/pages/) | `LoginPage` 登录页、`ChatPage` 对话页（装配会话侧栏 / 消息流 / 确认卡 / 取消）；`chat/` 子目录为对话页拆件：`MessageList.tsx`（纯文本渲染）、`ConfirmationCard.tsx`（确认卡）。 |

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

## 对话页形态（A2）

- 流式问答：`agui/agent.ts` 的 `createChatAgent` 构造 `HttpAgent`，注入
  `authFetch` 作为传输（EventSource 不能带头，fetch 可带令牌，见 TODO 决策 2）；
  每次运行发起时从 auth store 取当前令牌，401 时复用 `api/http.ts` 的单飞刷新
  状态机，刷新失败清态回登录页。运行事件经 `agui/events.ts` 的纯函数 reducer
  折叠为渲染态：文本增量（TEXT_MESSAGE_*）、工具调用生命周期（TOOL_CALL_*，
  参数/结果折叠展示）、步骤（STEP_*）与错误（RUN_ERROR → 错误横幅）。
- 历史（thread）回放：切换会话时 POST `/agui/history` 拉一次 messages snapshot
  （`agui/history.ts` 手解 SSE data 行）。不跟 follow 流——上游 track follow 的
  同毫秒事件可能被跳过（TODO §1.5 / ack D4），快照即回放，新消息由运行事件折叠。
- 确认卡：流内 `tool_confirmation` 自定义事件（载荷字段与 ack `Payload` 一字
  对齐）出卡；决定 POST `/agui/ack/confirmation`（approve/deny × remember
  session|global），404 视为已失效收敛；待确认列表 GET `/agui/ack/confirmations?threadId=`
  在加载会话、运行结束与 30s 低频轮询兜底，服务端列表为对账基准。
- 取消：运行中显示取消按钮，POST `/agui/cancel`（RunAgentInput 形状，
  threadId + runId 定位）；404 为运行已结束的正常竞态。
- 会话列表：GET `/agui/sessions`（后端见 `internal/service/agent_sessions.go`）
  列出当前用户会话元数据，按更新时间降序；点击切换 thread（重置对话态并拉
  历史），新建会话生成新 threadId。归属由服务端认证身份决定。
- sanitize 口径：模型文本与工具参数/结果一律不可信，全部以 React 文本节点
  渲染（等价 textContent），不引入 innerHTML / markdown 注入路径；将来引入
  markdown 渲染必须先过 sanitizer（如 dompurify）并另行评审。

## 命令

| 命令 | 说明 |
| ---- | ---- |
| `npm run dev` | 开发服务器（代理后端）。 |
| `npm run build` | 类型检查 + 产物构建到 `dist/`。 |
| `npm test` | Vitest 单元测试（jsdom）。 |
| `npm run lint` | ESLint（typescript-eslint 推荐集，从简口径）。 |
