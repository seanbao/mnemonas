import { useState, useEffect } from 'react'
import { Button, Spinner } from '@heroui/react'
import { AlertCircle } from 'lucide-react'
import { buildPreviewUrl } from '@/lib/preview-utils'
import { authFetch } from '@/api/auth'
import { cn } from '@/lib/utils'

export interface PdfPreviewProps {
  path: string
  filename: string
  className?: string
}

/**
 * PDF preview using native browser PDF viewer (iframe).
 * Fetches PDF with auth token and creates blob URL.
 */
export function PdfPreview({ path, filename, className }: PdfPreviewProps) {
  const pdfUrl = buildPreviewUrl(path, { includeAuth: false })
  const [blobUrl, setBlobUrl] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  
  useEffect(() => {
    let cancelled = false
    let currentBlobUrl: string | null = null
    
    const fetchPdf = async () => {
      setIsLoading(true)
      setError(null)
      setBlobUrl(null)
      
      try {
        const response = await authFetch(pdfUrl)
        if (!response.ok) throw new Error(`HTTP ${response.status}`)
        const blob = await response.blob()
        if (!cancelled) {
          currentBlobUrl = URL.createObjectURL(blob)
          setBlobUrl(currentBlobUrl)
          setIsLoading(false)
        }
      } catch {
        if (!cancelled) {
          setError('无法加载 PDF')
          setIsLoading(false)
        }
      }
    }
    
    fetchPdf()
    return () => {
      cancelled = true
      if (currentBlobUrl) URL.revokeObjectURL(currentBlobUrl)
    }
  }, [pdfUrl])
  
  return (
    <div className={cn("h-full flex flex-col bg-content1 rounded-lg overflow-hidden", className)}>
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-2 bg-content2 border-b border-divider">
        <span className="text-sm font-medium truncate">{filename}</span>
        {blobUrl && (
          <Button
            as="a"
            href={blobUrl}
            target="_blank"
            rel="noopener noreferrer"
            size="sm"
            variant="flat"
            color="primary"
          >
            在新标签打开
          </Button>
        )}
      </div>
      
      {/* Content */}
      <div className="flex-1 flex items-center justify-center">
        {isLoading && <Spinner size="lg" />}
        {error && (
          <div className="text-center text-danger">
            <AlertCircle size={48} className="mx-auto mb-4" />
            <p>{error}</p>
          </div>
        )}
        {blobUrl && (
          <iframe
            src={blobUrl}
            className="w-full h-full border-0"
            title={filename}
          />
        )}
      </div>
    </div>
  )
}

// Alias for consistent naming
export const SimplePdfPreview = PdfPreview

export default PdfPreview
