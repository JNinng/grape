import { useMemo } from 'react'
import type { ChatItem } from '../../agui/events'
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
} from '@/components/ui/popover'
import { useHoverPopover } from './useHoverPopover'

// QuestionNode 是一条可跳转的提问：id 与用户消息同源，text 是消息正文。
export interface QuestionNode {
  id: string
  text: string
}

// userQuestionsOf 按流内顺序提取用户提问节点：只取用户消息，空正文不造
// 节点（历史脏数据不生成不可读的跳转目标）。
export function userQuestionsOf(items: ChatItem[]): QuestionNode[] {
  const questions: QuestionNode[] = []
  for (const item of items) {
    if (item.kind === 'message' && item.role === 'user' && item.content !== '') {
      questions.push({ id: item.id, text: item.content })
    }
  }
  return questions
}

// QuestionNavigator 是消息区右侧中部的提问导航：竖排圆点对应每条提问，
// 悬浮弹出单行提问列表（超长省略），点圆点或列表项都滚动到对应提问。
// 没有提问时不渲染。滚动位置由调用方按 id 定位，本组件不感知滚动容器。
export function QuestionNavigator({
  items,
  onJump,
}: {
  items: ChatItem[]
  onJump: (messageId: string) => void
}) {
  const questions = useMemo(() => userQuestionsOf(items), [items])
  const { open, setOpen, cancelClose, scheduleClose } = useHoverPopover()

  const jump = (messageId: string) => {
    onJump(messageId)
    cancelClose()
    setOpen(false)
  }

  if (questions.length === 0) {
    return null
  }

  return (
    // 外层不接收指针事件，只有圆点轨道可交互，避免遮挡消息区的滚动与点选。
    // 右侧留出滚动条宽度（8px slim）另加空隙，不压住滚动条。
    <div className="pointer-events-none absolute inset-y-0 right-4 flex items-center">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverAnchor asChild>
          <div
            role="group"
            aria-label="提问导航"
            onMouseEnter={() => {
              cancelClose()
              setOpen(true)
            }}
            onMouseLeave={scheduleClose}
            className="pointer-events-auto flex max-h-[60%] flex-col items-center gap-1.5 overflow-y-auto rounded-full border border-transparent bg-soft px-1 py-2 transition-colors duration-200 hover:border-border hover:bg-card hover:shadow-sm"
          >
            {questions.map((question, index) => (
              <button
                key={question.id}
                type="button"
                aria-label={`跳转到提问 ${index + 1}`}
                onClick={() => jump(question.id)}
                className="group flex size-4 shrink-0 cursor-pointer items-center justify-center rounded-full"
              >
                <span
                  aria-hidden
                  className="size-1.5 rounded-full bg-muted-foreground transition-colors group-hover:bg-primary"
                />
              </button>
            ))}
          </div>
        </PopoverAnchor>
        <PopoverContent
          side="left"
          align="center"
          sideOffset={8}
          aria-label="提问列表"
          className="w-72 p-1.5"
          onMouseEnter={cancelClose}
          onMouseLeave={scheduleClose}
          onOpenAutoFocus={(event) => event.preventDefault()}
        >
          <p className="px-2 py-1 text-xs text-muted-foreground">提问列表</p>
          <ul className="space-y-0.5">
            {questions.map((question) => (
              <li key={question.id}>
                <button
                  type="button"
                  onClick={() => jump(question.id)}
                  className="block w-full cursor-pointer truncate rounded-md px-2 py-1.5 text-left text-sm text-foreground transition-colors select-none hover:bg-brand-soft"
                >
                  {question.text}
                </button>
              </li>
            ))}
          </ul>
        </PopoverContent>
      </Popover>
    </div>
  )
}
