import { useState, useCallback, useRef, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { 
  Button,
  Modal,
  ModalContent,
  ModalBody
} from '@heroui/react'
import { 
  Image as ImageIcon, 
  X, 
  ChevronLeft, 
  ChevronRight,
  Download,
  ZoomIn,
  ZoomOut,
  RotateCw,
  Info,
  AlertCircle,
} from 'lucide-react'
import { refreshAuthSession } from '@/api/auth'
import { listFiles, getDownloadUrl, getThumbnailUrl, downloadFile, type FileItem } from '@/api/files'
import { formatBytes, formatDate, isImageFile, cn } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'

// Constants for recursive image fetching
const MAX_DEPTH = 5 // Maximum directory depth to traverse
const MAX_IMAGES = 1000 // Maximum images to collect
const CONCURRENCY_LIMIT = 3 // Maximum concurrent directory requests

function withSessionRetryParam(url: string): string {
  const separator = url.includes('?') ? '&' : '?'
  return `${url}${separator}session_retry=1`
}

interface AlbumFetchErrorState {
  hadPartialError: boolean
}

interface AlbumQueryResult {
  images: FileItem[]
  hadPartialError: boolean
}

// Recursively fetch all images with safety limits
async function fetchAllImages(
  path: string = '/',
  depth: number = 0,
  signal?: AbortSignal,
  collectedCount: { count: number } = { count: 0 },
  errorState: AlbumFetchErrorState = { hadPartialError: false }
): Promise<FileItem[]> {
  // Check abort signal
  if (signal?.aborted) {
    return []
  }
  
  // Safety limits
  if (depth > MAX_DEPTH || collectedCount.count >= MAX_IMAGES) {
    return []
  }
  
  const response = await listFiles(path)
  const images: FileItem[] = []
  const directories: FileItem[] = []
  
  // Separate files and directories
  for (const file of response.files) {
    if (collectedCount.count >= MAX_IMAGES) break
    
    if (file.isDir) {
      directories.push(file)
    } else if (isImageFile(file.name)) {
      images.push(file)
      collectedCount.count++
    }
  }
  
  // Process directories with concurrency limit
  for (let i = 0; i < directories.length; i += CONCURRENCY_LIMIT) {
    if (signal?.aborted || collectedCount.count >= MAX_IMAGES) break
    
    const batch = directories.slice(i, i + CONCURRENCY_LIMIT)
    const results = await Promise.all(
      batch.map(dir => 
        fetchAllImages(dir.path, depth + 1, signal, collectedCount)
          .catch(() => {
            errorState.hadPartialError = true
            return [] as FileItem[]
          })
      )
    )
    
    for (const subImages of results) {
      images.push(...subImages)
    }
  }
  
  return images
}

// Image thumbnail component with lazy loading
function ImageThumbnail({ 
  file, 
  onClick,
  index
}: { 
  file: FileItem
  onClick: () => void
  index: number
}) {
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState(false)
  const [thumbnailUrl, setThumbnailUrl] = useState<string | null>(null)
  const [hasRetried, setHasRetried] = useState(false)
  const imgRef = useRef<HTMLImageElement>(null)

  useEffect(() => {
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          // Use thumbnail API instead of original image
          setThumbnailUrl(getThumbnailUrl(file.path, 'medium'))
          observer.disconnect()
        }
      },
      { rootMargin: '100px' }
    )

    if (imgRef.current) {
      observer.observe(imgRef.current)
    }

    return () => observer.disconnect()
  }, [file.path])

  // Vary height for masonry effect
  const heights = ['h-48', 'h-56', 'h-64', 'h-72']
  const heightClass = heights[index % heights.length]

  return (
    <div 
      className={cn(
        "relative rounded-lg overflow-hidden cursor-pointer group",
        "bg-content2 border border-divider hover:border-accent-primary/30",
        "transition-all",
        heightClass
      )}
      onClick={onClick}
    >
      {!loaded && !error && (
        <div className="absolute inset-0 skeleton-shimmer" />
      )}
      
      <img
        ref={imgRef}
        alt={file.name}
        src={thumbnailUrl ?? undefined}
        className={cn(
          "w-full h-full object-cover transition-transform duration-300",
          "group-hover:scale-105",
          loaded ? "opacity-100" : "opacity-0"
        )}
        onLoad={() => setLoaded(true)}
        onError={() => {
          if (!hasRetried && thumbnailUrl) {
            void (async () => {
              const refreshed = await refreshAuthSession()
              if (refreshed) {
                setHasRetried(true)
                setError(false)
                setLoaded(false)
                setThumbnailUrl(withSessionRetryParam(getThumbnailUrl(file.path, 'medium')))
                return
              }
              setError(true)
            })()
            return
          }
          setError(true)
        }}
      />
      
      {error && (
        <div className="absolute inset-0 flex items-center justify-center text-default-500">
          <ImageIcon size={32} />
        </div>
      )}
      
      {/* Overlay on hover */}
      <div className="absolute inset-0 bg-gradient-to-t from-black/70 via-transparent to-transparent opacity-0 group-hover:opacity-100 transition-opacity flex flex-col justify-end p-3">
        <div className="text-white text-sm font-medium truncate">
          {file.name}
        </div>
        <div className="text-white/60 text-xs">
          {formatBytes(file.size)}
        </div>
      </div>
    </div>
  )
}

// Image preview modal with loading state and preloading
function ImagePreview({ 
  images, 
  currentIndex, 
  isOpen, 
  onClose,
  onNavigate
}: {
  images: FileItem[]
  currentIndex: number
  isOpen: boolean
  onClose: () => void
  onNavigate: (index: number) => void
}) {
  const currentImage = images[currentIndex]

  const [zoom, setZoom] = useState(1)
  const [rotation, setRotation] = useState(0)
  const [showInfo, setShowInfo] = useState(false)
  const [loading, setLoading] = useState(true)
  const [imageUrl, setImageUrl] = useState(() => currentImage?.path ? getDownloadUrl(currentImage.path) : '')
  const [hasRetried, setHasRetried] = useState(false)
  const [touchStart, setTouchStart] = useState<{ x: number; y: number } | null>(null)
  
  const handlePrev = useCallback(() => {
    if (images.length === 0) return
    onNavigate((currentIndex - 1 + images.length) % images.length)
  }, [currentIndex, images.length, onNavigate])
  
  const handleNext = useCallback(() => {
    if (images.length === 0) return
    onNavigate((currentIndex + 1) % images.length)
  }, [currentIndex, images.length, onNavigate])

  // Preload adjacent images
  useEffect(() => {
    if (!isOpen || images.length === 0) return

    const preloadIndexes = [
      (currentIndex - 1 + images.length) % images.length,
      (currentIndex + 1) % images.length,
    ]

    preloadIndexes.forEach(idx => {
      const target = images[idx]
      if (idx !== currentIndex && target?.path) {
        const img = new Image()
        img.src = getDownloadUrl(target.path)
      }
    })
  }, [currentIndex, images, isOpen])

  // Keyboard navigation
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (!isOpen) return
      
      switch (e.key) {
        case 'ArrowLeft':
          handlePrev()
          break
        case 'ArrowRight':
          handleNext()
          break
        case 'Escape':
          onClose()
          break
        case '+':
        case '=':
          setZoom(z => Math.min(3, z + 0.25))
          break
        case '-':
          setZoom(z => Math.max(0.5, z - 0.25))
          break
        case 'r':
          setRotation(r => (r + 90) % 360)
          break
        case '0':
          setZoom(1)
          setRotation(0)
          break
      }
    }
    
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, handlePrev, handleNext, onClose])

  // Touch swipe navigation
  const handleTouchStart = (e: React.TouchEvent) => {
    setTouchStart({
      x: e.touches[0].clientX,
      y: e.touches[0].clientY,
    })
  }

  const handleTouchEnd = (e: React.TouchEvent) => {
    if (!touchStart) return

    const touchEnd = {
      x: e.changedTouches[0].clientX,
      y: e.changedTouches[0].clientY,
    }

    const deltaX = touchEnd.x - touchStart.x
    const deltaY = touchEnd.y - touchStart.y

    // Only trigger if horizontal swipe is more significant than vertical
    if (Math.abs(deltaX) > Math.abs(deltaY) && Math.abs(deltaX) > 50) {
      if (deltaX > 0) {
        handlePrev()
      } else {
        handleNext()
      }
    }

    setTouchStart(null)
  }

  if (!currentImage || !currentImage.path) return null

  return (
    <Modal 
      isOpen={isOpen} 
      onClose={onClose}
      size="full"
      classNames={{
        base: "bg-black/95",
        closeButton: "text-white hover:bg-white/10",
      }}
      hideCloseButton
    >
      <ModalContent>
        <ModalBody 
          className="p-0 relative flex items-center justify-center min-h-screen"
          onTouchStart={handleTouchStart}
          onTouchEnd={handleTouchEnd}
        >
          {/* Close button */}
          <Button
            isIconOnly
            variant="light"
            aria-label="关闭预览"
            className="absolute top-4 right-4 z-50 text-white rounded-xl"
            onPress={onClose}
          >
            <X size={24} />
          </Button>
          
          {/* Navigation arrows */}
          <Button
            isIconOnly
            variant="light"
            aria-label="上一张图片"
            className="absolute left-4 top-1/2 -translate-y-1/2 z-50 text-white hidden md:flex rounded-xl"
            onPress={handlePrev}
          >
            <ChevronLeft size={32} />
          </Button>
          
          <Button
            isIconOnly
            variant="light"
            aria-label="下一张图片"
            className="absolute right-4 top-1/2 -translate-y-1/2 z-50 text-white hidden md:flex rounded-xl"
            onPress={handleNext}
          >
            <ChevronRight size={32} />
          </Button>
          
          {/* Loading indicator */}
          {loading && (
            <div className="absolute inset-0 flex items-center justify-center z-40">
              <div className="w-12 h-12 border-4 border-white/20 border-t-white rounded-full animate-spin" />
            </div>
          )}
          
          {/* Image */}
          {currentImage && (
          <div className="relative max-w-[90vw] max-h-[90vh] overflow-hidden">
            <img
              src={imageUrl}
              alt={currentImage.name}
              className={cn(
                "max-w-full max-h-[90vh] object-contain transition-all duration-200",
                loading ? "opacity-0" : "opacity-100"
              )}
              style={{
                transform: `scale(${zoom}) rotate(${rotation}deg)`,
              }}
              onLoad={() => setLoading(false)}
              onError={() => {
                if (!hasRetried && currentImage?.path) {
                  void (async () => {
                    const refreshed = await refreshAuthSession()
                    if (refreshed) {
                      setHasRetried(true)
                      setLoading(true)
                      setImageUrl(withSessionRetryParam(getDownloadUrl(currentImage.path)))
                      return
                    }
                    setLoading(false)
                  })()
                  return
                }
                setLoading(false)
              }}
            />
          </div>
          )}
          
          {/* Bottom toolbar */}
          <div className="absolute bottom-0 left-0 right-0 bg-gradient-to-t from-black/80 to-transparent p-4">
            <div className="flex items-center justify-between max-w-4xl mx-auto">
              <div className="text-white">
                <p className="font-medium truncate max-w-md">{currentImage?.name}</p>
                <p className="text-sm text-white/60">
                  {currentIndex + 1} / {images.length}
                </p>
              </div>
              
              <div className="flex items-center gap-2">
                <Button
                  isIconOnly
                  size="sm"
                  variant="light"
                  aria-label="缩小图片"
                  className="text-white rounded-xl"
                  onPress={() => setZoom(z => Math.max(0.5, z - 0.25))}
                >
                  <ZoomOut size={18} />
                </Button>
                <Button
                  isIconOnly
                  size="sm"
                  variant="light"
                  aria-label="放大图片"
                  className="text-white rounded-xl"
                  onPress={() => setZoom(z => Math.min(3, z + 0.25))}
                >
                  <ZoomIn size={18} />
                </Button>
                <Button
                  isIconOnly
                  size="sm"
                  variant="light"
                  aria-label="旋转图片"
                  className="text-white rounded-xl"
                  onPress={() => setRotation(r => (r + 90) % 360)}
                >
                  <RotateCw size={18} />
                </Button>
                <Button
                  isIconOnly
                  size="sm"
                  variant="light"
                  aria-label={showInfo ? '隐藏图片信息' : '显示图片信息'}
                  className="text-white rounded-xl"
                  onPress={() => setShowInfo(!showInfo)}
                >
                  <Info size={18} />
                </Button>
                <Button
                  isIconOnly
                  size="sm"
                  variant="light"
                  aria-label="下载当前图片"
                  className="text-white rounded-xl"
                  onPress={() => currentImage && void downloadFile(currentImage.path, { filename: currentImage.name })}
                >
                  <Download size={18} />
                </Button>
              </div>
            </div>
            
            {/* Image info panel */}
            {showInfo && (
              <div className="mt-4 max-w-4xl mx-auto bg-black/50 rounded-lg p-4 text-white text-sm">
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                  <div>
                    <span className="text-white/60">文件名</span>
                    <p className="truncate">{currentImage?.name}</p>
                  </div>
                  <div>
                    <span className="text-white/60">大小</span>
                    <p>{formatBytes(currentImage?.size ?? 0)}</p>
                  </div>
                  <div>
                    <span className="text-white/60">修改时间</span>
                    <p>{formatDate(currentImage?.modTime ?? '')}</p>
                  </div>
                  <div>
                    <span className="text-white/60">路径</span>
                    <p className="truncate">{currentImage?.path}</p>
                  </div>
                </div>
              </div>
            )}
          </div>
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export function AlbumPage() {
  const [previewIndex, setPreviewIndex] = useState<number | null>(null)
  const abortControllerRef = useRef<AbortController | null>(null)
  
  const { data, isLoading, error, refetch } = useQuery<AlbumQueryResult>({
    queryKey: ['album-images'],
    queryFn: async () => {
      // Cancel previous request if any
      abortControllerRef.current?.abort()
      abortControllerRef.current = new AbortController()
      const errorState = { hadPartialError: false }
      const images = await fetchAllImages('/', 0, abortControllerRef.current.signal, { count: 0 }, errorState)
      return {
        images,
        hadPartialError: errorState.hadPartialError,
      }
    },
    staleTime: 1000 * 60 * 5, // Cache for 5 minutes
  })

  const images = data?.images
  
  // Cleanup on unmount
  useEffect(() => {
    return () => {
      abortControllerRef.current?.abort()
    }
  }, [])

  const handleOpenPreview = useCallback((index: number) => {
    setPreviewIndex(index)
  }, [])

  const handleClosePreview = useCallback(() => {
    setPreviewIndex(null)
  }, [])

  if (isLoading) {
    return (
      <div className="h-full overflow-auto custom-scrollbar p-7">
        <div className="space-y-6">
          <PageHeader
            title="相册"
            subtitle="正在加载..."
            icon={ImageIcon}
          />
          <div className="columns-2 md:columns-3 lg:columns-4 xl:columns-5 gap-3 space-y-3">
            {Array.from({ length: 12 }).map((_, i) => (
              <div 
                key={i} 
                className={cn(
                  "w-full rounded-lg skeleton-shimmer",
                  ['h-48', 'h-56', 'h-64', 'h-72'][i % 4]
                )} 
              />
            ))}
          </div>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="h-full overflow-auto custom-scrollbar p-7">
        <div className="space-y-6">
          <PageHeader
            title="相册"
            subtitle="加载失败"
            icon={ImageIcon}
          />
          <EmptyState
            icon={AlertCircle}
            title="加载相册失败"
            description="无法扫描图片目录，当前结果不可用。请检查连接状态后重试。"
            action={
              <Button className="rounded-xl" variant="bordered" onPress={() => void refetch()}>
                重新加载
              </Button>
            }
          />
        </div>
      </div>
    )
  }

  return (
    <div className="h-full overflow-auto custom-scrollbar p-7">
      <div className="space-y-6">
        <PageHeader
          title="相册"
          subtitle={images ? `共 ${images.length} 张图片` : undefined}
          icon={ImageIcon}
        />

        {data?.hadPartialError && (
          <div className="flex items-start gap-3 rounded-2xl border border-warning/30 bg-warning/5 px-4 py-3 text-sm text-foreground">
            <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
            <div>
              <p className="font-medium">部分目录扫描失败</p>
              <p className="text-default-600">当前相册仅展示已成功加载的图片，结果可能不完整。</p>
            </div>
          </div>
        )}

        {images && images.length > 0 ? (
          <>
            {/* Masonry grid */}
            <div className="columns-2 md:columns-3 lg:columns-4 xl:columns-5 gap-3 space-y-3">
              {images.map((image, index) => (
                <ImageThumbnail
                  key={image.path}
                  file={image}
                  index={index}
                  onClick={() => handleOpenPreview(index)}
                />
              ))}
            </div>

            {/* Preview modal - only render when images exist */}
            {images.length > 0 && previewIndex !== null && (
              <ImagePreview
                key={images[previewIndex]?.path ?? String(previewIndex)}
                images={images}
                currentIndex={previewIndex}
                isOpen={true}
                onClose={handleClosePreview}
                onNavigate={setPreviewIndex}
              />
            )}
          </>
        ) : (
          <EmptyState
            icon={ImageIcon}
            title="暂无图片"
            description="上传图片到 NAS 后，这里将自动展示"
          />
        )}
      </div>
    </div>
  )
}
