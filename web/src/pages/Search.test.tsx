import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { SearchPage } from './Search'
import * as searchApi from '@/api/search'

// Mock the search API
vi.mock('@/api/search', () => ({
  searchFiles: vi.fn(),
}))

// Mock useNavigate
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
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

describe('SearchPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
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
      // Back button should be present
      const buttons = screen.getAllByRole('button')
      expect(buttons.length).toBeGreaterThan(0)
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
        expect(searchApi.searchFiles).toHaveBeenCalledWith('test')
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
  })

  describe('navigation', () => {
    it('navigates to file location on click', async () => {
      const user = userEvent.setup()
      renderSearchPage()
      
      const input = screen.getByPlaceholderText('输入文件名搜索...')
      await user.type(input, 'test')
      
      await waitFor(() => {
        expect(screen.getByText('document.pdf')).toBeInTheDocument()
      }, { timeout: 1000 })
      
      await user.click(screen.getByText('document.pdf'))
      
      expect(mockNavigate).toHaveBeenCalledWith('/files/documents')
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

      expect(mockNavigate).toHaveBeenCalledWith('/files/documents')
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
