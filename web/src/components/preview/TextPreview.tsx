import { useState, useEffect, type ReactNode } from 'react'
import { Spinner } from '@heroui/react'
import { FileCode, AlertCircle } from 'lucide-react'
import { buildPreviewUrl, getLanguageFromExtension } from '@/lib/preview-utils'
import { authFetch } from '@/api/auth'
import { readDownloadJsonErrorDetails } from '@/lib/downloadResponse'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { cn } from '@/lib/utils'

const maxPreviewBytes = 1024 * 1024
const textPreviewTooLargeMessage = '文件过大，无法预览'

const keywords: Record<string, string[]> = {
  javascript: ['const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'class', 'import', 'export', 'from', 'async', 'await', 'try', 'catch', 'throw', 'new', 'this', 'true', 'false', 'null', 'undefined'],
  typescript: ['const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'class', 'import', 'export', 'from', 'async', 'await', 'try', 'catch', 'throw', 'new', 'this', 'true', 'false', 'null', 'undefined', 'interface', 'type', 'enum', 'implements', 'extends'],
  python: ['def', 'class', 'import', 'from', 'return', 'if', 'elif', 'else', 'for', 'while', 'try', 'except', 'with', 'as', 'True', 'False', 'None', 'and', 'or', 'not', 'in', 'is', 'lambda', 'yield', 'async', 'await'],
  go: ['func', 'package', 'import', 'return', 'if', 'else', 'for', 'range', 'switch', 'case', 'default', 'struct', 'interface', 'type', 'var', 'const', 'true', 'false', 'nil', 'defer', 'go', 'chan', 'map', 'make', 'new'],
  rust: ['fn', 'let', 'mut', 'const', 'if', 'else', 'for', 'while', 'loop', 'match', 'struct', 'enum', 'impl', 'trait', 'pub', 'use', 'mod', 'crate', 'self', 'super', 'true', 'false', 'async', 'await', 'return', 'where'],
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function buildTokenRegex(language: string): RegExp {
  const langKeywords = keywords[language] || keywords.javascript || []
  const keywordPattern = langKeywords.map(escapeRegExp).join('|')
  const patterns = [
    '(["\\\'`])(?:\\\\.|(?!\\1).)*\\1',
    '\\/\\/.*|#.*',
    '\\b\\d+\\.?\\d*\\b',
  ]
  if (keywordPattern) {
    patterns.push(`\\b(?:${keywordPattern})\\b`)
  }
  return new RegExp(patterns.join('|'), 'g')
}

function tokenClass(token: string, language: string): string | null {
  if (/^(["'`])/.test(token)) {
    return 'text-emerald-500'
  }
  if (token.startsWith('//') || token.startsWith('#')) {
    return 'text-default-400 italic'
  }
  if (/^\d+\.?\d*$/.test(token)) {
    return 'text-amber-500'
  }
  const langKeywords = keywords[language] || keywords.javascript || []
  if (langKeywords.includes(token)) {
    return 'text-purple-400 font-medium'
  }
  return null
}

function highlightLine(line: string, language: string): ReactNode[] {
  const tokenRegex = buildTokenRegex(language)
  const parts: ReactNode[] = []
  let lastIndex = 0

  for (const match of line.matchAll(tokenRegex)) {
    const token = match[0]
    const index = match.index ?? 0
    if (index > lastIndex) {
      parts.push(line.slice(lastIndex, index))
    }

    const className = tokenClass(token, language)
    parts.push(className ? (
      <span key={`${index}-${token}`} className={className}>
        {token}
      </span>
    ) : token)
    lastIndex = index + token.length
  }

  if (lastIndex < line.length) {
    parts.push(line.slice(lastIndex))
  }

  return parts
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function getTextPreviewErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message === textPreviewTooLargeMessage) {
    return textPreviewTooLargeMessage
  }

  return getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION)
}

async function readLimitedText(response: Response, signal?: AbortSignal): Promise<string> {
  const contentLength = response.headers.get('content-length')
  if (contentLength && parseInt(contentLength, 10) > maxPreviewBytes) {
    throw new Error(textPreviewTooLargeMessage)
  }

  if (signal?.aborted) {
    throw new DOMException('Preview request aborted', 'AbortError')
  }

  if (!response.body) {
    const text = await response.text()
    if (new TextEncoder().encode(text).byteLength > maxPreviewBytes) {
      throw new Error(textPreviewTooLargeMessage)
    }
    return text
  }

  const reader = response.body.getReader()
  const chunks: Uint8Array[] = []
  let received = 0
  const abortReader = () => {
    void reader.cancel().catch(() => {})
  }

  try {
    signal?.addEventListener('abort', abortReader, { once: true })
    while (true) {
      if (signal?.aborted) {
        throw new DOMException('Preview request aborted', 'AbortError')
      }
      const { done, value } = await reader.read()
      if (done) {
        break
      }
      received += value.byteLength
      if (received > maxPreviewBytes) {
        void reader.cancel().catch(() => {})
        throw new Error(textPreviewTooLargeMessage)
      }
      chunks.push(value)
    }
  } finally {
    signal?.removeEventListener('abort', abortReader)
    reader.releaseLock()
  }

  const buffer = new Uint8Array(received)
  let offset = 0
  for (const chunk of chunks) {
    buffer.set(chunk, offset)
    offset += chunk.byteLength
  }
  return new TextDecoder().decode(buffer)
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
    let cancelled = false
    const controller = new AbortController()

    async function loadContent() {
      setIsLoading(true)
      setError(null)
      setContent(null)
      
      try {
        const url = buildPreviewUrl(path, { includeAuth: false })
        const response = await authFetch(url, { signal: controller.signal })

        if (!response.ok) {
          const jsonError = await readDownloadJsonErrorDetails(response, '加载失败')
          if (jsonError) {
            if (!cancelled) {
              setError(getTextPreviewErrorMessage(new Error(jsonError.message)))
            }
            return
          }

          throw new Error(`加载失败: ${response.statusText}`)
        }
        
        const text = await readLimitedText(response, controller.signal)
        if (!cancelled) {
          setContent(text)
        }
      } catch (err) {
        if (!cancelled && !controller.signal.aborted && !isAbortError(err)) {
          setError(getTextPreviewErrorMessage(err))
        }
      } finally {
        if (!cancelled) {
          setIsLoading(false)
        }
      }
    }
    
    loadContent()

    return () => {
      cancelled = true
      controller.abort()
    }
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

  return (
    <div className={cn("h-full flex flex-col bg-content1 rounded-lg overflow-hidden", className)}>
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
            <code className="block">
              {lines.map((line, index) => (
                <span key={index} className="block min-h-6">
                  {highlightLine(line, language)}
                </span>
              ))}
            </code>
          </pre>
        </div>
      </div>
    </div>
  )
}

export default TextPreview
