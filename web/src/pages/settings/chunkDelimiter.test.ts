import { describe, expect, it } from 'vitest'
import { decodeChunkDelimiter, describeChunkDelimiter, encodeChunkDelimiter } from './chunkDelimiter'

describe('chunk delimiter codec', () => {
  it('encodes_control_characters_and_backslash_as_escapes', () => {
    expect(encodeChunkDelimiter('\n\n')).toBe('\\n\\n')
    expect(encodeChunkDelimiter('a\tb\r\\')).toBe('a\\tb\\r\\\\')
  })

  it('encodes_plain_and_multibyte_text_unchanged', () => {
    expect(encodeChunkDelimiter('。。---')).toBe('。。---')
  })

  it('decodes_known_escapes', () => {
    expect(decodeChunkDelimiter('\\n\\n')).toBe('\n\n')
    expect(decodeChunkDelimiter('a\\tb\\r\\\\')).toBe('a\tb\r\\')
  })

  it('decodes_unknown_escape_keeping_backslash', () => {
    expect(decodeChunkDelimiter('\\x')).toBe('\\x')
  })

  it('decodes_empty_input_to_empty_delimiter', () => {
    expect(decodeChunkDelimiter('')).toBe('')
  })

  it('round_trips_arbitrary_delimiters', () => {
    for (const value of ['\n\n', '\\n', '\r\n', '##\t##', '\\', '']) {
      expect(decodeChunkDelimiter(encodeChunkDelimiter(value))).toBe(value)
    }
  })

  it('describes_missing_delimiter_as_default', () => {
    expect(describeChunkDelimiter(undefined)).toBe('区分串 \\n\\n')
  })

  it('describes_empty_delimiter_as_disabled', () => {
    expect(describeChunkDelimiter('')).toBe('不按区分串')
  })
})
