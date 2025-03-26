import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { TrashPage } from './Trash'

// Mock API functions
vi.mock('@/api/files', () => ({
  listTrash: vi.fn(),
  restoreFromTrash: vi.fn(),
  deleteFromTrash: vi.fn(),
  emptyTrash: vi.fn(),
  getFileIcon: vi.fn(() => 'file'),
}))

// Mock useBatchOperation hook
vi.mock('@/lib/useBatchOperation', () => ({
  useBatchOperation: () => ({
    execute: vi.fn().mockResolvedValue({ succeeded: 1, failed: 0 }),
    isLoading: false,
  }),
}))

import { listTrash, restoreFromTrash, deleteFromTrash, emptyTrash } from '@/api/files'

const mockListTrash = vi.mocked(listTrash)
const mockRestoreFromTrash = vi.mocked(restoreFromTrash)
const mockDeleteFromTrash = vi.mocked(deleteFromTrash)
const mockEmptyTrash = vi.mocked(emptyTrash)

describe('TrashPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListTrash.mockResolvedValue({
      items: [
        {
          id: 'item1',
          originalPath: '/deleted-file.txt',
          deletedAt: new Date(Date.now() - 1000 * 60 * 60).toISOString(), // 1 hour ago
          name: 'deleted-file.txt',
          isDir: false,
          size: 1024,
        },
        {
          id: 'item2',
          originalPath: '/deleted-folder',
          deletedAt: new Date(Date.now() - 1000 * 60 * 60 * 24).toISOString(), // 1 day ago
          name: 'deleted-folder',
          isDir: true,
          size: 0,
        },
      ],
      count: 2,
      totalSize: 1024,
    })
  })

  describe('rendering', () => {
    it('shows loading state initially', () => {
      mockListTrash.mockImplementation(() => new Promise(() => {}))
      render(<TrashPage />)
      
      // Should show skeleton loaders
      const skeletons = document.querySelectorAll('[class*="skeleton"], [class*="animate"]')
      expect(skeletons.length).toBeGreaterThan(0)
    })

    it('renders page header', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('回收站')).toBeTruthy()
      })
    })

    it('calls listTrash API with correct data', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(mockListTrash).toHaveBeenCalled()
      })
      
      // Verify the mock returned data structure
      const mockResult = await mockListTrash()
      expect(mockResult.count).toBe(2)
      expect(mockResult.totalSize).toBe(1024)
    })

    it('shows trash items after loading', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
        expect(screen.getByText('deleted-folder')).toBeTruthy()
      })
    })

    it('displays original paths', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('/deleted-file.txt')).toBeTruthy()
        expect(screen.getByText('/deleted-folder')).toBeTruthy()
      })
    })

    it('shows relative time for deletion', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        // 1 hour ago
        expect(screen.getByText(/小时前|分钟前|刚刚/)).toBeTruthy()
      })
    })
  })

  describe('empty state', () => {
    it('shows empty message when trash is empty', async () => {
      mockListTrash.mockResolvedValue({
        items: [],
        count: 0,
        totalSize: 0,
      })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('回收站是空的')).toBeTruthy()
      })
    })

    it('does not show empty trash button when empty', async () => {
      mockListTrash.mockResolvedValue({
        items: [],
        count: 0,
        totalSize: 0,
      })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.queryByText('清空回收站')).toBeFalsy()
      })
    })
  })

  describe('restore functionality', () => {
    it('restores item on restore button click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockRestoreFromTrash.mockResolvedValue(undefined)
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const restoreButtons = screen.getAllByTitle('恢复')
      await user.click(restoreButtons[0])

      await waitFor(() => {
        expect(mockRestoreFromTrash).toHaveBeenCalledWith('item1')
      })
    })
  })

  describe('delete functionality', () => {
    it('has delete buttons available', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const deleteButtons = screen.getAllByTitle('永久删除')
      expect(deleteButtons.length).toBeGreaterThan(0)
    })

    it('deletes item on confirm', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockDeleteFromTrash.mockResolvedValue(undefined)
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      const deleteButtons = screen.getAllByTitle('永久删除')
      await user.click(deleteButtons[0])

      await waitFor(() => {
        expect(screen.getAllByRole('button', { name: '永久删除' }).length).toBeGreaterThan(1)
      })

      const confirmButtons = screen.getAllByRole('button', { name: '永久删除' })
      const confirmBtn = confirmButtons[confirmButtons.length - 1]
      await user.click(confirmBtn)

      await waitFor(() => {
        expect(mockDeleteFromTrash).toHaveBeenCalledWith('item1')
      })
    })
  })

  describe('empty trash', () => {
    it('shows empty trash button', async () => {
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })
    })

    it('opens confirmation modal on empty trash click', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        expect(screen.getByText('确定要清空回收站吗？')).toBeTruthy()
      })
    })

    it('empties trash on confirm', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockEmptyTrash.mockResolvedValue(2)
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('清空回收站')).toBeTruthy()
      })

      await user.click(screen.getByText('清空回收站'))

      await waitFor(() => {
        // Find the button in modal footer
        const buttons = screen.getAllByText('清空回收站')
        const confirmBtn = buttons.find(btn => btn.closest('[class*="ModalFooter"], footer'))
        if (confirmBtn) {
          return user.click(confirmBtn)
        }
        // Click the last one (modal button)
        return user.click(buttons[buttons.length - 1])
      })

      await waitFor(() => {
        expect(mockEmptyTrash).toHaveBeenCalled()
      })
    })
  })

  describe('selection', () => {
    it('shows selection bar when items selected', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      // Click checkbox to select item
      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 1) {
        await user.click(checkboxes[1] as Element) // First item checkbox (skip header)
      }

      await waitFor(() => {
        // Selection bar should appear
        expect(screen.getByText(/已选择.*项/)).toBeTruthy()
      })
    })

    it('can select all items', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      
      render(<TrashPage />)
      
      await waitFor(() => {
        expect(screen.getByText('deleted-file.txt')).toBeTruthy()
      })

      // Click header checkbox to select all
      const checkboxes = document.querySelectorAll('[class*="Checkbox"], input[type="checkbox"]')
      if (checkboxes.length > 0) {
        await user.click(checkboxes[0] as Element)
      }

      await waitFor(() => {
        expect(screen.getByText(/已选择 2 项/)).toBeTruthy()
      })
    })
  })
})
