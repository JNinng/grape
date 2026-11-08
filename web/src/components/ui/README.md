# src/components/ui — shadcn/ui 本地改写组件层

shadcn/ui 官方 `new-york-v4` 组件的本地改写层：统一依赖包 `radix-ui`、
`cn`、`class-variance-authority`、`lucide-react`、`cmdk`、
`tw-animate-css`、`react-day-picker`。当前落地 24 个够用即止的组件：
alert、alert-dialog、badge、button、calendar、card、checkbox、
collapsible、command、dialog、dropdown-menu、empty、input、label、
pagination、popover、select、separator、spinner、switch、table、
textarea、toast、tooltip。

[components.json](../../../components.json) 保留 shadcn CLI 配置；未来
`npx shadcn add <x>` 拉取新组件后，须按下方兼容改写规则过一遍再提交。

## 关键文件导览（本地改写差异）

| 文件 | 职责与本地改写差异 |
| ---- | ---- |
| pagination.tsx | 按钮化改写（官方为链接版）：管理页分页是页内状态切换，只保留上/下一页，页码由 DataTable 拼装。 |
| toast.tsx | 零依赖（store + `useSyncExternalStore`），`<Toaster/>` 全局挂载，入口 `toast.success/error/info`。 |
| checkbox.tsx | 半选态以减号图标呈现；指示器不做位移动画。 |
| calendar.tsx | react-day-picker v10 自绘、未引入样式表；导航为单月布局（无下拉年月），日期样式由 DayButton 按 modifiers 计算。 |
| command.tsx | 移除依赖 Dialog 的 `CommandDialog`（无命令面板场景）。 |

calendar 的消费方为 manage/query-filter 的日期值编辑器。

其余组件的行为与官方一致，差异只来自下方兼容改写规则（颜色、
透明度、位移与居中方式）。

## 兼容改写规则（新组件落地必须过一遍）

官方组件默认依赖 oklch 色值与透明度修饰符（编译为 `color-mix()`，需
Chrome 111+）；Tailwind v4 的位移/旋转工具类编译为独立 CSS `translate`
/ `rotate` 属性（需 Chrome 104+）。本项目兼容目标内核两者都不支持，
不认识的声明会被**静默丢弃**——颜色失效或元素纹丝不动。语义 token 的
取值与浅深两套主题落在 [src/index.css](../../index.css)，全内核可解析。

1. 颜色一律字面 hex；半透明（遮罩、弱化底色）用 rgba() 形式的专用
   token（`--overlay`、`--destructive-soft`、`--soft`），rgba 全内核可
   解析。
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
   `fixed inset-0 grid place-items-center` 容器（dialog / alert-dialog
   先例）；动画的 transform 关键帧（tw-animate-css 走传统 `transform`）
   不受影响。

已按位移/旋转规则改写：switch（滑块位移）、tooltip（箭头居中 + 旋转）、
select（popper 四向贴边微调）、alert / table（svg 与行内 checkbox
对齐微调）、dialog / alert-dialog（全屏 grid 居中）。

## 测试

- 组件测试与被测代码同目录（`*.test.tsx`），经 Testing Library 断言
  可观察行为与可访问名，不绑定内部实现。
- radix 交互走「打开 → 点选选项」，不用原生 `selectOptions`；页面级
  测试涉及主题切换器时需包 `ThemeProvider`（见
  [../../../README.md](../../../README.md)）。
- toast 用假定时器推进自动消失，避免真实等待；新增组件至少覆盖开合、
  受控态与键盘可达。

## 测试垫片

radix 组件在 jsdom 下需要 [../../test/setup.ts](../../test/setup.ts) 的
无操作垫片：pointer capture、scrollIntoView、ResizeObserver、matchMedia。
