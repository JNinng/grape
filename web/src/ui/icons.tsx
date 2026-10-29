import type { SVGProps } from 'react'

// icons：共享内联 SVG 图标集。全部 aria-hidden 且不携带可访问名，纯视觉
// 装饰；语义信息一律由周边文本承载（测试与可访问性按文本断言/朗读）。
// 图标默认继承 currentColor 与字体尺寸（h-4 w-4 由调用方传入）。
function svgProps(props: SVGProps<SVGSVGElement>): SVGProps<SVGSVGElement> {
  return {
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 2,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    'aria-hidden': true,
    ...props,
  }
}

// GrapeMark 品牌标：渐变圆角方块 + 葡萄果粒，仅登录页与各页头部使用。
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

export function PlusIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}

export function SendIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="m22 2-7 20-4-9-9-4Z" />
      <path d="M22 2 11 13" />
    </svg>
  )
}

export function StopIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <rect x="6" y="6" width="12" height="12" rx="2" />
    </svg>
  )
}

export function WrenchIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="M14.7 6.3a4.5 4.5 0 0 0-6 6L3 18l3 3 5.7-5.7a4.5 4.5 0 0 0 6-6L14 13l-3-3Z" />
    </svg>
  )
}

export function ChevronDownIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

export function LogOutIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
      <path d="m16 17 5-5-5-5" />
      <path d="M21 12H9" />
    </svg>
  )
}

export function ArrowLeftIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="m12 19-7-7 7-7" />
      <path d="M19 12H5" />
    </svg>
  )
}

export function RefreshIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="M21 12a9 9 0 1 1-2.64-6.36" />
      <path d="M21 3v6h-6" />
    </svg>
  )
}

export function ShieldIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="M12 22s8-3.6 8-10V5l-8-3-8 3v7c0 6.4 8 10 8 10Z" />
    </svg>
  )
}

export function AlertCircleIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <circle cx="12" cy="12" r="10" />
      <path d="M12 8v4M12 16h.01" />
    </svg>
  )
}

export function FlaskIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)}>
      <path d="M10 2v7.5L4.7 18a2 2 0 0 0 1.7 3h11.2a2 2 0 0 0 1.7-3L14 9.5V2" />
      <path d="M8.5 2h7" />
      <path d="M7 15h10" />
    </svg>
  )
}

export function SpinnerIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...svgProps(props)} className={`animate-spin ${props.className ?? ''}`}>
      <path d="M21 12a9 9 0 1 1-6.2-8.56" />
    </svg>
  )
}
