import { useState, useCallback, useEffect } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  Card,
  CardBody,
  CardHeader,
  Input,
  Button,
} from '@heroui/react'
import {
  Search as SearchIcon,
  ArrowLeft,
} from 'lucide-react'
import { PageHeader } from '@/components/ui/PageHeader'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { searchFiles, type SearchResult } from '@/api/search'
import { formatBytes, formatDate, cn } from '@/lib/utils'

// Search result item component
function SearchResultItem({ result, onClick }: { result: SearchResult; onClick: () => void }) {
  return (
    <div
      className={cn(
        "flex items-center gap-3 px-4 py-2.5 cursor-pointer transition-all duration-150",
        "hover:bg-content2 border-b border-divider last:border-b-0"
      )}
      onClick={onClick}
    >
      <div className="flex-shrink-0">
        <FileIcon name={result.name} isDir={result.is_dir} size={24} variant="bare" />
      </div>
      <div className="flex-1 min-w-0">
        <div className="font-medium text-foreground truncate">{result.name}</div>
        <div className="text-xs text-default-500 truncate">{result.path}</div>
      </div>
      <div className="flex-shrink-0 text-right text-sm text-default-500">
        {result.is_dir ? (
          <span>文件夹</span>
        ) : (
          <span>{formatBytes(result.size)}</span>
        )}
      </div>
      <div className="flex-shrink-0 text-right text-xs text-default-500 w-24">
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
          className="text-default-500"
        >
          <ArrowLeft size={20} />
        </Button>
        <PageHeader
          title="搜索"
          subtitle="搜索文件和文件夹"
          icon={SearchIcon}
          className="flex-1"
        />
      </div>

      {/* Search Input */}
      <div className="mb-6">
        <Input
          placeholder="输入文件名搜索..."
          value={query}
          onValueChange={setQuery}
          onKeyDown={handleKeyDown}
          startContent={<SearchIcon size={18} className="text-default-500" />}
          autoFocus
          size="lg"
          classNames={{
            inputWrapper: cn(
              "input-shell",
              "group-data-[focus=true]:border-accent-primary"
            ),
          }}
        />
      </div>

      {/* Results */}
      <Card className="flex-1 card-meridian overflow-hidden">
        <CardHeader className="border-b border-divider bg-content2/30">
          <div className="flex items-center justify-between w-full">
            <h2 className="font-semibold text-foreground">搜索结果</h2>
            {data && (
              <span className="text-sm text-default-500">
                找到 {data.count} 个结果
              </span>
            )}
          </div>
        </CardHeader>
        <CardBody className="p-0 overflow-auto custom-scrollbar">
          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <div className="text-center">
                <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
                <p className="text-default-500">搜索中...</p>
              </div>
            </div>
          ) : error ? (
            <div className="flex flex-col items-center justify-center h-40 text-rose-500">
              <p>搜索失败</p>
              <p className="text-sm text-default-500">{(error as Error).message}</p>
            </div>
          ) : !debouncedQuery ? (
            <div className="flex items-center justify-center h-40">
              <EmptyState
                icon={SearchIcon}
                title="输入关键词开始搜索"
              />
            </div>
          ) : !data?.results?.length ? (
            <div className="flex items-center justify-center h-40">
              <EmptyState
                icon={SearchIcon}
                title="未找到匹配的文件"
                description="尝试使用其他关键词"
              />
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
