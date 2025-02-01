import { useState, useEffect, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { 
  Card, 
  CardBody, 
  CardHeader,
  Button,
  Input,
  Switch,
  Divider,
  Tabs,
  Tab,
  addToast,
  Snippet,
} from '@heroui/react'
import { 
  Server, 
  Shield, 
  HardDrive,
  Clock,
  Save,
  RefreshCw,
  Globe,
  Lock,
  User,
  Folder,
  Zap,
  Link2,
  Eye,
  EyeOff,
  Copy,
  CheckCircle2,
  Key,
  AlertCircle,
} from 'lucide-react'
import { cn, copyTextToClipboard, parseByteSize, normalizeWebDAVPrefix, formatWebDAVUrl, formatBytes } from '@/lib/utils'
import { ShareManager } from '@/components/share'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { getSettings, updateSettings, getWebDAVCredentials, type UpdateSettingsRequest } from '@/api/settings'

// Settings section component
function SettingsSection({ 
  title, 
  description, 
  icon: Icon, 
  children 
}: { 
  title: string
  description: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  children: React.ReactNode 
}) {
  return (
    <Card className="card-meridian">
      <CardHeader className="flex gap-4 pb-2">
        <div className="gradient-meridian rounded-xl p-2.5 shadow-sm">
          <Icon size={20} className="text-white" />
        </div>
        <div className="flex-1">
          <h3 className="text-base font-semibold text-foreground">{title}</h3>
          <p className="text-xs text-default-500 mt-0.5">{description}</p>
        </div>
      </CardHeader>
      <CardBody className="pt-2">
        {children}
      </CardBody>
    </Card>
  )
}

// Setting row component
function SettingRow({ 
  label, 
  description, 
  children 
}: { 
  label: string
  description?: string
  children: React.ReactNode 
}) {
  return (
    <div className="flex items-center justify-between py-2.5 first:pt-0 last:pb-0">
      <div className="flex-1 min-w-0 pr-4">
        <div className="text-sm font-medium text-foreground">{label}</div>
        {description && (
          <div className="text-xs text-default-500 mt-0.5">{description}</div>
        )}
      </div>
      <div className="shrink-0">
        {children}
      </div>
    </div>
  )
}

export function SettingsPage() {
  const [selectedTab, setSelectedTab] = useState('general')
  const queryClient = useQueryClient()
  
  // WebDAV credentials state
  const [showWebDAVPassword, setShowWebDAVPassword] = useState(false)
  const [copiedField, setCopiedField] = useState<string | null>(null)
  
  // Fetch settings from API
  const { data: settingsData, isLoading, error, refetch, isRefetching } = useQuery({
    queryKey: ['settings'],
    queryFn: getSettings,
  })

  // Fetch WebDAV credentials
  const { data: webdavCredentials } = useQuery({
    queryKey: ['webdav-credentials'],
    queryFn: getWebDAVCredentials,
    enabled: selectedTab === 'webdav', // Only fetch when WebDAV tab is selected
  })

  const webdavUrl = useMemo(() => {
    return formatWebDAVUrl(window.location.origin, webdavCredentials?.url ?? '')
  }, [webdavCredentials?.url])

  // Copy to clipboard helper
  const handleCopy = async (field: string, value: string) => {
    try {
      await copyTextToClipboard(value)
      setCopiedField(field)
      setTimeout(() => setCopiedField(null), 2000)
    } catch {
      addToast({ title: '复制失败', color: 'danger' })
    }
  }

  // Local editable state
  const [settings, setSettings] = useState({
    serverHost: '0.0.0.0',
    serverPort: '8080',
    storageRoot: '',
    maxVersions: 100,
    maxAge: '8760h',
    minFreeSpace: '10GB',
    gcInterval: '24h',
    webdavEnabled: true,
    webdavPrefix: '/dav',
    webdavReadOnly: false,
    webdavAuthType: 'basic',
    webdavUsername: 'admin',
    webdavPassword: '',
    minChunkSize: '256KB',
    avgChunkSize: '1MB',
    maxChunkSize: '4MB',
  })

  // Update local state when API data loads
  // This is a common pattern for editable forms that sync from server data
  useEffect(() => {
    if (settingsData?.data) {
      const d = settingsData.data
      // eslint-disable-next-line react-hooks/set-state-in-effect -- Sync server data to local editable state
      setSettings({
        serverHost: d.server.host,
        serverPort: String(d.server.port),
        storageRoot: d.storage.root,
        maxVersions: d.retention.max_versions,
        maxAge: d.retention.max_age,
        minFreeSpace: formatBytes(d.retention.min_free_space),
        gcInterval: d.retention.gc_interval,
        webdavEnabled: d.webdav.enabled,
        webdavPrefix: d.webdav.prefix,
        webdavReadOnly: d.webdav.read_only,
        webdavAuthType: d.webdav.auth_type,
        webdavUsername: d.webdav.username,
        webdavPassword: '',
        minChunkSize: formatBytes(d.cdc.min_chunk_size),
        avgChunkSize: formatBytes(d.cdc.avg_chunk_size),
        maxChunkSize: formatBytes(d.cdc.max_chunk_size),
      })
    }
  }, [settingsData])

  // Save mutation
  const saveMutation = useMutation({
    mutationFn: updateSettings,
    onSuccess: () => {
      addToast({ title: '设置已保存', color: 'success' })
      queryClient.invalidateQueries({ queryKey: ['settings'] })
    },
    onError: (err: Error) => {
      addToast({ title: '保存失败: ' + err.message, color: 'danger' })
    },
  })

  const handleSave = () => {
    let minFreeSpaceBytes: number
    let minChunkBytes: number
    let avgChunkBytes: number
    let maxChunkBytes: number

    try {
      minFreeSpaceBytes = parseByteSize(settings.minFreeSpace)
      minChunkBytes = parseByteSize(settings.minChunkSize)
      avgChunkBytes = parseByteSize(settings.avgChunkSize)
      maxChunkBytes = parseByteSize(settings.maxChunkSize)
    } catch (err) {
      addToast({
        title: '大小格式无效',
        description: err instanceof Error ? err.message : '请使用 1024、1 KB、1.5 MB 之类的格式',
        color: 'danger',
      })
      return
    }

    if (Number.isNaN(Number(settings.serverPort))) {
      addToast({
        title: '端口格式无效',
        description: '端口必须是数字',
        color: 'danger',
      })
      return
    }

    const req: UpdateSettingsRequest = {
      server: {
        host: settings.serverHost,
        port: parseInt(settings.serverPort),
      },
      retention: {
        max_versions: settings.maxVersions,
        max_age: settings.maxAge,
        min_free_space: minFreeSpaceBytes,
        gc_interval: settings.gcInterval,
      },
      cdc: {
        min_chunk_size: minChunkBytes,
        avg_chunk_size: avgChunkBytes,
        max_chunk_size: maxChunkBytes,
      },
      webdav: {
        enabled: settings.webdavEnabled,
        prefix: normalizeWebDAVPrefix(settings.webdavPrefix),
        read_only: settings.webdavReadOnly,
        auth_type: settings.webdavAuthType,
        username: settings.webdavUsername,
        ...(settings.webdavPassword && { password: settings.webdavPassword }),
      },
    }
    saveMutation.mutate(req)
  }

  const handleReset = async () => {
    const result = await refetch()
    if (result.error) {
      addToast({
        title: '重置失败',
        description: result.error instanceof Error ? result.error.message : '请稍后重试',
        color: 'danger',
      })
      return
    }

    addToast({ title: '已恢复为服务端当前配置', color: 'success' })
  }

  if (isLoading) {
    return (
      <div className="h-full flex items-center justify-center">
        <div className="text-center">
          <div className="w-12 h-12 border-3 border-accent-primary border-t-transparent rounded-full animate-spin mx-auto mb-4" />
          <p className="text-default-500">加载设置...</p>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="h-full flex items-center justify-center p-6">
        <EmptyState
          icon={AlertCircle}
          title="加载设置失败"
          description={(error as Error).message}
          action={
            <Button variant="bordered" className="rounded-xl" onPress={() => refetch()} isLoading={isRefetching}>
              重新加载
            </Button>
          }
        />
      </div>
    )
  }

  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="max-w-4xl mx-auto p-7">
        {/* Header */}
        <PageHeader
          title="系统设置"
          subtitle="配置 MnemoNAS 系统参数"
          actions={
            <>
              <Button
                variant="bordered"
                className="btn-secondary btn-md rounded-xl"
                startContent={<RefreshCw size={16} />}
                onPress={handleReset}
                isLoading={isRefetching}
              >
                重置
              </Button>
              <Button
                className="btn-primary btn-md rounded-xl"
                startContent={<Save size={16} />}
                isLoading={saveMutation.isPending}
                onPress={handleSave}
              >
                保存设置
              </Button>
            </>
          }
          className="mb-8"
        />

        {/* Tabs */}
        <Tabs 
          selectedKey={selectedTab}
          onSelectionChange={(key) => setSelectedTab(key as string)}
          classNames={{
            tabList: "bg-content1 border border-divider rounded-xl p-1 gap-1 shadow-[var(--shadow-soft)]",
            tab: "px-4 py-2 rounded-lg text-default-600 data-[selected=true]:bg-accent-primary data-[selected=true]:text-white data-[selected=true]:shadow-sm whitespace-nowrap",
            cursor: "hidden",
          }}
        >
          <Tab key="general" title="常规">
            <div className="space-y-6 mt-6">
              <SettingsSection
                title="服务器"
                description="配置服务器网络参数"
                icon={Server}
              >
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">监听地址</label>
                    <Input
                      placeholder="0.0.0.0"
                      value={settings.serverHost}
                      onValueChange={(v) => setSettings(s => ({ ...s, serverHost: v }))}
                      startContent={<Globe size={16} className="text-default-500" />}
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                      }}
                    />
                  </div>
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">端口</label>
                    <Input
                      placeholder="8080"
                      value={settings.serverPort}
                      onValueChange={(v) => setSettings(s => ({ ...s, serverPort: v }))}
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                      }}
                    />
                  </div>
                </div>
              </SettingsSection>

              <SettingsSection
                title="存储路径"
                description="显示当前数据存储根目录"
                icon={Folder}
              >
                <div className="space-y-4">
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">存储根目录</label>
                    <div className="w-full rounded-xl border border-divider bg-content2/40 px-3 py-3 text-sm text-foreground">
                      {settings.storageRoot || '~/.mnemonas'}
                    </div>
                  </div>
                  <div className="text-xs text-default-500">
                    当前值由服务端配置文件决定，界面中不可直接修改。如需调整，请修改配置文件并重启服务。
                  </div>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="retention" title="版本保留">
            <div className="space-y-6 mt-6">
              <SettingsSection
                title="版本策略"
                description="配置文件历史版本保留规则"
                icon={Clock}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="最大版本数"
                    description="每个文件最多保留的历史版本数量"
                  >
                    <Input
                      type="number"
                      value={String(settings.maxVersions)}
                      onValueChange={(v) => setSettings(s => ({ ...s, maxVersions: parseInt(v) || 0 }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大保留时间"
                    description="历史版本的最长保留期限"
                  >
                    <Input
                      value={settings.maxAge}
                      onValueChange={(v) => setSettings(s => ({ ...s, maxAge: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最小空闲空间"
                    description="触发自动清理的磁盘空间阈值"
                  >
                    <Input
                      value={settings.minFreeSpace}
                      onValueChange={(v) => setSettings(s => ({ ...s, minFreeSpace: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="GC 运行间隔"
                    description="垃圾回收任务的执行周期"
                  >
                    <Input
                      value={settings.gcInterval}
                      onValueChange={(v) => setSettings(s => ({ ...s, gcInterval: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="webdav" title="WebDAV">
            <div className="space-y-6 mt-6">
              {/* WebDAV Credentials Card */}
              {webdavCredentials?.enabled && webdavCredentials?.auth_type === 'basic' && (
                <SettingsSection
                  title="WebDAV 访问凭据"
                  description="用于挂载网络驱动器的登录凭据"
                  icon={Key}
                >
                  <div className="space-y-4">
                    <div className="p-4 rounded-xl bg-content2/50 border border-divider">
                      <div className="space-y-4">
                        {/* WebDAV URL */}
                        <div className="space-y-1.5">
                          <label className="text-xs text-default-500">WebDAV 地址</label>
                          <div className="flex items-center gap-2">
                            <Snippet
                              symbol=""
                              variant="flat"
                              className="flex-1"
                              classNames={{
                                base: "bg-content1 border border-divider",
                                pre: "font-mono text-sm",
                              }}
                              hideSymbol
                              hideCopyButton
                            >
                              {webdavUrl}
                            </Snippet>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              onPress={() => handleCopy('url', webdavUrl)}
                            >
                              <span className="sr-only">复制 WebDAV 地址</span>
                              {copiedField === 'url' ? (
                                <CheckCircle2 size={16} className="text-success" />
                              ) : (
                                <Copy size={16} />
                              )}
                            </Button>
                          </div>
                        </div>
                        
                        {/* Username */}
                        <div className="space-y-1.5">
                          <label className="text-xs text-default-500">用户名</label>
                          <div className="flex items-center gap-2">
                            <Snippet
                              symbol=""
                              variant="flat"
                              className="flex-1"
                              classNames={{
                                base: "bg-content1 border border-divider",
                                pre: "font-mono",
                              }}
                              hideSymbol
                              hideCopyButton
                            >
                              {webdavCredentials.username || 'admin'}
                            </Snippet>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              onPress={() => handleCopy('username', webdavCredentials.username || 'admin')}
                            >
                              <span className="sr-only">复制 WebDAV 用户名</span>
                              {copiedField === 'username' ? (
                                <CheckCircle2 size={16} className="text-success" />
                              ) : (
                                <Copy size={16} />
                              )}
                            </Button>
                          </div>
                        </div>

                        {/* Password */}
                        <div className="space-y-1.5">
                          <label className="text-xs text-default-500">密码</label>
                          <div className="flex items-center gap-2">
                            <Snippet
                              symbol=""
                              variant="flat"
                              className="flex-1"
                              classNames={{
                                base: "bg-content1 border border-divider",
                                pre: "font-mono",
                              }}
                              hideSymbol
                              hideCopyButton
                            >
                              {showWebDAVPassword
                                ? (webdavCredentials.password || '已设置（不可读取）')
                                : '••••••••••••••••'}
                            </Snippet>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              onPress={() => setShowWebDAVPassword(!showWebDAVPassword)}
                            >
                              <span className="sr-only">{showWebDAVPassword ? '隐藏 WebDAV 密码' : '显示 WebDAV 密码'}</span>
                              {showWebDAVPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                            </Button>
                            <Button
                              isIconOnly
                              size="sm"
                              variant="flat"
                              onPress={() => handleCopy('password', webdavCredentials.password || '')}
                              isDisabled={!webdavCredentials.password}
                            >
                              <span className="sr-only">复制 WebDAV 密码</span>
                              {copiedField === 'password' ? (
                                <CheckCircle2 size={16} className="text-success" />
                              ) : (
                                <Copy size={16} />
                              )}
                            </Button>
                          </div>
                        </div>
                      </div>
                    </div>
                    
                    <div className="text-xs text-default-400">
                      使用以上凭据在文件管理器中挂载 WebDAV 网络驱动器。
                      Windows: 映射网络驱动器 | macOS: 前往 → 连接服务器
                    </div>
                  </div>
                </SettingsSection>
              )}

              <SettingsSection
                title="WebDAV 服务"
                description="配置 WebDAV 协议接入"
                icon={Globe}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用 WebDAV"
                    description="允许通过 WebDAV 协议访问文件"
                  >
                    <Switch
                      isSelected={settings.webdavEnabled}
                      onValueChange={(v) => setSettings(s => ({ ...s, webdavEnabled: v }))}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="URL 前缀"
                    description="WebDAV 挂载点路径"
                  >
                    <Input
                      value={settings.webdavPrefix}
                      onValueChange={(v) => setSettings(s => ({ ...s, webdavPrefix: v }))}
                      className="w-32"
                      isDisabled={!settings.webdavEnabled}
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="只读模式"
                    description="启用后仅允许读取操作"
                  >
                    <Switch
                      isSelected={settings.webdavReadOnly}
                      onValueChange={(v) => setSettings(s => ({ ...s, webdavReadOnly: v }))}
                      isDisabled={!settings.webdavEnabled}
                      classNames={{
                        wrapper: cn(
                          "group-data-[selected=true]:bg-accent-primary",
                          "bg-content2"
                        ),
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="WebDAV 认证"
                description="配置访问凭据"
                icon={Shield}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="认证方式"
                    description="当前使用 Basic Auth 认证"
                  >
                    <div className="flex items-center gap-2 text-sm text-default-600">
                      <Lock size={14} />
                      <span>Basic Auth</span>
                    </div>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-2 gap-4">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">用户名</label>
                      <Input
                        placeholder="admin"
                        value={settings.webdavUsername}
                        onValueChange={(v) => setSettings(s => ({ ...s, webdavUsername: v }))}
                        isDisabled={!settings.webdavEnabled}
                        startContent={<User size={16} className="text-default-500" />}
                        classNames={{ 
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">密码</label>
                      <Input
                        type="password"
                        placeholder="••••••••"
                        value={settings.webdavPassword}
                        onValueChange={(v) => setSettings(s => ({ ...s, webdavPassword: v }))}
                        isDisabled={!settings.webdavEnabled}
                        startContent={<Lock size={16} className="text-default-500" />}
                        classNames={{ 
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                  </div>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="advanced" title="高级">
            <div className="space-y-6 mt-6">
              <SettingsSection
                title="CDC 分块参数"
                description="配置内容定义分块算法"
                icon={Zap}
              >
                <div className="space-y-4">
                  <div className="p-4 rounded-xl bg-content2 border border-divider">
                    <div className="flex items-start gap-3">
                      <div className="w-8 h-8 rounded-lg bg-accent-primary/15 flex items-center justify-center shrink-0 mt-0.5">
                        <HardDrive size={16} className="text-accent-primary" />
                      </div>
                      <div>
                        <div className="text-sm font-medium text-foreground">关于 CDC 分块</div>
                        <div className="text-xs text-default-500 mt-1 leading-relaxed">
                          内容定义分块（CDC）将文件按内容边界切分，实现高效去重。
                          较小的块大小可提高去重率，但会增加元数据开销；
                          较大的块大小则相反。建议保持默认值。
                        </div>
                      </div>
                    </div>
                  </div>
                  
                  <SettingRow
                    label="最小块大小"
                    description="分块的最小尺寸"
                  >
                    <Input
                      value={settings.minChunkSize}
                      onValueChange={(v) => setSettings(s => ({ ...s, minChunkSize: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="平均块大小"
                    description="分块的目标平均尺寸"
                  >
                    <Input
                      value={settings.avgChunkSize}
                      onValueChange={(v) => setSettings(s => ({ ...s, avgChunkSize: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大块大小"
                    description="分块的最大尺寸"
                  >
                    <Input
                      value={settings.maxChunkSize}
                      onValueChange={(v) => setSettings(s => ({ ...s, maxChunkSize: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="数据面连接"
                description="配置与 Rust 数据面的 gRPC 连接"
                icon={Zap}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="gRPC 地址"
                    description="Rust 数据面服务地址"
                  >
                    <code className="text-sm text-accent-primary bg-accent-primary/10 px-2 py-1 rounded">
                      127.0.0.1:9090
                    </code>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="连接超时"
                    description="gRPC 调用超时时间"
                  >
                    <code className="text-sm text-default-600 bg-content2 px-2 py-1 rounded">
                      30s
                    </code>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大重试次数"
                    description="失败后重试次数"
                  >
                    <code className="text-sm text-default-600 bg-content2 px-2 py-1 rounded">
                      3
                    </code>
                  </SettingRow>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="shares" title="分享管理">
            <div className="space-y-6 mt-6">
              <SettingsSection
                title="分享链接管理"
                description="管理所有已创建的分享链接"
                icon={Link2}
              >
                <ShareManager />
              </SettingsSection>
            </div>
          </Tab>
        </Tabs>
      </div>
    </div>
  )
}
