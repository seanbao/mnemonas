import { useState, useEffect } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { 
  Modal, 
  ModalContent, 
  ModalHeader, 
  ModalBody, 
  ModalFooter,
  Button,
  Snippet,
} from '@heroui/react'
import { Key, Eye, EyeOff, CheckCircle2, Copy } from 'lucide-react'
import { getSetupStatus, acknowledgeSetup } from '@/api/setup'

export function WelcomeDialog() {
  const [isOpen, setIsOpen] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [copied, setCopied] = useState<string | null>(null)

  const { data: setupStatus } = useQuery({
    queryKey: ['setup-status'],
    queryFn: getSetupStatus,
    staleTime: Infinity, // Only fetch once
    retry: false,
  })

  const acknowledgeMutation = useMutation({
    mutationFn: acknowledgeSetup,
    onSuccess: () => {
      setIsOpen(false)
    },
  })

  // Show dialog on first run if there are Web credentials to show
  useEffect(() => {
    if (setupStatus?.is_first_run && setupStatus?.auth_enabled && setupStatus?.web_password) {
      setIsOpen(true)
    }
  }, [setupStatus])

  const handleClose = () => {
    acknowledgeMutation.mutate()
  }

  const handleCopy = async (key: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(key)
      setTimeout(() => setCopied(null), 2000)
    } catch {
      // Fallback for older browsers
      const textarea = document.createElement('textarea')
      textarea.value = value
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
      setCopied(key)
      setTimeout(() => setCopied(null), 2000)
    }
  }

  // Only show if first run with Web auth enabled and password available
  if (!setupStatus?.is_first_run || !setupStatus?.auth_enabled || !setupStatus?.web_password) {
    return null
  }

  return (
    <Modal 
      isOpen={isOpen} 
      onClose={handleClose}
      size="md"
      backdrop="blur"
      isDismissable={false}
      hideCloseButton
      classNames={{
        base: "bg-white dark:bg-zinc-900",
        header: "border-b border-divider",
        footer: "border-t border-divider",
      }}
    >
      <ModalContent>
        <ModalHeader className="flex flex-col gap-1">
          <div className="flex items-center gap-3">
            <div className="gradient-meridian rounded-xl p-2.5">
              <Key className="h-5 w-5 text-white" />
            </div>
            <div>
              <h3 className="text-lg font-semibold">欢迎使用 MnemoNAS</h3>
              <p className="text-sm text-default-500 font-normal">
                首次启动，已为您生成登录凭据
              </p>
            </div>
          </div>
        </ModalHeader>
        
        <ModalBody className="py-6">
          <div className="space-y-6">
            {/* Web Auth Credentials */}
            <div className="space-y-4">
              <div className="space-y-3">
                {/* Username */}
                <div className="space-y-1.5">
                  <label className="text-xs text-default-500">用户名</label>
                  <div className="flex items-center gap-2">
                    <Snippet
                      symbol=""
                      variant="flat"
                      className="flex-1"
                      classNames={{
                        base: "bg-content2/50",
                        pre: "font-mono",
                      }}
                      hideSymbol
                      hideCopyButton
                    >
                      {setupStatus.web_username || 'admin'}
                    </Snippet>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="flat"
                      onPress={() => handleCopy('username', setupStatus.web_username || 'admin')}
                    >
                      {copied === 'username' ? (
                        <CheckCircle2 size={16} className="text-success" />
                      ) : (
                        <Copy size={16} />
                      )}
                    </Button>
                  </div>
                </div>

                {/* Password */}
                <div className="space-y-1.5">
                  <label className="text-xs text-default-500">密码（自动生成）</label>
                  <div className="flex items-center gap-2">
                    <Snippet
                      symbol=""
                      variant="flat"
                      className="flex-1"
                      classNames={{
                        base: "bg-content2/50",
                        pre: "font-mono",
                      }}
                      hideSymbol
                      hideCopyButton
                    >
                      {showPassword ? setupStatus.web_password : '••••••••••••••••'}
                    </Snippet>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="flat"
                      onPress={() => setShowPassword(!showPassword)}
                    >
                      {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                    </Button>
                    <Button
                      isIconOnly
                      size="sm"
                      variant="flat"
                      onPress={() => handleCopy('password', setupStatus.web_password!)}
                    >
                      {copied === 'password' ? (
                        <CheckCircle2 size={16} className="text-success" />
                      ) : (
                        <Copy size={16} />
                      )}
                    </Button>
                  </div>
                </div>
              </div>
            </div>

            {/* Warning */}
            <div className="bg-warning-50 dark:bg-warning-900/20 border border-warning-200 dark:border-warning-800 rounded-lg p-4">
              <p className="text-sm text-warning-700 dark:text-warning-400">
                <strong>重要提示：</strong>请妥善保存以上凭据。此对话框关闭后，密码将不再显示。建议首次登录后修改密码。
              </p>
            </div>
          </div>
        </ModalBody>
        
        <ModalFooter>
          <Button 
            color="primary" 
            onPress={handleClose}
            isLoading={acknowledgeMutation.isPending}
            className="w-full"
          >
            我已保存凭据，开始使用
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default WelcomeDialog
