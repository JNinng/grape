import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MenuDTO, MenuTreeNodeDTO, MenuTreeReplyDTO } from '@/api/menus'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { MenuManagePage } from './MenuManagePage'
import { createMenu, deleteMenu, fetchMenuTree, moveMenu, updateMenu } from '@/api/menus'
import { fetchMyPermissions } from '@/api/permissions'

vi.mock('@/api/menus', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/menus')>()),
  fetchMenuTree: vi.fn(),
  createMenu: vi.fn(),
  updateMenu: vi.fn(),
  moveMenu: vi.fn(),
  deleteMenu: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

const mockFetchTree = vi.mocked(fetchMenuTree)
const mockCreateMenu = vi.mocked(createMenu)
const mockUpdateMenu = vi.mocked(updateMenu)
const mockMoveMenu = vi.mocked(moveMenu)
const mockDeleteMenu = vi.mocked(deleteMenu)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)

beforeEach(() => {
  clearToasts()
  // 全量权限码：SettingsLayout 导航按权限码过滤后仍展示全部模块。
  mockFetchMyPermissions.mockResolvedValue(['sys:role', 'sys:menu'])
  mockFetchTree.mockResolvedValue(sampleTree())
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

// menu 构造 DTO 基线：可选字段缺省即未设置（protojson 口径）。
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

// sampleTree 是三级样例树：系统管理 →（菜单管理 → 菜单查询）。
function sampleTree(): MenuTreeReplyDTO {
  return {
    roots: [
      node(menu(), [
        node(
          menu({ id: '214', name: '菜单管理', permissionCode: 'sys:menu', menuType: 2, parentId: '201', sortOrder: 3 }),
          [node(menu({ id: '215', name: '菜单查询', permissionCode: 'sys:menu:query', menuType: 3, parentId: '214', sortOrder: 1 }))],
        ),
      ]),
    ],
    total: '3',
  }
}

// renderPage 以真实布局挂载菜单页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/menus']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="menus" element={<MenuManagePage />} />
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

describe('MenuManagePage tree rendering', () => {
  it('renders_the_nested_tree_with_the_total_on_mount', async () => {
    renderPage()

    // 页头与树节点同名，树内断言以「菜单树」容器为作用域。
    const treeScope = within(await screen.findByLabelText('菜单树'))
    expect(treeScope.getByText('系统管理')).toBeInTheDocument()
    expect(treeScope.getByText('菜单管理')).toBeInTheDocument()
    expect(treeScope.getByText('菜单查询')).toBeInTheDocument()
    expect(screen.getByLabelText('列表统计')).toHaveTextContent('共 3 个菜单')
    expect(mockFetchTree).toHaveBeenCalledTimes(1)
  })

  it('collapses_and_expands_a_subtree_without_unmounting_the_parent_row', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('菜单查询')

    const treeScope = within(screen.getByLabelText('菜单树'))
    await user2.click(screen.getByRole('button', { name: '收起 菜单管理' }))
    expect(treeScope.queryByText('菜单查询')).not.toBeInTheDocument()
    expect(treeScope.getByText('菜单管理')).toBeInTheDocument()

    await user2.click(screen.getByRole('button', { name: '展开 菜单管理' }))
    expect(treeScope.getByText('菜单查询')).toBeInTheDocument()
  })

  it('shows_the_empty_state_when_the_tenant_has_no_menus', async () => {
    mockFetchTree.mockResolvedValue({ roots: [], total: '0' })
    renderPage()

    expect(await screen.findByText('暂无菜单')).toBeInTheDocument()
    expect(screen.getByLabelText('列表统计')).toHaveTextContent('共 0 个菜单')
    expect(screen.getByRole('button', { name: '新建菜单' })).toBeInTheDocument()
  })

  it('shows_a_retry_banner_and_recovers_when_the_tree_load_fails', async () => {
    const user2 = userEvent.setup()
    mockFetchTree.mockRejectedValueOnce(new Error('network down'))
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('菜单树加载失败：network down')
    // 侧栏「系统管理」标题与树节点同名，树卸载以「菜单树」容器消失为准。
    expect(screen.queryByLabelText('菜单树')).not.toBeInTheDocument()

    await user2.click(screen.getByRole('button', { name: '重试' }))

    expect(await screen.findByLabelText('菜单树')).toBeInTheDocument()
    expect(within(screen.getByLabelText('菜单树')).getByText('系统管理')).toBeInTheDocument()
    expect(mockFetchTree).toHaveBeenCalledTimes(2)
  })
})

describe('MenuManagePage create', () => {
  it('creates_a_root_menu_with_the_immutable_columns_and_reloads_the_tree', async () => {
    const user2 = userEvent.setup()
    mockCreateMenu.mockResolvedValue(menu({ id: '216', name: '配置管理', permissionCode: 'sys:config', menuType: 1 }))
    renderPage()
    await screen.findByText('系统管理')

    await user2.click(screen.getByRole('button', { name: '新建菜单' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('名称'), '配置管理')
    await user2.type(within(dialog).getByLabelText('权限编码'), 'sys:config')
    await user2.click(within(dialog).getByRole('combobox', { name: '菜单类型' }))
    await user2.click(screen.getByRole('option', { name: '目录' }))
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    // 上级菜单保持缺省（根菜单）即不发送 parentId。
    await waitFor(() =>
      expect(mockCreateMenu).toHaveBeenCalledWith({
        name: '配置管理',
        permissionCode: 'sys:config',
        menuType: 1,
        sortOrder: 0,
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('菜单 配置管理 已创建')
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('creates_a_child_menu_under_the_preselected_parent', async () => {
    const user2 = userEvent.setup()
    mockCreateMenu.mockResolvedValue(menu({ id: '216', name: '菜单新增', permissionCode: 'sys:menu:create', menuType: 3, parentId: '214', sortOrder: 2 }))
    renderPage()
    await screen.findByText('菜单管理')

    const actions = await openRowActions(user2, '菜单管理')
    await user2.click(actions.getByRole('menuitem', { name: '新建子菜单' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('combobox', { name: '上级菜单' })).toHaveTextContent('菜单管理')
    await user2.type(within(dialog).getByLabelText('名称'), '菜单新增')
    await user2.type(within(dialog).getByLabelText('权限编码'), 'sys:menu:create')
    await user2.click(within(dialog).getByRole('combobox', { name: '菜单类型' }))
    await user2.click(screen.getByRole('option', { name: '按钮' }))
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateMenu).toHaveBeenCalledWith({
        name: '菜单新增',
        permissionCode: 'sys:menu:create',
        parentId: '214',
        menuType: 3,
        sortOrder: 0,
      }),
    )
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('rejects_missing_name_or_code_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('系统管理')

    await user2.click(screen.getByRole('button', { name: '新建菜单' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '创建' }))

    expect(screen.getByRole('alert')).toHaveTextContent('name and permission code are required')
    expect(mockCreateMenu).not.toHaveBeenCalled()
  })

  it('rejects_a_non_integer_sort_order_inline', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('系统管理')

    await user2.click(screen.getByRole('button', { name: '新建菜单' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('名称'), '新菜单')
    await user2.type(within(dialog).getByLabelText('权限编码'), 'sys:new')
    await user2.clear(within(dialog).getByLabelText('排序号'))
    await user2.type(within(dialog).getByLabelText('排序号'), 'x1')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('sort order must be an integer')
    expect(mockCreateMenu).not.toHaveBeenCalled()
  })

  it('maps_the_duplicate_code_rejection_to_a_readable_hint_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateMenu.mockRejectedValue(new Error('menu already exists'))
    renderPage()
    await screen.findByText('系统管理')

    await user2.click(screen.getByRole('button', { name: '新建菜单' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('名称'), '菜单管理')
    await user2.type(within(dialog).getByLabelText('权限编码'), 'sys:menu')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a menu with the same permission code already exists in the tenant',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockFetchTree).toHaveBeenCalledTimes(1)
  })
})

describe('MenuManagePage edit', () => {
  it('prefills_the_row_values_and_saves_only_the_mutable_fields', async () => {
    const user2 = userEvent.setup()
    mockUpdateMenu.mockResolvedValue(menu({ id: '214', name: '菜单管理' }))
    renderPage()
    const actions = await openRowActions(user2, '菜单管理')

    await user2.click(actions.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('名称')).toHaveValue('菜单管理')
    // 权限编码与菜单类型不在编辑表单（创建后不可变）。
    expect(within(dialog).queryByLabelText('权限编码')).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('combobox', { name: '菜单类型' })).not.toBeInTheDocument()

    await user2.click(within(dialog).getByRole('switch', { name: '状态' }))
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 更新载荷不含 permissionCode/menuType/parentId（服务端忽略，前端不上送）。
    await waitFor(() =>
      expect(mockUpdateMenu).toHaveBeenCalledWith({
        id: '214',
        status: 0,
        name: '菜单管理',
        sortOrder: 3,
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('菜单 菜单管理 已更新')
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('reports_the_update_failure_inline_without_closing', async () => {
    const user2 = userEvent.setup()
    mockUpdateMenu.mockRejectedValue(new Error('invalid menu argument'))
    renderPage()
    const actions = await openRowActions(user2, '菜单管理')

    await user2.click(actions.getByRole('menuitem', { name: '编辑' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))

    expect(await within(screen.getByRole('dialog')).findByRole('alert')).toHaveTextContent(
      'invalid argument; the parent may be missing or the move would create a cycle',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockFetchTree).toHaveBeenCalledTimes(1)
  })
})

describe('MenuManagePage move', () => {
  it('moves_a_menu_to_another_parent_and_reloads_the_tree', async () => {
    const user2 = userEvent.setup()
    mockMoveMenu.mockResolvedValue(menu({ id: '215', name: '菜单查询', parentId: '201' }))
    renderPage()
    const actions = await openRowActions(user2, '菜单查询')

    await user2.click(actions.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('combobox', { name: '上级菜单' })).toHaveTextContent('菜单管理')

    await user2.click(within(dialog).getByRole('combobox', { name: '上级菜单' }))
    await user2.click(screen.getByRole('option', { name: '系统管理' }))
    await user2.click(within(dialog).getByRole('button', { name: '移动' }))

    await waitFor(() => expect(mockMoveMenu).toHaveBeenCalledWith('215', '201'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('菜单 菜单查询 已移动')
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('moves_a_menu_to_the_root_when_the_root_option_is_picked', async () => {
    const user2 = userEvent.setup()
    mockMoveMenu.mockResolvedValue(menu({ id: '214', name: '菜单管理' }))
    renderPage()
    const actions = await openRowActions(user2, '菜单管理')

    await user2.click(actions.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('combobox', { name: '上级菜单' }))
    await user2.click(screen.getByRole('option', { name: '无（根菜单）' }))
    await user2.click(within(dialog).getByRole('button', { name: '移动' }))

    // 移为根菜单：parentId 未设置（undefined 即请求体省略该键）。
    await waitFor(() => expect(mockMoveMenu).toHaveBeenCalledWith('214', undefined))
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('excludes_the_moved_menu_and_its_subtree_from_the_parent_options', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const actions = await openRowActions(user2, '菜单管理')

    await user2.click(actions.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('combobox', { name: '上级菜单' }))

    // 移动 菜单管理 时：根选项与其余菜单齐备，唯独不含它自身及其子树（菜单查询）。
    expect(screen.getByRole('option', { name: '无（根菜单）' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '系统管理' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '菜单管理' })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '菜单查询' })).not.toBeInTheDocument()
  })

  it('keeps_leaf_descendants_selectable_when_moving_a_leaf_menu', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const actions = await openRowActions(user2, '菜单查询')

    await user2.click(actions.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('combobox', { name: '上级菜单' }))

    // 移动叶子节点：除自身外全部可选（含祖先与同级）。
    expect(screen.getByRole('option', { name: '系统管理' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '菜单管理' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '菜单查询' })).not.toBeInTheDocument()
  })

  it('surfaces_the_cycle_rejection_from_the_backend_inline', async () => {
    const user2 = userEvent.setup()
    mockMoveMenu.mockRejectedValue(new Error('invalid menu argument'))
    renderPage()
    const actions = await openRowActions(user2, '菜单查询')

    await user2.click(actions.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('combobox', { name: '上级菜单' }))
    await user2.click(screen.getByRole('option', { name: '系统管理' }))
    await user2.click(within(dialog).getByRole('button', { name: '移动' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'invalid argument; the parent may be missing or the move would create a cycle',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockFetchTree).toHaveBeenCalledTimes(1)
  })
})

describe('MenuManagePage delete', () => {
  it('asks_for_confirmation_and_only_deletes_on_confirm', async () => {
    const user2 = userEvent.setup()
    mockDeleteMenu.mockResolvedValue(undefined)
    renderPage()
    const actions = await openRowActions(user2, '菜单查询')

    await user2.click(actions.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除菜单 菜单查询？')
    expect(mockDeleteMenu).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockDeleteMenu).not.toHaveBeenCalled()

    const actionsAgain = await openRowActions(user2, '菜单查询')
    await user2.click(actionsAgain.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteMenu).toHaveBeenCalledWith('215'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('菜单 菜单查询 已删除')
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('hints_to_delete_children_first_when_the_backend_rejects_with_has_children', async () => {
    const user2 = userEvent.setup()
    mockDeleteMenu.mockRejectedValue(new Error('menu has active children'))
    renderPage()
    const actions = await openRowActions(user2, '菜单管理')

    await user2.click(actions.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert', { hidden: true })).toHaveTextContent(
      '删除失败：menu has active children; delete its child menus first',
    )
  })
})
