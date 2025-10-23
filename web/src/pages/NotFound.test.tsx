import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { NotFoundPage } from './NotFound'

// Mock navigation
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

describe('NotFoundPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('rendering', () => {
    it('displays 404 error code', () => {
      render(<NotFoundPage />)
      expect(screen.getByText('404')).toBeTruthy()
    })

    it('displays page not found message', () => {
      render(<NotFoundPage />)
      expect(screen.getByText('页面不存在')).toBeTruthy()
    })

    it('displays helpful description', () => {
      render(<NotFoundPage />)
      expect(screen.getByText(/该页面可能已被移动或删除/)).toBeTruthy()
    })

    it('renders go back button', () => {
      render(<NotFoundPage />)
      expect(screen.getByText('返回上页')).toBeTruthy()
    })

    it('renders home button', () => {
      render(<NotFoundPage />)
      expect(screen.getByText('回到首页')).toBeTruthy()
    })
  })

  describe('navigation', () => {
    it('navigates back on back button click', async () => {
      const user = userEvent.setup()
      render(<NotFoundPage />)

      await user.click(screen.getByText('返回上页'))

      expect(mockNavigate).toHaveBeenCalledWith(-1)
    })

    it('navigates to home on home button click', async () => {
      const user = userEvent.setup()
      render(<NotFoundPage />)

      await user.click(screen.getByText('回到首页'))

      expect(mockNavigate).toHaveBeenCalledWith('/')
    })
  })
})
