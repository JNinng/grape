import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DeptDTO, DeptTreeNodeDTO, DeptTreeReplyDTO } from '@/api/depts'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { DeptManagePage } from './DeptManagePage'
import { createDept, deleteDept, fetchDeptTree, moveDept, updateDept } from '@/api/depts'

vi.mock('@/api/depts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/depts')>()),
  fetchDeptTree: vi.fn(),
  createDept: vi.fn(),
  updateDept: vi.fn(),
  moveDept: vi.fn(),
  deleteDept: vi.fn(),
}))

const mockFetchTree = vi.mocked(fetchDeptTree)
const mockCreateDept = vi.mocked(createDept)
const mockUpdateDept = vi.mocked(updateDept)
const mockMoveDept = vi.mocked(moveDept)
const mockDeleteDept = vi.mocked(deleteDept)

// dept 构造 DTO 基线：可选字段缺省即未设置（protojson 口径）。
function dept(overrides: Partial<DeptDTO> = {}): DeptDTO {
  return {
    id: '1',
    status: 1,
    name: '总公司',
    ownerUserId: '7',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

function node(d: DeptDTO, children: DeptTreeNodeDTO[] = []): DeptTreeNodeDTO {
  return { dept: d, children }
}

// sampleTree 是两级半的样例树：总公司 →（研发部 → 前端组、市场部）。
function sampleTree(): DeptTreeReplyDTO {
  return {
    roots: [
      node(dept(), [
        node(dept({ id: '2', name: '研发部', parentId: '1', createdAt: '2026-01-02T08:00:00Z' }), [
          node(dept({ id: '4', name: '前端组', parentId: '2', createdAt: '2026-01-04T08:00:00Z' })),
        ]),
        node(dept({ id: '3', name: '市场部', parentId: '1', createdAt: '2026-01-03T08:00:00Z' })),
      ]),
    ],
    total: '4',
  }
}

beforeEach(() => {
  clearToasts()
  mockFetchTree.mockResolvedValue(sampleTree())
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

// renderPage 以真实布局挂载部门页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/depts']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="depts" element={<DeptManagePage />} />
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

describe('DeptManagePage tree rendering', () => {
  it('renders_the_nested_tree_with_the_total_on_mount', async () => {
    renderPage()

    expect(await screen.findByText('总公司')).toBeInTheDocument()
    expect(screen.getByText('研发部')).toBeInTheDocument()
    expect(screen.getByText('前端组')).toBeInTheDocument()
    expect(screen.getByText('市场部')).toBeInTheDocument()
    expect(screen.getByLabelText('列表统计')).toHaveTextContent('共 4 个部门')
    expect(mockFetchTree).toHaveBeenCalledTimes(1)
  })

  it('collapses_and_expands_a_subtree_without_unmounting_the_parent_row', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('前端组')

    await user2.click(screen.getByRole('button', { name: '收起 研发部' }))
    expect(screen.queryByText('前端组')).not.toBeInTheDocument()
    expect(screen.getByText('研发部')).toBeInTheDocument()

    await user2.click(screen.getByRole('button', { name: '展开 研发部' }))
    expect(screen.getByText('前端组')).toBeInTheDocument()
  })

  it('shows_the_empty_state_when_the_tenant_has_no_depts', async () => {
    mockFetchTree.mockResolvedValue({ roots: [], total: '0' })
    renderPage()

    expect(await screen.findByText('暂无部门')).toBeInTheDocument()
    expect(screen.getByLabelText('列表统计')).toHaveTextContent('共 0 个部门')
    expect(screen.getByRole('button', { name: '新建部门' })).toBeInTheDocument()
  })
})

describe('DeptManagePage load failure', () => {
  it('shows_a_retry_banner_and_recovers_when_the_retry_succeeds', async () => {
    const user2 = userEvent.setup()
    mockFetchTree.mockRejectedValueOnce(new Error('network down'))
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('组织树加载失败：network down')
    expect(screen.queryByText('总公司')).not.toBeInTheDocument()

    await user2.click(screen.getByRole('button', { name: '重试' }))

    expect(await screen.findByText('总公司')).toBeInTheDocument()
    expect(mockFetchTree).toHaveBeenCalledTimes(2)
  })
})

describe('DeptManagePage create', () => {
  it('creates_a_root_dept_and_reloads_the_tree', async () => {
    const user2 = userEvent.setup()
    mockCreateDept.mockResolvedValue(dept({ id: '9', name: '客服部' }))
    renderPage()
    await screen.findByText('总公司')

    await user2.click(screen.getByRole('button', { name: '新建部门' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('部门名称'), '客服部')
    await user2.type(within(dialog).getByLabelText('部门管理员用户 ID'), '7')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    // 上级部门保持缺省（根部门）即不发送 parentId。
    await waitFor(() =>
      expect(mockCreateDept).toHaveBeenCalledWith({ name: '客服部', ownerUserId: '7' }),
    )
    expect(await screen.findByRole('status')).toHaveTextContent('部门 客服部 已创建')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('creates_a_child_dept_under_the_preselected_parent', async () => {
    const user2 = userEvent.setup()
    mockCreateDept.mockResolvedValue(dept({ id: '9', name: '测试组', parentId: '2' }))
    renderPage()
    await screen.findByText('研发部')

    const menu = await openRowActions(user2, '研发部')
    await user2.click(menu.getByRole('menuitem', { name: '新建子部门' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('combobox', { name: '上级部门' })).toHaveTextContent('研发部')
    await user2.type(within(dialog).getByLabelText('部门名称'), '测试组')
    await user2.type(within(dialog).getByLabelText('部门管理员用户 ID'), '7')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateDept).toHaveBeenCalledWith({ name: '测试组', parentId: '2', ownerUserId: '7' }),
    )
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('rejects_missing_name_or_owner_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('总公司')

    await user2.click(screen.getByRole('button', { name: '新建部门' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '创建' }))

    expect(screen.getByRole('alert')).toHaveTextContent('name and owner user id are required')
    expect(mockCreateDept).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('rejects_non_numeric_owner_ids_inline', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('总公司')

    await user2.click(screen.getByRole('button', { name: '新建部门' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('部门名称'), '客服部')
    await user2.type(within(dialog).getByLabelText('部门管理员用户 ID'), 'x7')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('owner ids must be positive ids')
    expect(mockCreateDept).not.toHaveBeenCalled()
  })

  it('surfaces_the_duplicate_name_rejection_inline_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateDept.mockRejectedValue(new Error('dept already exists'))
    renderPage()
    await screen.findByText('总公司')

    await user2.click(screen.getByRole('button', { name: '新建部门' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('部门名称'), '研发部')
    await user2.type(within(dialog).getByLabelText('部门管理员用户 ID'), '7')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a dept with the same name already exists in the tenant',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockFetchTree).toHaveBeenCalledTimes(1)
  })
})

describe('DeptManagePage edit', () => {
  it('prefills_the_row_values_and_saves_with_an_explicit_status', async () => {
    const user2 = userEvent.setup()
    mockUpdateDept.mockResolvedValue(dept({ id: '2', name: '研发部' }))
    renderPage()
    const menu = await openRowActions(user2, '研发部')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('部门名称')).toHaveValue('研发部')

    // 停用开关后保存：status 显式为 0（更新语义必填）。
    await user2.click(within(dialog).getByRole('switch', { name: '状态' }))
    expect(within(dialog).getByText('禁用')).toBeInTheDocument()
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(mockUpdateDept).toHaveBeenCalledWith({ id: '2', status: 0, name: '研发部' }),
    )
    expect(await screen.findByRole('status')).toHaveTextContent('部门 研发部 已更新')
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('reports_the_update_failure_inline_without_closing', async () => {
    const user2 = userEvent.setup()
    mockUpdateDept.mockRejectedValue(new Error('forbidden'))
    renderPage()
    const menu = await openRowActions(user2, '研发部')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))

    expect(await within(screen.getByRole('dialog')).findByRole('alert')).toHaveTextContent('forbidden')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockFetchTree).toHaveBeenCalledTimes(1)
  })
})

describe('DeptManagePage move', () => {
  it('moves_a_dept_to_another_parent_and_reloads_the_tree', async () => {
    const user2 = userEvent.setup()
    mockMoveDept.mockResolvedValue(dept({ id: '3', name: '市场部', parentId: '2' }))
    renderPage()
    const menu = await openRowActions(user2, '市场部')

    await user2.click(menu.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('combobox', { name: '上级部门' })).toHaveTextContent('总公司')

    await user2.click(within(dialog).getByRole('combobox', { name: '上级部门' }))
    await user2.click(screen.getByRole('option', { name: '研发部' }))
    await user2.click(within(dialog).getByRole('button', { name: '移动' }))

    await waitFor(() => expect(mockMoveDept).toHaveBeenCalledWith('3', '2'))
    expect(await screen.findByRole('status')).toHaveTextContent('部门 市场部 已移动')
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('moves_a_dept_to_the_root_when_the_root_option_is_picked', async () => {
    const user2 = userEvent.setup()
    mockMoveDept.mockResolvedValue(dept({ id: '4', name: '前端组' }))
    renderPage()
    const menu = await openRowActions(user2, '前端组')

    await user2.click(menu.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('combobox', { name: '上级部门' }))
    await user2.click(screen.getByRole('option', { name: '无（根部门）' }))
    await user2.click(within(dialog).getByRole('button', { name: '移动' }))

    // 移为根部门：parentId 未设置（undefined 即请求体省略该键）。
    await waitFor(() => expect(mockMoveDept).toHaveBeenCalledWith('4', undefined))
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('surfaces_the_cycle_rejection_from_the_backend_inline', async () => {
    const user2 = userEvent.setup()
    mockMoveDept.mockRejectedValue(new Error('invalid dept argument'))
    renderPage()
    const menu = await openRowActions(user2, '研发部')

    await user2.click(menu.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('combobox', { name: '上级部门' }))
    await user2.click(screen.getByRole('option', { name: '前端组' }))
    await user2.click(within(dialog).getByRole('button', { name: '移动' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'invalid argument; the parent may be missing or the move would create a cycle',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockFetchTree).toHaveBeenCalledTimes(1)
  })

  it('excludes_the_moved_dept_itself_from_the_parent_options_when_the_dialog_opens', async () => {
    const user2 = userEvent.setup()
    renderPage()
    const menu = await openRowActions(user2, '研发部')

    await user2.click(menu.getByRole('menuitem', { name: '移动' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('combobox', { name: '上级部门' }))

    // 选项里其余部门与根选项齐备，唯独不含被移动部门自身。
    expect(screen.getByRole('option', { name: '无（根部门）' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '总公司' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '市场部' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '研发部' })).not.toBeInTheDocument()
  })
})

describe('DeptManagePage delete', () => {
  it('asks_for_confirmation_and_only_deletes_on_confirm', async () => {
    const user2 = userEvent.setup()
    mockDeleteDept.mockResolvedValue(undefined)
    renderPage()
    const menu = await openRowActions(user2, '前端组')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除部门 前端组？')
    expect(mockDeleteDept).not.toHaveBeenCalled()

    // 取消：关框且不删除。
    await user2.click(within(confirmDialog).getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockDeleteDept).not.toHaveBeenCalled()

    // 再次打开并确认：删除并重查树。
    const menuAgain = await openRowActions(user2, '前端组')
    await user2.click(menuAgain.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteDept).toHaveBeenCalledWith('4'))
    expect(await screen.findByRole('status')).toHaveTextContent('部门 前端组 已删除')
    await waitFor(() => expect(mockFetchTree).toHaveBeenCalledTimes(2))
  })

  it('hints_to_delete_children_first_when_the_backend_rejects_with_has_children', async () => {
    const user2 = userEvent.setup()
    mockDeleteDept.mockRejectedValue(new Error('dept has active children'))
    renderPage()
    const menu = await openRowActions(user2, '研发部')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '删除失败：dept has active children; delete its child depts first',
    )
  })
})
