import { useState, useEffect, useCallback, useMemo } from 'react'
import { Modal, ModalContent, ModalBody, Button, Spinner, addToast } from '@heroui/react'
import { X, ChevronLeft, ChevronRight, Download, ExternalLink, AlertCircle, Music, FileQuestion } from 'lucide-react'
import { downloadFile } from '@/api/files'
import { ensureDownloadSession, refreshAuthSession } from '@/api/auth'
import { getPreviewType, buildPreviewUrl } from '@/lib/preview-utils'
import { getFileDownloadErrorToast } from '@/lib/fileActionErrors'
import { TextPreview } from './TextPreview'
import { ImagePreview } from './ImagePreview'
import { PdfPreview } from './PdfPreview'
import { cn, openUrlInNewTab } from '@/lib/utils'

export interface PreviewFile {
  path: string
  name: string
}

export interface PreviewModalProps {
  isOpen: boolean
  onClose: () => void
  file: PreviewFile | null
  files?: PreviewFile[]  // All files in current directory for navigation
  onFileChange?: (file: PreviewFile) => void
}

function buildStreamUrl(path: string): string {
  return buildPreviewUrl(path)
}

function withSessionRetryParam(url: string): string {
  const separator = url.includes('?') ? '&' : '?'
  return `${url}${separator}session_retry=1`
}

function useRetryableMediaUrl(baseUrl: string, errorMessage: string) {
  const [mediaUrl, setMediaUrl] = useState(baseUrl)
  const [error, setError] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [hasRetried, setHasRetried] = useState(false)

  const handleLoaded = useCallback(() => {
    setIsLoading(false)
    setError(null)
  }, [])

  const handleError = useCallback(async () => {
    if (!hasRetried) {
      const refreshed = await refreshAuthSession()
      if (refreshed) {
        setHasRetried(true)
        setError(null)
        setIsLoading(true)
        setMediaUrl(withSessionRetryParam(baseUrl))
        return
      }
    }

    setIsLoading(false)
    setError(errorMessage)
  }, [baseUrl, errorMessage, hasRetried])

  return {
    mediaUrl,
    error,
    isLoading,
    handleLoaded,
    handleError,
  }
}

export function PreviewModal({ 
  isOpen, 
  onClose, 
  file, 
  files = [],
  onFileChange,
}: PreviewModalProps) {
  const currentIndex = useMemo(() => {
    if (!file || files.length === 0) {
      return -1
    }
    return files.findIndex(f => f.path === file.path)
  }, [file, files])

  // Get current file (either from navigation or prop)
  const currentFile = useMemo(() => {
    if (currentIndex >= 0 && currentIndex < files.length) {
      return files[currentIndex]
    }
    return file
  }, [files, currentIndex, file])

  // Get preview type for current file
  const previewType = useMemo(() => {
    return currentFile ? getPreviewType(currentFile.name) : 'unsupported'
  }, [currentFile])

  // Navigation handlers
  const canNavigatePrev = files.length > 0 && currentIndex > 0
  const canNavigateNext = files.length > 0 && currentIndex < files.length - 1

  const handlePrev = useCallback(() => {
    if (canNavigatePrev) {
      onFileChange?.(files[currentIndex - 1])
    }
  }, [canNavigatePrev, currentIndex, files, onFileChange])

  const handleNext = useCallback(() => {
    if (canNavigateNext) {
      onFileChange?.(files[currentIndex + 1])
    }
  }, [canNavigateNext, currentIndex, files, onFileChange])

  // Keyboard navigation
  useEffect(() => {
    if (!isOpen) return

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
        e.preventDefault()
        handlePrev()
      } else if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
        e.preventDefault()
        handleNext()
      } else if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, handlePrev, handleNext, onClose])

  const handleDownload = useCallback(() => {
    if (!currentFile) return
    void downloadFile(currentFile.path, { filename: currentFile.name }).catch((error: unknown) => {
      addToast(getFileDownloadErrorToast(error))
    })
  }, [currentFile])

  const handleOpenExternal = useCallback(async () => {
    if (!currentFile) return

    const session = await ensureDownloadSession()
    if (!session.ok) {
      addToast({ title: session.message ?? '原始预览和下载会话同步失败，请稍后重试', color: 'warning' })
      return
    }

    const url = buildStreamUrl(currentFile.path)
    if (!openUrlInNewTab(url)) {
      addToast({ title: '浏览器拦截了新标签页，请允许弹窗后重试', color: 'warning' })
    }
  }, [currentFile])

  // Render preview content based on type
  const renderPreview = () => {
    if (!currentFile) {
      return (
        <div className="flex items-center justify-center h-full">
          <Spinner size="lg" />
        </div>
      )
    }

    switch (previewType) {
      case 'text':
      case 'markdown':
        return (
          <TextPreview 
            path={currentFile.path} 
            filename={currentFile.name} 
            className="h-full"
          />
        )
      
      case 'image':
        return (
          <ImagePreview 
            path={currentFile.path} 
            filename={currentFile.name}
            className="h-full"
          />
        )
      
      case 'pdf':
        return (
          <PdfPreview 
            path={currentFile.path} 
            filename={currentFile.name}
            className="h-full"
          />
        )
      
      case 'video':
        return (
          <VideoPreview 
            key={currentFile.path}
            path={currentFile.path} 
            filename={currentFile.name}
          />
        )
      
      case 'audio':
        return (
          <AudioPreview 
            key={currentFile.path}
            path={currentFile.path} 
            filename={currentFile.name}
          />
        )
      
      default:
        return (
          <UnsupportedPreview 
            filename={currentFile.name}
            onDownload={handleDownload}
            onOpenExternal={handleOpenExternal}
          />
        )
    }
  }

  return (
    <Modal 
      isOpen={isOpen} 
      onClose={onClose}
      size="full"
      backdrop="blur"
      classNames={{
        wrapper: "z-[60]",
        base: "bg-content1/95 backdrop-blur-lg absolute inset-4 m-0 max-w-none max-h-none rounded-lg",
        body: "p-0",
      }}
      hideCloseButton
    >
      <ModalContent>
        <ModalBody className="relative h-full overflow-hidden">
          {/* Top toolbar */}
          <div className="absolute left-0 right-0 top-0 z-20 flex items-center justify-between gap-3 bg-gradient-to-b from-black/60 to-transparent px-4 py-3">
            <div className="flex min-w-0 items-center gap-3 text-white">
              <span className="max-w-[calc(100vw-12rem)] truncate font-medium sm:max-w-[400px]">
                {currentFile?.name}
              </span>
              {files.length > 1 && (
                <span className="text-sm opacity-70">
                  {currentIndex + 1} / {files.length}
                </span>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-1">
              <Button
                isIconOnly
                size="sm"
                variant="flat"
                className="text-white bg-white/10 hover:bg-white/20"
                onPress={handleDownload}
                title="下载"
              >
                <Download size={18} />
              </Button>
              <Button
                isIconOnly
                size="sm"
                variant="flat"
                className="text-white bg-white/10 hover:bg-white/20"
                onPress={handleOpenExternal}
                title="在新标签页打开"
              >
                <ExternalLink size={18} />
              </Button>
              <Button
                isIconOnly
                size="sm"
                variant="flat"
                className="text-white bg-white/10 hover:bg-white/20"
                onPress={onClose}
                title="关闭 (Esc)"
              >
                <X size={18} />
              </Button>
            </div>
          </div>

          {/* Navigation buttons */}
          {files.length > 1 && (
            <>
              <Button
                isIconOnly
                size="lg"
                variant="flat"
                className={cn(
                  "absolute left-4 top-1/2 -translate-y-1/2 z-20 text-white bg-black/30 hover:bg-black/50",
                  !canNavigatePrev && "opacity-30 cursor-not-allowed"
                )}
                onPress={handlePrev}
                isDisabled={!canNavigatePrev}
                title="上一个 (←)"
              >
                <ChevronLeft size={24} />
              </Button>
              <Button
                isIconOnly
                size="lg"
                variant="flat"
                className={cn(
                  "absolute right-4 top-1/2 -translate-y-1/2 z-20 text-white bg-black/30 hover:bg-black/50",
                  !canNavigateNext && "opacity-30 cursor-not-allowed"
                )}
                onPress={handleNext}
                isDisabled={!canNavigateNext}
                title="下一个 (→)"
              >
                <ChevronRight size={24} />
              </Button>
            </>
          )}

          {/* Preview content */}
          <div className="h-full pt-14">
            {renderPreview()}
          </div>
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

// Simple video preview using native video element
function VideoPreview({ path }: { path: string; filename: string }) {
  const url = buildStreamUrl(path)
  const {
    mediaUrl,
    error,
    isLoading,
    handleLoaded,
    handleError,
  } = useRetryableMediaUrl(url, '无法加载视频')
  
  return (
    <div className="h-full flex flex-col bg-black rounded-lg overflow-hidden">
      <div className="flex-1 flex items-center justify-center">
        {isLoading && <Spinner size="lg" />}
        {error && (
          <div className="text-center text-danger">
            <AlertCircle size={48} className="mx-auto mb-4" />
            <p>{error}</p>
          </div>
        )}
        {!error && (
          <video
            src={mediaUrl}
            controls
            preload="metadata"
            autoPlay
            className="max-w-full max-h-full"
            onLoadedData={handleLoaded}
            onError={() => {
              void handleError()
            }}
          >
            <track kind="captions" srcLang="zh" label="中文" />
            浏览器不支持视频播放
          </video>
        )}
      </div>
    </div>
  )
}

// Simple audio preview
function AudioPreview({ path, filename }: { path: string; filename: string }) {
  const url = buildStreamUrl(path)
  const {
    mediaUrl,
    error,
    isLoading,
    handleLoaded,
    handleError,
  } = useRetryableMediaUrl(url, '无法加载音频')
  
  return (
    <div className="h-full flex flex-col items-center justify-center bg-content1 rounded-lg">
      <div className="text-center mb-8">
        <div className="w-32 h-32 mx-auto mb-4 rounded-full bg-gradient-to-br from-primary to-secondary flex items-center justify-center">
          <Music size={56} className="text-white" aria-hidden />
        </div>
        <p className="text-lg font-medium">{filename}</p>
      </div>
      {isLoading && <Spinner size="lg" />}
      {error && (
        <div className="text-center text-danger">
          <AlertCircle size={32} className="mx-auto mb-2" />
          <p>{error}</p>
        </div>
      )}
      {!error && (
        <audio
          src={mediaUrl}
          controls
          preload="metadata"
          autoPlay
          className="w-[400px] max-w-full"
          onLoadedData={handleLoaded}
          onError={() => {
            void handleError()
          }}
        >
          浏览器不支持音频播放
        </audio>
      )}
    </div>
  )
}

// Unsupported file type
function UnsupportedPreview({ 
  filename, 
  onDownload,
  onOpenExternal,
}: { 
  filename: string
  onDownload: () => void
  onOpenExternal: () => void 
}) {
  return (
    <div className="h-full flex flex-col items-center justify-center bg-content1 rounded-lg">
      <div className="text-center max-w-md">
        <div className="w-20 h-20 mx-auto mb-6 rounded-lg bg-default-100 flex items-center justify-center">
          <FileQuestion size={40} className="text-default-500" aria-hidden />
        </div>
        <h3 className="text-xl font-semibold mb-2">{filename}</h3>
        <p className="text-default-500 mb-6">
          此文件类型暂不支持预览
        </p>
        <div className="flex gap-3 justify-center">
          <Button
            color="primary"
            onPress={onDownload}
            startContent={<Download size={18} />}
          >
            下载文件
          </Button>
          <Button
            variant="flat"
            onPress={onOpenExternal}
            startContent={<ExternalLink size={18} />}
          >
            在新标签页打开
          </Button>
        </div>
      </div>
    </div>
  )
}

export default PreviewModal
