import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { UserDTO } from '@/api/users'
import { getListSchema, type ListSchemaDTO } from '@/api/querySchema'
import { SettingsLayout } from './SettingsLayout'
import { UserManagePage } from './UserManagePage'
import { Toaster, clearToasts } from '@/components/ui/toast'
import {
  assignRoles,
  createUser,
  deleteUser,
  listUsers,
  setUserPassword,
  transferUserOwner,
  updateUser,
} from '@/api/users'
import { fetchMyPermissions } from '@/api/permissions'

vi.mock('@/api/users', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/users')>()),
  listUsers: vi.fn(),
  createUser: vi.fn(),
  updateUser: vi.fn(),
  setUserPassword: vi.fn(),
  deleteUser: vi.fn(),
  assignRoles: vi.fn(),
  transferUserOwner: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

vi.mock('@/api/querySchema', () => ({
  getListSchema: vi.fn(),
}))

const mockListUsers = vi.mocked(listUsers)
const mockCreateUser = vi.mocked(createUser)
const mockUpdateUser = vi.mocked(updateUser)
const mockSetPassword = vi.mocked(setUserPassword)
const mockDeleteUser = vi.mocked(deleteUser)
const mockAssignRoles = vi.mocked(assignRoles)
const mockTransferOwner = vi.mocked(transferUserOwner)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)
const mockGetListSchema = vi.mocked(getListSchema)

// user 构造 DTO 基线：可选字段缺省即未设置（protojson 口径）。
function user(overrides: Partial<UserDTO> = {}): UserDTO {
  return {
    id: '11',
    name: 'alice',
    nickname: 'Alice',
    locale: 'zh-CN',
    createdBy: '1',
    updatedBy: '1',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

function bob(): UserDTO {
  return user({ id: '12', name: 'bob', nickname: 'Bob' })
}

// schema 基线对齐 GetListSchema 响应口径：字段 label/ops/dataType 直取
// 响应（status 为 integer 即数字编辑器），hideable 驱动列显隐，默认排序
// 与页大小驱动表格。
function schema(): ListSchemaDTO {
  return {
    resource: 'sys_user',
    hash: 'test-hash',
    fields: [
      { field: 'id', label: '用户 ID', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'in', 'not_in'] },
      { field: 'status', label: '状态', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq'] },
      { field: 'name', label: '用户名', dataType: 'string', sortable: true, hideable: true, selectable: true, ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] },
      { field: 'nickname', label: '昵称', dataType: 'string', sortable: true, hideable: true, selectable: true, ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] },
      { field: 'email', label: '邮箱', dataType: 'string', nullable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'prefix'] },
      { field: 'phone', label: '手机号', dataType: 'string', nullable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'prefix'] },
      { field: 'lastLoginAt', label: '最后登录时间', dataType: 'datetime', nullable: true, sortable: true, ops: ['gt', 'gte', 'lt', 'lte', 'between', 'is_null', 'not_null'] },
      { field: 'createdAt', label: '创建时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
      { field: 'updatedAt', label: '更新时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
    ],
    defaultSort: [{ field: 'updatedAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 400, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

beforeEach(() => {
  clearToasts()
  // 权限码拉取失败：SettingsLayout 导航回落静态全量展示（显式 mock，
  // 不依赖 jsdom fetch 对相对地址抛错）。
  mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
  mockGetListSchema.mockResolvedValue(schema())
  mockListUsers.mockResolvedValue({ items: [user(), bob()], total: '2', hasMore: false, nextCursor: '', page: 1 })
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

// renderPage 以真实布局挂载用户页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/users']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="users" element={<UserManagePage />} />
        </Route>
      </Routes>
      <Toaster />
    </MemoryRouter>,
  )
}

async function openRowActions(user2: ReturnType<typeof userEvent.setup>, name: string) {
  await user2.click(await screen.findByRole('button', { name: `操作 ${name}` }))
  const menu = await screen.findByRole('menu')
  return within(menu)
}

describe('UserManagePage list & filters', () => {
  it('loads_and_renders_users_with_the_default_sort_on_mount', async () => {
    renderPage()

    expect(await screen.findByText('alice')).toBeInTheDocument()
    expect(screen.getByText('Bob')).toBeInTheDocument()
    expect(mockListUsers).toHaveBeenCalledTimes(1)
    expect(mockListUsers.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'updatedAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
  })

  // openFilter 走渐进式筛选入口：点筛选框 → 在 Popover 内选字段进入值
  // 编辑步（字段按钮须在 dialog 内定位——表头排序按钮与字段同名）。
  async function openFilter(user2: ReturnType<typeof userEvent.setup>, fieldLabel: string) {
    await user2.click(screen.getByRole('button', { name: '用户筛选' }))
    const dialog = await screen.findByRole('dialog')
    await user2.click(within(dialog).getByRole('button', { name: fieldLabel }))
  }

  it('applies_the_name_contains_filter_when_the_text_value_is_confirmed', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('alice')

    await openFilter(user2, '用户名')
    await user2.type(screen.getByLabelText('筛选值'), 'ali')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(2))
    expect(mockListUsers.mock.calls[1][0]).toMatchObject({
      where: [{ field: 'name', op: 'contains', value: 'ali' }],
      pagination: { page: 1 },
    })
  })

  it('applies_the_status_equality_filter_with_the_number_editor_from_the_schema', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('alice')

    // status 的 dataType 为 integer：值编辑器为数字框，默认 op 即 eq。
    await openFilter(user2, '状态')
    await user2.type(screen.getByLabelText('筛选值'), '0')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(2))
    expect(mockListUsers.mock.calls[1][0]).toMatchObject({
      where: [{ field: 'status', op: 'eq', value: 0 }],
    })
  })

  it('removes_a_dropped_condition_chip_and_refetches_immediately', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('alice')

    await openFilter(user2, '状态')
    await user2.type(screen.getByLabelText('筛选值'), '0')
    await user2.click(screen.getByRole('button', { name: '确定' }))
    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(2))

    // chip ✕ 移除即重查（落条即查的对称行为）。
    await user2.click(screen.getByRole('button', { name: '移除条件：状态 等于 0' }))
    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(3))
    expect(mockListUsers.mock.calls[2][0]).toMatchObject({ where: [] })
  })

  it('surfaces_the_schema_failure_and_refetches_on_retry', async () => {
    const user2 = userEvent.setup()
    mockGetListSchema.mockRejectedValueOnce(new Error('resource unknown'))
    renderPage()

    expect(await screen.findByText('查询能力加载失败：resource unknown')).toBeInTheDocument()
    expect(mockListUsers).not.toHaveBeenCalled()

    // 重试成功后按 schema 驱动渲染列表。
    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('alice')).toBeInTheDocument()
    expect(mockGetListSchema).toHaveBeenCalledTimes(2)
  })

  it('toggles_a_hideable_column_via_the_column_settings_menu', async () => {
    const user2 = userEvent.setup()
    renderPage()
    expect(await screen.findByRole('columnheader', { name: '昵称' })).toBeInTheDocument()

    // 契约：hideable 列可显隐——菜单勾选后保持打开，可连续切换。modal 菜单
    // 打开期间外部内容被标记 aria-hidden，列头断言须带 hidden 查询（列隐藏
    // 时节点是真不存在，而非仅不可见）。
    await user2.click(screen.getByRole('button', { name: '列设置' }))
    const menu = await screen.findByRole('menu')
    await user2.click(within(menu).getByRole('menuitemcheckbox', { name: '昵称' }))
    expect(within(screen.getByRole('menu')).getByRole('menuitemcheckbox', { name: '昵称' })).toBeInTheDocument()
    expect(screen.queryByRole('columnheader', { name: '昵称', hidden: true })).not.toBeInTheDocument()

    await user2.click(within(screen.getByRole('menu')).getByRole('menuitemcheckbox', { name: '昵称' }))
    expect(await screen.findByRole('columnheader', { name: '昵称', hidden: true })).toBeInTheDocument()
  })

  it('keeps_the_created_at_header_sortable_while_actions_stay_unsorted', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('alice')

    // 契约：仅 @sortable 字段可点表头排序；操作列不声明 field 恒普通列头。
    await user2.click(screen.getByRole('button', { name: '创建时间' }))
    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(2))
    expect(mockListUsers.mock.calls[1][0]).toMatchObject({ sort: [{ field: 'createdAt', order: 'asc' }] })
  })
})

describe('UserManagePage create', () => {
  it('creates_a_user_from_the_dialog_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockCreateUser.mockResolvedValue(user({ id: '99', name: 'carol', nickname: 'Carol' }))
    renderPage()
    await screen.findByText('alice')

    await user2.click(screen.getByRole('button', { name: '新建用户' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('用户名'), 'carol')
    await user2.type(within(dialog).getByLabelText('昵称'), 'Carol')
    await user2.type(within(dialog).getByLabelText('初始密码'), 'secret-1')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateUser).toHaveBeenCalledWith(
        { name: 'carol', nickname: 'Carol' },
        'secret-1',
      ),
    )
    // 成功反馈上屏、弹窗关闭、列表重查。
    expect(await screen.findByRole('status')).toHaveTextContent('用户 carol 已创建')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(2))
  })

  it('shows_an_inline_error_and_keeps_the_dialog_when_required_fields_are_missing', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('alice')

    await user2.click(screen.getByRole('button', { name: '新建用户' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '创建' }))

    expect(screen.getByRole('alert')).toHaveTextContent('username, nickname and password are required')
    expect(mockCreateUser).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('surfaces_the_backend_rejection_inline_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateUser.mockRejectedValue(new Error('name already exists'))
    renderPage()
    await screen.findByText('alice')

    await user2.click(screen.getByRole('button', { name: '新建用户' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('用户名'), 'carol')
    await user2.type(within(dialog).getByLabelText('昵称'), 'Carol')
    await user2.type(within(dialog).getByLabelText('初始密码'), 'secret-1')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('name already exists')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListUsers).toHaveBeenCalledTimes(1)
  })
})

describe('UserManagePage edit', () => {
  it('prefills_the_row_values_and_saves_with_an_explicit_status', async () => {
    const user2 = userEvent.setup()
    mockUpdateUser.mockResolvedValue(user())
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('用户名')).toHaveValue('alice')
    expect(within(dialog).getByLabelText('昵称')).toHaveValue('Alice')

    // 停用开关后保存：status 显式为 0（更新语义必填）。
    const statusSwitch = within(dialog).getByRole('switch', { name: '状态' })
    await user2.click(statusSwitch)
    expect(within(dialog).getByText('禁用')).toBeInTheDocument()
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(mockUpdateUser).toHaveBeenCalledWith({
        id: '11',
        status: 0,
        name: 'alice',
        nickname: 'Alice',
        locale: 'zh-CN',
      }),
    )
    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(2))
  })

  it('reports_the_update_failure_inline_without_closing', async () => {
    const user2 = userEvent.setup()
    mockUpdateUser.mockRejectedValue(new Error('forbidden'))
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))

    expect(await within(screen.getByRole('dialog')).findByRole('alert')).toHaveTextContent('forbidden')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})

describe('UserManagePage password', () => {
  it('rejects_mismatched_or_empty_passwords_before_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '修改密码' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('新密码'), 'pw-1')
    await user2.type(within(dialog).getByLabelText('确认新密码'), 'pw-2')
    await user2.click(within(dialog).getByRole('button', { name: '更新密码' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent(
      'passwords must match and be non-empty',
    )
    expect(mockSetPassword).not.toHaveBeenCalled()
  })

  it('sets_the_password_when_the_confirmation_matches', async () => {
    const user2 = userEvent.setup()
    mockSetPassword.mockResolvedValue(undefined)
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '修改密码' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('新密码'), 'pw-1')
    await user2.type(within(dialog).getByLabelText('确认新密码'), 'pw-1')
    await user2.click(within(dialog).getByRole('button', { name: '更新密码' }))

    await waitFor(() => expect(mockSetPassword).toHaveBeenCalledWith('11', 'pw-1'))
    expect(await screen.findByRole('status')).toHaveTextContent('用户 alice 的密码已更新')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })
})

describe('UserManagePage assign roles', () => {
  it('parses_deduplicates_and_assigns_the_role_ids', async () => {
    const user2 = userEvent.setup()
    mockAssignRoles.mockResolvedValue({ added: '2' })
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '分配角色' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('角色 ID'), '3, 5 3')
    await user2.click(within(dialog).getByRole('button', { name: '分配' }))

    await waitFor(() => expect(mockAssignRoles).toHaveBeenCalledWith('11', ['3', '5']))
    expect(await screen.findByRole('status')).toHaveTextContent('已为用户 alice 新增 2 个角色')
  })

  it('rejects_non_numeric_role_ids_inline', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '分配角色' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('角色 ID'), 'abc')
    await user2.click(within(dialog).getByRole('button', { name: '分配' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent(
      'enter at least one positive role id',
    )
    expect(mockAssignRoles).not.toHaveBeenCalled()
  })
})

describe('UserManagePage transfer owner', () => {
  it('prefills_current_owners_and_sends_the_complete_triple', async () => {
    const user2 = userEvent.setup()
    mockListUsers.mockResolvedValue({
      items: [user({ ownerUserId: '8', ownerDeptId: '3' }), bob()],
      total: '2',
      hasMore: false,
      nextCursor: '',
      page: 1,
    })
    mockTransferOwner.mockResolvedValue(user({ ownerUserId: '8', ownerDeptId: '3', ownerUserGroupId: '5' }))
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '转移归属' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('归属用户 ID')).toHaveValue('8')
    expect(within(dialog).getByLabelText('归属部门 ID')).toHaveValue('3')
    await user2.type(within(dialog).getByLabelText('归属用户组 ID'), '5')
    await user2.click(within(dialog).getByRole('button', { name: '转移' }))

    await waitFor(() =>
      expect(mockTransferOwner).toHaveBeenCalledWith('11', {
        ownerUserId: '8',
        ownerDeptId: '3',
        ownerUserGroupId: '5',
      }),
    )
    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(2))
  })

  it('clears_left_blank_owner_fields_as_null', async () => {
    const user2 = userEvent.setup()
    mockTransferOwner.mockResolvedValue(user())
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '转移归属' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '转移' }))

    await waitFor(() =>
      expect(mockTransferOwner).toHaveBeenCalledWith('11', {
        ownerUserId: null,
        ownerDeptId: null,
        ownerUserGroupId: null,
      }),
    )
  })

  it('rejects_non_numeric_owner_ids_inline', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '转移归属' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('归属用户 ID'), 'x9')
    await user2.click(within(dialog).getByRole('button', { name: '转移' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('owner user must be a positive id')
    expect(mockTransferOwner).not.toHaveBeenCalled()
  })
})

describe('UserManagePage delete', () => {
  it('asks_for_confirmation_and_only_deletes_on_confirm', async () => {
    const user2 = userEvent.setup()
    mockDeleteUser.mockResolvedValue(undefined)
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除用户 alice？')
    expect(mockDeleteUser).not.toHaveBeenCalled()

    // 取消：关框且不删除。
    await user2.click(within(confirmDialog).getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockDeleteUser).not.toHaveBeenCalled()

    // 再次打开并确认：删除并重查列表。
    const menuAgain = await openRowActions(user2, 'alice')
    await user2.click(menuAgain.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteUser).toHaveBeenCalledWith('11'))
    expect(await screen.findByRole('status')).toHaveTextContent('用户 alice 已删除')
    await waitFor(() => expect(mockListUsers).toHaveBeenCalledTimes(2))
  })

  it('surfaces_the_delete_failure_as_a_toast', async () => {
    const user2 = userEvent.setup()
    mockDeleteUser.mockRejectedValue(new Error('user not found'))
    renderPage()
    const menu = await openRowActions(user2, 'alice')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('删除失败：user not found')
  })
})
