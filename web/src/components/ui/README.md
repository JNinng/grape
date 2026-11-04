# src/components/ui — shadcn/ui 本地改写组件层

组件取自 shadcn/ui 官方仓库 `new-york-v4`（依赖统一包 `radix-ui`、`cn`、
`class-variance-authority`、`lucide-react`、`cmdk`、`tw-animate-css`），
落地 23 个够用即止的组件：alert、alert-dialog、badge、button、card、
checkbox、collapsible、command、dialog、dropdown-menu、empty、input、
label、pagination、popover、select、separator、spinner、switch、table、
textarea、toast、tooltip。

[components.json](../../../components.json) 保留 shadcn CLI 配置，未来
`npx shadcn add <x>` 拉取新组件后须按下方兼容改写规则过一遍再提交。

## 本地改写组件（与官方版的行为差异）

- **pagination**：按钮化改写（官方为链接版）——管理页分页是页内状态切换，
  不是路由跳转。
- **toast**：零依赖本地实现（模块级 store + `useSyncExternalStore`），
  `<Toaster/>` 全局挂载于 App，`toast.success/error/info` 命令式入口。
- **checkbox**：以半选态（`checked='indeterminate'`）呈现减号指示，供角色
  页授权树表达「部分子节点已授权」。

## 兼容改写规则（新组件落地必须过一遍）

官方组件默认依赖 oklch 色值与透明度修饰符（编译为 `color-mix()`，需
Chrome 111+）；Tailwind v4 的位移/旋转工具类编译为独立 CSS `translate` /
`rotate` 属性（需 Chrome 104+）。本项目兼容目标内核两者都不支持，不认识
的声明会被**静默丢弃**——颜色失效或元素纹丝不动。语义 token 的取值与
浅深两套主题落在 [src/index.css](../../index.css)，全内核可解析。

1. 颜色一律字面 hex；半透明（遮罩、弱化底色）用 rgba() 形式的专用 token
   （`--overlay`、`--destructive-soft`、`--soft`），rgba 全内核可解析。
2. 组件内 `/NN` 透明度修饰符全部消除：悬停加深 → `--primary-hover` /
   `--destructive-hover`；聚焦/校验环 → 实色 `ring-ring` /
   `ring-destructive-soft`；弱化悬停面 → `bg-soft`。
3. `dark:` 分支若仅为补偿 alpha 删除；若为真实配色差异改写为语义 token
   （两套主题在 `:root` / `.dark` 各自给值）。
4. 位移与旋转不用 `translate-x-*` / `translate-y-*` / `rotate-*` 工具类，
   一律写 `transform`（任意属性形式，如
   `data-[state=checked]:[transform:translateX(1rem)]`）。同元素既要位移
   又要旋转必须合并为单个 transform——translate/rotate 独立属性拆开写，
   旧内核两条声明一起失效（tooltip 箭头先例）。模态居中不用位移，用
   `fixed inset-0 grid place-items-center` 容器（alert-dialog 先例）；
   动画的 transform 关键帧（tw-animate-css 走传统 `transform`）不受影响。
   已按本条改写：switch（滑块位移）、tooltip（箭头居中 + 旋转）、
   select（popper 四向贴边微调）、alert / table（svg 与行内 checkbox
   对齐微调）。

## 测试垫片

radix 组件在 jsdom 下需要 [src/test/setup.ts](../../../src/test/setup.ts)
的无操作垫片：pointer capture、scrollIntoView、ResizeObserver、matchMedia。
