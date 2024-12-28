import { useState } from 'react'
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
} from '@heroui/react'
import { 
  Server, 
  Database, 
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
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { ShareManager } from '@/components/share'

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
    <Card className="bg-bg-card border border-divider shadow-sm">
      <CardHeader className="flex gap-4 pb-2">
        <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-accent-primary to-accent-dark flex items-center justify-center shadow-sm">
          <Icon size={20} className="text-white" />
        </div>
        <div className="flex-1">
          <h3 className="text-base font-semibold text-text-primary">{title}</h3>
          <p className="text-xs text-text-muted mt-0.5">{description}</p>
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
    <div className="flex items-center justify-between py-3 first:pt-0 last:pb-0">
      <div className="flex-1 min-w-0 pr-4">
        <div className="text-sm font-medium text-text-primary">{label}</div>
        {description && (
          <div className="text-xs text-text-muted mt-0.5">{description}</div>
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
  const [isSaving, setIsSaving] = useState(false)
  
  // Settings state (would be loaded from API in production)
  const [settings, setSettings] = useState({
    // Server
    serverHost: '0.0.0.0',
    serverPort: '8080',
    
    // Storage
    dataDir: '/var/lib/mnemonas/data',
    metadataDir: '/var/lib/mnemonas/metadata',
    tempDir: '/var/lib/mnemonas/tmp',
    
    // Retention
    maxVersions: 100,
    maxAge: '8760h',
    minFreeSpace: '10GB',
    gcInterval: '24h',
    
    // WebDAV
    webdavEnabled: true,
    webdavPrefix: '/dav',
    webdavReadOnly: false,
    webdavAuthType: 'basic',
    webdavUsername: 'admin',
    webdavPassword: '',
    
    // CDC
    minChunkSize: '256KB',
    avgChunkSize: '1MB',
    maxChunkSize: '4MB',
  })

  const handleSave = async () => {
    setIsSaving(true)
    // Simulate API call
    await new Promise(resolve => setTimeout(resolve, 1000))
    setIsSaving(false)
    addToast({ title: '设置已保存', color: 'success' })
  }

  const handleReset = () => {
    addToast({ title: '设置已重置为默认值', color: 'warning' })
  }

  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="max-w-4xl mx-auto p-7">
        {/* Header */}
        <div className="flex items-center justify-between mb-8">
          <div>
            <h1 className="text-2xl font-bold text-text-primary">系统设置</h1>
            <p className="text-sm text-text-muted mt-1">配置 MnemoNAS 系统参数</p>
          </div>
          <div className="flex gap-3">
            <Button
              variant="bordered"
              className="border-divider bg-bg-card text-text-secondary hover:bg-bg-hover"
              startContent={<RefreshCw size={16} />}
              onPress={handleReset}
            >
              重置
            </Button>
            <Button
              className="bg-gradient-to-br from-accent-primary to-accent-dark text-white shadow-[0_4px_12px_rgba(167,139,250,0.4)]"
              startContent={<Save size={16} />}
              isLoading={isSaving}
              onPress={handleSave}
            >
              保存设置
            </Button>
          </div>
        </div>

        {/* Tabs */}
        <Tabs 
          selectedKey={selectedTab}
          onSelectionChange={(key) => setSelectedTab(key as string)}
          classNames={{
            tabList: "bg-bg-card border border-divider rounded-xl p-1 gap-1",
            tab: "px-4 py-2 rounded-lg data-[selected=true]:bg-accent-primary data-[selected=true]:text-white",
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
                  <Input
                    label="监听地址"
                    placeholder="0.0.0.0"
                    value={settings.serverHost}
                    onValueChange={(v) => setSettings(s => ({ ...s, serverHost: v }))}
                    startContent={<Globe size={16} className="text-text-muted" />}
                    classNames={{ 
                      inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                      label: "text-text-secondary",
                    }}
                  />
                  <Input
                    label="端口"
                    placeholder="8080"
                    value={settings.serverPort}
                    onValueChange={(v) => setSettings(s => ({ ...s, serverPort: v }))}
                    classNames={{ 
                      inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                      label: "text-text-secondary",
                    }}
                  />
                </div>
              </SettingsSection>

              <SettingsSection
                title="存储路径"
                description="配置数据存储目录"
                icon={Folder}
              >
                <div className="space-y-4">
                  <Input
                    label="数据目录"
                    placeholder="/var/lib/mnemonas/data"
                    value={settings.dataDir}
                    onValueChange={(v) => setSettings(s => ({ ...s, dataDir: v }))}
                    startContent={<Database size={16} className="text-text-muted" />}
                    classNames={{ 
                      inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                      label: "text-text-secondary",
                    }}
                  />
                  <Input
                    label="元数据目录"
                    placeholder="/var/lib/mnemonas/metadata"
                    value={settings.metadataDir}
                    onValueChange={(v) => setSettings(s => ({ ...s, metadataDir: v }))}
                    startContent={<Database size={16} className="text-text-muted" />}
                    classNames={{ 
                      inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                      label: "text-text-secondary",
                    }}
                  />
                  <Input
                    label="临时目录"
                    placeholder="/var/lib/mnemonas/tmp"
                    value={settings.tempDir}
                    onValueChange={(v) => setSettings(s => ({ ...s, tempDir: v }))}
                    startContent={<Folder size={16} className="text-text-muted" />}
                    classNames={{ 
                      inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                      label: "text-text-secondary",
                    }}
                  />
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
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary h-9",
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
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary h-9",
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
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary h-9",
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
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary h-9",
                      }}
                    />
                  </SettingRow>
                </div>
              </SettingsSection>
            </div>
          </Tab>

          <Tab key="webdav" title="WebDAV">
            <div className="space-y-6 mt-6">
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
                          "bg-bg-secondary"
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
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary h-9",
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
                          "bg-bg-secondary"
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
                    <div className="flex items-center gap-2 text-sm text-text-secondary">
                      <Lock size={14} />
                      <span>Basic Auth</span>
                    </div>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <div className="grid grid-cols-2 gap-4">
                    <Input
                      label="用户名"
                      placeholder="admin"
                      value={settings.webdavUsername}
                      onValueChange={(v) => setSettings(s => ({ ...s, webdavUsername: v }))}
                      isDisabled={!settings.webdavEnabled}
                      startContent={<User size={16} className="text-text-muted" />}
                      classNames={{ 
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                        label: "text-text-secondary",
                      }}
                    />
                    <Input
                      label="密码"
                      type="password"
                      placeholder="••••••••"
                      value={settings.webdavPassword}
                      onValueChange={(v) => setSettings(s => ({ ...s, webdavPassword: v }))}
                      isDisabled={!settings.webdavEnabled}
                      startContent={<Lock size={16} className="text-text-muted" />}
                      classNames={{ 
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                        label: "text-text-secondary",
                      }}
                    />
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
                  <div className="p-4 rounded-xl bg-bg-secondary border border-divider">
                    <div className="flex items-start gap-3">
                      <div className="w-8 h-8 rounded-lg bg-starlight/20 flex items-center justify-center shrink-0 mt-0.5">
                        <HardDrive size={16} className="text-starlight" />
                      </div>
                      <div>
                        <div className="text-sm font-medium text-text-primary">关于 CDC 分块</div>
                        <div className="text-xs text-text-muted mt-1 leading-relaxed">
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
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary h-9",
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
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary h-9",
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
                        inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary h-9",
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
                    <code className="text-sm text-text-secondary bg-bg-secondary px-2 py-1 rounded">
                      30s
                    </code>
                  </SettingRow>
                  <Divider className="bg-divider" />
                  <SettingRow
                    label="最大重试次数"
                    description="失败后重试次数"
                  >
                    <code className="text-sm text-text-secondary bg-bg-secondary px-2 py-1 rounded">
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
