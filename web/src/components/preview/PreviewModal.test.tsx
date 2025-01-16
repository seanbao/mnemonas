import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PreviewModal, type PreviewFile } from './PreviewModal'

vi.mock('@heroui/react', () => ({
  Modal: ({ children, isOpen }: { children: React.ReactNode; isOpen: boolean }) =>
    isOpen ? <div data-testid="modal">{children}</div> : null,
  ModalContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalBody: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Button: ({ children, onPress, isDisabled, title, isIconOnly, isLoading }: { children: React.ReactNode; onPress?: () => void; isDisabled?: boolean; title?: string; isIconOnly?: boolean; isLoading?: boolean }) => (
    <button disabled={isDisabled || isLoading} onClick={onPress} title={title} aria-hidden={isIconOnly}>{children}</button>
  ),
  Spinner: () => <div>loading</div>,
}))

let tokenValue: string | null = 'token-123'

vi.mock('@/api/auth', () => ({
  getStoredToken: () => tokenValue,
}))

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
    tokenValue = 'token-123'
  })

  it('renders video preview with auth query', () => {
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
    expect(video?.getAttribute('src')).toContain('auth=token-123')
  })

  it('renders audio preview with auth query', () => {
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
    expect(audio?.getAttribute('src')).toContain('auth=token-123')
  })

  it('opens external link with auth query', () => {
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
      expect.stringContaining('auth=token-123'),
      '_blank',
      'noopener,noreferrer'
    )
  })

  it('renders video preview without auth when no token', () => {
    tokenValue = null
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
    expect(video?.getAttribute('src')).not.toContain('auth=')
  })
})
