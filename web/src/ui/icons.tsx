// GrapeMark 品牌标：渐变圆角方块 + 葡萄果粒，仅登录页与各页头部使用。
// 通用图标一律改用 lucide-react；此品牌标为项目专属，保留手绘。
export function GrapeMark({ className = '' }: { className?: string }) {
  return (
    <span
      className={`inline-flex shrink-0 items-center justify-center rounded-xl bg-linear-to-br from-violet-500 to-fuchsia-500 text-white shadow-xs ${className}`}
    >
      <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className="h-[62%] w-[62%]">
        <path d="M12.2 6.2c.1-2 1.7-3.4 3.8-3.4-.1 2.1-1.7 3.5-3.8 3.4Z" />
        <circle cx="12" cy="10.4" r="3.1" />
        <circle cx="8.6" cy="15.2" r="3.1" />
        <circle cx="15.4" cy="15.2" r="3.1" />
        <circle cx="12" cy="19.6" r="3.1" />
      </svg>
    </span>
  )
}
