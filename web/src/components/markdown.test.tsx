import { cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { MarkdownContent, renderedPlainTextOf } from './markdown'

afterEach(cleanup)

describe('MarkdownContent', () => {
  it('renders_gfm_markdown_structure', () => {
    const { container } = render(
      <MarkdownContent text={'# 标题\n\n**加粗** 与 `代码`\n\n- 项目'} />,
    )

    expect(container.querySelector('h1')).toHaveTextContent('标题')
    expect(container.querySelector('[data-streamdown="strong"]')).toHaveTextContent('加粗')
    expect(container.querySelector('code')).toHaveTextContent('代码')
    expect(container.querySelector('li')).toHaveTextContent('项目')
  })

  it('renders_soft_line_breaks_as_br', () => {
    const { container } = render(<MarkdownContent text={'第一行\n第二行'} />)

    expect(container.querySelector('br')).not.toBeNull()
  })

  it('escapes_raw_html_instead_of_parsing_it', () => {
    const { container } = render(<MarkdownContent text={'<img src=x onerror="alert(1)">'} />)

    expect(container.querySelector('img')).toBeNull()
    expect(container).toHaveTextContent('<img src=x onerror="alert(1)">')
  })

  it('drops_external_images', () => {
    const { container } = render(<MarkdownContent text={'![远程图片](https://example.com/x.png)'} />)

    expect(container.querySelector('img')).toBeNull()
  })

  it('blocks_javascript_links', () => {
    const { container } = render(<MarkdownContent text={'[点我](javascript:alert(1))'} />)

    expect(container.querySelector('a[href^="javascript"]')).toBeNull()
  })
})

describe('renderedPlainTextOf', () => {
  it('returns_the_trimmed_rendered_text', () => {
    const root = document.createElement('div')
    root.innerHTML = '<p>加粗 <span>文本</span></p>'

    expect(renderedPlainTextOf(root)).toBe('加粗 文本')
  })

  it('returns_an_empty_string_for_a_null_root', () => {
    expect(renderedPlainTextOf(null)).toBe('')
  })
})
