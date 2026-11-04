import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { RoleDTO, RoleDataScopeDTO } from '@/api/roles'
import type { MenuTreeReplyDTO, MenuTreeNodeDTO, MenuDTO } from '@/api/menus'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { RoleManagePage } from './RoleManagePage'
import { createRole, deleteRole, fetchRoleDataScope, listRoleMenus, listRoles, setRoleDataScope, setRoleMenus, updateRole } from '@/api/roles'
import { fetchMenuTree } from '@/api/menus'
import { fetchMyPermissions } from '@/api/permissions'

vi.mock('@/api/roles', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/roles')>()),
  listRoles: vi.fn(),
  createRole: vi.fn(),
  updateRole: vi.fn(),
  deleteRole: vi.fn(),
  fetchRoleDataScope: vi.fn(),
  setRoleDataScope: vi.fn(),
  listRoleMenus: vi.fn(),
  setRoleMenus: vi.fn(),
}))

vi.mock('@/api/menus', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/menus')>()),
  fetchMenuTree: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

const mockListRoles = vi.mocked(listRoles)
const mockCreateRole = vi.mocked(createRole)
const mockUpdateRole = vi.mocked(updateRole)
const mockDeleteRole = vi.mocked(deleteRole)
const mockFetchDataScope = vi.mocked(fetchRoleDataScope)
const mockSetDataScope = vi.mocked(setRoleDataScope)
const mockListRoleMenus = vi.mocked(listRoleMenus)
const mockSetRoleMenus = vi.mocked(setRoleMenus)
const mockFetchMenuTree = vi.mocked(fetchMenuTree)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)

beforeEach(() => {
  clearToasts()
  // 全量权限码：SettingsLayout 导航按权限码过滤后仍展示全部模块。
  mockFetchMyPermissions.mockResolvedValue(['sys:role', 'sys:menu'])
  mockListRoles.mockResolvedValue({
    items: [role(), presetRole()],
    total: '2',
    hasMore: false,
    nextCursor: '',
    page: 1,
  })
  mockFetchMenuTree.mockResolvedValue(sampleMenuTree())
  mockListRoleMenus.mockResolvedValue({ menuIds: ['201', '202'] })
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

// role 构造 DTO 基线：可选字段缺省即未设置（protojson 口径）。
function role(overrides: Partial<RoleDTO> = {}): RoleDTO {
  return {
    id: '5',
    status: 1,
    name: '运营',
    code: 'operator',
    roleType: 2,
    dataScope: 1,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

function presetRole(): RoleDTO {
  return role({ id: '1', name: '超级管理员', code: 'admin', roleType: 1, dataScope: 7, createdAt: '2025-12-31T08:00:00Z' })
}

function menu(overrides: Partial<MenuDTO> = {}): MenuDTO {
  return {
    id: '201',
    status: 1,
    name: '系统管理',
    permissionCode: 'sys:manage',
    menuType: 1,
    sortOrder: 1,
    createdBy: '1',
    updatedBy: '1',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

function node(m: MenuDTO, children: MenuTreeNodeDTO[] = []): MenuTreeNodeDTO {
  return { menu: m, children }
}

// sampleMenuTree 是三级样例树：系统管理 → 用户管理 →（用户查询、用户新增）
// 外加一个停用按钮（不可授权）。
function sampleMenuTree(): MenuTreeReplyDTO {
  return {
    roots: [
      node(menu(), [
        node(
          menu({ id: '202', name: '用户管理', permissionCode: 'sys:user', menuType: 2, parentId: '201', sortOrder: 1 }),
          [
            node(menu({ id: '203', name: '用户查询', permissionCode: 'sys:user:query', menuType: 3, parentId: '202', sortOrder: 1 })),
            node(menu({ id: '204', name: '用户新增', permissionCode: 'sys:user:create', menuType: 3, parentId: '202', sortOrder: 2 })),
            node(menu({ id: '205', name: '用户编辑', permissionCode: 'sys:user:update', menuType: 3, parentId: '202', sortOrder: 3, status: 0 })),
          ],
        ),
      ]),
    ],
    total: '5',
  }
}

// renderPage 以真实布局挂载角色页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/roles']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="roles" element={<RoleManagePage />} />
        </Route>
      </Routes>
      <Toaster />
    </MemoryRouter>,
  )
}

async function openRowActions(user2: ReturnType<typeof userEvent.setup>, name: string) {
  await user2.click(await screen.findByRole('button', { name: `操作 ${name}` }))
  const menu2 = await screen.findByRole('menu')
  return within(menu2)
}

describe('RoleManagePage list', () => {
  it('renders_the_role_rows_with_type_and_data_scope_labels', async () => {
    renderPage()

    expect(await screen.findByText('运营')).toBeInTheDocument()
    expect(screen.getByText('超级管理员')).toBeInTheDocument()
    // 数据范围规则与角色类型按词表渲染。
    expect(screen.getByRole('row', { name: /运营/ })).toHaveTextContent('仅本人')
    expect(screen.getByRole('row', { name: /超级管理员/ })).toHaveTextContent('全租户')
    expect(screen.getByRole('row', { name: /超级管理员/ })).toHaveTextContent('系统预置')
    expect(mockListRoles).toHaveBeenCalledTimes(1)
  })

  it('sends_the_name_contains_code_prefix_and_status_filters_on_apply', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('运营')

    await user2.type(screen.getByLabelText('角色名关键字'), '运营')
    await user2.type(screen.getByLabelText('编码前缀'), 'op')
    await user2.click(screen.getByLabelText('状态筛选'))
    await user2.click(screen.getByRole('option', { name: '启用' }))
    await user2.click(screen.getByRole('button', { name: '筛选' }))

    await waitFor(() => {
      const lastCall = mockListRoles.mock.calls.at(-1)
      expect(lastCall?.[0]?.where).toEqual([
        { field: 'name', op: 'contains', value: '运营' },
        { field: 'code', op: 'prefix', value: 'op' },
        { field: 'status', op: 'eq', value: 1 },
      ])
    })
  })
})

describe('RoleManagePage create', () => {
  it('creates_a_role_with_name_and_code_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockCreateRole.mockResolvedValue(role({ id: '9', name: '客服', code: 'support' }))
    renderPage()
    await screen.findByText('运营')

    await user2.click(screen.getByRole('button', { name: '新建角色' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('角色名'), '客服')
    await user2.type(within(dialog).getByLabelText('角色编码'), 'support')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateRole).toHaveBeenCalledWith({ name: '客服', code: 'support' }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('角色 客服 已创建')
    await waitFor(() => expect(mockListRoles).toHaveBeenCalledTimes(2))
  })

  it('rejects_missing_name_or_code_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('运营')

    await user2.click(screen.getByRole('button', { name: '新建角色' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '创建' }))

    expect(screen.getByRole('alert')).toHaveTextContent('name and code are required')
    expect(mockCreateRole).not.toHaveBeenCalled()
  })

  it('maps_the_duplicate_rejection_to_a_readable_hint_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateRole.mockRejectedValue(new Error('role already exists'))
    renderPage()
    await screen.findByText('运营')

    await user2.click(screen.getByRole('button', { name: '新建角色' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('角色名'), '运营')
    await user2.type(within(dialog).getByLabelText('角色编码'), 'operator')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a role with the same code or name already exists in the tenant',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListRoles).toHaveBeenCalledTimes(1)
  })
})

describe('RoleManagePage edit', () => {
  it('prefills_the_row_values_and_saves_with_an_explicit_status', async () => {
    const user2 = userEvent.setup()
    mockUpdateRole.mockResolvedValue(role({ name: '运营二部' }))
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('角色名')).toHaveValue('运营')

    await user2.click(within(dialog).getByRole('switch', { name: '状态' }))
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 更新只覆盖 name/status/remark，code 不在载荷中。
    await waitFor(() =>
      expect(mockUpdateRole).toHaveBeenCalledWith({ id: '5', status: 0, name: '运营' }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('角色 运营 已更新')
    await waitFor(() => expect(mockListRoles).toHaveBeenCalledTimes(2))
  })

  it('reports_the_update_failure_inline_without_closing', async () => {
    const user2 = userEvent.setup()
    mockUpdateRole.mockRejectedValue(new Error('forbidden'))
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '编辑' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))

    expect(await within(screen.getByRole('dialog')).findByRole('alert')).toHaveTextContent('forbidden')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})

describe('RoleManagePage data scope', () => {
  function dataScope(overrides: Partial<RoleDataScopeDTO> = {}): RoleDataScopeDTO {
    return {
      roleId: '5',
      dataScope: 5,
      scopeDeptId: '3',
      idScopes: [
        { type: 2, dataId: '3' },
        { type: 2, dataId: '9' },
      ],
      allowedDeptIds: ['3'],
      ...overrides,
    }
  }

  beforeEach(() => {
    mockFetchDataScope.mockResolvedValue(dataScope())
  })

  it('loads_the_config_and_prefills_the_dept_scope_face', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '数据范围' }))
    const dialog = await screen.findByRole('dialog')

    expect(await within(dialog).findByRole('combobox', { name: '数据范围规则' })).toHaveTextContent('指定部门')
    expect(within(dialog).getByLabelText('指定部门 ID（逗号或空白分隔）')).toHaveValue('3, 9')
    expect(within(dialog).getByLabelText('分配层级限定部门 ID（可选，留空=全租户可分配）')).toHaveValue('3')
    expect(within(dialog).getByLabelText('分发部门 ID（逗号或空白分隔，留空=遵循通用分配逻辑）')).toHaveValue('3')
  })

  it('keeps_other_type_entries_and_clears_scope_dept_when_switching_to_all_tenant', async () => {
    const user2 = userEvent.setup()
    mockSetDataScope.mockResolvedValue(dataScope({ dataScope: 7, scopeDeptId: undefined, idScopes: [], allowedDeptIds: [] }))
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '数据范围' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByLabelText('指定部门 ID（逗号或空白分隔）')

    await user2.click(within(dialog).getByRole('combobox', { name: '数据范围规则' }))
    await user2.click(screen.getByRole('option', { name: '全租户' }))
    // 指定部门编辑面随规则切换消失。
    expect(within(dialog).queryByLabelText('指定部门 ID（逗号或空白分隔）')).not.toBeInTheDocument()
    await user2.clear(within(dialog).getByLabelText('分配层级限定部门 ID（可选，留空=全租户可分配）'))
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 整组替换：scope_dept_id 清回 NULL；惰性的 type-2 条目原样保留。
    await waitFor(() =>
      expect(mockSetDataScope).toHaveBeenCalledWith('5', {
        dataScope: 7,
        scopeDeptId: undefined,
        idScopes: [
          { type: 2, dataId: '3' },
          { type: 2, dataId: '9' },
        ],
        allowedDeptIds: ['3'],
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('角色 运营 的数据范围已更新')
  })

  it('replaces_only_the_face_type_entries_when_switching_between_id_scopes', async () => {
    const user2 = userEvent.setup()
    // 混合明细夹具：指定人（type-1）编辑面的现值 + 指定部门（type-2）的
    // 惰性条目，验证「替换当前面、其余类型保留」真实发生。
    mockFetchDataScope.mockResolvedValue(
      dataScope({
        dataScope: 4,
        idScopes: [
          { type: 1, dataId: '11' },
          { type: 2, dataId: '3' },
        ],
      }),
    )
    mockSetDataScope.mockResolvedValue(dataScope({ dataScope: 4 }))
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '数据范围' }))
    const dialog = await screen.findByRole('dialog')
    const face = await within(dialog).findByLabelText('指定用户 ID（逗号或空白分隔）')
    expect(face).toHaveValue('11')

    await user2.clear(face)
    await user2.type(face, '12, 13')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 指定人面整组替换为输入集合；其余类型的惰性条目（type-2）原样保留。
    await waitFor(() =>
      expect(mockSetDataScope).toHaveBeenCalledWith('5', {
        dataScope: 4,
        scopeDeptId: '3',
        idScopes: [
          { type: 2, dataId: '3' },
          { type: 1, dataId: '12' },
          { type: 1, dataId: '13' },
        ],
        allowedDeptIds: ['3'],
      }),
    )
  })

  it('rejects_non_numeric_scope_dept_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '数据范围' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByLabelText('分配层级限定部门 ID（可选，留空=全租户可分配）')

    await user2.clear(within(dialog).getByLabelText('分配层级限定部门 ID（可选，留空=全租户可分配）'))
    await user2.type(within(dialog).getByLabelText('分配层级限定部门 ID（可选，留空=全租户可分配）'), 'x3')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('scope dept must be a positive id')
    expect(mockSetDataScope).not.toHaveBeenCalled()
  })

  it('maps_the_unknown_reference_rejection_to_a_readable_hint', async () => {
    const user2 = userEvent.setup()
    mockSetDataScope.mockRejectedValue(new Error('invalid role argument'))
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '数据范围' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByLabelText('分发部门 ID（逗号或空白分隔，留空=遵循通用分配逻辑）')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'invalid argument; referenced users, depts or groups must exist and be enabled',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('shows_a_retry_banner_and_recovers_when_the_config_load_fails', async () => {
    const user2 = userEvent.setup()
    mockFetchDataScope.mockRejectedValueOnce(new Error('network down'))
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '数据范围' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('数据范围加载失败：network down')
    expect(screen.queryByLabelText('数据范围规则')).not.toBeInTheDocument()

    await user2.click(screen.getByRole('button', { name: '重试' }))

    expect(await screen.findByRole('combobox', { name: '数据范围规则' })).toBeInTheDocument()
    expect(mockFetchDataScope).toHaveBeenCalledTimes(2)
  })
})

describe('RoleManagePage menu grants', () => {
  it('renders_the_tree_with_the_granted_menus_checked_from_the_echo_set', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '菜单授权' }))
    const dialog = await screen.findByRole('dialog')

    // 回显集合 ['201','202']：系统管理与用户管理勾选，叶子未勾。
    expect(await within(dialog).findByRole('checkbox', { name: '系统管理' })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: '用户管理' })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: '用户查询' })).not.toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: '用户管理' })).toBeInTheDocument()
    // 停用菜单不可授权。
    expect(within(dialog).getByRole('checkbox', { name: '用户编辑' })).toBeDisabled()
  })

  it('shows_the_parent_as_indeterminate_when_only_a_leaf_of_its_subtree_is_granted', async () => {
    const user2 = userEvent.setup()
    // 回显集合只含叶子（203 用户查询）：两级父节点都应呈半选态。
    mockListRoleMenus.mockResolvedValue({ menuIds: ['203'] })
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '菜单授权' }))
    const dialog = await screen.findByRole('dialog')

    expect(await within(dialog).findByRole('checkbox', { name: '用户查询' })).toBeChecked()
    // 半选态经 aria-checked="mixed" 表达（checkbox 的减号指示随之呈现）。
    expect(within(dialog).getByRole('checkbox', { name: '用户管理' })).toHaveAttribute('aria-checked', 'mixed')
    expect(within(dialog).getByRole('checkbox', { name: '系统管理' })).toHaveAttribute('aria-checked', 'mixed')
    expect(within(dialog).getByRole('checkbox', { name: '用户新增' })).not.toBeChecked()
    expect(mockSetRoleMenus).not.toHaveBeenCalled()
  })

  it('checks_the_whole_subtree_when_clicking_an_indeterminate_parent', async () => {
    const user2 = userEvent.setup()
    mockListRoleMenus.mockResolvedValue({ menuIds: ['203'] })
    mockSetRoleMenus.mockResolvedValue({ menuIds: ['201', '202', '203', '204'] })
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '菜单授权' }))
    const dialog = await screen.findByRole('dialog')
    const root = await within(dialog).findByRole('checkbox', { name: '系统管理' })
    expect(root).toHaveAttribute('aria-checked', 'mixed')

    // 半选父节点不在勾选集合内：点击按「勾选」方向级联整棵子树（停用菜单除外）。
    await user2.click(root)
    expect(within(dialog).getByRole('checkbox', { name: '用户管理' })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: '用户查询' })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: '用户新增' })).toBeChecked()
    expect(within(dialog).getByRole('checkbox', { name: '用户编辑' })).toBeDisabled()

    await user2.click(within(dialog).getByRole('button', { name: '保存授权' }))
    await waitFor(() => expect(mockSetRoleMenus).toHaveBeenCalledWith('5', ['201', '202', '203', '204']))
  })

  it('cascades_the_subtree_on_check_and_saves_the_full_selected_set', async () => {
    const user2 = userEvent.setup()
    mockSetRoleMenus.mockResolvedValue({ menuIds: ['201', '202', '203'] })
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '菜单授权' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByRole('checkbox', { name: '用户查询' })

    await user2.click(within(dialog).getByRole('checkbox', { name: '用户查询' }))
    await user2.click(within(dialog).getByRole('button', { name: '保存授权' }))

    // 提交勾选后的完整有效菜单集合（数值升序）。
    await waitFor(() => expect(mockSetRoleMenus).toHaveBeenCalledWith('5', ['201', '202', '203']))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent(
      '角色 运营 的菜单授权已更新（共 3 项）',
    )
    await waitFor(() => expect(mockListRoles).toHaveBeenCalledTimes(2))
  })

  it('cascades_the_subtree_on_uncheck_and_saves_an_empty_set_to_clear_all_grants', async () => {
    const user2 = userEvent.setup()
    mockSetRoleMenus.mockResolvedValue({ menuIds: [] })
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '菜单授权' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByRole('checkbox', { name: '系统管理' })

    await user2.click(within(dialog).getByRole('checkbox', { name: '系统管理' }))
    expect(within(dialog).getByRole('checkbox', { name: '用户管理' })).not.toBeChecked()
    await user2.click(within(dialog).getByRole('button', { name: '保存授权' }))

    // 空集合合法：清空全部授权。
    await waitFor(() => expect(mockSetRoleMenus).toHaveBeenCalledWith('5', []))
  })

  it('keeps_the_save_button_disabled_until_the_tree_loads_and_recovers_on_retry', async () => {
    const user2 = userEvent.setup()
    mockFetchMenuTree.mockRejectedValueOnce(new Error('network down'))
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '菜单授权' }))

    // 树加载失败：错误横幅 + 保存禁用（防止误清空授权）。
    expect(await screen.findByRole('alert')).toHaveTextContent('菜单树加载失败：network down')
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('button', { name: '保存授权' })).toBeDisabled()
    expect(mockSetRoleMenus).not.toHaveBeenCalled()

    await user2.click(screen.getByRole('button', { name: '重试' }))

    await within(dialog).findByRole('checkbox', { name: '系统管理' })
    expect(within(dialog).getByRole('button', { name: '保存授权' })).toBeEnabled()
  })

  it('reports_the_save_failure_inline_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockSetRoleMenus.mockRejectedValue(new Error('invalid role argument'))
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '菜单授权' }))
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByRole('checkbox', { name: '系统管理' })

    await user2.click(within(dialog).getByRole('button', { name: '保存授权' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'invalid argument; referenced users, depts or groups must exist and be enabled',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListRoles).toHaveBeenCalledTimes(1)
  })
})

describe('RoleManagePage delete', () => {
  it('asks_for_confirmation_and_only_deletes_on_confirm', async () => {
    const user2 = userEvent.setup()
    mockDeleteRole.mockResolvedValue(undefined)
    renderPage()
    const actions = await openRowActions(user2, '运营')

    await user2.click(actions.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除角色 运营？')
    expect(mockDeleteRole).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockDeleteRole).not.toHaveBeenCalled()

    const actionsAgain = await openRowActions(user2, '运营')
    await user2.click(actionsAgain.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteRole).toHaveBeenCalledWith('5'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('角色 运营 已删除')
    await waitFor(() => expect(mockListRoles).toHaveBeenCalledTimes(2))
  })

  it('hints_that_preset_roles_cannot_be_deleted_when_the_backend_rejects', async () => {
    const user2 = userEvent.setup()
    mockDeleteRole.mockRejectedValue(new Error('preset role cannot be deleted'))
    renderPage()
    const actions = await openRowActions(user2, '超级管理员')

    await user2.click(actions.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert', { hidden: true })).toHaveTextContent(
      '删除失败：preset role cannot be deleted',
    )
  })
})
