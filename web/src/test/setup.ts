import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

// Allow global in Node environment (used by Vitest)
declare const global: typeof globalThis & {
  ResizeObserver: typeof ResizeObserver
  IntersectionObserver: typeof IntersectionObserver
  URL: typeof URL
}

// Cleanup after each test
afterEach(() => {
  cleanup()
})

// Global HeroUI Table mock to avoid jsdom compatibility issues
// HeroUI uses React Aria Collections which throws errors in jsdom
vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual('@heroui/react')
  const React = await import('react')
  
  // Mock Table components that have jsdom issues
  const MockTable = ({
    children,
    'aria-label': ariaLabel,
    // Strip HeroUI-only props to avoid React warnings
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    removeWrapper: _removeWrapper,
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    classNames: _classNames,
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    isStriped: _isStriped,
    ...props
  }: { 
    children: React.ReactNode
    'aria-label'?: string
    removeWrapper?: boolean
    classNames?: Record<string, string>
    isStriped?: boolean
  }) => 
    React.createElement('div', { 'data-testid': 'heroui-table', role: 'table', 'aria-label': ariaLabel, ...props }, children)
  
  const MockTableHeader = ({ children }: { children: React.ReactNode }) => 
    React.createElement('div', { 'data-testid': 'table-header', role: 'rowgroup' },
      React.createElement('div', { role: 'row' }, children))
  
  const MockTableColumn = ({ children }: { children: React.ReactNode }) => 
    React.createElement('div', { role: 'columnheader' }, children)
  
  const MockTableBody = ({ children, items, emptyContent }: { 
    children: React.ReactNode | ((item: unknown) => React.ReactNode)
    items?: unknown[]
    emptyContent?: React.ReactNode 
  }) => {
    if (typeof children === 'function' && items) {
      if (items.length === 0) {
        return React.createElement('div', { 'data-testid': 'table-body', role: 'rowgroup' }, emptyContent)
      }
      return React.createElement('div', { 'data-testid': 'table-body', role: 'rowgroup' },
        items.map((item, index) => React.createElement(React.Fragment, { key: index }, children(item))))
    }
    const childrenNode = typeof children === 'function' ? null : children
    return React.createElement('div', { 'data-testid': 'table-body', role: 'rowgroup' }, childrenNode)
  }
  
  const MockTableRow = ({ children, className }: { children: React.ReactNode; className?: string }) => 
    React.createElement('div', { role: 'row', className }, children)
  
  const MockTableCell = ({ children }: { children: React.ReactNode }) => 
    React.createElement('div', { role: 'cell' }, children)

  return {
    ...actual,
    Table: MockTable,
    TableHeader: MockTableHeader,
    TableColumn: MockTableColumn,
    TableBody: MockTableBody,
    TableRow: MockTableRow,
    TableCell: MockTableCell,
  }
})

// Mock window.matchMedia
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
})

// Mock ResizeObserver as a proper class
class ResizeObserverMock {
  observe = vi.fn()
  unobserve = vi.fn()
  disconnect = vi.fn()
}
global.ResizeObserver = ResizeObserverMock

// Mock IntersectionObserver as a proper class
class IntersectionObserverMock {
  observe = vi.fn()
  unobserve = vi.fn()
  disconnect = vi.fn()
  root = null
  rootMargin = ''
  thresholds = []
  takeRecords = vi.fn().mockReturnValue([])
}
global.IntersectionObserver = IntersectionObserverMock as unknown as typeof IntersectionObserver

// Mock scrollTo
Element.prototype.scrollTo = vi.fn()
window.scrollTo = vi.fn()

// Mock scrollIntoView
Element.prototype.scrollIntoView = vi.fn()

// Mock getBoundingClientRect
Element.prototype.getBoundingClientRect = vi.fn().mockReturnValue({
  width: 100,
  height: 100,
  top: 0,
  left: 0,
  bottom: 100,
  right: 100,
  x: 0,
  y: 0,
  toJSON: () => {},
})

// Mock URL.createObjectURL and revokeObjectURL
global.URL.createObjectURL = vi.fn(() => 'mock-url')
global.URL.revokeObjectURL = vi.fn()

