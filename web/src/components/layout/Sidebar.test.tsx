import { describe, it, expect } from 'vitest'
import { render, screen } from '@/test/utils'
import { Sidebar } from './Sidebar'

describe('Sidebar', () => {
  describe('rendering', () => {
    it('renders logo', () => {
      render(<Sidebar />)
      expect(screen.getByText('MnemoNAS')).toBeTruthy()
      expect(screen.getByText('Memory Palace')).toBeTruthy()
    })

    it('renders navigation sections', () => {
      render(<Sidebar />)
      expect(screen.getByText('浏览')).toBeTruthy()
      expect(screen.getByText('管理')).toBeTruthy()
      expect(screen.getByText('系统')).toBeTruthy()
    })
  })

  describe('navigation items', () => {
    it('renders files link', () => {
      render(<Sidebar />)
      expect(screen.getByText('文件')).toBeTruthy()
    })

    it('renders album link', () => {
      render(<Sidebar />)
      expect(screen.getByText('相册')).toBeTruthy()
    })

    it('renders versions link with badge', () => {
      render(<Sidebar />)
      expect(screen.getByText('时光回溯')).toBeTruthy()
      expect(screen.getByText('核心')).toBeTruthy()
    })

    it('renders trash link', () => {
      render(<Sidebar />)
      expect(screen.getByText('回收站')).toBeTruthy()
    })

    it('renders storage link', () => {
      render(<Sidebar />)
      expect(screen.getByText('存储')).toBeTruthy()
    })

    it('renders maintenance link', () => {
      render(<Sidebar />)
      expect(screen.getByText('守护')).toBeTruthy()
    })

    it('renders health link', () => {
      render(<Sidebar />)
      expect(screen.getByText('健康')).toBeTruthy()
    })

    it('renders settings link', () => {
      render(<Sidebar />)
      expect(screen.getByText('设置')).toBeTruthy()
    })
  })

  describe('navigation links', () => {
    it('has correct href for files', () => {
      render(<Sidebar />)
      const link = screen.getByText('文件').closest('a')
      expect(link).toHaveAttribute('href', '/files')
    })

    it('has correct href for album', () => {
      render(<Sidebar />)
      const link = screen.getByText('相册').closest('a')
      expect(link).toHaveAttribute('href', '/album')
    })

    it('has correct href for versions', () => {
      render(<Sidebar />)
      const link = screen.getByText('时光回溯').closest('a')
      expect(link).toHaveAttribute('href', '/versions')
    })

    it('has correct href for trash', () => {
      render(<Sidebar />)
      const link = screen.getByText('回收站').closest('a')
      expect(link).toHaveAttribute('href', '/trash')
    })

    it('has correct href for storage', () => {
      render(<Sidebar />)
      const link = screen.getByText('存储').closest('a')
      expect(link).toHaveAttribute('href', '/storage')
    })

    it('has correct href for maintenance', () => {
      render(<Sidebar />)
      const link = screen.getByText('守护').closest('a')
      expect(link).toHaveAttribute('href', '/maintenance')
    })

    it('has correct href for health', () => {
      render(<Sidebar />)
      const link = screen.getByText('健康').closest('a')
      expect(link).toHaveAttribute('href', '/system-health')
    })

    it('has correct href for settings', () => {
      render(<Sidebar />)
      const link = screen.getByText('设置').closest('a')
      expect(link).toHaveAttribute('href', '/settings')
    })
  })

  describe('storage status', () => {
    it('renders storage usage section', () => {
      render(<Sidebar />)
      expect(screen.getByText('存储空间')).toBeTruthy()
    })

    it('renders progress bar', () => {
      render(<Sidebar />)
      const storageSection = screen.getByText('存储空间').closest('div')?.parentElement
      const progressBar = storageSection?.querySelector('div.bg-accent-primary')
      expect(progressBar).toBeTruthy()
    })
  })

  describe('collapsed mode', () => {
    it('hides text when collapsed', () => {
      render(<Sidebar collapsed={true} />)
      // Text like "Memory Palace" should not be visible
      expect(screen.queryByText('Memory Palace')).toBeFalsy()
    })

    it('hides section titles when collapsed', () => {
      render(<Sidebar collapsed={true} />)
      expect(screen.queryByText('浏览')).toBeFalsy()
      expect(screen.queryByText('管理')).toBeFalsy()
      expect(screen.queryByText('系统')).toBeFalsy()
    })

    it('hides storage status when collapsed', () => {
      render(<Sidebar collapsed={true} />)
      expect(screen.queryByText('存储空间')).toBeFalsy()
    })
  })
})
