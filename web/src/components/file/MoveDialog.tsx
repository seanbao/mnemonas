import { useState, useCallback, useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  Button,
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  addToast,
} from '@heroui/react'
import { Move, AlertTriangle } from 'lucide-react'
import { DirectoryPicker } from './DirectoryPicker'
import { moveFile, copyFile, ApiError } from '@/api/files'
import { FileIcon } from '@/components/ui/FileIcon'
import { useUser } from '@/stores/auth'
import { getFileQueryScopeKey, getFilesQueryKey } from '@/lib/fileQueryKey'
import { getSharedPathConflictErrorToast, getSharedQuotaExceededErrorToast } from '@/lib/fileActionErrors'

const missingMoveSourceWarningTitle = '文件或文件夹已不存在，已同步更新'

function isMissingFileError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404
}

function isAbortError(error: unknown): boolean {
  return typeof error === 'object'
    && error !== null
    && 'name' in error
    && (error as { name?: unknown }).name === 'AbortError'
}

function getMoveDialogSuccessToast(
  mode: 'move' | 'copy',
  successCount: number,
  warningMessages: string[]
): {
  title: string
  color: 'success' | 'warning'
} {
  const actionText = mode === 'move' ? '移动' : '复制'

  if (warningMessages.length > 0) {
    const synchronizedWarning = warningMessages.find(message => message === missingMoveSourceWarningTitle)
    return {
      title: synchronizedWarning ?? `成功${actionText} ${successCount} 个项目，但存在警告`,
      color: 'warning',
    }
  }

  return {
    title: `成功${actionText} ${successCount} 个项目`,
    color: 'success',
  }
}

function getMoveDialogFailureToast(
  mode: 'move' | 'copy',
  successCount: number,
  errorCount: number,
  errors: unknown[]
): {
  title: string
  description?: string
  color: 'warning' | 'danger'
} {
  const actionText = mode === 'move' ? '移动' : '复制'

  if (successCount === 0 && errors.length > 0 && errors.every((error) => error instanceof ApiError && error.isUnavailable)) {
    return {
      title: `批量${actionText}暂不可用`,
      description: '文件系统当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }

  const pathConflictToast = getSharedPathConflictErrorToast(errors)
  if (pathConflictToast) {
    return pathConflictToast
  }

  const quotaExceededToast = getSharedQuotaExceededErrorToast(errors)
  if (quotaExceededToast) {
    return quotaExceededToast
  }

  return {
    title: `批量${actionText}失败`,
    description: `共 ${errorCount} 个项目失败`,
    color: 'danger',
  }
}

export interface MoveDialogProps {
  isOpen: boolean
  onClose: () => void
  files: { path: string; name: string; isDir: boolean }[]
  currentPath: string
  mode: 'move' | 'copy'
}

function MoveDialogContent({
  onClose,
  files,
  currentPath,
  mode,
}: Omit<MoveDialogProps, 'isOpen'>) {
  const queryClient = useQueryClient()
  const user = useUser()
  const fileScopeKey = getFileQueryScopeKey(user)
  const [isPickerOpen, setIsPickerOpen] = useState(false)
  const [targetPath, setTargetPath] = useState<string | null>(null)
  const [isProcessing, setIsProcessing] = useState(false)
  const [pendingFiles, setPendingFiles] = useState(files)
  const isActiveRef = useRef(true)
  const operationAbortControllerRef = useRef<AbortController | null>(null)

  const title = mode === 'move' ? '移动到' : '复制到'
  const actionText = mode === 'move' ? '移动' : '复制'
  const Icon = mode === 'move' ? Move : Move // Could use different icons

  // Exclude paths that cannot be moved into (self and descendants)
  const excludePaths = pendingFiles.map(f => f.path)

  const abortActiveOperation = useCallback(() => {
    operationAbortControllerRef.current?.abort()
    operationAbortControllerRef.current = null
  }, [])

  useEffect(() => {
    return () => {
      isActiveRef.current = false
      abortActiveOperation()
    }
  }, [abortActiveOperation])

  const handleSelectTarget = useCallback((path: string) => {
    setTargetPath(path)
    setIsPickerOpen(false)
  }, [])

  const handleConfirm = useCallback(async () => {
    if (!targetPath || isProcessing) return

    const filesToProcess = pendingFiles
    const sourcePath = currentPath
    const destinationPath = targetPath
    const operationController = new AbortController()
    operationAbortControllerRef.current?.abort()
    operationAbortControllerRef.current = operationController
    const { signal } = operationController

    setIsProcessing(true)
    let successCount = 0
    let errorCount = 0
    const failedFiles: typeof filesToProcess = []
    const failedErrors: unknown[] = []
    const warningMessages: string[] = []

    try {
      for (const file of filesToProcess) {
        if (signal.aborted || !isActiveRef.current) {
          return
        }

        const fileName = file.path.split('/').pop() || ''
        const destPath = destinationPath === '/' ? `/${fileName}` : `${destinationPath}/${fileName}`

        try {
          if (mode === 'move') {
            const result = await moveFile(file.path, destPath, { signal })
            if (signal.aborted || !isActiveRef.current) {
              return
            }
            if (result.warning) {
              warningMessages.push(result.message ?? '')
            }
          } else {
            const result = await copyFile(file.path, destPath, { signal })
            if (signal.aborted || !isActiveRef.current) {
              return
            }
            if (result.warning) {
              warningMessages.push(result.message ?? '')
            }
          }
          successCount++
        } catch (error) {
          if (signal.aborted || isAbortError(error) || !isActiveRef.current) {
            return
          }

          if (isMissingFileError(error)) {
            successCount++
            warningMessages.push(missingMoveSourceWarningTitle)
            continue
          }

          errorCount++
          failedFiles.push(file)
          failedErrors.push(error)
        }
      }

      if (signal.aborted || !isActiveRef.current) {
        return
      }

      // Invalidate queries
      queryClient.invalidateQueries({ queryKey: getFilesQueryKey(fileScopeKey, sourcePath) })
      queryClient.invalidateQueries({ queryKey: getFilesQueryKey(fileScopeKey, destinationPath) })

      // Show result
      if (errorCount === 0) {
        if (isActiveRef.current) {
          onClose()
        }
        addToast(getMoveDialogSuccessToast(mode, successCount, warningMessages))
        return
      }

      if (isActiveRef.current) {
        setPendingFiles(failedFiles)
        setIsProcessing(false)
      }

      if (successCount > 0) {
        addToast({
          title: `批量${actionText}部分完成`,
          description: `成功 ${successCount} 个，失败 ${errorCount} 个`,
          color: 'warning',
        })
      } else {
        addToast(getMoveDialogFailureToast(mode, successCount, errorCount, failedErrors))
      }
    } finally {
      if (operationAbortControllerRef.current === operationController) {
        operationAbortControllerRef.current = null
      }
    }
  }, [targetPath, isProcessing, pendingFiles, mode, currentPath, fileScopeKey, queryClient, onClose, actionText])

  const handleClose = useCallback(() => {
    if (isProcessing) {
      return
    }
    onClose()
  }, [isProcessing, onClose])

  return (
    <>
      <Modal
        isOpen={!isPickerOpen}
        onClose={handleClose}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-xl rounded-lg",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-lg bg-accent-primary/10 text-accent-primary flex items-center justify-center">
              <Icon size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">{title}</h3>
              <p className="text-xs text-default-500 font-normal">
                {pendingFiles.length === 1 ? pendingFiles[0].name : `${pendingFiles.length} 个项目`}
              </p>
            </div>
          </ModalHeader>

          <ModalBody className="px-6 py-4">
            {/* Files to move */}
            <div className="mb-4">
              <div className="text-xs font-medium text-default-500 mb-2">
                {actionText}以下项目:
              </div>
              <div className="max-h-32 overflow-y-auto space-y-1 border border-divider rounded-lg p-2">
                {pendingFiles.slice(0, 5).map(file => (
                  <div key={file.path} className="flex items-center gap-2 py-1">
                    <FileIcon name={file.name} isDir={file.isDir} size={20} variant="bare" />
                    <span className="text-sm truncate">{file.name}</span>
                  </div>
                ))}
                {pendingFiles.length > 5 && (
                  <div className="text-xs text-default-500 py-1">
                    ...还有 {pendingFiles.length - 5} 个项目
                  </div>
                )}
              </div>
            </div>

            {/* Target selection */}
            <div>
              <div className="text-xs font-medium text-default-500 mb-2">
                目标位置:
              </div>
              <Button
                variant="bordered"
                className="w-full justify-start rounded-lg"
                onPress={() => setIsPickerOpen(true)}
              >
                {targetPath ? (
                  <span className="truncate">
                    {targetPath === '/' ? '根目录' : targetPath}
                  </span>
                ) : (
                  <span className="text-default-400">点击选择目标文件夹</span>
                )}
              </Button>
            </div>

            {/* Warning for same directory */}
            {targetPath === currentPath && (
              <div className="flex items-center gap-2 mt-4 p-3 bg-warning/10 rounded-lg text-warning">
                <AlertTriangle size={16} />
                <span className="text-sm">目标目录与当前目录相同</span>
              </div>
            )}
          </ModalBody>

          <ModalFooter className="px-6 pb-6 pt-2 gap-2">
            <Button variant="flat" onPress={handleClose} isDisabled={isProcessing} className="text-default-600 rounded-lg">
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleConfirm}
              isLoading={isProcessing}
              isDisabled={!targetPath || targetPath === currentPath}
              className="rounded-lg"
            >
              {actionText}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      {/* Directory picker */}
      <DirectoryPicker
        isOpen={isPickerOpen}
        onClose={() => setIsPickerOpen(false)}
        onSelect={handleSelectTarget}
        title={`选择${actionText}目标`}
        description="选择要将文件放入的文件夹"
        excludePaths={excludePaths}
        initialPath={currentPath}
      />
    </>
  )
}

export function MoveDialog({
  isOpen,
  onClose,
  files,
  currentPath,
  mode,
}: MoveDialogProps) {
  if (!isOpen) {
    return null
  }

  const dialogKey = `${mode}:${currentPath}:${files.map((file) => `${file.path}:${file.isDir ? 'dir' : 'file'}`).join('|')}`

  return (
    <MoveDialogContent
      key={dialogKey}
      onClose={onClose}
      files={files}
      currentPath={currentPath}
      mode={mode}
    />
  )
}

export default MoveDialog
