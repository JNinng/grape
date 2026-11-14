import { Streamdown, defaultRehypePlugins, defaultRemarkPlugins } from 'streamdown'
import { code } from '@streamdown/code'
import { math } from '@streamdown/math'
import { mermaid } from '@streamdown/mermaid'
import { cjk } from '@streamdown/cjk'
import { harden } from 'rehype-harden'
import remarkBreaks from 'remark-breaks'
import { cn } from 'cn'
import type { PluggableList } from 'unified'
import 'streamdown/styles.css'
import 'katex/dist/katex.min.css'

// 官方插件：代码高亮（Shiki）、数学公式（KaTeX）、Mermaid 图表、CJK 断行。
// 各自按需懒加载，代码围栏闭合后才会触发起亮器。
const PLUGINS = { code, math, mermaid, cjk }

// remark 侧保留 Streamdown 默认（GFM）并追加换行转 <br>：聊天里模型常用
// 单换行分隔短句，CommonMark 的软换行会折成空格，读起来是连成一片。
const REMARK_PLUGINS: PluggableList = [...Object.values(defaultRemarkPlugins), remarkBreaks]

// rehype 侧刻意去掉 rehype-raw：模型输出按不可信文本处理，不解析原始
// HTML（sanitize 之前先丢弃，脚注类结构仍按 Markdown 处理）。链接允许
// http(s)/mailto 等安全协议（javascript:/data: 由 harden 内建拦截），
// 外链图片一律不渲染——模型给出的远程图片会带来追踪与隐私面，按纯文本
// 降级。
const REHYPE_PLUGINS: PluggableList = [
  defaultRehypePlugins.sanitize,
  [
    harden,
    {
      allowedLinkPrefixes: ['*'],
      allowedImagePrefixes: [],
      allowDataImages: false,
      imageBlockPolicy: 'text-only',
    },
  ],
]

// 代码块/表格控件的可见文案统一中文；未列出的沿用官方默认。
const TRANSLATIONS = {
  copyCode: '复制代码',
  copied: '已复制',
  copyTable: '复制表格',
  copyTableAsCsv: '复制为 CSV',
  copyTableAsMarkdown: '复制为 Markdown',
  copyTableAsTsv: '复制为 TSV',
  downloadFile: '下载文件',
  downloadTable: '下载表格',
  downloadDiagram: '下载图表',
  mermaidAriaLabel: 'Mermaid 图表',
  viewFullscreen: '全屏查看',
  exitFullscreen: '退出全屏',
  externalLinkWarning: '即将打开外部链接',
  openExternalLink: '打开外部链接',
  close: '关闭',
}

// renderedPlainTextOf 取渲染后的整体文本（「复制文本」用）：优先
// innerText（真实浏览器会按块级结构补换行、忽略隐藏节点），测试环境
// （jsdom 未实现 innerText）回退 textContent。空容器返回空串。
export function renderedPlainTextOf(root: HTMLElement | null): string {
  if (root === null) {
    return ''
  }
  const text = typeof root.innerText === 'string' ? root.innerText : (root.textContent ?? '')
  return text.trim()
}

// MarkdownContent 是模型内容的统一渲染口径：assistant 正文与思考内容都经
// 它渲染。流式增量走 streaming 模式（补全未闭合语法）并显示光标，结算后
// 切静态模式以命中块级缓存。安全默认见上方 rehype 说明。
export function MarkdownContent({
  text,
  streaming = false,
  className,
}: {
  text: string
  streaming?: boolean
  className?: string
}) {
  return (
    <Streamdown
      mode={streaming ? 'streaming' : 'static'}
      isAnimating={streaming}
      caret="block"
      plugins={PLUGINS}
      remarkPlugins={REMARK_PLUGINS}
      rehypePlugins={REHYPE_PLUGINS}
      controls={{ code: true, table: true, mermaid: true }}
      translations={TRANSLATIONS}
      className={cn('streamdown-chat space-y-2', className)}
    >
      {text}
    </Streamdown>
  )
}
