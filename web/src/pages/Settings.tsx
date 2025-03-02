import { useCallback, useMemo, useState } from 'react'
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
  Star,
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
  const defaultSettings = {
    serverHost: '0.0.0.0',
    serverPort: '8080',
    serverReadTimeout: '30s',
    serverWriteTimeout: '60s',
    serverIdleTimeout: '120s',
    tlsEnabled: false,
    tlsCertFile: '',
    tlsKeyFile: '',
    tlsAutoGenerate: true,
    tlsCertDir: '',
    storageRoot: '',
    trashEnabled: true,
    trashRetentionDays: '30',
    trashMaxSize: '10 GB',
    maxVersions: '100',
    maxAge: '8760h',
    minFreeSpace: '10GB',
    gcInterval: '24h',
    versioningExtensions: '.md\n.txt\n.go',
    versioningFilenames: 'README\nDockerfile\nMakefile',
    versioningMaxSize: '100 MB',
    webdavEnabled: true,
    webdavPrefix: '/dav',
    webdavReadOnly: false,
    webdavAuthType: 'basic',
    webdavUsername: 'admin',
    webdavPassword: '',
    shareEnabled: false,
    shareBaseURL: '',
    favoritesEnabled: true,
    alertsEnabled: false,
    alertsCheckInterval: '1h',
    alertsThresholdPct: '90',
    alertsCriticalPct: '95',
    alertsMinFreeSpace: '10GB',
    alertsCooldownPeriod: '4h',
    alertsWebhookURL: '',
    alertsWebhookMethod: 'POST',
    alertsWebhookHeaders: '',
    dataplaneGrpcAddress: '127.0.0.1:9090',
    dataplaneTimeout: '30s',
    dataplaneMaxRetries: '3',
    minChunkSize: '256KB',
    avgChunkSize: '1MB',
    maxChunkSize: '4MB',
  }
  
  // WebDAV credentials state
  const [showWebDAVPassword, setShowWebDAVPassword] = useState(false)
  const [copiedField, setCopiedField] = useState<string | null>(null)
  
  // Fetch settings from API
  const { data: settingsData, isLoading, error, refetch, isRefetching } = useQuery({
    queryKey: ['settings'],
    queryFn: getSettings,
  })

  // Fetch WebDAV credentials
  const {
    data: webdavCredentials,
    error: webdavCredentialsError,
    refetch: refetchWebDAVCredentials,
    isRefetching: isRefetchingWebDAVCredentials,
  } = useQuery({
    queryKey: ['webdav-credentials'],
    queryFn: getWebDAVCredentials,
    enabled: selectedTab === 'webdav', // Only fetch when WebDAV tab is selected
  })

  const webdavUrl = useMemo(() => {
    return formatWebDAVUrl(window.location.origin, webdavCredentials?.url ?? '')
  }, [webdavCredentials?.url])

  const handleCopy = async (field: string, value: string) => {
    try {
      await copyTextToClipboard(value)
      setCopiedField(field)
      setTimeout(() => setCopiedField(null), 2000)
    } catch {
      addToast({ title: '复制失败', color: 'danger' })
    }
  }

  const [draftSettings, setDraftSettings] = useState(defaultSettings)
  const [isDirty, setIsDirty] = useState(false)

  const mapServerSettings = useCallback((data: NonNullable<typeof settingsData>['data']) => {
    return {
      serverHost: data.server.host,
      serverPort: String(data.server.port),
      serverReadTimeout: data.server.read_timeout,
      serverWriteTimeout: data.server.write_timeout,
      serverIdleTimeout: data.server.idle_timeout,
      tlsEnabled: data.server.tls?.enabled ?? false,
      tlsCertFile: data.server.tls?.cert_file ?? '',
      tlsKeyFile: data.server.tls?.key_file ?? '',
      tlsAutoGenerate: data.server.tls?.auto_generate ?? true,
      tlsCertDir: data.server.tls?.cert_dir ?? '',
      storageRoot: data.storage.root,
      trashEnabled: data.trash?.enabled ?? true,
      trashRetentionDays: String(data.trash?.retention_days ?? 30),
      trashMaxSize: formatBytes(data.trash?.max_size ?? 10737418240),
      maxVersions: String(data.retention.max_versions),
      maxAge: data.retention.max_age,
      minFreeSpace: formatBytes(data.retention.min_free_space),
      gcInterval: data.retention.gc_interval,
      versioningExtensions: data.versioning?.auto_versioned_extensions?.join('\n') ?? '.md\n.txt\n.go',
      versioningFilenames: data.versioning?.auto_versioned_filenames?.join('\n') ?? 'README\nDockerfile\nMakefile',
      versioningMaxSize: formatBytes(data.versioning?.max_versioned_size ?? 104857600),
      webdavEnabled: data.webdav.enabled,
      webdavPrefix: data.webdav.prefix,
      webdavReadOnly: data.webdav.read_only,
      webdavAuthType: data.webdav.auth_type,
      webdavUsername: data.webdav.username,
      webdavPassword: '',
      shareEnabled: data.share.enabled,
      shareBaseURL: data.share.base_url,
      favoritesEnabled: data.favorites?.enabled ?? true,
      alertsEnabled: data.alerts?.enabled ?? false,
      alertsCheckInterval: data.alerts?.check_interval ?? '1h',
      alertsThresholdPct: String(data.alerts?.threshold_pct ?? 90),
      alertsCriticalPct: String(data.alerts?.critical_pct ?? 95),
      alertsMinFreeSpace: formatBytes(data.alerts?.min_free_bytes ?? 10737418240),
      alertsCooldownPeriod: data.alerts?.cooldown_period ?? '4h',
      alertsWebhookURL: data.alerts?.webhook_url ?? '',
      alertsWebhookMethod: data.alerts?.webhook_method ?? 'POST',
      alertsWebhookHeaders: data.alerts?.webhook_headers?.join('\n') ?? '',
      dataplaneMaxRetries: String(data.dataplane.max_retries),
      dataplaneGrpcAddress: data.dataplane.grpc_address,
      dataplaneTimeout: data.dataplane.timeout,
      minChunkSize: formatBytes(data.cdc.min_chunk_size),
      avgChunkSize: formatBytes(data.cdc.avg_chunk_size),
      maxChunkSize: formatBytes(data.cdc.max_chunk_size),
    }
  }, [])

  const settings = useMemo(() => {
    if (!isDirty && settingsData?.data) {
      return mapServerSettings(settingsData.data)
    }
    return draftSettings
  }, [draftSettings, isDirty, mapServerSettings, settingsData])

  const updateDirtySettings = (updater: (prev: typeof draftSettings) => typeof draftSettings) => {
    setIsDirty(true)
    setDraftSettings((prev) => updater(isDirty ? prev : settings))
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

    if (result.data?.data) {
      setDraftSettings(mapServerSettings(result.data.data))
    }
    setIsDirty(false)

    addToast({ title: '已恢复为服务端当前配置', color: 'success' })
  }

  // Save mutation
  const saveMutation = useMutation({
    mutationFn: updateSettings,
    onSuccess: () => {
      setIsDirty(false)
      addToast({ title: '设置已保存', color: 'success' })
      queryClient.invalidateQueries({ queryKey: ['settings'] })
    },
    onError: (err: Error) => {
      addToast({ title: '保存失败: ' + err.message, color: 'danger' })
    },
  })

  const handleSave = () => {
    let minFreeSpaceBytes: number
    let alertsMinFreeBytes: number
    let trashMaxSizeBytes: number
    let versioningMaxSizeBytes: number
    let minChunkBytes: number
    let avgChunkBytes: number
    let maxChunkBytes: number
    const trimmedPort = settings.serverPort.trim()
    const parsedPort = Number(trimmedPort)
    const trimmedReadTimeout = settings.serverReadTimeout.trim()
    const trimmedWriteTimeout = settings.serverWriteTimeout.trim()
    const trimmedIdleTimeout = settings.serverIdleTimeout.trim()
    const trimmedMaxVersions = settings.maxVersions.trim()
    const parsedMaxVersions = Number(trimmedMaxVersions)
    const trimmedTrashRetentionDays = settings.trashRetentionDays.trim()
    const parsedTrashRetentionDays = Number(trimmedTrashRetentionDays)
    const trimmedDataplaneTimeout = settings.dataplaneTimeout.trim()
    const trimmedMaxRetries = settings.dataplaneMaxRetries.trim()
    const parsedMaxRetries = Number(trimmedMaxRetries)
    const trimmedAlertsCheckInterval = settings.alertsCheckInterval.trim()
    const trimmedAlertsCooldownPeriod = settings.alertsCooldownPeriod.trim()
    const trimmedAlertsThresholdPct = settings.alertsThresholdPct.trim()
    const trimmedAlertsCriticalPct = settings.alertsCriticalPct.trim()
    const trimmedAlertsWebhookMethod = settings.alertsWebhookMethod.trim().toUpperCase()
    const alertsWebhookHeaders = settings.alertsWebhookHeaders
      .split('\n')
      .map(header => header.trim())
      .filter(Boolean)
    const parsedAlertsThresholdPct = Number(trimmedAlertsThresholdPct)
    const parsedAlertsCriticalPct = Number(trimmedAlertsCriticalPct)
    const versioningExtensions = settings.versioningExtensions
      .split('\n')
      .map(entry => entry.trim())
      .filter(Boolean)
    const versioningFilenames = settings.versioningFilenames
      .split('\n')
      .map(entry => entry.trim())
      .filter(Boolean)

    try {
      minFreeSpaceBytes = parseByteSize(settings.minFreeSpace)
      alertsMinFreeBytes = parseByteSize(settings.alertsMinFreeSpace)
      trashMaxSizeBytes = parseByteSize(settings.trashMaxSize)
      versioningMaxSizeBytes = parseByteSize(settings.versioningMaxSize)
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

    if (!Number.isInteger(parsedPort) || parsedPort < 1 || parsedPort > 65535) {
      addToast({
        title: '端口格式无效',
        description: '端口必须是 1 到 65535 之间的整数',
        color: 'danger',
      })
      return
    }

	if (!trimmedReadTimeout) {
		addToast({
			title: '读取超时格式无效',
			description: '读取超时不能为空',
			color: 'danger',
		})
		return
	}

	if (!trimmedWriteTimeout) {
		addToast({
			title: '写入超时格式无效',
			description: '写入超时不能为空',
			color: 'danger',
		})
		return
	}

	if (!trimmedIdleTimeout) {
		addToast({
			title: '空闲超时格式无效',
			description: '空闲超时不能为空',
			color: 'danger',
		})
		return
	}

    if (!/^\d+$/.test(trimmedMaxVersions) || !Number.isInteger(parsedMaxVersions) || parsedMaxVersions < 0) {
      addToast({
        title: '最大版本数格式无效',
        description: '最大版本数必须是 0 或正整数',
        color: 'danger',
      })
      return
    }

    if (!trimmedDataplaneTimeout) {
      addToast({
        title: '数据面超时格式无效',
        description: '连接超时不能为空',
        color: 'danger',
      })
      return
    }

    if (!/^\d+$/.test(trimmedMaxRetries) || !Number.isInteger(parsedMaxRetries) || parsedMaxRetries < 0) {
      addToast({
        title: '最大重试次数格式无效',
        description: '最大重试次数必须是 0 或正整数',
        color: 'danger',
      })
      return
    }

    if (!trimmedAlertsCheckInterval) {
      addToast({
        title: '告警检查间隔格式无效',
        description: '检查间隔不能为空',
        color: 'danger',
      })
      return
    }

    if (!trimmedAlertsCooldownPeriod) {
      addToast({
        title: '告警冷却时间格式无效',
        description: '冷却时间不能为空',
        color: 'danger',
      })
      return
    }

    if (!Number.isFinite(parsedAlertsThresholdPct) || parsedAlertsThresholdPct < 0 || parsedAlertsThresholdPct > 100) {
      addToast({
        title: '告警阈值格式无效',
        description: '告警阈值必须在 0 到 100 之间',
        color: 'danger',
      })
      return
    }

    if (!Number.isFinite(parsedAlertsCriticalPct) || parsedAlertsCriticalPct < 0 || parsedAlertsCriticalPct > 100) {
      addToast({
        title: '严重告警阈值格式无效',
        description: '严重告警阈值必须在 0 到 100 之间',
        color: 'danger',
      })
      return
    }

    if (parsedAlertsCriticalPct < parsedAlertsThresholdPct) {
      addToast({
        title: '告警阈值关系无效',
        description: '严重告警阈值不能小于普通告警阈值',
        color: 'danger',
      })
      return
    }

	if (trimmedAlertsWebhookMethod !== 'GET' && trimmedAlertsWebhookMethod !== 'POST') {
		addToast({
			title: 'Webhook 方法无效',
			description: 'Webhook 方法必须是 GET 或 POST',
			color: 'danger',
		})
		return
	}

  if (!/^\d+$/.test(trimmedTrashRetentionDays) || !Number.isInteger(parsedTrashRetentionDays) || parsedTrashRetentionDays < 0) {
    addToast({
      title: '回收站保留天数格式无效',
      description: '回收站保留天数必须是 0 或正整数',
      color: 'danger',
    })
    return
  }

	for (const header of alertsWebhookHeaders) {
		const separator = header.indexOf(':')
		if (separator <= 0 || separator === header.length - 1) {
			addToast({
				title: 'Webhook Header 格式无效',
				description: '每行必须使用 Key:Value 格式',
				color: 'danger',
			})
			return
		}
	}

    const req: UpdateSettingsRequest = {
      server: {
        host: settings.serverHost,
        port: parsedPort,
        read_timeout: trimmedReadTimeout,
        write_timeout: trimmedWriteTimeout,
        idle_timeout: trimmedIdleTimeout,
        tls: {
          enabled: settings.tlsEnabled,
          cert_file: settings.tlsCertFile.trim(),
          key_file: settings.tlsKeyFile.trim(),
          auto_generate: settings.tlsAutoGenerate,
          cert_dir: settings.tlsCertDir.trim(),
        },
      },
      retention: {
        max_versions: parsedMaxVersions,
        max_age: settings.maxAge,
        min_free_space: minFreeSpaceBytes,
        gc_interval: settings.gcInterval,
      },
      versioning: {
        auto_versioned_extensions: versioningExtensions,
        auto_versioned_filenames: versioningFilenames,
        max_versioned_size: versioningMaxSizeBytes,
      },
      trash: {
        enabled: settings.trashEnabled,
        retention_days: parsedTrashRetentionDays,
        max_size: trashMaxSizeBytes,
      },
      dataplane: {
        grpc_address: settings.dataplaneGrpcAddress,
        timeout: trimmedDataplaneTimeout,
        max_retries: parsedMaxRetries,
      },
      share: {
        enabled: settings.shareEnabled,
        base_url: settings.shareBaseURL.trim(),
      },
      favorites: {
        enabled: settings.favoritesEnabled,
      },
      alerts: {
        enabled: settings.alertsEnabled,
        check_interval: trimmedAlertsCheckInterval,
        threshold_pct: parsedAlertsThresholdPct,
        critical_pct: parsedAlertsCriticalPct,
        min_free_bytes: alertsMinFreeBytes,
        cooldown_period: trimmedAlertsCooldownPeriod,
        webhook_url: settings.alertsWebhookURL.trim(),
        webhook_method: trimmedAlertsWebhookMethod,
        webhook_headers: alertsWebhookHeaders,
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
                description="配置服务器网络参数；保存后需重启服务才能影响监听地址、端口和 HTTP 超时"
                icon={Server}
              >
                <div className="space-y-4">
                  <div className="grid grid-cols-2 gap-4">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">监听地址</label>
                      <Input
                        placeholder="0.0.0.0"
                        value={settings.serverHost}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverHost: v }))}
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
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverPort: v }))}
                        classNames={{ 
                          inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                        }}
                      />
                    </div>
                  </div>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-3 gap-4">
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">读取超时</label>
                    <Input
                      placeholder="30s"
                      value={settings.serverReadTimeout}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverReadTimeout: v }))}
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                      }}
                    />
                  </div>
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">写入超时</label>
                    <Input
                      placeholder="60s"
                      value={settings.serverWriteTimeout}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverWriteTimeout: v }))}
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                      }}
                    />
                  </div>
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">空闲超时</label>
                    <Input
                      placeholder="120s"
                      value={settings.serverIdleTimeout}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, serverIdleTimeout: v }))}
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary",
                      }}
                    />
                  </div>
                  </div>
                </div>
              </SettingsSection>

        <SettingsSection
        title="TLS / HTTPS"
        description="配置 HTTPS 证书与自动生成策略；保存后需重启服务才能切换运行中的监听器"
        icon={Shield}
        >
        <div className="space-y-4">
          <SettingRow
          label="启用 HTTPS"
          description="启用后服务将使用 TLS 证书提供 HTTPS"
          >
          <Switch
            isSelected={settings.tlsEnabled}
            onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsEnabled: v }))}
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
          label="自动生成证书"
          description="证书缺失时自动生成自签名证书"
          >
          <Switch
            isSelected={settings.tlsAutoGenerate}
            onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsAutoGenerate: v }))}
            isDisabled={!settings.tlsEnabled}
            classNames={{
            wrapper: cn(
              "group-data-[selected=true]:bg-accent-primary",
              "bg-content2"
            ),
            }}
          />
          </SettingRow>
          <Divider className="bg-divider" />
          <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="text-sm font-medium text-default-600 mb-1.5 block">证书文件</label>
            <Input
            value={settings.tlsCertFile}
            onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsCertFile: v }))}
            placeholder="/path/to/server.crt"
            isDisabled={!settings.tlsEnabled}
            classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
            />
          </div>
          <div>
            <label className="text-sm font-medium text-default-600 mb-1.5 block">私钥文件</label>
            <Input
            value={settings.tlsKeyFile}
            onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsKeyFile: v }))}
            placeholder="/path/to/server.key"
            isDisabled={!settings.tlsEnabled}
            classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
            />
          </div>
          </div>
          <Divider className="bg-divider" />
          <SettingRow
          label="证书目录"
          description="自动生成证书时使用的存放目录"
          >
          <Input
            value={settings.tlsCertDir}
            onValueChange={(v) => updateDirtySettings(s => ({ ...s, tlsCertDir: v }))}
            placeholder="~/.mnemonas/certs"
            isDisabled={!settings.tlsEnabled || !settings.tlsAutoGenerate}
            classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9" }}
          />
          </SettingRow>
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
                description="配置文件历史版本保留规则；保存后会立即更新运行中的保留阈值，gc_interval 设为 0 表示禁用周期清理"
                icon={Clock}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用回收站"
                    description="关闭后删除操作将直接永久删除，不再进入回收站"
                  >
                    <Switch
                    aria-label="启用回收站"
                      isSelected={settings.trashEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, trashEnabled: v }))}
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
                  label="回收站保留天数"
                  description="回收站项目的保留时间；设置为 0 表示进入后立即过期，等待清理任务删除"
                  >
                  <Input
                    aria-label="回收站保留天数"
                    type="number"
                    value={settings.trashRetentionDays}
                    onValueChange={(v) => updateDirtySettings(s => ({ ...s, trashRetentionDays: v }))}
                    className="w-24"
                    classNames={{ 
                    inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                    }}
                  />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                  label="回收站最大容量"
                  description="超过该上限时，系统会优先清理最早删除的项目，为最新删除的项目腾出空间"
                  >
                  <Input
                    aria-label="回收站最大容量"
                    value={settings.trashMaxSize}
                    onValueChange={(v) => updateDirtySettings(s => ({ ...s, trashMaxSize: v }))}
                    className="w-32"
                    classNames={{ 
                    inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                    }}
                  />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大版本数"
                    description="每个文件最多保留的历史版本数量"
                  >
                    <Input
                      type="number"
                      value={settings.maxVersions}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, maxVersions: v }))}
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
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, maxAge: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最小空闲空间"
                    description="剩余空间低于该阈值时，写入后会强制执行一次全局历史版本清理"
                  >
                    <Input
                      value={settings.minFreeSpace}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, minFreeSpace: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="GC 运行间隔"
                    description="后台历史版本清理任务的执行周期；设为 0 表示禁用周期清理"
                  >
                    <Input
                      value={settings.gcInterval}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, gcInterval: v }))}
                      className="w-24"
                      classNames={{ 
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="自动版本化"
                description="配置默认自动版本化规则；保存后会立即影响后续新写入文件的版本策略"
                icon={Folder}
              >
                <div className="space-y-4">
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">自动版本化后缀</label>
                    <textarea
                      aria-label="自动版本化后缀"
                      value={settings.versioningExtensions}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, versioningExtensions: event.target.value }))}
                      rows={4}
                      className="input-shell w-full rounded-medium px-3 py-2 text-sm bg-transparent outline-none border border-transparent focus:border-accent-primary"
                    />
                    <p className="text-xs text-default-500 mt-1">每行一个后缀，例如 `.md`、`.txt`。</p>
                  </div>
                  <Divider className="bg-divider" />
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">自动版本化文件名</label>
                    <textarea
                      aria-label="自动版本化文件名"
                      value={settings.versioningFilenames}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, versioningFilenames: event.target.value }))}
                      rows={4}
                      className="input-shell w-full rounded-medium px-3 py-2 text-sm bg-transparent outline-none border border-transparent focus:border-accent-primary"
                    />
                    <p className="text-xs text-default-500 mt-1">每行一个文件名，例如 `README`、`Dockerfile`。</p>
                  </div>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大自动版本化文件大小"
                    description="超过该大小的文件默认不再自动创建历史版本"
                  >
                    <Input
                      value={settings.versioningMaxSize}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, versioningMaxSize: v }))}
                      className="w-32"
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
              {webdavCredentialsError && (
                <div className="flex items-start gap-3 rounded-2xl border border-warning/30 bg-warning/5 px-4 py-3 text-sm text-foreground">
                  <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
                  <div className="flex-1">
                    <p className="font-medium">WebDAV 凭据加载失败</p>
                    <p className="text-default-600">{(webdavCredentialsError as Error).message || '请稍后重试'}</p>
                  </div>
                  <Button
                    size="sm"
                    variant="bordered"
                    className="rounded-xl"
                    onPress={() => refetchWebDAVCredentials()}
                    isLoading={isRefetchingWebDAVCredentials}
                  >
                    重新加载凭据
                  </Button>
                </div>
              )}

              {/* WebDAV Credentials Card */}
              {webdavCredentials?.enabled && webdavCredentials?.auth_type === 'basic' && (
                <SettingsSection
                  title="WebDAV 访问凭据"
                  description="用于挂载当前运行中的 WebDAV 服务；保存成功后这里会显示最新的运行配置"
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
                description="配置 WebDAV 协议接入；保存后会立即更新运行中的 WebDAV 配置"
                icon={Globe}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用 WebDAV"
                    description="允许通过 WebDAV 协议访问文件"
                  >
                    <Switch
                      isSelected={settings.webdavEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavEnabled: v }))}
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
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavPrefix: v }))}
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
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavReadOnly: v }))}
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
                description="配置访问凭据；保存后会立即作用到运行中的 WebDAV 服务"
                icon={Shield}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="认证方式"
                    description="选择 WebDAV 访问所需的认证方式"
                  >
                    <select
                      value={settings.webdavAuthType}
                      onChange={(event) => updateDirtySettings((current) => ({
                        ...current,
                        webdavAuthType: event.target.value as 'basic' | 'none',
                      }))}
                      disabled={!settings.webdavEnabled}
                      className="input-shell h-9 rounded-xl px-3 text-sm bg-content1 border border-divider min-w-[160px]"
                      aria-label="WebDAV 认证方式"
                    >
                      <option value="basic">Basic Auth</option>
                      <option value="none">无认证</option>
                    </select>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-2 gap-4">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">用户名</label>
                      <Input
                        placeholder="admin"
                        value={settings.webdavUsername}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavUsername: v }))}
                        isDisabled={!settings.webdavEnabled || settings.webdavAuthType === 'none'}
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
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, webdavPassword: v }))}
                        isDisabled={!settings.webdavEnabled || settings.webdavAuthType === 'none'}
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
                description="配置内容定义分块算法；保存后需重启数据面服务，且仅影响后续新写入数据"
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
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, minChunkSize: v }))}
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
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, avgChunkSize: v }))}
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
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, maxChunkSize: v }))}
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
                description="配置与 Rust 数据面的 gRPC 连接；地址变更会立即校验并切换，超时与重试设置用于后续连接建立"
                icon={Zap}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="gRPC 地址"
                    description="Rust 数据面服务地址"
                  >
                    <Input
                      value={settings.dataplaneGrpcAddress}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, dataplaneGrpcAddress: v }))}
                      className="w-56"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="连接超时"
                    description="gRPC 调用超时时间"
                  >
                    <Input
                      value={settings.dataplaneTimeout}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, dataplaneTimeout: v }))}
                      className="w-32"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大重试次数"
                    description="失败后重试次数"
                  >
                    <Input
                      type="number"
                      value={settings.dataplaneMaxRetries}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, dataplaneMaxRetries: v }))}
                      className="w-24"
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

              <SettingsSection
                title="存储告警"
                description="配置磁盘空间监控和 Webhook 告警；保存后会立即更新运行中的告警监控"
                icon={AlertCircle}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用告警"
                    description="启用后定期检查存储空间并发送告警，保存后立即生效"
                  >
                    <Switch
                      aria-label="启用告警"
                      isSelected={settings.alertsEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsEnabled: v }))}
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
                    label="检查间隔"
                    description="磁盘空间检查频率"
                  >
                    <Input
                      value={settings.alertsCheckInterval}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsCheckInterval: v }))}
                      className="w-32"
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-2 gap-4">
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">普通告警阈值 (%)</label>
                      <Input
                        type="number"
                        value={settings.alertsThresholdPct}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsThresholdPct: v }))}
                        isDisabled={!settings.alertsEnabled}
                        classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
                      />
                    </div>
                    <div>
                      <label className="text-sm font-medium text-default-600 mb-1.5 block">严重告警阈值 (%)</label>
                      <Input
                        type="number"
                        value={settings.alertsCriticalPct}
                        onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsCriticalPct: v }))}
                        isDisabled={!settings.alertsEnabled}
                        classNames={{ inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary" }}
                      />
                    </div>
                  </div>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最小剩余空间"
                    description="剩余空间低于该值时发送告警"
                  >
                    <Input
                      value={settings.alertsMinFreeSpace}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsMinFreeSpace: v }))}
                      className="w-32"
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="冷却时间"
                    description="同级别连续告警之间的最小间隔"
                  >
                    <Input
                      value={settings.alertsCooldownPeriod}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsCooldownPeriod: v }))}
                      className="w-32"
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="Webhook URL"
                    description="发送告警通知的目标地址"
                  >
                    <Input
                      value={settings.alertsWebhookURL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, alertsWebhookURL: v }))}
                      placeholder="https://hooks.example.com/alert"
                      isDisabled={!settings.alertsEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="Webhook 方法"
                    description="告警通知请求使用的 HTTP 方法"
                  >
                    <select
                      aria-label="Webhook 方法"
                      value={settings.alertsWebhookMethod}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, alertsWebhookMethod: event.target.value }))}
                      disabled={!settings.alertsEnabled}
                      className="input-shell min-w-[8rem] px-3 py-2 text-sm bg-transparent outline-none"
                    >
                      <option value="POST">POST</option>
                      <option value="GET">GET</option>
                    </select>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div>
                    <label className="text-sm font-medium text-default-600 mb-1.5 block">自定义 Header</label>
                    <textarea
                      aria-label="Webhook 自定义 Header"
                      value={settings.alertsWebhookHeaders}
                      onChange={(event) => updateDirtySettings(s => ({ ...s, alertsWebhookHeaders: event.target.value }))}
                      disabled={!settings.alertsEnabled}
                      placeholder={"Authorization: Bearer token\nX-MnemoNAS: alerts"}
                      rows={3}
                      className={cn(
                        "input-shell w-full rounded-medium px-3 py-2 text-sm bg-transparent outline-none",
                        "border border-transparent focus:border-accent-primary",
                        !settings.alertsEnabled && "opacity-60 cursor-not-allowed"
                      )}
                    />
                    <p className="text-xs text-default-500 mt-1">每行一个 Header，使用 Key:Value 格式。</p>
                  </div>
                </div>
              </SettingsSection>

              <SettingsSection
                title="收藏功能"
                description="控制文件收藏能力；关闭后收藏接口会立即拒绝请求"
                icon={Star}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用收藏功能"
                    description="允许标记收藏、查询收藏状态和维护收藏备注"
                  >
                    <Switch
                      aria-label="启用收藏功能"
                      isSelected={settings.favoritesEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, favoritesEnabled: v }))}
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
            </div>
          </Tab>

          <Tab key="shares" title="分享管理">
            <div className="space-y-6 mt-6">
              <SettingsSection
                title="分享功能配置"
                description="控制分享链接功能与默认基础地址；关闭后公开访问会立即失效"
                icon={Link2}
              >
                <div className="space-y-4">
                  <SettingRow
                    label="启用分享功能"
                    description="允许创建和访问公开分享链接"
                  >
                    <Switch
                      isSelected={settings.shareEnabled}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, shareEnabled: v }))}
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
                    label="分享基础 URL"
                    description="用于生成完整分享链接，可留空使用当前访问地址；保存后会立即影响新创建的分享"
                  >
                    <Input
                      value={settings.shareBaseURL}
                      onValueChange={(v) => updateDirtySettings(s => ({ ...s, shareBaseURL: v }))}
                      placeholder="https://nas.example.com"
                      isDisabled={!settings.shareEnabled}
                      classNames={{
                        inputWrapper: "input-shell group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>

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
