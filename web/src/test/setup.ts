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

  const omitKeys = <T extends Record<string, unknown>>(props: T, keys: string[]) => {
    const next = { ...props }
    for (const key of keys) {
      delete next[key as keyof T]
    }
    return next
  }

  const passthrough = (tag = 'div') => {
    return ({ children, ...props }: { children?: React.ReactNode }) =>
      React.createElement(
        tag,
        omitKeys(props, ['classNames', 'showDivider', 'startContent', 'endContent', 'isDisabled', 'isReadOnly', 'isIconOnly', 'removeWrapper', 'labelPlacement', 'variant', 'color', 'size', 'radius', 'placement', 'backdrop', 'hideCloseButton', 'isStriped', 'isIndeterminate']),
        children
      )
  }

  const Button = ({ children, onPress, onClick, isDisabled, isLoading, startContent, endContent, ...props }: { children?: React.ReactNode; onPress?: () => void; onClick?: () => void; isDisabled?: boolean; isLoading?: boolean; startContent?: React.ReactNode; endContent?: React.ReactNode }) =>
    React.createElement(
      'button',
      { onClick: onClick ?? onPress, disabled: isDisabled || isLoading, ...omitKeys(props, ['classNames', 'isIconOnly', 'variant', 'color', 'size', 'radius']) },
      startContent,
      children,
      endContent
    )

  const Input = ({ value, onValueChange, onChange, label, isReadOnly, isDisabled, ...props }: { value?: string; onValueChange?: (value: string) => void; onChange?: React.ChangeEventHandler<HTMLInputElement>; label?: React.ReactNode; isReadOnly?: boolean; isDisabled?: boolean }) => {
    const input = React.createElement('input', {
      value: value ?? '',
      readOnly: !!isReadOnly,
      disabled: !!isDisabled,
      onChange: (event: React.ChangeEvent<HTMLInputElement>) => {
        onValueChange?.(event.target.value)
        onChange?.(event)
      },
      ...omitKeys(props, ['classNames', 'labelPlacement', 'variant', 'size', 'startContent', 'endContent', 'removeWrapper']),
    })

    if (!label) {
      return input
    }

    return React.createElement('label', null, label, input)
  }

  const Switch = ({ isSelected, onValueChange, onChange, isDisabled, ...props }: { isSelected?: boolean; onValueChange?: (value: boolean) => void; onChange?: React.ChangeEventHandler<HTMLInputElement>; isDisabled?: boolean }) =>
    React.createElement('input', {
      type: 'checkbox',
      checked: !!isSelected,
      disabled: !!isDisabled,
      onChange: (event: React.ChangeEvent<HTMLInputElement>) => {
        onValueChange?.(event.target.checked)
        onChange?.(event)
      },
      ...omitKeys(props, ['classNames', 'color', 'size']),
    })

  const Checkbox = ({ isSelected, isIndeterminate, onValueChange, onChange, isDisabled, ...props }: { isSelected?: boolean; isIndeterminate?: boolean; onValueChange?: (value: boolean) => void; onChange?: React.ChangeEventHandler<HTMLInputElement>; isDisabled?: boolean }) =>
    React.createElement('input', {
      type: 'checkbox',
      checked: !!isSelected,
      'data-indeterminate': !!isIndeterminate,
      disabled: !!isDisabled,
      onChange: (event: React.ChangeEvent<HTMLInputElement>) => {
        onValueChange?.(event.target.checked)
        onChange?.(event)
      },
      ...omitKeys(props, ['classNames', 'color', 'size']),
    })

  const Select = ({ children, selectedKeys, onSelectionChange, placeholder, startContent, ...props }: { children?: React.ReactNode; selectedKeys?: Iterable<string>; onSelectionChange?: (keys: Set<string>) => void; placeholder?: string; startContent?: React.ReactNode }) => {
    const selected = selectedKeys ? Array.from(selectedKeys)[0] ?? '' : ''
    return React.createElement('label', null,
      startContent,
      React.createElement(
        'select',
        {
          value: selected,
          onChange: (event: React.ChangeEvent<HTMLSelectElement>) => {
            onSelectionChange?.(new Set([event.target.value]))
          },
          ...omitKeys(props, ['classNames', 'variant', 'size', 'color', 'removeWrapper']),
        },
        placeholder ? React.createElement('option', { value: '' }, placeholder) : null,
        children
      )
    )
  }

  const SelectItem = ({ children, value, ...props }: { children?: React.ReactNode; value?: string }) =>
    React.createElement('option', { value, ...omitKeys(props, ['startContent']) }, children)

  const Tabs = ({ children }: { children?: React.ReactNode }) => React.createElement('div', null, children)
  const Tab = ({ children, title }: { children?: React.ReactNode; title?: React.ReactNode }) =>
    React.createElement('div', null, title, children)
  const Progress = ({ value = 0, label, isIndeterminate, ...props }: { value?: number; label?: React.ReactNode; isIndeterminate?: boolean }) =>
    React.createElement('div', { role: 'progressbar', 'aria-valuenow': value, 'data-indeterminate': !!isIndeterminate, ...omitKeys(props, ['classNames', 'color', 'size']) }, label)
  const useDisclosure = () => {
    const [isOpen, setIsOpen] = React.useState(false)
    return {
      isOpen,
      onOpen: () => setIsOpen(true),
      onClose: () => setIsOpen(false),
      onOpenChange: () => setIsOpen((value) => !value),
    }
  }

  const Modal = ({ children, isOpen }: { children?: React.ReactNode; isOpen?: boolean }) =>
    (isOpen ? React.createElement('div', null, children) : null)

  const addToast = noop

  // Table mocks (HeroUI Table uses React Aria Collections)
  const Table = ({ children, 'aria-label': ariaLabel, ...props }: { children?: React.ReactNode; 'aria-label'?: string }) =>
    React.createElement('div', { role: 'table', 'aria-label': ariaLabel, ...omitKeys(props, ['classNames', 'isStriped', 'removeWrapper']) }, children)
  const TableHeader = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', { role: 'rowgroup' }, React.createElement('div', { role: 'row' }, children))
  const TableColumn = passthrough('div')
  const TableBody = ({ children }: { children?: React.ReactNode }) => passthrough('div')({ children })
  const TableRow = passthrough('div')
  const TableCell = passthrough('div')

  const Chip = ({ children, startContent, ...props }: { children?: React.ReactNode; startContent?: React.ReactNode }) =>
    React.createElement('span', omitKeys(props, ['classNames', 'variant', 'color', 'size']), startContent, children)

  const DropdownSection = ({ children, title, ...props }: { children?: React.ReactNode; title?: React.ReactNode }) =>
    React.createElement('div', omitKeys(props, ['showDivider']), title, children)

  const DropdownItem = ({ children, onPress, startContent, isDisabled, ...props }: { children?: React.ReactNode; onPress?: () => void; startContent?: React.ReactNode; isDisabled?: boolean }) =>
    React.createElement('button', { onClick: isDisabled ? undefined : onPress, disabled: !!isDisabled, ...omitKeys(props, ['classNames', 'color']) }, startContent, children)

  return {
    HeroUIProvider: passthrough('div'),
    Button,
    Input,
    Switch,
    Checkbox,
    Select,
    SelectItem,
    Tabs,
    Tab,
    Progress,
    useDisclosure,
    Modal,
    ModalContent: passthrough('div'),
    ModalHeader: passthrough('div'),
    ModalBody: passthrough('div'),
    ModalFooter: passthrough('div'),
    Skeleton: passthrough('div'),
    Spinner: passthrough('div'),
    Card: passthrough('div'),
    CardBody: passthrough('div'),
    CardHeader: passthrough('div'),
    Dropdown: passthrough('div'),
    DropdownTrigger: passthrough('div'),
    DropdownMenu: passthrough('div'),
    DropdownSection,
    DropdownItem,
    Chip,
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

