// 知识库切分区分串的表单编解码：区分串常含换行等不可见字符，单行输入框
// 以转义形态展示与编辑——\n 换行、\r 回车、\t 制表、\\ 反斜杠，其余字符
// 原样；未识别的转义（如 \x）按字面保留反斜杠。

export const DEFAULT_CHUNK_DELIMITER = '\n\n'

export function encodeChunkDelimiter(value: string): string {
  return value.replace(/[\\\n\r\t]/g, (ch) => {
    switch (ch) {
      case '\n':
        return '\\n'
      case '\r':
        return '\\r'
      case '\t':
        return '\\t'
      default:
        return '\\\\'
    }
  })
}

export function decodeChunkDelimiter(value: string): string {
  return value.replace(/\\([\\nrt])/g, (_, ch: string) => {
    switch (ch) {
      case 'n':
        return '\n'
      case 'r':
        return '\r'
      case 't':
        return '\t'
      default:
        return '\\'
    }
  })
}

// describeChunkDelimiter 是库摘要行的区分串展示：空串即不按区分串切分。
export function describeChunkDelimiter(value: string | undefined): string {
  const delimiter = value ?? DEFAULT_CHUNK_DELIMITER
  return delimiter === '' ? '不按区分串' : `区分串 ${encodeChunkDelimiter(delimiter)}`
}
