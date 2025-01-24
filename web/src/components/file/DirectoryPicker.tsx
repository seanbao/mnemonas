import { useState, useCallback, useMemo, useEffect } from 'react'
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
} from 'lucide-react'
import { listFiles, createDirectory, type FileItem } from '@/api/files'
import { cn } from '@/lib/utils'

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

interface TreeNodeData {
  path: string
  name: string
  isExpanded: boolean
  children: TreeNodeData[]
  isLoaded: boolean
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
  const [selectedPath, setSelectedPath] = useState(initialPath)
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(new Set(['/']))
  const [loadedPaths, setLoadedPaths] = useState<Set<string>>(new Set())
  const [folderContents, setFolderContents] = useState<Map<string, FileItem[]>>(new Map())
  const [isCreatingFolder, setIsCreatingFolder] = useState(false)
  const [newFolderName, setNewFolderName] = useState('')
  const [isCreating, setIsCreating] = useState(false)

  useEffect(() => {
    if (!isOpen) return
    setSelectedPath(initialPath)
    setExpandedPaths(new Set(['/']))
    setLoadedPaths(new Set())
    setFolderContents(new Map())
    setIsCreatingFolder(false)
    setNewFolderName('')
  }, [isOpen, initialPath])

  
  // Load root directory
  const { data: rootData, isLoading: isLoadingRoot } = useQuery({
    queryKey: ['files', '/'],
    queryFn: () => listFiles('/'),
    enabled: isOpen,
  })

  // Load expanded directories
  const loadDirectory = useCallback(async (path: string) => {
    if (loadedPaths.has(path)) return
    
    try {
      const data = await listFiles(path)
      setFolderContents(prev => new Map(prev).set(path, data.files))
      setLoadedPaths(prev => new Set(prev).add(path))
    } catch {
      // Silently fail - directory will show as empty
    }
  }, [loadedPaths])

  const handleToggle = useCallback(async (path: string) => {
    const newExpanded = new Set(expandedPaths)
    if (newExpanded.has(path)) {
      newExpanded.delete(path)
    } else {
      newExpanded.add(path)
      // Load contents when expanding
      await loadDirectory(path)
    }
    setExpandedPaths(newExpanded)
  }, [expandedPaths, loadDirectory])

  // Build tree structure
  const buildTree = useCallback((files: FileItem[]): TreeNodeData[] => {
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
  }, [expandedPaths, folderContents, loadedPaths])

  const rootFolders = useMemo(() => {
    if (!rootData?.files) return []
    return buildTree(rootData.files)
  }, [rootData, buildTree])

  const handleCreateFolder = useCallback(async () => {
    if (!newFolderName.trim()) return
    
    setIsCreating(true)
    try {
      const newPath = selectedPath === '/' 
        ? `/${newFolderName}` 
        : `${selectedPath}/${newFolderName}`
      await createDirectory(newPath)
      
      // Reload parent directory
      const parentFiles = await listFiles(selectedPath)
      setFolderContents(prev => new Map(prev).set(selectedPath, parentFiles.files))
      setLoadedPaths(prev => new Set(prev).add(selectedPath))
      
      // Select the new folder
      setSelectedPath(newPath)
      setExpandedPaths(prev => new Set(prev).add(selectedPath))
      
      setNewFolderName('')
      setIsCreatingFolder(false)
    } catch {
      addToast({ title: '创建文件夹失败', color: 'danger' })
    } finally {
      setIsCreating(false)
    }
  }, [newFolderName, selectedPath])

  const handleConfirm = useCallback(() => {
    onSelect(selectedPath)
    onClose()
  }, [selectedPath, onSelect, onClose])

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      placement="center"
      size="md"
      scrollBehavior="inside"
      classNames={{
        base: "bg-content1 border border-divider shadow-2xl rounded-2xl max-h-[80vh]",
        backdrop: "bg-black/60 backdrop-blur-md",
        closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
      }}
    >
      <ModalContent>
        <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
          <div className="w-10 h-10 rounded-xl bg-accent-primary/10 text-accent-primary flex items-center justify-center">
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
              {selectedPath === '/' ? '根目录' : selectedPath}
            </span>
          </div>
          
          {/* Directory tree */}
          <div className="border border-divider rounded-xl p-2 min-h-[200px] max-h-[300px] overflow-auto custom-scrollbar">
            {isLoadingRoot ? (
              <div className="flex items-center justify-center h-32">
                <Spinner size="sm" />
              </div>
            ) : (
              <>
                {/* Root selector */}
                <div
                  className={cn(
                    "flex items-center gap-2 px-2 py-1.5 rounded-lg cursor-pointer transition-colors",
                    selectedPath === '/' && "bg-accent-primary/10 text-accent-primary",
                    selectedPath !== '/' && "hover:bg-content2"
                  )}
                  onClick={() => setSelectedPath('/')}
                >
                  <div className="w-5 h-5" />
                  <Home size={18} className={selectedPath === '/' ? "text-accent-primary" : "text-default-500"} />
                  <span className="text-sm">根目录</span>
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
          {allowCreateFolder && (
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
                        setIsCreatingFolder(false)
                        setNewFolderName('')
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
                    onPress={() => {
                      setIsCreatingFolder(false)
                      setNewFolderName('')
                    }}
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
          <Button variant="flat" onPress={onClose} className="text-default-600 rounded-xl">
            取消
          </Button>
          <Button 
            color="primary" 
            onPress={handleConfirm}
            className="rounded-xl"
          >
            选择此目录
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default DirectoryPicker
