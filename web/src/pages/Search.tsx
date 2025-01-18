import { useState, useCallback, useEffect } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  Card,
  CardBody,
  CardHeader,
  Input,
  Button,
  Spinner,
} from '@heroui/react'
import {
  Search as SearchIcon,
  File,
  Folder,
  FileText,
  FileImage,
  FileVideo,
  FileAudio,
  Archive,
  ArrowLeft,
} from 'lucide-react'
import { searchFiles, type SearchResult } from '@/api/search'
import { formatBytes, formatDate, cn } from '@/lib/utils'

// File icon based on type
function FileIcon({ name, isDir, size = 20 }: { name: string; isDir: boolean; size?: number }) {
  if (isDir) {
    return <Folder size={size} className="text-starlight" />
  }

  const ext = name.split('.').pop()?.toLowerCase() || ''

  // Images
  if (['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico'].includes(ext)) {
    return <FileImage size={size} className="text-emerald-400" />
  }

  // Videos
  if (['mp4', 'webm', 'mov', 'avi', 'mkv', 'm4v', 'wmv'].includes(ext)) {
    return <FileVideo size={size} className="text-rose-400" />
  }

  // Audio
  if (['mp3', 'wav', 'flac', 'aac', 'm4a', 'ogg', 'wma'].includes(ext)) {
    return <FileAudio size={size} className="text-violet-400" />
  }

  // Archives
  if (['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz'].includes(ext)) {
    return <Archive size={size} className="text-amber-400" />
  }

  // Documents
  if (['pdf', 'doc', 'docx', 'txt', 'md', 'rtf', 'odt'].includes(ext)) {
    return <FileText size={size} className="text-blue-400" />
  }

  return <File size={size} className="text-text-muted" />
}

// Search result item component
function SearchResultItem({ result, onClick }: { result: SearchResult; onClick: () => void }) {
  return (
    <div
      className={cn(
        "flex items-center gap-3 px-4 py-3 cursor-pointer transition-all duration-150",
        "hover:bg-bg-secondary border-b border-divider last:border-b-0"
      )}
      onClick={onClick}
    >
      <div className="flex-shrink-0">
        <FileIcon name={result.name} isDir={result.is_dir} size={24} />
      </div>
      <div className="flex-1 min-w-0">
        <div className="font-medium text-text-primary truncate">{result.name}</div>
        <div className="text-xs text-text-muted truncate">{result.path}</div>
      </div>
      <div className="flex-shrink-0 text-right text-sm text-text-muted">
        {result.is_dir ? (
          <span>文件夹</span>
        ) : (
          <span>{formatBytes(result.size)}</span>
        )}
      </div>
      <div className="flex-shrink-0 text-right text-xs text-text-muted w-24">
        {formatDate(result.mod_time)}
      </div>
    </div>
  )
}

export function SearchPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const initialQuery = searchParams.get('q') || ''
  const [query, setQuery] = useState(initialQuery)
  const [debouncedQuery, setDebouncedQuery] = useState(initialQuery)

  // Debounce search query
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedQuery(query)
      if (query) {
        setSearchParams({ q: query })
      } else {
        setSearchParams({})
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [query, setSearchParams])

  const { data, isLoading, error } = useQuery({
    queryKey: ['search', debouncedQuery],
    queryFn: () => searchFiles(debouncedQuery),
    enabled: debouncedQuery.length > 0,
  })

  const handleResultClick = useCallback((result: SearchResult) => {
    if (result.is_dir) {
      navigate(`/files${result.path}`)
    } else {
      // Navigate to parent directory and highlight the file
      const parentPath = result.path.substring(0, result.path.lastIndexOf('/')) || '/'
      navigate(`/files${parentPath}`)
    }
  }, [navigate])

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      setDebouncedQuery(query)
      if (query) {
        setSearchParams({ q: query })
      }
    }
  }, [query, setSearchParams])

  return (
    <div className="h-full flex flex-col p-6">
      {/* Header */}
      <div className="flex items-center gap-4 mb-6">
        <Button
          isIconOnly
          variant="light"
          onPress={() => navigate(-1)}
          className="text-text-muted"
        >
          <ArrowLeft size={20} />
        </Button>
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-accent-primary to-accent-dark flex items-center justify-center shadow-sm">
            <SearchIcon size={20} className="text-white" />
          </div>
          <div>
            <h1 className="text-xl font-semibold text-text-primary">搜索</h1>
            <p className="text-sm text-text-muted">
              搜索文件和文件夹
            </p>
          </div>
        </div>
      </div>

      {/* Search Input */}
      <div className="mb-6">
        <Input
          placeholder="输入文件名搜索..."
          value={query}
          onValueChange={setQuery}
          onKeyDown={handleKeyDown}
          startContent={<SearchIcon size={18} className="text-text-muted" />}
          autoFocus
          size="lg"
          classNames={{
            inputWrapper: cn(
              "bg-bg-secondary border-divider",
              "group-data-[focus=true]:border-accent-primary"
            ),
          }}
        />
      </div>

      {/* Results */}
      <Card className="flex-1 bg-bg-card border border-divider shadow-sm overflow-hidden">
        <CardHeader className="border-b border-divider">
          <div className="flex items-center justify-between w-full">
            <h2 className="font-semibold text-text-primary">搜索结果</h2>
            {data && (
              <span className="text-sm text-text-muted">
                找到 {data.count} 个结果
              </span>
            )}
          </div>
        </CardHeader>
        <CardBody className="p-0 overflow-auto custom-scrollbar">
          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <Spinner size="lg" color="secondary" />
            </div>
          ) : error ? (
            <div className="flex flex-col items-center justify-center h-40 text-rose-500">
              <p>搜索失败</p>
              <p className="text-sm text-text-muted">{(error as Error).message}</p>
            </div>
          ) : !debouncedQuery ? (
            <div className="flex flex-col items-center justify-center h-40 text-text-muted">
              <SearchIcon size={32} className="mb-2 opacity-50" />
              <p>输入关键词开始搜索</p>
            </div>
          ) : !data?.results?.length ? (
            <div className="flex flex-col items-center justify-center h-40 text-text-muted">
              <SearchIcon size={32} className="mb-2 opacity-50" />
              <p>未找到匹配的文件</p>
              <p className="text-sm mt-1">尝试使用其他关键词</p>
            </div>
          ) : (
            <div>
              {data.results.map((result, index) => (
                <SearchResultItem
                  key={`${result.path}-${index}`}
                  result={result}
                  onClick={() => handleResultClick(result)}
                />
              ))}
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  )
}
