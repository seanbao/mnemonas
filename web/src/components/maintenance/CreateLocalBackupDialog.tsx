import { useState, type FormEvent } from 'react'
import {
  Button,
  Input,
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
  Switch,
} from '@heroui/react'
import { Archive, HardDrive } from 'lucide-react'
import { type CreateLocalBackupJobRequest } from '@/api/files'
import { hasControlCharacter } from '@/lib/utils'

interface CreateLocalBackupDialogProps {
  isOpen: boolean
  isSubmitting: boolean
  onClose: () => void
  onSubmit: (request: CreateLocalBackupJobRequest) => void
}

interface CreateLocalBackupErrors {
  name?: string
  destination?: string
}

const defaultName = '外置硬盘备份'

function getDestinationError(value: string): string | undefined {
  if (hasControlCharacter(value) || value.includes('\\')) {
    return '目标目录不能包含反斜杠或控制字符。'
  }

  const trimmed = value.trim()
  if (!trimmed) {
    return '请填写备份目标目录。'
  }
  if (!trimmed.startsWith('/')) {
    return '目标目录必须是服务器上的绝对路径。'
  }

  const segments = trimmed.split('/').filter(Boolean)
  if (segments.length === 0) {
    return '不能把文件系统根目录作为备份目标。'
  }
  if (segments.some((segment) => segment === '.' || segment === '..')) {
    return '目标目录不能包含 . 或 .. 路径段。'
  }
  return undefined
}

function validateDraft(name: string, destination: string): CreateLocalBackupErrors {
  const errors: CreateLocalBackupErrors = {}
  const trimmedName = name.trim()
  if (!trimmedName) {
    errors.name = '请填写备份名称。'
  } else if (hasControlCharacter(trimmedName)) {
    errors.name = '备份名称不能包含控制字符。'
  }
  errors.destination = getDestinationError(destination)
  return errors
}

export function CreateLocalBackupDialog({
  isOpen,
  isSubmitting,
  onClose,
  onSubmit,
}: CreateLocalBackupDialogProps) {
  const [name, setName] = useState(defaultName)
  const [destination, setDestination] = useState('')
  const [isAutomatic, setIsAutomatic] = useState(true)
  const [errors, setErrors] = useState<CreateLocalBackupErrors>({})

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (isSubmitting) {
      return
    }

    const nextErrors = validateDraft(name, destination)
    setErrors(nextErrors)
    if (nextErrors.name || nextErrors.destination) {
      return
    }

    onSubmit({
      name: name.trim(),
      destination: destination.trim(),
      ...(isAutomatic ? {} : { schedule_interval: '0' as const }),
    })
  }

  const handleOpenChange = (open: boolean) => {
    if (!open && !isSubmitting) {
      onClose()
    }
  }

  return (
    <Modal
      isOpen={isOpen}
      onOpenChange={handleOpenChange}
      isDismissable={!isSubmitting}
      hideCloseButton={isSubmitting}
      placement="center"
      scrollBehavior="inside"
      size="2xl"
      classNames={{ base: 'border border-divider bg-content1' }}
    >
      <ModalContent>
        <form onSubmit={handleSubmit}>
          <ModalHeader className="flex items-center gap-3">
            <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <HardDrive size={20} aria-hidden="true" />
            </span>
            <span className="min-w-0">
              <span className="block text-base font-semibold">添加本地备份</span>
              <span className="mt-1 block text-xs font-normal text-default-500">把当前数据复制到服务器已挂载的独立目录</span>
            </span>
          </ModalHeader>
          <ModalBody>
            <div className="space-y-4">
              <Input
                autoFocus
                label="备份名称"
                value={name}
                onValueChange={(value) => {
                  setName(value)
                  setErrors((current) => ({ ...current, name: undefined }))
                }}
                isDisabled={isSubmitting}
                isInvalid={Boolean(errors.name)}
                errorMessage={errors.name}
              />
              <Input
                label="目标目录"
                placeholder="/mnt/backup-drive/mnemonas"
                value={destination}
                onValueChange={(value) => {
                  setDestination(value)
                  setErrors((current) => ({ ...current, destination: undefined }))
                }}
                isDisabled={isSubmitting}
                description="填写服务器上已挂载的独立磁盘目录，不能位于当前数据目录内。"
                isInvalid={Boolean(errors.destination)}
                errorMessage={errors.destination}
              />
              <div className="rounded-lg border border-divider bg-content2/45 p-4">
                <Switch
                  aria-label="每天自动备份"
                  isSelected={isAutomatic}
                  onValueChange={setIsAutomatic}
                  isDisabled={isSubmitting}
                >
                  <span className="font-medium">每天自动备份</span>
                </Switch>
                <p className="mt-2 text-xs leading-5 text-default-500">
                  {isAutomatic
                    ? '创建后将开始首次备份，之后每天自动运行。'
                    : '仅创建任务；需要备份时可在任务列表中选择“立即备份”。'}
                </p>
              </div>
              <div className="flex items-start gap-3 rounded-lg border border-primary/20 bg-primary/5 p-3 text-xs leading-5 text-default-600">
                <Archive size={17} className="mt-0.5 shrink-0 text-primary" aria-hidden="true" />
                <span>系统默认保留最近 7 份备份，包含系统配置，并在备份后自动校验。</span>
              </div>
            </div>
          </ModalBody>
          <ModalFooter className="grid grid-cols-1 gap-2 sm:flex sm:justify-end">
            <Button
              type="button"
              variant="light"
              className="min-h-11 rounded-lg"
              isDisabled={isSubmitting}
              onPress={onClose}
            >
              取消
            </Button>
            <Button
              type="submit"
              color="primary"
              className="min-h-11 rounded-lg"
              isLoading={isSubmitting}
            >
              {isAutomatic ? '创建并开始首次备份' : '仅创建备份任务'}
            </Button>
          </ModalFooter>
        </form>
      </ModalContent>
    </Modal>
  )
}
