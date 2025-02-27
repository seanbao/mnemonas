import { useState, useCallback, useRef, useEffect } from 'react'
import { Spinner, Button } from '@heroui/react'
import { ZoomIn, ZoomOut, RotateCw, Maximize2, AlertCircle } from 'lucide-react'
import { buildPreviewUrl } from '@/lib/preview-utils'
import { cn } from '@/lib/utils'
import { getAuthHeaders } from '@/api/auth'

export interface ImagePreviewProps {
  path: string
  filename: string
  className?: string
}

export function ImagePreview({ path, filename, className }: ImagePreviewProps) {
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [scale, setScale] = useState(1)
  const [rotation, setRotation] = useState(0)
  const [position, setPosition] = useState({ x: 0, y: 0 })
  const [isDragging, setIsDragging] = useState(false)
  const [dragStart, setDragStart] = useState({ x: 0, y: 0 })
  const [blobUrl, setBlobUrl] = useState<string | null>(null)
  
  const containerRef = useRef<HTMLDivElement>(null)
  const imageRef = useRef<HTMLImageElement>(null)

  const imageUrl = buildPreviewUrl(path)

  // Fetch image with auth token and create blob URL
  useEffect(() => {
    let cancelled = false
    let currentBlobUrl: string | null = null
    
    const fetchImage = async () => {
      setIsLoading(true)
      setError(null)
      setBlobUrl(null)
      
      try {
        const response = await fetch(imageUrl, {
          headers: getAuthHeaders(),
        })
        
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`)
        }
        
        const blob = await response.blob()
        if (!cancelled) {
          currentBlobUrl = URL.createObjectURL(blob)
          setBlobUrl(currentBlobUrl)
        }
      } catch (err) {
        if (!cancelled) {
          setError('无法加载图片')
          setIsLoading(false)
        }
      }
    }
    
    fetchImage()
    
    return () => {
      cancelled = true
      if (currentBlobUrl) {
        URL.revokeObjectURL(currentBlobUrl)
      }
    }
  }, [imageUrl])

  const handleLoad = useCallback(() => {
    setIsLoading(false)
    setError(null)
  }, [])

  const handleError = useCallback(() => {
    setIsLoading(false)
    setError('无法加载图片')
  }, [])

  const handleZoomIn = useCallback(() => {
    setScale(s => Math.min(s * 1.25, 5))
  }, [])

  const handleZoomOut = useCallback(() => {
    setScale(s => Math.max(s / 1.25, 0.1))
  }, [])

  const handleRotate = useCallback(() => {
    setRotation(r => (r + 90) % 360)
  }, [])

  const handleReset = useCallback(() => {
    setScale(1)
    setRotation(0)
    setPosition({ x: 0, y: 0 })
  }, [])

  // Mouse drag handlers
  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    if (scale <= 1) return
    setIsDragging(true)
    setDragStart({ x: e.clientX - position.x, y: e.clientY - position.y })
  }, [scale, position])

  const handleMouseMove = useCallback((e: React.MouseEvent) => {
    if (!isDragging) return
    setPosition({
      x: e.clientX - dragStart.x,
      y: e.clientY - dragStart.y,
    })
  }, [isDragging, dragStart])

  const handleMouseUp = useCallback(() => {
    setIsDragging(false)
  }, [])

  // Wheel zoom
  const handleWheel = useCallback((e: React.WheelEvent) => {
    e.preventDefault()
    if (e.deltaY < 0) {
      setScale(s => Math.min(s * 1.1, 5))
    } else {
      setScale(s => Math.max(s / 1.1, 0.1))
    }
  }, [])

  // Reset transform when path changes
  useEffect(() => {
    setScale(1)
    setRotation(0)
    setPosition({ x: 0, y: 0 })
  }, [path])

  // Cleanup blob URL on unmount
  useEffect(() => {
    return () => {
      if (blobUrl) {
        URL.revokeObjectURL(blobUrl)
      }
    }
  }, [blobUrl])

  return (
    <div className={cn("h-full flex flex-col bg-content1 rounded-xl overflow-hidden", className)}>
      {/* Toolbar */}
      <div className="flex items-center justify-between px-4 py-2 bg-content2 border-b border-divider">
        <span className="text-sm font-medium truncate max-w-[200px]">{filename}</span>
        <div className="flex items-center gap-1">
          <Button
            isIconOnly
            size="sm"
            variant="light"
            onPress={handleZoomOut}
            title="缩小"
            className="rounded-lg"
          >
            <ZoomOut size={16} />
          </Button>
          <span className="text-xs text-default-500 w-12 text-center">
            {Math.round(scale * 100)}%
          </span>
          <Button
            isIconOnly
            size="sm"
            variant="light"
            onPress={handleZoomIn}
            title="放大"
            className="rounded-lg"
          >
            <ZoomIn size={16} />
          </Button>
          <div className="w-px h-4 bg-divider mx-1" />
          <Button
            isIconOnly
            size="sm"
            variant="light"
            onPress={handleRotate}
            title="旋转"
            className="rounded-lg"
          >
            <RotateCw size={16} />
          </Button>
          <Button
            isIconOnly
            size="sm"
            variant="light"
            onPress={handleReset}
            title="重置"
            className="rounded-lg"
          >
            <Maximize2 size={16} />
          </Button>
        </div>
      </div>
      
      {/* Image container */}
      <div 
        ref={containerRef}
        className={cn(
          "flex-1 flex items-center justify-center overflow-hidden bg-default-100",
          scale > 1 && "cursor-grab",
          isDragging && "cursor-grabbing"
        )}
        onMouseDown={handleMouseDown}
        onMouseMove={handleMouseMove}
        onMouseUp={handleMouseUp}
        onMouseLeave={handleMouseUp}
        onWheel={handleWheel}
      >
        {isLoading && (
          <div className="absolute inset-0 flex items-center justify-center">
            <Spinner size="lg" />
          </div>
        )}
        
        {error && (
          <div className="text-center text-danger">
            <AlertCircle size={48} className="mx-auto mb-4" />
            <p>{error}</p>
          </div>
        )}
        
        {blobUrl && (
          <img
            ref={imageRef}
            src={blobUrl}
            alt={filename}
            className={cn(
              "max-w-full max-h-full object-contain transition-opacity",
              isLoading && "opacity-0",
              !isLoading && !error && "opacity-100"
            )}
            style={{
              transform: `translate(${position.x}px, ${position.y}px) scale(${scale}) rotate(${rotation}deg)`,
              transition: isDragging ? 'none' : 'transform 0.2s ease',
            }}
            onLoad={handleLoad}
            onError={handleError}
            draggable={false}
          />
        )}
      </div>
    </div>
  )
}

export default ImagePreview
