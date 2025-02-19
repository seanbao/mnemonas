import { Button } from '@heroui/react'
import { buildPreviewUrl } from '@/lib/preview-utils'
import { cn } from '@/lib/utils'

export interface PdfPreviewProps {
  path: string
  filename: string
  className?: string
}

/**
 * PDF preview using native browser PDF viewer (iframe).
 * For a richer experience, install @react-pdf-viewer/core and @react-pdf-viewer/default-layout:
 *   npm install @react-pdf-viewer/core @react-pdf-viewer/default-layout pdfjs-dist
 */
export function PdfPreview({ path, filename, className }: PdfPreviewProps) {
  const pdfUrl = buildPreviewUrl(path)
  
  return (
    <div className={cn("h-full flex flex-col bg-content1 rounded-xl overflow-hidden", className)}>
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-2 bg-content2 border-b border-divider">
        <span className="text-sm font-medium truncate">{filename}</span>
        <Button
          as="a"
          href={pdfUrl}
          target="_blank"
          rel="noopener noreferrer"
          size="sm"
          variant="flat"
          color="primary"
        >
          在新标签打开
        </Button>
      </div>
      
      {/* Embedded PDF */}
      <div className="flex-1">
        <iframe
          src={pdfUrl}
          className="w-full h-full border-0"
          title={filename}
        />
      </div>
    </div>
  )
}

// Alias for consistent naming
export const SimplePdfPreview = PdfPreview

export default PdfPreview
