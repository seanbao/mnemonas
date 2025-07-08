import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { authFetch, ensureDownloadSession, refreshAuthSession } from '@/api/auth'
import { downloadFile } from '@/api/files'
import { PreviewModal, type PreviewFile } from './PreviewModal'

vi.mock('@/api/auth', () => ({
  authFetch: vi.fn(),
  ensureDownloadSession: vi.fn(),
  refreshAuthSession: vi.fn(),
}))

const mockAddToast = vi.fn()

vi.mock('@heroui/react', () => ({
  Modal: ({ children, isOpen }: { children: React.ReactNode; isOpen: boolean }) =>
    isOpen ? <div role="dialog" aria-label="预览对话框">{children}</div> : null,
  ModalContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ModalBody: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Button: ({ children, onPress, isDisabled, title, isLoading, 'aria-label': ariaLabel }: { children: React.ReactNode; onPress?: () => void; isDisabled?: boolean; title?: string; isIconOnly?: boolean; isLoading?: boolean; 'aria-label'?: string }) => (
    <button disabled={isDisabled || isLoading} onClick={onPress} title={title} aria-label={ariaLabel}>{children}</button>
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

vi.mock('./ImagePreview', () => ({
  ImagePreview: ({ filename }: { filename: string }) => <div>image preview {filename}</div>,
}))

vi.mock('./PdfPreview', () => ({
  PdfPreview: ({ filename }: { filename: string }) => <div>pdf preview {filename}</div>,
}))

function createDeferred<T>() {
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((_resolve, rej) => {
    reject = rej
  })
  return { promise, reject }
}

function getVideoPreview(filename: string): HTMLVideoElement {
  return screen.getByLabelText(`视频预览 ${filename}`)
}

function getAudioPreview(filename: string): HTMLAudioElement {
  return screen.getByLabelText(`音频预览 ${filename}`)
}

describe('PreviewModal', () => {
  const mockEnsureDownloadSession = vi.mocked(ensureDownloadSession)
  const mockRefreshAuthSession = vi.mocked(refreshAuthSession)
  const mockAuthFetch = vi.mocked(authFetch)
  const mockDownloadFile = vi.mocked(downloadFile)

  beforeEach(() => {
    vi.clearAllMocks()
    vi.restoreAllMocks()
    mockEnsureDownloadSession.mockResolvedValue({ ok: true })
    mockRefreshAuthSession.mockResolvedValue(false)
    mockAuthFetch.mockRejectedValue(new Error('media error probe failed'))
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

  it('ignores toolbar file actions when no current file is available', async () => {
    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={null}
        files={[]}
      />
    )

    screen.getByRole('button', { name: '下载' }).click()
    screen.getByRole('button', { name: '在新标签页打开' }).click()

    await Promise.resolve()
    expect(mockDownloadFile).not.toHaveBeenCalled()
    expect(mockEnsureDownloadSession).not.toHaveBeenCalled()
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

    const video = getVideoPreview('video.mp4')
    expect(video).toHaveAttribute('src', '/api/v1/download/video.mp4')
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

    const audio = getAudioPreview('audio.mp3')
    expect(audio).toHaveAttribute('src', '/api/v1/download/audio.mp3')
  })

  it('renders image previews through the image preview component', () => {
    const file: PreviewFile = { path: '/photo.png', name: 'photo.png' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    expect(screen.getByText('image preview photo.png')).toBeInTheDocument()
  })

  it('renders PDF previews through the PDF preview component', () => {
    const file: PreviewFile = { path: '/manual.pdf', name: 'manual.pdf' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    expect(screen.getByText('pdf preview manual.pdf')).toBeInTheDocument()
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

    const video = getVideoPreview('video.mp4')

    fireEvent.error(video)

    await waitFor(() => {
      expect(mockRefreshAuthSession).toHaveBeenCalledTimes(1)
      expect(video).toHaveAttribute('src', '/api/v1/download/video.mp4?session_retry=1')
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

    const video = getVideoPreview('video.mp4')
    expect(screen.getByText('loading')).toBeInTheDocument()

    fireEvent.loadedData(video)

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

    const video = getVideoPreview('video.mp4')
    fireEvent.error(video)

    await waitFor(() => {
      expect(video).toHaveAttribute('src', '/api/v1/download/video.mp4?session_retry=1')
    })

    fireEvent.error(video)

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

    const audio = getAudioPreview('audio.mp3')

    fireEvent.error(audio)

    await waitFor(() => {
      expect(screen.getByText('无法加载音频')).toBeInTheDocument()
    })
  })

  it('uses a stable media preview message after session recovery fails with structured JSON', async () => {
    mockRefreshAuthSession.mockResolvedValueOnce(false)
    mockAuthFetch.mockResolvedValueOnce(new Response(JSON.stringify({
      success: false,
      error: {
        code: 'SERVICE_UNAVAILABLE',
        message: 'preview storage unavailable',
      },
    }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
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

    const video = getVideoPreview('video.mp4')
    fireEvent.error(video)

    await waitFor(() => {
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/video.mp4', {
        headers: {
          Range: 'bytes=0-0',
          'X-Mnemonas-Download-Probe': 'json-error',
        },
      })
      expect(screen.getByText('无法加载视频')).toBeInTheDocument()
    })
    expect(screen.queryByText('preview storage unavailable')).not.toBeInTheDocument()
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

    const externalButton = screen.getByRole('button', { name: '在新标签页打开' })
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

  it('shows a warning and skips opening when external preview returns a structured JSON error', async () => {
    mockAuthFetch.mockResolvedValueOnce(new Response(JSON.stringify({
      success: false,
      error: {
        code: 'SERVICE_UNAVAILABLE',
        message: 'preview storage unavailable',
      },
    }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    }))
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

    screen.getByRole('button', { name: '在新标签页打开' }).click()

    await waitFor(() => {
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/download/video.mp4', {
        headers: {
          Range: 'bytes=0-0',
          'X-Mnemonas-Download-Probe': 'json-error',
        },
      })
      expect(openSpy).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '预览暂不可用',
        description: '数据加载失败，请检查网络或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('shows a missing-file warning when external preview target no longer exists', async () => {
    mockAuthFetch.mockResolvedValueOnce(new Response(JSON.stringify({
      success: false,
      error: {
        code: 'FILE_NOT_FOUND',
        message: 'file not found',
      },
    }), {
      status: 404,
      headers: { 'Content-Type': 'application/json' },
    }))
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    const file: PreviewFile = { path: '/missing.mp4', name: 'missing.mp4' }

    render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    screen.getByRole('button', { name: '在新标签页打开' }).click()

    await waitFor(() => {
      expect(openSpy).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '预览暂不可用',
        description: '该文件可能已被移动或删除，请刷新列表后重试。',
        color: 'warning',
      })
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

    screen.getByRole('button', { name: '上一个 (←)' }).click()
    expect(onFileChange).toHaveBeenCalledWith(files[0])

    screen.getByRole('button', { name: '下一个 (→)' }).click()
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
      expect(mockDownloadFile).toHaveBeenCalledWith('/archive.bin', expect.objectContaining({
        filename: 'archive.bin',
        signal: expect.any(AbortSignal),
      }))
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

    screen.getByRole('button', { name: '在新标签页打开' }).click()

    await waitFor(() => {
      expect(openSpy).not.toHaveBeenCalled()
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '原始预览和下载会话同步失败，请稍后重试',
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

    screen.getByRole('button', { name: '在新标签页打开' }).click()

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

    screen.getByRole('button', { name: '下载' }).click()

    await waitFor(() => {
      expect(mockDownloadFile).toHaveBeenCalledWith('/video.mp4', expect.objectContaining({
        filename: 'video.mp4',
        signal: expect.any(AbortSignal),
      }))
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '下载暂不可用',
        description: '文件系统当前不可用，请检查设备状态或稍后重试。',
        color: 'warning',
      })
    })
  })

  it('aborts pending toolbar downloads when the modal closes and ignores abort feedback', async () => {
    const download = createDeferred<void>()
    let signal: AbortSignal | undefined
    mockDownloadFile.mockImplementationOnce((_path, options) => {
      signal = options?.signal
      return download.promise
    })
    const file: PreviewFile = { path: '/video.mp4', name: 'video.mp4' }

    const { rerender } = render(
      <PreviewModal
        isOpen={true}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    screen.getByRole('button', { name: '下载' }).click()

    await waitFor(() => {
      expect(signal).toBeInstanceOf(AbortSignal)
    })

    rerender(
      <PreviewModal
        isOpen={false}
        onClose={() => {}}
        file={file}
        files={[file]}
      />
    )

    expect(signal?.aborted).toBe(true)
    download.reject(new DOMException('download aborted', 'AbortError'))

    await waitFor(() => {
      expect(mockAddToast).not.toHaveBeenCalled()
    })
  })
})
