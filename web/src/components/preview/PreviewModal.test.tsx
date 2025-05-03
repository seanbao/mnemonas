import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { ensureDownloadSession, refreshAuthSession } from '@/api/auth'
import { downloadFile } from '@/api/files'
import { PreviewModal, type PreviewFile } from './PreviewModal'

vi.mock('@/api/auth', () => ({
  ensureDownloadSession: vi.fn(),
  refreshAuthSession: vi.fn(),
}))

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
  const mockEnsureDownloadSession = vi.mocked(ensureDownloadSession)
  const mockRefreshAuthSession = vi.mocked(refreshAuthSession)
  const mockDownloadFile = vi.mocked(downloadFile)

  beforeEach(() => {
    vi.clearAllMocks()
    vi.restoreAllMocks()
    mockEnsureDownloadSession.mockResolvedValue({ ok: true })
    mockRefreshAuthSession.mockResolvedValue(false)
    mockDownloadFile.mockResolvedValue(undefined)
  })

  it('renders a loading placeholder when opened without a current file', () => {
    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={null}
        files={[]}
      />
    )

    expect(screen.getByText('loading')).toBeInTheDocument()
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

  it('retries video preview once after refreshing the auth session', async () => {
    mockRefreshAuthSession.mockResolvedValueOnce(true)
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

    fireEvent.error(video!)

    await waitFor(() => {
      expect(mockRefreshAuthSession).toHaveBeenCalledTimes(1)
      expect(video?.getAttribute('src')).toBe('/api/v1/download/video.mp4?session_retry=1')
    })
  })

  it('clears the media loading state after video metadata loads', () => {
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
    expect(screen.getByText('loading')).toBeInTheDocument()

    fireEvent.loadedData(video!)

    expect(screen.queryByText('loading')).not.toBeInTheDocument()
  })

  it('shows an error when retried video preview still fails', async () => {
    mockRefreshAuthSession.mockResolvedValueOnce(true)
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
    fireEvent.error(video!)

    await waitFor(() => {
      expect(video?.getAttribute('src')).toBe('/api/v1/download/video.mp4?session_retry=1')
    })

    fireEvent.error(video!)

    await waitFor(() => {
      expect(screen.getByText('无法加载视频')).toBeInTheDocument()
    })
  })

  it('shows an error when audio preview still cannot recover after session refresh', async () => {
    mockRefreshAuthSession.mockResolvedValueOnce(false)
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

    fireEvent.error(audio!)

    await waitFor(() => {
      expect(screen.getByText('无法加载音频')).toBeInTheDocument()
    })
  })

  it('opens external link without auth query', async () => {
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

    await waitFor(() => {
      expect(mockEnsureDownloadSession).toHaveBeenCalledTimes(1)
      expect(openSpy).toHaveBeenCalledWith(
        '/api/v1/download/video.mp4',
        '_blank',
        'noopener,noreferrer'
      )
    })
  })

  it('supports button and keyboard navigation between preview files', () => {
    const onFileChange = vi.fn()
    const onClose = vi.fn()
    const files: PreviewFile[] = [
      { path: '/first.txt', name: 'first.txt' },
      { path: '/second.txt', name: 'second.txt' },
      { path: '/third.txt', name: 'third.txt' },
    ]

    render(
      <PreviewModal
        isOpen={true}
        onClose={onClose}
        file={files[1]}
        files={files}
        onFileChange={onFileChange}
      />
    )

    screen.getByTitle('上一个 (←)').click()
    expect(onFileChange).toHaveBeenCalledWith(files[0])

    screen.getByTitle('下一个 (→)').click()
    expect(onFileChange).toHaveBeenCalledWith(files[2])

    fireEvent.keyDown(window, { key: 'ArrowLeft' })
    expect(onFileChange).toHaveBeenCalledWith(files[0])

    fireEvent.keyDown(window, { key: 'ArrowRight' })
    expect(onFileChange).toHaveBeenCalledWith(files[2])

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('renders unsupported file actions', async () => {
    vi.spyOn(window, 'open').mockReturnValue({ opener: null } as Window)
    const file: PreviewFile = { path: '/archive.bin', name: 'archive.bin' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    expect(screen.getByText('此文件类型暂不支持预览')).toBeInTheDocument()

    screen.getByText('下载文件').click()
    await waitFor(() => {
      expect(mockDownloadFile).toHaveBeenCalledWith('/archive.bin', { filename: 'archive.bin' })
    })

    screen.getByText('在新标签页打开').click()
    await waitFor(() => {
      expect(mockEnsureDownloadSession).toHaveBeenCalled()
      expect(window.open).toHaveBeenCalledWith('/api/v1/download/archive.bin', '_blank', 'noopener,noreferrer')
    })
  })

  it('shows warning when download session cannot be prepared for external preview', async () => {
    mockEnsureDownloadSession.mockResolvedValueOnce({ ok: false, message: 'download session unavailable' })
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

    screen.getByTitle('在新标签页打开').click()

    await waitFor(() => {
      expect(openSpy).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: 'download session unavailable',
        color: 'warning',
      })
    })
  })

  it('shows toast when browser blocks external preview', async () => {
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

    await waitFor(() => {
      expect(mockEnsureDownloadSession).toHaveBeenCalledTimes(1)
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '浏览器拦截了新标签页，请允许弹窗后重试',
        color: 'warning',
      })
    })
  })

  it('shows unavailable toast when preview download fails because the filesystem is unavailable', async () => {
    mockDownloadFile.mockRejectedValue(Object.assign(new Error('filesystem unavailable'), {
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
    }))
    const file: PreviewFile = { path: '/video.mp4', name: 'video.mp4' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    screen.getByTitle('下载').click()

    await waitFor(() => {
      expect(mockDownloadFile).toHaveBeenCalledWith('/video.mp4', { filename: 'video.mp4' })
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '下载暂不可用',
        description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
        color: 'warning',
      })
    })
  })
})
