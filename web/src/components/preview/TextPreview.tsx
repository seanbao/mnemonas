import { useState, useEffect } from 'react'
import { Spinner } from '@heroui/react'
import { FileCode, AlertCircle } from 'lucide-react'
import { buildPreviewUrl, getLanguageFromExtension } from '@/lib/preview-utils'
import { authFetch } from '@/api/auth'
import { cn } from '@/lib/utils'

// Simple syntax highlighting for common patterns
function highlightCode(code: string, language: string): string {
  // Basic keyword highlighting for common languages
  const keywords: Record<string, string[]> = {
    javascript: ['const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'class', 'import', 'export', 'from', 'async', 'await', 'try', 'catch', 'throw', 'new', 'this', 'true', 'false', 'null', 'undefined'],
    typescript: ['const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'class', 'import', 'export', 'from', 'async', 'await', 'try', 'catch', 'throw', 'new', 'this', 'true', 'false', 'null', 'undefined', 'interface', 'type', 'enum', 'implements', 'extends'],
    python: ['def', 'class', 'import', 'from', 'return', 'if', 'elif', 'else', 'for', 'while', 'try', 'except', 'with', 'as', 'True', 'False', 'None', 'and', 'or', 'not', 'in', 'is', 'lambda', 'yield', 'async', 'await'],
    go: ['func', 'package', 'import', 'return', 'if', 'else', 'for', 'range', 'switch', 'case', 'default', 'struct', 'interface', 'type', 'var', 'const', 'true', 'false', 'nil', 'defer', 'go', 'chan', 'map', 'make', 'new'],
    rust: ['fn', 'let', 'mut', 'const', 'if', 'else', 'for', 'while', 'loop', 'match', 'struct', 'enum', 'impl', 'trait', 'pub', 'use', 'mod', 'crate', 'self', 'super', 'true', 'false', 'async', 'await', 'return', 'where'],
  }
  
  // Get keywords for the language or use a default set
  const langKeywords = keywords[language] || keywords.javascript || []
  
  // Escape HTML
  let escaped = code
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
  
  // Highlight strings (simple single and double quotes)
  escaped = escaped.replace(
    /(["'`])(?:(?!\1|\\).|\\.)*\1/g,
    '<span class="text-emerald-500">$&</span>'
  )
  
  // Highlight comments (// and #)
  escaped = escaped.replace(
    /(\/\/.*|#.*)/g,
    '<span class="text-default-400 italic">$1</span>'
  )
  
  // Highlight numbers
  escaped = escaped.replace(
    /\b(\d+\.?\d*)\b/g,
    '<span class="text-amber-500">$1</span>'
  )
  
  // Highlight keywords
  for (const keyword of langKeywords) {
    const regex = new RegExp(`\\b(${keyword})\\b`, 'g')
    escaped = escaped.replace(
      regex,
      '<span class="text-purple-400 font-medium">$1</span>'
    )
  }
  
  return escaped
}

export interface TextPreviewProps {
  path: string
  filename: string
  className?: string
}

export function TextPreview({ path, filename, className }: TextPreviewProps) {
  const [content, setContent] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  
  const language = getLanguageFromExtension(filename)

  useEffect(() => {
    async function loadContent() {
      setIsLoading(true)
      setError(null)
      
      try {
        const url = buildPreviewUrl(path, { includeAuth: false })
        const response = await authFetch(url)
        
        if (!response.ok) {
          throw new Error(`加载失败: ${response.statusText}`)
        }
        
        // Check file size - limit to 1MB for preview
        const contentLength = response.headers.get('content-length')
        if (contentLength && parseInt(contentLength) > 1024 * 1024) {
          throw new Error('文件过大，无法预览')
        }
        
        const text = await response.text()
        setContent(text)
      } catch (err) {
        setError(err instanceof Error ? err.message : '加载失败')
      } finally {
        setIsLoading(false)
      }
    }
    
    loadContent()
  }, [path])

  if (isLoading) {
    return (
      <div className={cn("flex items-center justify-center h-full", className)}>
        <div className="text-center">
          <Spinner size="lg" />
          <p className="text-default-500 mt-4">加载文件内容...</p>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className={cn("flex items-center justify-center h-full", className)}>
        <div className="text-center text-danger">
          <AlertCircle size={48} className="mx-auto mb-4" />
          <p>{error}</p>
        </div>
      </div>
    )
  }

  if (content === null) {
    return null
  }

  const lines = content.split('\n')
  const highlightedContent = highlightCode(content, language)

  return (
    <div className={cn("h-full flex flex-col bg-content1 rounded-xl overflow-hidden", className)}>
      {/* Header */}
      <div className="flex items-center gap-2 px-4 py-2 bg-content2 border-b border-divider">
        <FileCode size={16} className="text-default-500" />
        <span className="text-sm font-medium">{filename}</span>
        <span className="text-xs text-default-400 ml-2">
          {language.toUpperCase()} · {lines.length} 行
        </span>
      </div>
      
      {/* Content */}
      <div className="flex-1 overflow-auto custom-scrollbar">
        <div className="flex">
          {/* Line numbers */}
          <div className="flex-shrink-0 py-4 pr-4 pl-4 text-right select-none bg-content2/50 border-r border-divider">
            {lines.map((_, i) => (
              <div key={i} className="text-xs text-default-400 leading-6 font-mono">
                {i + 1}
              </div>
            ))}
          </div>
          
          {/* Code content */}
          <pre className="flex-1 py-4 px-4 text-sm font-mono leading-6 overflow-x-auto">
            <code 
              dangerouslySetInnerHTML={{ __html: highlightedContent }}
            />
          </pre>
        </div>
      </div>
    </div>
  )
}

export default TextPreview
