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
| [src/api/](src/api/) | 会话 API 与带 401 状态机的 fetch 封装：`session.ts`（登录 / 登出）、`http.ts`（附 Bearer 头、401 → 单飞刷新 → 重放一次）、`toApiError.ts`、`types.ts`。 |
| [src/auth/](src/auth/) | 会话态：`store.ts`（令牌存取）、`refresh.ts` + `singleFlight.ts`（单飞刷新轮换）、`ProtectedRoute.tsx`（受保护路由）、`callbacks.ts`（会话失效回调接缝）。 |
| [src/pages/](src/pages/) | `LoginPage` 登录页、`ChatPage` 占位对话页。 |
| [src/agui/](src/agui/) | A2 对话页接入点：`@ag-ui/client` `HttpAgent` 工厂（本任务只留接缝）。 |

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

## 命令

| 命令 | 说明 |
| ---- | ---- |
| `npm run dev` | 开发服务器（代理后端）。 |
| `npm run build` | 类型检查 + 产物构建到 `dist/`。 |
| `npm test` | Vitest 单元测试（jsdom）。 |
| `npm run lint` | ESLint（typescript-eslint 推荐集，从简口径）。 |
