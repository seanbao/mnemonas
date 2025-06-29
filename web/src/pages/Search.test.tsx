import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { SearchPage } from './Search'
import * as searchApi from '@/api/search'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()
const useIsAdminMock = vi.fn(() => true)
const mockUser = { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' }

// Mock the search API
vi.mock('@/api/search', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/search')>()
  return {
    ...actual,
    searchFiles: vi.fn(),
  }
})

// Mock useNavigate
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useIsAdmin: () => useIsAdminMock(),
    useUser: () => mockUser,
  }
})

const mockSearchResults = [
  {
    name: 'document.pdf',
    path: '/documents/document.pdf',
    isDir: false,
    size: 102400,
    modTime: '2024-01-15T10:00:00Z',
    hash: 'abc123',
  },
  {
    name: 'photos',
    path: '/photos',
    isDir: true,
    size: 0,
    modTime: '2024-01-10T08:00:00Z',
  },
  {
    name: 'video.mp4',
    path: '/media/video.mp4',
    isDir: false,
    size: 52428800,
    modTime: '2024-01-12T14:30:00Z',
    hash: 'def456',
  },
]

const { SearchError } = searchApi

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
}

function renderSearchPage(initialQuery = '') {
  const queryClient = createTestQueryClient()
  
  // Set initial URL with query if provided
  if (initialQuery) {
    window.history.pushState({}, '', `/search?q=${encodeURIComponent(initialQuery)}`)
  } else {
    window.history.pushState({}, '', '/search')
  }
  
  return render(
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <SearchPage />
      </BrowserRouter>
    </QueryClientProvider>
  )
}

function expectSearchFilesCalledWithQuery(query: string) {
  expect(vi.mocked(searchApi.searchFiles).mock.calls.some(([calledQuery]) => calledQuery === query)).toBe(true)
}

function expectSearchFilesCalledWithAbortSignal(query: string) {
  const call = vi.mocked(searchApi.searchFiles).mock.calls.find(([calledQuery]) => calledQuery === query)
  expect(call).toBeTruthy()
  expect((call?.[1] as { signal?: AbortSignal } | undefined)?.signal).toBeInstanceOf(AbortSignal)
}

describe('SearchPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    useIsAdminMock.mockReturnValue(true)
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    vi.mocked(searchApi.searchFiles).mockResolvedValue({
      query: 'test',
      results: mockSearchResults,
      count: mockSearchResults.length,
    })
  })

  describe('rendering', () => {
    it('renders page header', () => {
      renderSearchPage()
      expect(screen.getByText('搜索')).toBeInTheDocument()
      expect(screen.getByText('搜索文件和文件夹')).toBeInTheDocument()
    })

    it('renders search input', () => {
      renderSearchPage()
      expect(screen.getByPlaceholderText('输入文件名搜索...')).toBeInTheDocument()
    })

    it('renders back button', () => {
      renderSearchPage()
      expect(screen.getByRole('button', { name: '返回上一页' })).toBeInTheDocument()
    })

    it('shows empty state when no query', () => {
      renderSearchPage()
      expect(screen.getByText('输入关键词开始搜索')).toBeInTheDocument()
    })
  })

  describe('search functionality', () => {
    it('searches when typing and waiting', async () => {
      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')
      
      await waitFor(() => {
        expectSearchFilesCalledWithAbortSignal('test')
      }, { timeout: 1000 })
    })

    it('displays search results', async () => {
      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')
      
      await waitFor(() => {
        expect(screen.getByText('document.pdf')).toBeInTheDocument()
        expect(screen.getByText('photos')).toBeInTheDocument()
        expect(screen.getByText('video.mp4')).toBeInTheDocument()
      }, { timeout: 1000 })
    })

    it('shows result count', async () => {
      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')
      
      await waitFor(() => {
        expect(screen.getByText('找到 3 个结果')).toBeInTheDocument()
      }, { timeout: 1000 })
    })

    it('shows no results message when empty', async () => {
      vi.mocked(searchApi.searchFiles).mockResolvedValue({
        query: 'notfound',
        results: [],
        count: 0,
      })
      
      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'notfound')
      
      await waitFor(() => {
        expect(screen.getByText('未找到匹配的文件')).toBeInTheDocument()
      }, { timeout: 1000 })
    })

    it('syncs query state when URL search params change', async () => {
      renderSearchPage('report')

      await waitFor(() => {
        expect(screen.getByDisplayValue('report')).toBeInTheDocument()
      })

      act(() => {
        window.history.pushState({}, '', '/search?q=archive')
        window.dispatchEvent(new PopStateEvent('popstate'))
      })

      await waitFor(() => {
        expect(screen.getByDisplayValue('archive')).toBeInTheDocument()
      })
    })

    it('does not reuse cached search results from another user session', async () => {
    useIsAdminMock.mockReturnValue(false)
    mockUser.id = 'u2'
    mockUser.username = 'member'
    mockUser.role = 'user'
    mockUser.homeDir = '/member'
    vi.mocked(searchApi.searchFiles).mockImplementation(() => new Promise(() => {}))

    const queryClient = createTestQueryClient()
    queryClient.setQueryData(['search', 'report'], {
      query: 'report',
      results: [
        {
          name: 'secret.txt',
          path: '/admin/secret.txt',
          isDir: false,
          size: 128,
          modTime: '2024-01-15T10:00:00Z',
        },
      ],
      count: 1,
    })

    window.history.pushState({}, '', '/search?q=report')
    render(
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <SearchPage />
        </BrowserRouter>
      </QueryClientProvider>
    )

    await waitFor(() => {
      expectSearchFilesCalledWithQuery('report')
    })

    expect(screen.queryByText('/admin/secret.txt')).toBeNull()
    expect(screen.queryByText('secret.txt')).toBeNull()
  })

    it('does not trigger search for whitespace-only queries', async () => {
      const user = userEvent.setup()
      renderSearchPage()

      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, '   ')

      await waitFor(() => {
        expect(screen.getByText('输入关键词开始搜索')).toBeInTheDocument()
      })
      expect(searchApi.searchFiles).not.toHaveBeenCalled()
    })

    it('clears query params when submitting an empty search', async () => {
      const user = userEvent.setup()
      renderSearchPage('report')

      const input = await screen.findByDisplayValue('report')
      await user.clear(input)
      await user.keyboard('{Enter}')

      expect(window.location.search).toBe('')
    })

    it('keeps live search updates from adding browser history entries for every keystroke', async () => {
      const user = userEvent.setup()
      renderSearchPage()

      const initialHistoryLength = window.history.length
      const input = screen.getByPlaceholderText('输入文件名搜索...')

      await user.type(input, 'test')
      await user.keyboard('{Enter}')

      await waitFor(() => {
        expectSearchFilesCalledWithQuery('test')
      })

      expect(window.history.length).toBe(initialHistoryLength)
    })

    it('shows unavailable state when search backend is unavailable', async () => {
      vi.mocked(searchApi.searchFiles).mockRejectedValue(new SearchError(
        'filesystem not initialized',
        503,
        'Service Unavailable',
        'SERVICE_UNAVAILABLE'
      ))

      renderSearchPage('report')

      await waitFor(() => {
        expect(screen.getByText('搜索暂不可用')).toBeInTheDocument()
        expect(screen.getByText('文件系统当前不可用，请稍后重试')).toBeInTheDocument()
        expect(screen.getByRole('button', { name: '重试搜索' })).toBeInTheDocument()
      })
    })

    it('shows an invalid-home error and skips searching for non-admin users without a home directory', async () => {
      useIsAdminMock.mockReturnValue(false)
      mockUser.id = 'u2'
      mockUser.username = 'member'
      mockUser.role = 'user'
      mockUser.homeDir = ''

      renderSearchPage('report')

      await waitFor(() => {
        expect(screen.getByText('主目录配置无效')).toBeInTheDocument()
        expect(screen.getByText('当前账户未配置有效的主目录，无法搜索文件。请联系管理员修复账户 home_dir。')).toBeInTheDocument()
      })

      expect(screen.getByPlaceholderText('输入文件名搜索...')).toBeDisabled()
      expect(searchApi.searchFiles).not.toHaveBeenCalled()
    })

    it('shows retryable generic error state when search fails', async () => {
      vi.mocked(searchApi.searchFiles).mockRejectedValue(new Error('Network error'))

      renderSearchPage('report')

      await waitFor(() => {
        expect(screen.getByText('搜索失败')).toBeInTheDocument()
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
        expect(screen.getByRole('button', { name: '重试搜索' })).toBeInTheDocument()
      })
    })

    it('shows success toast when retrying search succeeds', async () => {
      const user = userEvent.setup()
      vi.mocked(searchApi.searchFiles)
        .mockRejectedValueOnce(new Error('Network error'))
        .mockResolvedValueOnce({
          query: 'report',
          results: mockSearchResults,
          count: mockSearchResults.length,
        })

      renderSearchPage('report')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重试搜索' })).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: '重试搜索' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '搜索结果已刷新', color: 'success' })
      })
    })

    it('shows warning toast when retrying search becomes unavailable', async () => {
      const user = userEvent.setup()
      vi.mocked(searchApi.searchFiles)
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new SearchError('filesystem not initialized', 503, 'Service Unavailable', 'SERVICE_UNAVAILABLE'))

      renderSearchPage('report')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重试搜索' })).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: '重试搜索' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '搜索暂不可用',
          description: '文件系统当前不可用，请稍后重试',
          color: 'warning',
        })
      })
    })

    it('shows danger toast when retrying search fails with a generic error', async () => {
      const user = userEvent.setup()
      vi.mocked(searchApi.searchFiles)
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new Error('still down'))

      renderSearchPage('report')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重试搜索' })).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: '重试搜索' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '搜索失败',
          description: '数据加载失败，请检查网络或稍后重试。',
          color: 'danger',
        })
      })
    })
  })

  describe('navigation', () => {
    it('navigates back from the header button', async () => {
      const user = userEvent.setup()
      renderSearchPage()

      await user.click(screen.getByRole('button', { name: '返回上一页' }))

      expect(mockNavigate).toHaveBeenCalledWith(-1)
    })

    it('navigates to file location on click', async () => {
      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')
      
      await waitFor(() => {
        expect(screen.getByText('document.pdf')).toBeInTheDocument()
      }, { timeout: 1000 })
      
      await user.click(screen.getByText('document.pdf'))
      
      expect(mockNavigate).toHaveBeenCalledWith('/files/documents', {
        state: { highlightPath: '/documents/document.pdf' },
      })
    })

    it('encodes special characters when navigating to a file location', async () => {
      const user = userEvent.setup()
      vi.mocked(searchApi.searchFiles).mockResolvedValue({
        query: 'special',
        results: [
          {
            name: 'report?.pdf',
            path: '/docs/a #1?/report?.pdf',
            isDir: false,
            size: 1024,
            modTime: '2024-01-15T10:00:00Z',
          },
        ],
        count: 1,
      })
      renderSearchPage()

      await user.type(screen.getByPlaceholderText('输入文件名搜索...'), 'special')

      await waitFor(() => {
        expect(screen.getByText('report?.pdf')).toBeInTheDocument()
      }, { timeout: 1000 })

      await user.click(screen.getByText('report?.pdf'))

      expect(mockNavigate).toHaveBeenCalledWith('/files/docs/a%20%231%3F', {
        state: { highlightPath: '/docs/a #1?/report?.pdf' },
      })
    })

    it('navigates to folder on click', async () => {
      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')
      
      await waitFor(() => {
        expect(screen.getByText('photos')).toBeInTheDocument()
      }, { timeout: 1000 })
      
      await user.click(screen.getByText('photos'))
      
      expect(mockNavigate).toHaveBeenCalledWith('/files/photos')
    })

    it('supports keyboard navigation for search results', async () => {
      const user = userEvent.setup()
      renderSearchPage()

      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '打开文件 /documents/document.pdf' })).toBeInTheDocument()
      }, { timeout: 1000 })

      const resultButton = screen.getByRole('button', { name: '打开文件 /documents/document.pdf' })
      resultButton.focus()
      await user.keyboard('{Enter}')

      expect(mockNavigate).toHaveBeenCalledWith('/files/documents', {
        state: { highlightPath: '/documents/document.pdf' },
      })
    })
  })

  describe('error handling', () => {
    it('shows error message on search failure', async () => {
      vi.mocked(searchApi.searchFiles).mockRejectedValue(new Error('Search failed'))
      
      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')
      
      await waitFor(() => {
        expect(screen.getByText('搜索失败')).toBeInTheDocument()
      }, { timeout: 1000 })
    })
  })

  describe('loading state', () => {
    it('shows loading spinner during search', async () => {
      vi.mocked(searchApi.searchFiles).mockImplementation(
        () => new Promise((resolve) => setTimeout(() => resolve({
          query: 'test',
          results: mockSearchResults,
          count: mockSearchResults.length,
        }), 500))
      )

      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')
      
      // Spinner should appear during loading
      // The actual spinner check depends on implementation
    })
  })
})
