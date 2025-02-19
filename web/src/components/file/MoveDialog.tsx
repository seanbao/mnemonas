import { useState, useCallback } from 'react'
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
import { moveFile, copyFile } from '@/api/files'
import { FileIcon } from '@/components/ui/FileIcon'

export interface MoveDialogProps {
  isOpen: boolean
  onClose: () => void
  files: { path: string; name: string; isDir: boolean }[]
  currentPath: string
  mode: 'move' | 'copy'
}

export function MoveDialog({
  isOpen,
  onClose,
  files,
  currentPath,
  mode,
}: MoveDialogProps) {
  const queryClient = useQueryClient()
  const [isPickerOpen, setIsPickerOpen] = useState(false)
  const [targetPath, setTargetPath] = useState<string | null>(null)
  const [isProcessing, setIsProcessing] = useState(false)

  const title = mode === 'move' ? '移动到' : '复制到'
  const actionText = mode === 'move' ? '移动' : '复制'
  const Icon = mode === 'move' ? Move : Move // Could use different icons

  // Exclude paths that cannot be moved into (self and descendants)
  const excludePaths = files.map(f => f.path)

  const handleSelectTarget = useCallback((path: string) => {
    setTargetPath(path)
    setIsPickerOpen(false)
  }, [])

  const handleConfirm = useCallback(async () => {
    if (!targetPath) return

    setIsProcessing(true)
    let successCount = 0
    let errorCount = 0
    const errors: string[] = []

    for (const file of files) {
      const fileName = file.path.split('/').pop() || ''
      const destPath = targetPath === '/' ? `/${fileName}` : `${targetPath}/${fileName}`

      try {
        if (mode === 'move') {
          await moveFile(file.path, destPath)
        } else {
          await copyFile(file.path, destPath)
        }
        successCount++
      } catch (error) {
        errorCount++
        errors.push(file.name)
      }
    }

    // Invalidate queries
    queryClient.invalidateQueries({ queryKey: ['files', currentPath] })
    queryClient.invalidateQueries({ queryKey: ['files', targetPath] })

    setIsProcessing(false)
    onClose()

    // Show result
    if (errorCount === 0) {
      addToast({
        title: `成功${actionText} ${successCount} 个项目`,
        color: 'success',
      })
    } else if (successCount > 0) {
      addToast({
        title: `${successCount} 个成功，${errorCount} 个失败`,
        description: `失败项: ${errors.join(', ')}`,
        color: 'warning',
      })
    } else {
      addToast({
        title: `${actionText}失败`,
        description: errors.join(', '),
        color: 'danger',
      })
    }
  }, [targetPath, files, mode, currentPath, queryClient, onClose, actionText])

  const handleClose = useCallback(() => {
    setTargetPath(null)
    onClose()
  }, [onClose])

  return (
    <>
      <Modal
        isOpen={isOpen && !isPickerOpen}
        onClose={handleClose}
        placement="center"
        size="md"
        classNames={{
          base: "bg-content1 border border-divider shadow-2xl rounded-2xl",
          backdrop: "bg-black/60 backdrop-blur-md",
          closeButton: "top-4 right-4 text-default-400 hover:text-foreground hover:bg-default-100 rounded-lg",
        }}
      >
        <ModalContent>
          <ModalHeader className="flex items-center gap-3 px-6 pt-6 pb-2">
            <div className="w-10 h-10 rounded-xl bg-accent-primary/10 text-accent-primary flex items-center justify-center">
              <Icon size={20} />
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">{title}</h3>
              <p className="text-xs text-default-500 font-normal">
                {files.length === 1 ? files[0].name : `${files.length} 个项目`}
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
                {files.slice(0, 5).map(file => (
                  <div key={file.path} className="flex items-center gap-2 py-1">
                    <FileIcon name={file.name} isDir={file.isDir} size={20} variant="bare" />
                    <span className="text-sm truncate">{file.name}</span>
                  </div>
                ))}
                {files.length > 5 && (
                  <div className="text-xs text-default-500 py-1">
                    ...还有 {files.length - 5} 个项目
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
                className="w-full justify-start rounded-xl"
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
            <Button variant="flat" onPress={handleClose} className="text-default-600 rounded-xl">
              取消
            </Button>
            <Button
              color="primary"
              onPress={handleConfirm}
              isLoading={isProcessing}
              isDisabled={!targetPath || targetPath === currentPath}
              className="rounded-xl"
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

export default MoveDialog
