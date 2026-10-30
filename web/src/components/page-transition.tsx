import type { ReactNode } from 'react'

// PageTransition 页面级过渡：路由切换时整页淡入并轻微上浮（.page-enter，
// 300ms easeOutCubic，见 index.css），消除对话页与复现视图间硬切换的
// 突兀感。挂载即播；不做出场动画——那需要双树并挂的过渡库，成本与收益
// 不成比例。
export function PageTransition({ children }: { children: ReactNode }) {
  return <div className="page-enter h-full">{children}</div>
}
