import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, userEvent } from '@/test/utils'
import { FavoritesPage } from './Favorites'
import * as favoritesApi from '@/api/favorites'

const mockNavigate = vi.fn()

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

vi.mock('@/api/favorites', () => ({
  listFavorites: vi.fn(),
  removeFavorite: vi.fn(),
  updateFavoriteNote: vi.fn(),
}))

vi.mock('@/lib/useBatchOperation', () => ({
  useBatchOperation: () => ({
    execute: vi.fn(),
    isLoading: false,
  }),
}))

const mockFavorites = [
  {
    path: '/docs/report.pdf',
    user_id: 'user-1',
    created_at: '2024-01-15T10:00:00Z',
    note: '月报',
  },
  {
    path: '/photos/',
    user_id: 'user-1',
    created_at: '2024-01-14T10:00:00Z',
  },
]

describe('FavoritesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockNavigate.mockClear()
    vi.mocked(favoritesApi.listFavorites).mockResolvedValue(mockFavorites)
  })

  it('renders favorites count after loading', async () => {
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('收藏夹')).toBeInTheDocument()
      expect(screen.getByText('2 项收藏')).toBeInTheDocument()
    })
  })

  it('shows empty state when there are no favorites', async () => {
    vi.mocked(favoritesApi.listFavorites).mockResolvedValue([])
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('还没有收藏')).toBeInTheDocument()
    })
  })

  it('shows retryable error state when favorites fail to load', async () => {
    vi.mocked(favoritesApi.listFavorites).mockRejectedValue(new Error('Network error'))
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByText('加载收藏列表失败')).toBeInTheDocument()
      expect(screen.getByText('Network error')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })
  })

  it('retries loading favorites from the error state', async () => {
    const user = userEvent.setup()
    vi.mocked(favoritesApi.listFavorites)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValueOnce(mockFavorites)

    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(screen.getByText('2 项收藏')).toBeInTheDocument()
      expect(screen.getByText('report.pdf')).toBeInTheDocument()
    })
  })

  it('supports keyboard navigation to a favorite item', async () => {
    const user = userEvent.setup()
    render(<FavoritesPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '打开文件 /docs/report.pdf' })).toBeInTheDocument()
    })

    const navigateButton = screen.getByRole('button', { name: '打开文件 /docs/report.pdf' })
    navigateButton.focus()
    await user.keyboard('{Enter}')

    expect(mockNavigate).toHaveBeenCalledWith('/files/docs')
  })
})