import '@testing-library/jest-dom/vitest'
import { webcrypto } from 'node:crypto'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

// Allow global in Node environment (used by Vitest)
declare const global: typeof globalThis & {
  ResizeObserver: typeof ResizeObserver
  IntersectionObserver: typeof IntersectionObserver
  URL: typeof URL
  crypto: Crypto
}

if (!globalThis.crypto || typeof globalThis.crypto.getRandomValues !== 'function') {
  globalThis.crypto = webcrypto as Crypto
  global.crypto = webcrypto as Crypto
}

// Cleanup after each test
afterEach(() => {
  cleanup()
})

// Mock HeroUI components to avoid jsdom incompatibilities.
vi.mock('@heroui/react', async () => {
  const React = await import('react')
  const noop = () => {}

  const passthrough = (tag = 'div') => {
    return ({ children, ...props }: { children?: React.ReactNode }) =>
      React.createElement(tag, props, children)
  }

  const Button = ({ children, onPress, onClick, ...props }: { children?: React.ReactNode; onPress?: () => void; onClick?: () => void }) =>
    React.createElement('button', { onClick: onClick ?? onPress, ...props }, children)

  const Input = ({ value, onValueChange, onChange, ...props }: { value?: string; onValueChange?: (value: string) => void; onChange?: React.ChangeEventHandler<HTMLInputElement> }) =>
    React.createElement('input', {
      value: value ?? '',
      onChange: (event: React.ChangeEvent<HTMLInputElement>) => {
        onValueChange?.(event.target.value)
        onChange?.(event)
      },
      ...props,
    })

  const Switch = ({ isSelected, onValueChange, onChange, ...props }: { isSelected?: boolean; onValueChange?: (value: boolean) => void; onChange?: React.ChangeEventHandler<HTMLInputElement> }) =>
    React.createElement('input', {
      type: 'checkbox',
      checked: !!isSelected,
      onChange: (event: React.ChangeEvent<HTMLInputElement>) => {
        onValueChange?.(event.target.checked)
        onChange?.(event)
      },
      ...props,
    })

  const Select = ({ children, selectedKeys, onSelectionChange, ...props }: { children?: React.ReactNode; selectedKeys?: Iterable<string>; onSelectionChange?: (keys: Set<string>) => void }) => {
    const selected = selectedKeys ? Array.from(selectedKeys)[0] ?? '' : ''
    return React.createElement(
      'select',
      {
        value: selected,
        onChange: (event: React.ChangeEvent<HTMLSelectElement>) => {
          onSelectionChange?.(new Set([event.target.value]))
        },
        ...props,
      },
      children
    )
  }

  const SelectItem = ({ children, value, ...props }: { children?: React.ReactNode; value?: string }) =>
    React.createElement('option', { value, ...props }, children)

  const Tabs = ({ children }: { children?: React.ReactNode }) => React.createElement('div', null, children)
  const Tab = ({ children }: { children?: React.ReactNode }) => React.createElement('div', null, children)

  const Modal = ({ children, isOpen }: { children?: React.ReactNode; isOpen?: boolean }) =>
    (isOpen ? React.createElement('div', null, children) : null)

  const addToast = noop

  // Table mocks (HeroUI Table uses React Aria Collections)
  const Table = ({ children, 'aria-label': ariaLabel, ...props }: { children?: React.ReactNode; 'aria-label'?: string }) =>
    React.createElement('div', { role: 'table', 'aria-label': ariaLabel, ...props }, children)
  const TableHeader = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', { role: 'rowgroup' }, React.createElement('div', { role: 'row' }, children))
  const TableColumn = passthrough('div')
  const TableBody = ({ children }: { children?: React.ReactNode }) => passthrough('div')({ children })
  const TableRow = passthrough('div')
  const TableCell = passthrough('div')

  return {
    Button,
    Input,
    Switch,
    Select,
    SelectItem,
    Tabs,
    Tab,
    Modal,
    ModalContent: passthrough('div'),
    ModalHeader: passthrough('div'),
    ModalBody: passthrough('div'),
    ModalFooter: passthrough('div'),
    Card: passthrough('div'),
    CardBody: passthrough('div'),
    CardHeader: passthrough('div'),
    Dropdown: passthrough('div'),
    DropdownTrigger: passthrough('div'),
    DropdownMenu: passthrough('div'),
    DropdownItem: passthrough('div'),
    Chip: passthrough('span'),
    Divider: passthrough('div'),
    Snippet: passthrough('pre'),
    addToast,
    Table,
    TableHeader,
    TableColumn,
    TableBody,
    TableRow,
    TableCell,
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

global.fetch = vi.fn().mockResolvedValue({
  ok: true,
  status: 200,
  statusText: 'OK',
  headers: new Headers(),
  json: async () => ({}),
  text: async () => '',
  blob: async () => new Blob(),
})

