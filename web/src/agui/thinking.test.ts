import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  buildThinkingForwardedProps,
  isThinkingLevel,
  loadThinkingPreference,
  saveThinkingPreference,
  thinkingLevelLabel,
} from './thinking'

beforeEach(() => {
  localStorage.clear()
})

describe('buildThinkingForwardedProps', () => {
  it('carries_the_level_string', () => {
    expect(buildThinkingForwardedProps('high')).toEqual({ thinking: 'high' })
    expect(buildThinkingForwardedProps('off')).toEqual({ thinking: 'off' })
  })
})

describe('thinking level vocabulary', () => {
  it('accepts_only_the_closed_vocabulary', () => {
    expect(isThinkingLevel('off')).toBe(true)
    expect(isThinkingLevel('low')).toBe(true)
    expect(isThinkingLevel('medium')).toBe(true)
    expect(isThinkingLevel('high')).toBe(true)
    expect(isThinkingLevel('on')).toBe(false)
    expect(isThinkingLevel('')).toBe(false)
  })

  it('maps_each_level_to_a_chinese_label', () => {
    expect(thinkingLevelLabel('off')).toBe('关闭')
    expect(thinkingLevelLabel('low')).toBe('低')
    expect(thinkingLevelLabel('medium')).toBe('中')
    expect(thinkingLevelLabel('high')).toBe('高')
  })
})

describe('thinking preference persistence', () => {
  it('defaults_to_medium_without_a_stored_value', () => {
    expect(loadThinkingPreference()).toBe('medium')
  })

  it('round_trips_the_stored_level', () => {
    saveThinkingPreference('high')
    expect(loadThinkingPreference()).toBe('high')

    saveThinkingPreference('off')
    expect(loadThinkingPreference()).toBe('off')
  })

  it('falls_back_to_medium_for_legacy_or_unknown_values', () => {
    localStorage.setItem('grape.thinking', 'on')
    expect(loadThinkingPreference()).toBe('medium')

    localStorage.setItem('grape.thinking', 'extreme')
    expect(loadThinkingPreference()).toBe('medium')
  })

  it('falls_back_to_medium_when_storage_is_unavailable', () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage denied')
    })
    expect(loadThinkingPreference()).toBe('medium')
    spy.mockRestore()
  })

  it('ignores_storage_errors_while_saving', () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage denied')
    })
    expect(() => saveThinkingPreference('low')).not.toThrow()
    spy.mockRestore()
  })
})
