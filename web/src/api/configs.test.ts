import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  createConfig,
  createFeatureFlag,
  createTenantConfig,
  createTranslation,
  deleteConfig,
  deleteFeatureFlag,
  deleteTenantConfig,
  deleteTranslation,
  fetchConfig,
  fetchFeatureFlag,
  fetchTenantConfig,
  fetchTranslation,
  listConfigs,
  listFeatureFlags,
  listTenantConfigs,
  listTranslations,
  updateConfig,
  updateFeatureFlag,
  updateTenantConfig,
  updateTranslation,
} from './configs'

const fetchMock = vi.fn<typeof fetch>()

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  setSession(loginReply('access-1', 'refresh-1'))
})

afterEach(() => {
  clearSession()
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

function loginReply(accessToken: string, refreshToken: string): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken,
    refreshToken,
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  }
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

// requestInit 便于从 fetch 调用参数还原 method / path / body 的断言。
function requestInit(call: readonly [unknown, RequestInit?]): RequestInit {
  return call[1] ?? {}
}

function sentBody(call: readonly [unknown, RequestInit?]): Record<string, unknown> {
  const raw = requestInit(call).body
  expect(typeof raw).toBe('string')
  return JSON.parse(raw as string) as Record<string, unknown>
}

// sentMethod 还原请求方法：fetch 未显式声明 method 时默认为 GET。
function sentMethod(call: readonly [unknown, RequestInit?]): string {
  return (requestInit(call).method ?? 'GET').toUpperCase()
}

function configDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '11',
    status: 1,
    name: 'SMTP 主机',
    configKey: 'smtp.host',
    value: 'smtp.example.com',
    valueType: 'string',
    remark: '邮件服务器',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function tenantConfigDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return { ...configDTO({ id: '21', tenantId: 't-1' }), ...overrides }
}

function featureFlagDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '31',
    status: 1,
    name: '导出功能',
    flagKey: 'export_enabled',
    description: '允许导出报表',
    isEnabled: true,
    remark: '',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    tenantId: 't-1',
    ...overrides,
  }
}

function translationDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '41',
    status: 1,
    tableName: 'sys_menu',
    rowId: '9007199254740993',
    columnName: 'name',
    locale: 'zh-CN',
    value: '系统设置',
    remark: '',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    tenantId: 't-1',
    ...overrides,
  }
}

// ---------- 全局配置 ----------

describe('createConfig', () => {
  it('posts_the_wrapped_config_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, configDTO()))

    const created = await createConfig({ name: 'SMTP 主机', configKey: 'smtp.host', valueType: 'string' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/configs/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      config: { name: 'SMTP 主机', configKey: 'smtp.host', valueType: 'string' },
    })
    expect(created.id).toBe('11')
    expect(created.configKey).toBe('smtp.host')
  })

  it('propagates_the_duplicate_key_conflict', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { reason: 'CONFIG_ALREADY_EXISTS', message: 'config already exists' }))

    await expect(createConfig({ name: 'x', configKey: 'smtp.host', valueType: 'string' })).rejects.toMatchObject({
      message: 'config already exists',
      reason: 'CONFIG_ALREADY_EXISTS',
    })
  })

  it('exposes_the_reason_when_the_error_body_has_no_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { reason: 'CONFIG_ALREADY_EXISTS' }))

    await expect(createConfig({ name: 'x', configKey: 'smtp.host', valueType: 'string' })).rejects.toMatchObject({
      message: 'CONFIG_ALREADY_EXISTS',
      reason: 'CONFIG_ALREADY_EXISTS',
    })
  })
})

describe('fetchConfig', () => {
  it('gets_the_config_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, configDTO()))

    const config = await fetchConfig('11')

    expect(sentMethod(fetchMock.mock.calls[0])).toBe('GET')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/configs/11')
    expect(config.value).toBe('smtp.example.com')
  })

  it('propagates_the_not_found_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { reason: 'CONFIG_NOT_FOUND', message: 'config not found' }))

    await expect(fetchConfig('999')).rejects.toThrow('config not found')
  })
})

describe('listConfigs', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listConfigs({ where: [{ field: 'configKey', op: 'eq', value: 'smtp.host' }] })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/configs/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'configKey', op: 'eq', value: 'smtp.host' }],
    })
    expect(set.items).toEqual([])
  })

  it('returns_the_page_envelope_when_items_are_present', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [configDTO()], hasMore: true, nextCursor: 'cur', page: 2, total: '3' }))

    const set = await listConfigs({ pagination: { page: 2 } })

    expect(set.items).toHaveLength(1)
    expect(set.total).toBe('3')
    expect(set.hasMore).toBe(true)
    expect(set.nextCursor).toBe('cur')
    expect(set.page).toBe(2)
  })
})

describe('updateConfig', () => {
  it('puts_the_config_message_as_the_request_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, configDTO({ name: '新名' })))

    await updateConfig({ id: '11', status: 0, name: '新名', value: 'v', remark: 'r' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/configs/update')
    // body: "config" 的 HTTP 规则把 Config 消息本身作为请求体，不是外层包装。
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '11', status: 0, name: '新名', value: 'v', remark: 'r' })
  })

  it('propagates_the_backend_rejection', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { reason: 'CONFIG_INVALID_ARGUMENT', message: 'invalid config argument' }))

    await expect(updateConfig({ id: '11', status: 1, name: '' })).rejects.toThrow('invalid config argument')
  })
})

describe('deleteConfig', () => {
  it('deletes_the_config_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteConfig('11')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/configs/11')
  })

  it('propagates_the_backend_rejection', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { reason: 'CONFIG_NOT_FOUND', message: 'config not found' }))

    await expect(deleteConfig('999')).rejects.toThrow('config not found')
  })
})

// ---------- 租户配置 ----------

describe('createTenantConfig', () => {
  it('posts_the_wrapped_tenant_config_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, tenantConfigDTO()))

    const created = await createTenantConfig({ name: '租户主题', configKey: 'ui.theme', valueType: 'string' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenant-configs/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      tenantConfig: { name: '租户主题', configKey: 'ui.theme', valueType: 'string' },
    })
    expect(created.tenantId).toBe('t-1')
  })

  it('propagates_the_duplicate_key_conflict', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, { reason: 'TENANT_CONFIG_ALREADY_EXISTS', message: 'tenant config already exists' }),
    )

    await expect(createTenantConfig({ name: 'x', configKey: 'k', valueType: 'string' })).rejects.toThrow(
      'tenant config already exists',
    )
  })
})

describe('fetchTenantConfig', () => {
  it('gets_the_tenant_config_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, tenantConfigDTO()))

    const config = await fetchTenantConfig('21')

    expect(sentMethod(fetchMock.mock.calls[0])).toBe('GET')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenant-configs/21')
    expect(config.id).toBe('21')
  })

  it('propagates_the_not_found_message', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'TENANT_CONFIG_NOT_FOUND', message: 'tenant config not found' }),
    )

    await expect(fetchTenantConfig('999')).rejects.toThrow('tenant config not found')
  })
})

describe('listTenantConfigs', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listTenantConfigs({ where: [{ field: 'configKey', op: 'prefix', value: 'ui.' }] })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenant-configs/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'configKey', op: 'prefix', value: 'ui.' }],
    })
    expect(set.items).toEqual([])
  })
})

describe('updateTenantConfig', () => {
  it('puts_the_tenant_config_message_as_the_request_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, tenantConfigDTO({ name: '新名' })))

    await updateTenantConfig({ id: '21', status: 1, name: '新名', value: 'v', remark: 'r' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenant-configs/update')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '21', status: 1, name: '新名', value: 'v', remark: 'r' })
  })

  it('propagates_the_backend_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, { reason: 'TENANT_CONFIG_INVALID_ARGUMENT', message: 'invalid tenant config argument' }),
    )

    await expect(updateTenantConfig({ id: '21', status: 1, name: '' })).rejects.toThrow('invalid tenant config argument')
  })
})

describe('deleteTenantConfig', () => {
  it('deletes_the_tenant_config_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteTenantConfig('21')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenant-configs/21')
  })

  it('propagates_the_backend_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'TENANT_CONFIG_NOT_FOUND', message: 'tenant config not found' }),
    )

    await expect(deleteTenantConfig('999')).rejects.toThrow('tenant config not found')
  })
})

// ---------- 功能开关 ----------

describe('createFeatureFlag', () => {
  it('posts_the_wrapped_feature_flag_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, featureFlagDTO()))

    const created = await createFeatureFlag({ name: '导出功能', flagKey: 'export_enabled', isEnabled: true })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/feature-flags/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      featureFlag: { name: '导出功能', flagKey: 'export_enabled', isEnabled: true },
    })
    expect(created.isEnabled).toBe(true)
  })

  it('propagates_the_duplicate_flag_key_conflict', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, { reason: 'FEATURE_FLAG_ALREADY_EXISTS', message: 'feature flag already exists' }),
    )

    await expect(createFeatureFlag({ name: 'x', flagKey: 'export_enabled' })).rejects.toThrow(
      'feature flag already exists',
    )
  })
})

describe('fetchFeatureFlag', () => {
  it('gets_the_feature_flag_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, featureFlagDTO()))

    const flag = await fetchFeatureFlag('31')

    expect(sentMethod(fetchMock.mock.calls[0])).toBe('GET')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/feature-flags/31')
    expect(flag.flagKey).toBe('export_enabled')
  })

  it('propagates_the_not_found_message', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'FEATURE_FLAG_NOT_FOUND', message: 'feature flag not found' }),
    )

    await expect(fetchFeatureFlag('999')).rejects.toThrow('feature flag not found')
  })
})

describe('listFeatureFlags', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listFeatureFlags({ where: [{ field: 'isEnabled', op: 'eq', value: true }] })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/feature-flags/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'isEnabled', op: 'eq', value: true }],
    })
    expect(set.items).toEqual([])
  })
})

describe('updateFeatureFlag', () => {
  it('puts_the_coverage_semantics_body_with_status_and_is_enabled', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, featureFlagDTO({ isEnabled: false, status: 0 })))

    await updateFeatureFlag({
      id: '31',
      status: 0,
      name: '导出功能',
      description: '允许导出报表',
      isEnabled: false,
      remark: 'r',
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/feature-flags/update')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      id: '31',
      status: 0,
      name: '导出功能',
      description: '允许导出报表',
      isEnabled: false,
      remark: 'r',
    })
  })

  it('propagates_the_backend_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, { reason: 'FEATURE_FLAG_INVALID_ARGUMENT', message: 'invalid feature flag argument' }),
    )

    await expect(updateFeatureFlag({ id: '31', status: 2, name: 'x', isEnabled: true })).rejects.toThrow(
      'invalid feature flag argument',
    )
  })
})

describe('deleteFeatureFlag', () => {
  it('deletes_the_feature_flag_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteFeatureFlag('31')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/feature-flags/31')
  })

  it('propagates_the_backend_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'FEATURE_FLAG_NOT_FOUND', message: 'feature flag not found' }),
    )

    await expect(deleteFeatureFlag('999')).rejects.toThrow('feature flag not found')
  })
})

// ---------- 翻译 ----------

describe('createTranslation', () => {
  it('posts_the_wrapped_translation_message_with_the_int64_row_id_as_a_string', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, translationDTO()))

    const created = await createTranslation({
      tableName: 'sys_menu',
      rowId: '9007199254740993',
      columnName: 'name',
      locale: 'zh-CN',
      value: '系统设置',
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/translations/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      translation: {
        tableName: 'sys_menu',
        rowId: '9007199254740993',
        columnName: 'name',
        locale: 'zh-CN',
        value: '系统设置',
      },
    })
    // int64 走 protojson 字符串口径：响应侧原样保留字符串。
    expect(created.rowId).toBe('9007199254740993')
  })

  it('propagates_the_invalid_argument_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, { reason: 'TRANSLATION_INVALID_ARGUMENT', message: 'invalid translation argument' }),
    )

    await expect(
      createTranslation({ tableName: 't', rowId: '0', columnName: 'c', locale: 'zh-CN', value: 'v' }),
    ).rejects.toThrow('invalid translation argument')
  })
})

describe('fetchTranslation', () => {
  it('gets_the_translation_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, translationDTO()))

    const translation = await fetchTranslation('41')

    expect(sentMethod(fetchMock.mock.calls[0])).toBe('GET')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/translations/41')
    expect(translation.locale).toBe('zh-CN')
  })

  it('propagates_the_not_found_message', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'TRANSLATION_NOT_FOUND', message: 'translation not found' }),
    )

    await expect(fetchTranslation('999')).rejects.toThrow('translation not found')
  })
})

describe('listTranslations', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listTranslations({ where: [{ field: 'tableName', op: 'eq', value: 'sys_menu' }] })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/translations/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'tableName', op: 'eq', value: 'sys_menu' }],
    })
    expect(set.items).toEqual([])
  })
})

describe('updateTranslation', () => {
  it('puts_only_the_mutable_fields_on_update', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, translationDTO({ value: '系统配置' })))

    await updateTranslation({ id: '41', status: 1, value: '系统配置', remark: 'r' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/translations/update')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '41', status: 1, value: '系统配置', remark: 'r' })
  })

  it('propagates_the_backend_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, { reason: 'TRANSLATION_INVALID_ARGUMENT', message: 'invalid translation argument' }),
    )

    await expect(updateTranslation({ id: '41', status: 1, value: ' ' })).rejects.toThrow('invalid translation argument')
  })
})

describe('deleteTranslation', () => {
  it('deletes_the_translation_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteTranslation('41')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/translations/41')
  })

  it('propagates_the_backend_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'TRANSLATION_NOT_FOUND', message: 'translation not found' }),
    )

    await expect(deleteTranslation('999')).rejects.toThrow('translation not found')
  })
})
