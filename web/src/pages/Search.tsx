import { useCallback, useDeferredValue } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  Card,
  CardBody,
  CardHeader,
  Input,
  Button,
  addToast,
} from '@heroui/react'
import {
  Search as SearchIcon,
  ArrowLeft,
  AlertCircle,
} from 'lucide-react'
import { PageHeader } from '@/components/ui/PageHeader'
import { FileIcon } from '@/components/ui/FileIcon'
import { EmptyState } from '@/components/ui/EmptyState'
import { searchFiles, SearchError, type SearchResult } from '@/api/search'
import { formatBytes, formatDate, cn } from '@/lib/utils'

const searchUnavailableDescription = '文件系统当前不可用，请稍后重试'

function getSearchErrorPresentation(error: unknown): {
  title: string
  description: string
} {
  if (error instanceof SearchError && error.isUnavailable) {
    return {
      title: '搜索暂不可用',
      description: searchUnavailableDescription,
    }
  }

  return {
    title: '搜索失败',
    description: error instanceof Error && error.message ? error.message : '请稍后重试',
  }
}

function getSearchRefreshErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  const presentation = getSearchErrorPresentation(error)
  if (error instanceof SearchError && error.isUnavailable) {
    return {
      ...presentation,
      color: 'warning',
    }
  }

  return {
    ...presentation,
    color: 'danger',
  }
}

// Search result item component
function SearchResultItem({ result, onClick }: { result: SearchResult; onClick: () => void }) {
  return (
    <button
      type="button"
      className={cn(
        "flex w-full items-center gap-3 px-4 py-2.5 text-left transition-all duration-150",
        "hover:bg-content2 border-b border-divider last:border-b-0",
        "focus:outline-none focus:bg-content2"
      )}
      onClick={onClick}
      aria-label={`${result.isDir ? '打开文件夹' : '打开文件'} ${result.path}`}
    >
      <div className="flex-shrink-0">
        <FileIcon name={result.name} isDir={result.isDir} size={24} variant="bare" />
      </div>
      <div className="flex-1 min-w-0">
        <div className="font-medium text-foreground truncate">{result.name}</div>
        <div className="text-xs text-default-500 truncate">{result.path}</div>
      </div>
      <div className="flex-shrink-0 text-right text-sm text-default-500">
        {result.isDir ? (
          <span>文件夹</span>
        ) : (
          <span>{formatBytes(result.size)}</span>
        )}
      </div>
      <div className="flex-shrink-0 text-right text-xs text-default-500 w-24">
        {formatDate(result.modTime)}
      </div>
    </button>
  )
}

export function SearchPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const query = searchParams.get('q') || ''
  const trimmedQuery = query.trim()
  const trimmedDebouncedQuery = useDeferredValue(trimmedQuery)

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['search', trimmedDebouncedQuery],
    queryFn: () => searchFiles(trimmedDebouncedQuery),
    enabled: trimmedDebouncedQuery.length > 0,
  })

  const handleRetrySearch = useCallback(async () => {
    const result = await refetch()
    if (result.error) {
      addToast(getSearchRefreshErrorToast(result.error))
      return
    }
    addToast({ title: '搜索结果已刷新', color: 'success' })
  }, [refetch])

  const handleResultClick = useCallback((result: SearchResult) => {
    if (result.isDir) {
      navigate(`/files${encodeURI(result.path)}`)
    } else {
      // Navigate to parent directory and highlight the file
      const parentPath = result.path.substring(0, result.path.lastIndexOf('/')) || '/'
      navigate(`/files${encodeURI(parentPath)}`, { state: { highlightPath: result.path } })
    }
  }, [navigate])

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      if (trimmedQuery) {
        setSearchParams({ q: query }, { replace: true })
      } else {
        setSearchParams({}, { replace: true })
      }
    }
  }, [query, setSearchParams, trimmedQuery])

  const handleQueryChange = useCallback((value: string) => {
    if (value.trim()) {
      setSearchParams({ q: value }, { replace: true })
      return
    }
    setSearchParams({}, { replace: true })
  }, [setSearchParams])

  return (
    <div className="h-full flex flex-col p-6">
      {/* Header */}
      <div className="flex items-center gap-4 mb-6">
        <Button
          isIconOnly
          variant="light"
          aria-label="返回上一页"
          onPress={() => navigate(-1)}
          className="text-default-500 rounded-xl"
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
          onValueChange={handleQueryChange}
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
            <div className="flex items-center justify-center h-40 p-6">
              <EmptyState
                icon={AlertCircle}
                title={getSearchErrorPresentation(error).title}
                description={getSearchErrorPresentation(error).description}
                action={
                  <Button variant="bordered" className="rounded-xl" onPress={handleRetrySearch}>
                    重试搜索
                  </Button>
                }
              />
            </div>
          ) : !trimmedDebouncedQuery ? (
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
