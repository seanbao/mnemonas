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
import { encodePathForUrl, formatBytes, formatDate, cn } from '@/lib/utils'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'
import { useIsAdmin, useUser } from '@/stores/auth'

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
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
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
        "flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-3 py-3 text-left transition-all duration-150 sm:flex-nowrap sm:px-4 sm:py-2.5",
        "hover:bg-content2 border-b border-divider last:border-b-0",
        "focus:outline-none focus:bg-content2"
      )}
      onClick={onClick}
      aria-label={`${result.isDir ? '打开文件夹' : '打开文件'} ${result.path}`}
    >
      <div className="flex-shrink-0">
        <FileIcon name={result.name} isDir={result.isDir} size={24} variant="bare" />
      </div>
      <div className="min-w-0 flex-1 basis-[calc(100%-3rem)] sm:basis-auto">
        <div className="font-medium text-foreground truncate">{result.name}</div>
        <div className="text-xs text-default-500 truncate">{result.path}</div>
      </div>
      <div className="ml-9 flex-shrink-0 text-left text-sm text-default-500 sm:ml-0 sm:text-right">
        {result.isDir ? (
          <span>文件夹</span>
        ) : (
          <span>{formatBytes(result.size)}</span>
        )}
      </div>
      <div className="hidden w-24 flex-shrink-0 text-right text-xs text-default-500 sm:block">
        {formatDate(result.modTime)}
      </div>
    </button>
  )
}

export function SearchPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const user = useUser()
  const isAdmin = useIsAdmin()
  const query = searchParams.get('q') || ''
  const trimmedQuery = query.trim()
  const trimmedDebouncedQuery = useDeferredValue(trimmedQuery)
  const { rootPath, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const authScopeKey = user?.id ?? 'anonymous'
  const homeScopeKey = hasInvalidHomeDir ? '__invalid__' : (rootPath ?? '/')

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['search', authScopeKey, isAdmin, homeScopeKey, trimmedDebouncedQuery],
    queryFn: ({ signal }) => searchFiles(trimmedDebouncedQuery, { signal }),
    enabled: !hasInvalidHomeDir && trimmedDebouncedQuery.length > 0,
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
      navigate(`/files${encodePathForUrl(result.path)}`)
    } else {
      // Navigate to parent directory and highlight the file
      const parentPath = result.path.substring(0, result.path.lastIndexOf('/')) || '/'
      navigate(`/files${encodePathForUrl(parentPath)}`, { state: { highlightPath: result.path } })
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
    <div className="flex h-full min-h-0 flex-col p-4 sm:p-6">
      {/* Header */}
      <div className="mb-6 flex min-w-0 items-center gap-3 sm:gap-4">
        <Button
          isIconOnly
          variant="light"
          aria-label="返回上一页"
          onPress={() => navigate(-1)}
          className="text-default-500 rounded-lg"
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
          isDisabled={hasInvalidHomeDir}
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
      <Card className="card-meridian min-h-0 flex-1 overflow-hidden">
        <CardHeader className="border-b border-divider bg-content2/30">
          <div className="flex w-full min-w-0 flex-wrap items-center justify-between gap-2">
            <h2 className="font-semibold text-foreground">搜索结果</h2>
            {data && (
              <span className="text-sm text-default-500">
                找到 {data.count} 个结果
              </span>
            )}
          </div>
        </CardHeader>
        <CardBody className="p-0 overflow-auto custom-scrollbar">
          {hasInvalidHomeDir ? (
            <div className="flex items-center justify-center h-40 p-6">
              <EmptyState
                icon={AlertCircle}
                title={invalidHomeDirTitle}
                description={getInvalidHomeDirDescription('搜索文件')}
              />
            </div>
          ) : isLoading ? (
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
                  <Button variant="bordered" className="rounded-lg" onPress={handleRetrySearch}>
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
