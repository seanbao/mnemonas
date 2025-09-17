import { useState, useCallback, useMemo, useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Button,
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Input,
  Spinner,
  addToast,
} from '@heroui/react'
import {
  Folder,
  FolderOpen,
  ChevronRight,
  ChevronDown,
  Home,
  FolderPlus,
  AlertCircle,
} from 'lucide-react'
import { listFiles, createDirectory, ApiError, type FileItem } from '@/api/files'
import { EmptyState } from '@/components/ui/EmptyState'
import { useUser } from '@/stores/auth'
import { getFileQueryScopeKey, getFilesQueryKey } from '@/lib/fileQueryKey'
import { getPathConflictErrorToast } from '@/lib/fileActionErrors'
import { GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { cn, normalizePath } from '@/lib/utils'
import { getInvalidHomeDirDescription, invalidHomeDirTitle, resolveUserHomeScope } from '@/lib/userScope'

export interface DirectoryPickerProps {
  isOpen: boolean
  onClose: () => void
  onSelect: (path: string) => void
  title?: string
  description?: string
  excludePaths?: string[]
  initialPath?: string
  allowCreateFolder?: boolean
}

function getDirectoryPickerRetryErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  return getDirectoryPickerErrorPresentation(error, {
    unavailable: '目录暂不可用',
    failure: '加载目录失败',
  }, GENERIC_LOAD_ERROR_DESCRIPTION)
}

interface TreeNodeData {
  path: string
  name: string
  isExpanded: boolean
  children: TreeNodeData[]
  isLoaded: boolean
}

const directoryPickerUnavailableDescription = '文件系统当前不可用，请检查设备状态或稍后重试。'

function getDirectoryPickerErrorPresentation(
  error: unknown,
  titles: {
    unavailable: string
    failure: string
  },
  fallbackDescription?: string
): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: titles.unavailable,
      description: directoryPickerUnavailableDescription,
      color: 'warning',
    }
  }

  return {
    title: titles.failure,
    description: getUserFacingErrorDescription(error, fallbackDescription),
    color: 'danger',
  }
}

function getDirectoryPickerCreateSuccessToast(): {
  title: string
  color: 'warning'
} {
  return {
    title: '文件夹创建完成，但存在警告',
    color: 'warning',
  }
}

function getDirectoryPickerCreateToast(result: { warning: boolean; message?: string }): {
  title: string
  color: 'warning'
} | null {
  if (result.warning) {
    return getDirectoryPickerCreateSuccessToast()
  }

  if (result.message === 'directory already exists') {
    return {
      title: '文件夹已存在，已同步更新',
      color: 'warning',
    }
  }

  return null
}

function getDirectoryPickerCreateErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  const pathConflictToast = getPathConflictErrorToast(error)
  if (pathConflictToast) {
    return pathConflictToast
  }

  return getDirectoryPickerErrorPresentation(error, {
    unavailable: '创建目录暂不可用',
    failure: '创建文件夹失败',
  })
}

function pathWithinBase(basePath: string, targetPath: string): boolean {
  if (basePath === '/') {
    return targetPath.startsWith('/')
  }
  return targetPath === basePath || targetPath.startsWith(`${basePath}/`)
}

function pathDisplayName(filePath: string): string {
  const normalized = normalizePath(filePath)
  if (normalized === '/') {
    return '根目录'
  }
  return normalized.split('/').filter(Boolean).pop() || normalized
}

function TreeNode({
  node,
  level,
  selectedPath,
  excludePaths,
  onSelect,
  onToggle,
}: {
  node: TreeNodeData
  level: number
  selectedPath: string
  excludePaths: string[]
  onSelect: (path: string) => void
  onToggle: (path: string) => void
}) {
  const isSelected = selectedPath === node.path
  const isExcluded = excludePaths.some(p => 
    node.path === p || node.path.startsWith(p + '/')
  )

  return (
    <div>
      <div
        className={cn(
          "flex items-center gap-2 px-2 py-1.5 rounded-lg cursor-pointer transition-colors",
          isSelected && "bg-accent-primary/10 text-accent-primary",
          !isSelected && !isExcluded && "hover:bg-content2",
          isExcluded && "opacity-50 cursor-not-allowed"
        )}
        style={{ paddingLeft: `${level * 16 + 8}px` }}
        onClick={() => {
          if (!isExcluded) {
            onSelect(node.path)
          }
        }}
      >
        <button
          className="w-5 h-5 flex items-center justify-center rounded hover:bg-content2/50"
          onClick={(e) => {
            e.stopPropagation()
            if (!isExcluded) {
              onToggle(node.path)
            }
          }}
        >
          {node.isExpanded ? (
            <ChevronDown size={14} className="text-default-500" />
          ) : (
            <ChevronRight size={14} className="text-default-500" />
          )}
        </button>
        {node.isExpanded ? (
          <FolderOpen size={18} className={isSelected ? "text-accent-primary" : "text-default-500"} />
        ) : (
          <Folder size={18} className={isSelected ? "text-accent-primary" : "text-default-500"} />
        )}
        <span className={cn("text-sm truncate", isExcluded && "line-through")}>
          {node.name}
        </span>
      </div>
      
      {node.isExpanded && node.children.length > 0 && (
        <div>
          {node.children.map(child => (
            <TreeNode
              key={child.path}
              node={child}
              level={level + 1}
              selectedPath={selectedPath}
              excludePaths={excludePaths}
              onSelect={onSelect}
              onToggle={onToggle}
            />
          ))}
        </div>
      )}
    </div>
  )
}

export function DirectoryPicker({
  isOpen,
  onClose,
  onSelect,
  title = "选择目录",
  description = "选择目标文件夹",
  excludePaths = [],
  initialPath = '/',
  allowCreateFolder = true,
}: DirectoryPickerProps) {
  const user = useUser()
  const fileScopeKey = getFileQueryScopeKey(user)
  const { rootPath, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const requestedInitialPath = useMemo(() => {
    if (!rootPath) {
      return '/'
    }
    try {
      return normalizePath(initialPath || rootPath)
    } catch {
      return rootPath
    }
  }, [initialPath, rootPath])
  const effectiveRootPath = useMemo(() => {
    if (!rootPath) {
      return '/'
    }
    if (requestedInitialPath !== '/' && !pathWithinBase(rootPath, requestedInitialPath)) {
      return requestedInitialPath
    }
    return rootPath
  }, [requestedInitialPath, rootPath])
  const rootFilesQueryKey = getFilesQueryKey(fileScopeKey, effectiveRootPath)
  const rootLabel = effectiveRootPath === '/' ? '根目录' : effectiveRootPath === rootPath ? '主目录' : pathDisplayName(effectiveRootPath)
  const normalizeInitialPath = useCallback((path: string) => {
    if (!rootPath) {
      return '/'
    }
    const normalized = normalizePath(path || effectiveRootPath)
    return pathWithinBase(effectiveRootPath, normalized) ? normalized : effectiveRootPath
  }, [effectiveRootPath, rootPath])

  const [selectedPath, setSelectedPath] = useState(() => normalizeInitialPath(initialPath))
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(new Set([effectiveRootPath]))
  const [loadedPaths, setLoadedPaths] = useState<Set<string>>(new Set())
  const [folderContents, setFolderContents] = useState<Map<string, FileItem[]>>(new Map())
  const [isCreatingFolder, setIsCreatingFolder] = useState(false)
  const [newFolderName, setNewFolderName] = useState('')
  const [isCreating, setIsCreating] = useState(false)
  const pickerSessionRef = useRef(0)
  const currentSelectedPathRef = useRef(normalizeInitialPath(initialPath))
  const currentNewFolderNameRef = useRef('')
  const currentOpenRef = useRef(isOpen)
  const directoryLoadControllersRef = useRef(new Map<string, AbortController>())
  const createFolderControllerRef = useRef<AbortController | null>(null)
  const createFolderReloadControllerRef = useRef<AbortController | null>(null)
  const lastResetTargetRef = useRef({
    rootPath: effectiveRootPath,
    selectedPath: normalizeInitialPath(initialPath),
  })

  const abortDirectoryLoads = useCallback(() => {
    for (const controller of directoryLoadControllersRef.current.values()) {
      controller.abort()
    }
    directoryLoadControllersRef.current.clear()
  }, [])

  const abortCreateFolderRequest = useCallback(() => {
    createFolderControllerRef.current?.abort()
    createFolderControllerRef.current = null
  }, [])

  const abortCreateFolderReload = useCallback(() => {
    createFolderReloadControllerRef.current?.abort()
    createFolderReloadControllerRef.current = null
  }, [])

  useEffect(() => {
    return () => {
      currentOpenRef.current = false
      abortDirectoryLoads()
      abortCreateFolderRequest()
      abortCreateFolderReload()
    }
  }, [abortDirectoryLoads, abortCreateFolderRequest, abortCreateFolderReload])

  useEffect(() => {
    if (!isOpen) {
      abortDirectoryLoads()
      abortCreateFolderRequest()
      abortCreateFolderReload()
    }
  }, [isOpen, abortDirectoryLoads, abortCreateFolderRequest, abortCreateFolderReload])

  useEffect(() => {
    currentSelectedPathRef.current = selectedPath
  }, [selectedPath])

  useEffect(() => {
    currentNewFolderNameRef.current = newFolderName
  }, [newFolderName])

  useEffect(() => {
    const wasOpen = currentOpenRef.current
    currentOpenRef.current = isOpen
    if (!isOpen) return
    const nextSelectedPath = normalizeInitialPath(initialPath)
    const lastResetTarget = lastResetTargetRef.current
    if (
      wasOpen
      && lastResetTarget.rootPath === effectiveRootPath
      && lastResetTarget.selectedPath === nextSelectedPath
    ) {
      return
    }

    lastResetTargetRef.current = {
      rootPath: effectiveRootPath,
      selectedPath: nextSelectedPath,
    }
    abortDirectoryLoads()
    abortCreateFolderRequest()
    abortCreateFolderReload()
    pickerSessionRef.current += 1
    let cancelled = false
    queueMicrotask(() => {
      if (cancelled) return
      setSelectedPath(nextSelectedPath)
      setExpandedPaths(new Set([effectiveRootPath]))
      setLoadedPaths(new Set())
      setFolderContents(new Map())
      setIsCreatingFolder(false)
      setNewFolderName('')
    })

    return () => {
      cancelled = true
    }
  }, [isOpen, initialPath, normalizeInitialPath, effectiveRootPath, abortDirectoryLoads, abortCreateFolderRequest, abortCreateFolderReload])

  
  // Load root directory
  const { data: rootData, error: rootError, isLoading: isLoadingRoot, refetch: refetchRoot } = useQuery({
    queryKey: rootFilesQueryKey,
    queryFn: ({ signal }) => listFiles(effectiveRootPath, { signal }),
    enabled: isOpen && !hasInvalidHomeDir,
  })

  useEffect(() => {
    if (!isOpen || !rootData?.files) {
      return
    }

    let cancelled = false
    queueMicrotask(() => {
      if (cancelled) return
      setFolderContents((prev) => {
        const next = new Map(prev)
        next.set(effectiveRootPath, rootData.files)
        return next
      })
      setLoadedPaths((prev) => new Set(prev).add(effectiveRootPath))
    })

    return () => {
      cancelled = true
    }
  }, [isOpen, rootData, effectiveRootPath])

  const handleRetryRoot = useCallback(async () => {
  const result = await refetchRoot()
  if (result.error) {
    addToast(getDirectoryPickerRetryErrorToast(result.error))
    return
  }
  addToast({ title: '目录已刷新', color: 'success' })
  }, [refetchRoot])

  // Load expanded directories
  const loadDirectory = useCallback(async (path: string) => {
    if (loadedPaths.has(path)) return true
    const sessionId = pickerSessionRef.current
    directoryLoadControllersRef.current.get(path)?.abort()
    const controller = new AbortController()
    directoryLoadControllersRef.current.set(path, controller)
    
    try {
      const data = await listFiles(path, { signal: controller.signal })
      if (controller.signal.aborted || pickerSessionRef.current !== sessionId || !currentOpenRef.current) {
        return false
      }
      setFolderContents(prev => new Map(prev).set(path, data.files))
      setLoadedPaths(prev => new Set(prev).add(path))
      return true
    } catch (error) {
      if (controller.signal.aborted) {
        return false
      }
      addToast(getDirectoryPickerErrorPresentation(error, {
        unavailable: '目录暂不可用',
        failure: '加载目录失败',
      }, GENERIC_LOAD_ERROR_DESCRIPTION))
      return false
    } finally {
      if (directoryLoadControllersRef.current.get(path) === controller) {
        directoryLoadControllersRef.current.delete(path)
      }
    }
  }, [loadedPaths])

  const handleToggle = useCallback(async (path: string) => {
    const newExpanded = new Set(expandedPaths)
    if (newExpanded.has(path)) {
      newExpanded.delete(path)
      setExpandedPaths(newExpanded)
    } else {
      // Load contents when expanding
      const loaded = await loadDirectory(path)
      if (!loaded) {
        return
      }
      newExpanded.add(path)
      setExpandedPaths(newExpanded)
    }
  }, [expandedPaths, loadDirectory])

  // Build tree structure
  const rootFolders = useMemo(() => {
    const rootFiles = folderContents.get(effectiveRootPath)
    if (!rootFiles) return []

    function buildTree(files: FileItem[]): TreeNodeData[] {
      const folders = files.filter(f => f.isDir)
      return folders.map(folder => {
        const childFiles = folderContents.get(folder.path) || []
        return {
          path: folder.path,
          name: folder.name,
          isExpanded: expandedPaths.has(folder.path),
          children: expandedPaths.has(folder.path) ? buildTree(childFiles) : [],
          isLoaded: loadedPaths.has(folder.path),
        }
      })
    }

    return buildTree(rootFiles)
  }, [expandedPaths, folderContents, loadedPaths, effectiveRootPath])

  const handleCreateFolder = useCallback(async () => {
    const trimmedFolderName = newFolderName.trim()
    if (!trimmedFolderName) return

    const sessionId = pickerSessionRef.current
    const parentPath = selectedPath
    abortCreateFolderRequest()
    const createController = new AbortController()
    createFolderControllerRef.current = createController
    let reloadController: AbortController | null = null
    
    setIsCreating(true)
    try {
      const newPath = parentPath === '/' 
        ? `/${trimmedFolderName}` 
        : `${parentPath}/${trimmedFolderName}`
      const result = await createDirectory(newPath, { signal: createController.signal })
      if (createController.signal.aborted || pickerSessionRef.current !== sessionId || !currentOpenRef.current) {
        return
      }
      
      // Reload parent directory
      abortCreateFolderReload()
      reloadController = new AbortController()
      createFolderReloadControllerRef.current = reloadController
      const parentFiles = await listFiles(parentPath, { signal: reloadController.signal })

      if (
        !reloadController.signal.aborted
        &&
        pickerSessionRef.current === sessionId
        && currentOpenRef.current
        && currentSelectedPathRef.current === parentPath
        && currentNewFolderNameRef.current.trim() === trimmedFolderName
      ) {
        setFolderContents(prev => new Map(prev).set(parentPath, parentFiles.files))
        setLoadedPaths(prev => new Set(prev).add(parentPath))
        
        // Select the new folder
        setSelectedPath(newPath)
        setExpandedPaths(prev => new Set(prev).add(parentPath))
        
        setNewFolderName('')
        setIsCreatingFolder(false)
      }

      const createToast = getDirectoryPickerCreateToast(result)
      if (createToast) {
        addToast(createToast)
      }
    } catch (error) {
      if (
        createController.signal.aborted
        ||
        reloadController?.signal.aborted
        || pickerSessionRef.current !== sessionId
        || !currentOpenRef.current
      ) {
        return
      }
      addToast(getDirectoryPickerCreateErrorToast(error))
    } finally {
      if (createFolderControllerRef.current === createController) {
        createFolderControllerRef.current = null
      }
      if (createFolderReloadControllerRef.current === reloadController) {
        createFolderReloadControllerRef.current = null
      }
      if (pickerSessionRef.current === sessionId && currentOpenRef.current) {
        setIsCreating(false)
      }
    }
  }, [newFolderName, selectedPath, abortCreateFolderRequest, abortCreateFolderReload])

  const handleCancelCreateFolder = useCallback(() => {
    if (isCreating) {
      return
    }
    setIsCreatingFolder(false)
    setNewFolderName('')
  }, [isCreating])

  const handleClosePicker = useCallback(() => {
    if (isCreating) {
      return
    }
    onClose()
  }, [isCreating, onClose])

  const handleConfirm = useCallback(() => {
    if (isCreating) {
      return
    }
    onSelect(selectedPath)
    onClose()
  }, [isCreating, onClose, onSelect, selectedPath])

  return (
    <Modal
      isOpen={isOpen}
      onClose={handleClosePicker}
      placement="center"
      size="md"
      scrollBehavior="inside"
      classNames={{
        base: "bg-content1 border border-divider shadow-xl rounded-lg max-h-[80vh]",
        backdrop: "bg-black/60 backdrop-blur-md",
        closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
      }}
    >
      <ModalContent>
        <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
          <div className="w-10 h-10 rounded-lg bg-accent-primary/10 text-accent-primary flex items-center justify-center">
            <Folder size={20} />
          </div>
          <div>
            <h3 className="text-lg font-semibold text-foreground">{title}</h3>
            <p className="text-xs text-default-500 font-normal">{description}</p>
          </div>
        </ModalHeader>
        
        <ModalBody className="px-6 py-4">
          {/* Selected path display */}
          <div className="flex items-center gap-2 mb-4 p-2 bg-content2 rounded-lg">
            <Home size={14} className="text-default-500" />
            <span className="text-sm text-default-600 truncate">
              {selectedPath === effectiveRootPath && (effectiveRootPath === '/' || effectiveRootPath === rootPath) ? rootLabel : selectedPath}
            </span>
          </div>
          
          {/* Directory tree */}
          <div className="border border-divider rounded-lg p-2 min-h-[200px] max-h-[300px] overflow-auto custom-scrollbar">
            {hasInvalidHomeDir ? (
              <div className="flex items-center justify-center h-32">
                <EmptyState
                  icon={AlertCircle}
                  title={invalidHomeDirTitle}
                  description={getInvalidHomeDirDescription('选择目录')}
                  className="max-w-md"
                />
              </div>
            ) : isLoadingRoot ? (
              <div className="flex items-center justify-center h-32">
                <Spinner size="sm" />
              </div>
            ) : rootError ? (
              (() => {
                const errorPresentation = getDirectoryPickerErrorPresentation(rootError, {
                  unavailable: '目录暂不可用',
                  failure: '加载目录失败',
                }, GENERIC_LOAD_ERROR_DESCRIPTION)

                return (
              <div className="flex h-32 flex-col items-center justify-center gap-3 text-center">
                <AlertCircle size={20} className={errorPresentation.color === 'warning' ? 'text-warning' : 'text-danger'} />
                <div>
                  <p className="text-sm font-medium text-foreground">{errorPresentation.title}</p>
                  <p className="text-xs text-default-500">{errorPresentation.description}</p>
                </div>
                <Button size="sm" variant="bordered" className="rounded-lg" onPress={handleRetryRoot}>
                  重新加载
                </Button>
              </div>
                )
              })()
            ) : (
              <>
                {/* Root selector */}
                <div
                  className={cn(
                    "flex items-center gap-2 px-2 py-1.5 rounded-lg cursor-pointer transition-colors",
                    selectedPath === effectiveRootPath && "bg-accent-primary/10 text-accent-primary",
                    selectedPath !== effectiveRootPath && "hover:bg-content2"
                  )}
                  onClick={() => setSelectedPath(effectiveRootPath)}
                >
                  <div className="w-5 h-5" />
                  <Home size={18} className={selectedPath === effectiveRootPath ? "text-accent-primary" : "text-default-500"} />
                  <span className="text-sm">{rootLabel}</span>
                </div>
                
                {/* Folder tree */}
                {rootFolders.map(node => (
                  <TreeNode
                    key={node.path}
                    node={node}
                    level={0}
                    selectedPath={selectedPath}
                    excludePaths={excludePaths}
                    onSelect={setSelectedPath}
                    onToggle={handleToggle}
                  />
                ))}
                
                {rootFolders.length === 0 && (
                  <div className="text-center py-8 text-default-500 text-sm">
                    当前目录没有子文件夹
                  </div>
                )}
              </>
            )}
          </div>
          
          {/* Create folder section */}
          {allowCreateFolder && !hasInvalidHomeDir && (
            <div className="mt-4">
              {isCreatingFolder ? (
                <div className="flex items-center gap-2">
                  <Input
                    placeholder="新文件夹名称"
                    value={newFolderName}
                    onValueChange={setNewFolderName}
                    size="sm"
                    variant="bordered"
                    autoFocus
                    classNames={{
                      inputWrapper: "rounded-lg",
                    }}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') handleCreateFolder()
                      if (e.key === 'Escape') {
                        handleCancelCreateFolder()
                      }
                    }}
                  />
                  <Button
                    size="sm"
                    color="primary"
                    isLoading={isCreating}
                    isDisabled={!newFolderName.trim()}
                    onPress={handleCreateFolder}
                    className="rounded-lg"
                  >
                    创建
                  </Button>
                  <Button
                    size="sm"
                    variant="flat"
                    onPress={handleCancelCreateFolder}
                    isDisabled={isCreating}
                    className="rounded-lg"
                  >
                    取消
                  </Button>
                </div>
              ) : (
                <Button
                  size="sm"
                  variant="flat"
                  startContent={<FolderPlus size={14} />}
                  onPress={() => setIsCreatingFolder(true)}
                  className="rounded-lg"
                >
                  在此处新建文件夹
                </Button>
              )}
            </div>
          )}
        </ModalBody>
        
        <ModalFooter className="px-6 pb-6 pt-2 gap-2">
          <Button variant="flat" onPress={handleClosePicker} isDisabled={isCreating} className="text-default-600 rounded-lg">
            取消
          </Button>
          <Button 
            color="primary" 
            onPress={handleConfirm}
            isDisabled={isCreating || hasInvalidHomeDir}
            className="rounded-lg"
          >
            选择此目录
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default DirectoryPicker
