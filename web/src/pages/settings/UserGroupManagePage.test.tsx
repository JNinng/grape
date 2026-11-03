import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { UserGroupDTO, UserGroupMemberDTO } from '@/api/userGroups'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { UserGroupManagePage } from './UserGroupManagePage'
import {
  addMembers,
  createUserGroup,
  deleteUserGroup,
  listUserGroupMembers,
  listUserGroups,
  removeMembers,
  updateUserGroup,
} from '@/api/userGroups'

vi.mock('@/api/userGroups', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/userGroups')>()),
  listUserGroups: vi.fn(),
  createUserGroup: vi.fn(),
  updateUserGroup: vi.fn(),
  deleteUserGroup: vi.fn(),
  listUserGroupMembers: vi.fn(),
  addMembers: vi.fn(),
  removeMembers: vi.fn(),
}))

const mockListGroups = vi.mocked(listUserGroups)
const mockCreateGroup = vi.mocked(createUserGroup)
const mockUpdateGroup = vi.mocked(updateUserGroup)
const mockDeleteGroup = vi.mocked(deleteUserGroup)
const mockListMembers = vi.mocked(listUserGroupMembers)
const mockAddMembers = vi.mocked(addMembers)
const mockRemoveMembers = vi.mocked(removeMembers)

// group 构造 DTO 基线：可选字段缺省即未设置（protojson 口径）。
function group(overrides: Partial<UserGroupDTO> = {}): UserGroupDTO {
  return {
    id: '5',
    status: 1,
    name: 'ops',
    code: 'OPS',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

function secondGroup(): UserGroupDTO {
  return group({ id: '6', name: 'dev', code: 'DEV', createdAt: '2026-01-02T08:00:00Z' })
}

// member 构造成员条目基线。
function member(overrides: Partial<UserGroupMemberDTO> = {}): UserGroupMemberDTO {
  return {
    userId: '11',
    name: 'alice',
    nickname: 'Alice',
    status: 1,
    addedBy: '7',
    addedAt: '2026-01-03T08:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  clearToasts()
  mockListGroups.mockResolvedValue({
    items: [group(), secondGroup()],
    total: '2',
    hasMore: false,
    nextCursor: '',
    page: 1,
  })
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

// renderPage 以真实布局挂载用户组页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/user-groups']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="user-groups" element={<UserGroupManagePage />} />
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

describe('UserGroupManagePage list & filters', () => {
  it('loads_and_renders_groups_with_the_default_sort_on_mount', async () => {
    renderPage()

    expect(await screen.findByText('ops')).toBeInTheDocument()
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(mockListGroups).toHaveBeenCalledTimes(1)
    expect(mockListGroups.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
  })

  it('applies_the_name_contains_filter_when_the_filter_form_is_submitted', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('ops')

    await user2.type(screen.getByLabelText('组名关键字'), 'op')
    await user2.click(screen.getByRole('button', { name: '筛选' }))

    await waitFor(() => expect(mockListGroups).toHaveBeenCalledTimes(2))
    expect(mockListGroups.mock.calls[1][0]).toMatchObject({
      where: [{ field: 'name', op: 'contains', value: 'op' }],
      pagination: { page: 1 },
    })
  })

  it('applies_the_code_prefix_filter_when_the_filter_form_is_submitted', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('ops')

    await user2.type(screen.getByLabelText('组编码前缀'), 'DE')
    await user2.click(screen.getByRole('button', { name: '筛选' }))

    await waitFor(() => expect(mockListGroups).toHaveBeenCalledTimes(2))
    expect(mockListGroups.mock.calls[1][0]).toMatchObject({
      where: [{ field: 'code', op: 'prefix', value: 'DE' }],
    })
  })

  it('applies_the_status_equality_filter_when_an_option_is_picked', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('ops')

    await user2.click(screen.getByRole('combobox', { name: '状态筛选' }))
    await user2.click(screen.getByRole('option', { name: '禁用' }))

    await waitFor(() => expect(mockListGroups).toHaveBeenCalledTimes(2))
    expect(mockListGroups.mock.calls[1][0]).toMatchObject({
      where: [{ field: 'status', op: 'eq', value: 0 }],
    })
  })
})

describe('UserGroupManagePage create', () => {
  it('creates_a_group_from_the_dialog_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockCreateGroup.mockResolvedValue(group({ id: '9', name: 'oncall', code: 'OC' }))
    renderPage()
    await screen.findByText('ops')

    await user2.click(screen.getByRole('button', { name: '新建用户组' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('组编码'), 'OC')
    await user2.type(within(dialog).getByLabelText('组名'), 'oncall')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateGroup).toHaveBeenCalledWith({ code: 'OC', name: 'oncall', status: 1 }),
    )
    // 成功反馈上屏、弹窗关闭、列表重查。
    expect(await screen.findByRole('status')).toHaveTextContent('用户组 oncall 已创建')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListGroups).toHaveBeenCalledTimes(2))
  })

  it('rejects_the_submission_inline_when_code_or_name_is_missing', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('ops')

    await user2.click(screen.getByRole('button', { name: '新建用户组' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '创建' }))

    expect(screen.getByRole('alert')).toHaveTextContent('code and name are required')
    expect(mockCreateGroup).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('surfaces_the_conflict_reason_as_a_readable_hint_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateGroup.mockRejectedValue(new Error('user group already exists'))
    renderPage()
    await screen.findByText('ops')

    await user2.click(screen.getByRole('button', { name: '新建用户组' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('组编码'), 'OPS')
    await user2.type(within(dialog).getByLabelText('组名'), 'ops')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a group with the same code or name already exists in the tenant',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListGroups).toHaveBeenCalledTimes(1)
  })
})

describe('UserGroupManagePage edit', () => {
  it('prefills_the_row_values_and_saves_name_remark_and_status_only', async () => {
    const user2 = userEvent.setup()
    mockUpdateGroup.mockResolvedValue(group({ remark: 'rotated' }))
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('组名')).toHaveValue('ops')
    expect(within(dialog).getByLabelText('备注')).toHaveValue('')

    await user2.type(within(dialog).getByLabelText('备注'), 'rotated')
    // 停用开关后保存：status 显式为 0（更新语义必填）。
    await user2.click(within(dialog).getByRole('switch', { name: '状态' }))
    expect(within(dialog).getByText('禁用')).toBeInTheDocument()
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 更新面不上送 code 与 ownerUserId（后端忽略，前端不发送）。
    await waitFor(() =>
      expect(mockUpdateGroup).toHaveBeenCalledWith({
        id: '5',
        status: 0,
        name: 'ops',
        remark: 'rotated',
      }),
    )
    await waitFor(() => expect(mockListGroups).toHaveBeenCalledTimes(2))
  })

  it('reports_the_update_failure_inline_without_closing', async () => {
    const user2 = userEvent.setup()
    mockUpdateGroup.mockRejectedValue(new Error('forbidden'))
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))

    expect(await within(screen.getByRole('dialog')).findByRole('alert')).toHaveTextContent('forbidden')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})

describe('UserGroupManagePage delete', () => {
  it('asks_for_confirmation_and_only_deletes_on_confirm', async () => {
    const user2 = userEvent.setup()
    mockDeleteGroup.mockResolvedValue(undefined)
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除用户组 ops？')
    expect(mockDeleteGroup).not.toHaveBeenCalled()

    // 取消：关框且不删除。
    await user2.click(within(confirmDialog).getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockDeleteGroup).not.toHaveBeenCalled()

    // 再次打开并确认：删除并重查列表。
    const menuAgain = await openRowActions(user2, 'ops')
    await user2.click(menuAgain.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteGroup).toHaveBeenCalledWith('5'))
    expect(await screen.findByRole('status')).toHaveTextContent('用户组 ops 已删除')
    await waitFor(() => expect(mockListGroups).toHaveBeenCalledTimes(2))
  })

  it('surfaces_the_delete_failure_toast_with_the_reason_hint', async () => {
    const user2 = userEvent.setup()
    mockDeleteGroup.mockRejectedValue(new Error('user group not found'))
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '删除失败：user group not found; it may have been deleted',
    )
  })
})

describe('UserGroupManagePage members panel', () => {
  it('loads_the_first_member_page_on_open_and_paginates_on_demand', async () => {
    const user2 = userEvent.setup()
    mockListMembers.mockResolvedValueOnce({
      items: [member()],
      total: '2',
      hasMore: true,
    })
    mockListMembers.mockResolvedValueOnce({
      items: [member({ userId: '12', name: 'bob', nickname: 'Bob', addedAt: '2026-01-04T08:00:00Z' })],
      total: '2',
      hasMore: false,
    })
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '成员管理' }))
    const dialog = screen.getByRole('dialog')

    expect(await within(dialog).findByText('alice')).toBeInTheDocument()
    expect(mockListMembers).toHaveBeenCalledWith('5', 1, 20)

    await user2.click(within(dialog).getByRole('button', { name: '成员下一页' }))
    expect(await within(dialog).findByText('bob')).toBeInTheDocument()
    expect(mockListMembers).toHaveBeenLastCalledWith('5', 2, 20)
  })

  it('parses_deduplicates_and_adds_member_ids_reporting_the_added_count', async () => {
    const user2 = userEvent.setup()
    mockListMembers.mockResolvedValue({ items: [member()], total: '1', hasMore: false })
    mockAddMembers.mockResolvedValue({ added: '2' })
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '成员管理' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('alice')

    await user2.type(within(dialog).getByLabelText('用户 ID'), '12, 13 12')
    await user2.click(within(dialog).getByRole('button', { name: '添加成员' }))

    // 重复项去重后上送。
    await waitFor(() => expect(mockAddMembers).toHaveBeenCalledWith('5', ['12', '13']))
    // 成员面板保持打开，Radix 把面板外内容（含全局 Toaster）标为
    // aria-hidden，toast 断言需显式包含隐藏节点。
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('已向组 ops 新增 2 个成员')
    // 添加成功后重查当前成员页。
    await waitFor(() => expect(mockListMembers).toHaveBeenCalledTimes(2))
  })

  it('rejects_non_numeric_user_ids_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    mockListMembers.mockResolvedValue({ items: [member()], total: '1', hasMore: false })
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '成员管理' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('alice')

    await user2.type(within(dialog).getByLabelText('用户 ID'), 'x9')
    await user2.click(within(dialog).getByRole('button', { name: '添加成员' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('enter at least one positive user id')
    expect(mockAddMembers).not.toHaveBeenCalled()
  })

  it('surfaces_the_whole_batch_rejection_inline_and_keeps_the_panel_open', async () => {
    const user2 = userEvent.setup()
    mockListMembers.mockResolvedValue({ items: [member()], total: '1', hasMore: false })
    mockAddMembers.mockRejectedValue(new Error('invalid user group argument'))
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '成员管理' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('alice')

    await user2.type(within(dialog).getByLabelText('用户 ID'), '999')
    await user2.click(within(dialog).getByRole('button', { name: '添加成员' }))

    // 整批拒绝：透传 reason 映射后的可读提示，面板保持打开。
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'invalid argument; every user id must refer to an existing active user in the tenant',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('removes_a_member_and_reloads_the_current_page', async () => {
    const user2 = userEvent.setup()
    mockListMembers.mockResolvedValue({
      items: [member(), member({ userId: '12', name: 'bob', nickname: 'Bob', addedAt: '2026-01-04T08:00:00Z' })],
      total: '2',
      hasMore: false,
    })
    mockRemoveMembers.mockResolvedValue({ removed: '1' })
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '成员管理' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('bob')

    await user2.click(within(dialog).getByRole('button', { name: '移除 bob' }))

    await waitFor(() => expect(mockRemoveMembers).toHaveBeenCalledWith('5', ['12']))
    // 成员面板保持打开，toast 断言需显式包含隐藏节点（见上例）。
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('已从组 ops 移除 1 个成员')
    await waitFor(() => expect(mockListMembers).toHaveBeenCalledTimes(2))
  })

  it('shows_a_retryable_error_when_the_member_list_fails_to_load', async () => {
    const user2 = userEvent.setup()
    mockListMembers.mockRejectedValueOnce(new Error('user group not found'))
    mockListMembers.mockResolvedValueOnce({ items: [member()], total: '1', hasMore: false })
    renderPage()
    const menu = await openRowActions(user2, 'ops')

    await user2.click(menu.getByRole('menuitem', { name: '成员管理' }))
    const dialog = screen.getByRole('dialog')
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('成员列表加载失败：user group not found')

    // 重试成功后成员正常渲染。
    await user2.click(within(dialog).getByRole('button', { name: '重试' }))
    expect(await within(dialog).findByText('alice')).toBeInTheDocument()
  })
})
