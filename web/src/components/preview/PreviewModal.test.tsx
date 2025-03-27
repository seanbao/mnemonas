import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PreviewModal, type PreviewFile } from './PreviewModal'

const mockAddToast = vi.fn()

vi.mock('@heroui/react', () => ({
  Modal: ({ children, isOpen }: { children: React.ReactNode; isOpen: boolean }) =>
    isOpen ? <div data-testid="modal">{children}</div> : null,
  ModalContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalBody: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Button: ({ children, onPress, isDisabled, title, isIconOnly, isLoading }: { children: React.ReactNode; onPress?: () => void; isDisabled?: boolean; title?: string; isIconOnly?: boolean; isLoading?: boolean }) => (
    <button disabled={isDisabled || isLoading} onClick={onPress} title={title} aria-hidden={isIconOnly}>{children}</button>
  ),
  Spinner: () => <div>loading</div>,
  addToast: (...args: unknown[]) => mockAddToast(...args),
}))

vi.mock('@/api/files', async () => {
  const actual = await vi.importActual<typeof import('@/api/files')>('@/api/files')
  return {
    ...actual,
    downloadFile: vi.fn(),
  }
})

vi.mock('@/lib/preview-utils', async () => {
  const actual = await vi.importActual<typeof import('@/lib/preview-utils')>('@/lib/preview-utils')
  return {
    ...actual,
    buildPreviewUrl: (path: string) => `/api/v1/download${path}`,
  }
})

describe('PreviewModal', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('renders video preview without auth query', () => {
    const file: PreviewFile = { path: '/video.mp4', name: 'video.mp4' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    const video = document.querySelector('video') as HTMLVideoElement | null
    expect(video).toBeTruthy()
    expect(video?.getAttribute('src')).toBe('/api/v1/download/video.mp4')
  })

  it('renders audio preview without auth query', () => {
    const file: PreviewFile = { path: '/audio.mp3', name: 'audio.mp3' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    const audio = document.querySelector('audio') as HTMLAudioElement | null
    expect(audio).toBeTruthy()
    expect(audio?.getAttribute('src')).toBe('/api/v1/download/audio.mp3')
  })

  it('opens external link without auth query', () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    const file: PreviewFile = { path: '/video.mp4', name: 'video.mp4' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    const externalButton = screen.getByTitle('在新标签页打开')
    externalButton.click()

    expect(openSpy).toHaveBeenCalledWith(
      '/api/v1/download/video.mp4',
      '_blank',
      'noopener,noreferrer'
    )
  })

  it('shows toast when browser blocks external preview', () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    const file: PreviewFile = { path: '/video.mp4', name: 'video.mp4' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    screen.getByTitle('在新标签页打开').click()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '浏览器拦截了新标签页，请允许弹窗后重试',
      color: 'warning',
    })
  })
})
