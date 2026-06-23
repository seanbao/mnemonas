import { useCallback, useEffect, useRef, useState } from 'react'
import { addToast, Button, Card, CardBody, CardHeader, Chip, Divider, Input, Switch } from '@heroui/react'
import { AlertCircle, HardDrive, RefreshCw, RotateCcw, Save } from 'lucide-react'
import { getSettings, SettingsError, updateSettings, type DiskHealthDeviceSettings } from '@/api/settings'
import {
  GENERIC_ACTION_ERROR_DESCRIPTION,
  GENERIC_LOAD_ERROR_DESCRIPTION,
  getUserFacingErrorDescription,
} from '@/lib/apiMessages'
import { cn, hasControlCharacter } from '@/lib/utils'

interface DiskHealthDraft {
  enabled: boolean
  checkInterval: string
  probeTimeout: string
  cooldownPeriod: string
  command: string
  temperatureWarningC: string
  temperatureCriticalC: string
  mediaWearWarningPercent: string
  mediaWearCriticalPercent: string
  devices: string
}

type DiskHealthDraftField = Exclude<keyof DiskHealthDraft, 'enabled'>
type DiskHealthValidationErrors = Partial<Record<DiskHealthDraftField, string>>

interface DiskHealthValidationResult {
  errors: DiskHealthValidationErrors
  toast?: {
    title: string
    description: string
  }
  devices?: DiskHealthDeviceSettings[]
}

const defaultDiskHealth: DiskHealthDraft = {
  enabled: false,
  checkInterval: '1h',
  probeTimeout: '15s',
  cooldownPeriod: '4h',
  command: 'smartctl',
  temperatureWarningC: '50',
  temperatureCriticalC: '60',
  mediaWearWarningPercent: '80',
  mediaWearCriticalPercent: '100',
  devices: '',
}

const goDurationPattern = /^(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/u

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

function isPositiveGoDuration(value: string): boolean {
  const trimmed = value.trim()
  if (!goDurationPattern.test(trimmed)) {
    return false
  }

  const numericParts = trimmed.match(/\d+(?:\.\d+)?/gu) ?? []
  return numericParts.some((part) => Number(part) > 0)
}

function isValidDiskHealthCommand(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed || hasControlCharacter(trimmed) || /\s/u.test(trimmed) || trimmed === '.' || trimmed === '..') {
    return false
  }
  return trimmed.startsWith('/') || !trimmed.includes('/')
}

function formatDiskHealthDeviceLines(devices: DiskHealthDeviceSettings[] | undefined): string {
  return (devices ?? [])
    .map((device) => [
      device.path,
      device.name ?? '',
      device.type ?? '',
      device.serial ?? '',
      device.temperature_warning_c ? String(device.temperature_warning_c) : '',
      device.temperature_critical_c ? String(device.temperature_critical_c) : '',
    ].join(' | '))
    .join('\n')
}

function parseOptionalNonNegativeIntegerCell(
  value: string,
  lineNumber: number,
  field: string,
): { value?: number; error?: string } {
  const trimmed = value.trim()
  if (!trimmed) {
    return {}
  }
  const parsed = Number(trimmed)
  if (!/^\d+$/u.test(trimmed) || !Number.isSafeInteger(parsed)) {
    return { error: `第 ${lineNumber} 行 ${field} 必须是 0 或不超过安全范围的整数` }
  }
  return { value: parsed }
}

function parseDiskHealthDeviceLines(value: string): { devices: DiskHealthDeviceSettings[]; error?: string } {
  const lines = value.split('\n')
  const devices: DiskHealthDeviceSettings[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const lineNumber = index + 1
    const line = lines[index].trim()
    if (!line) {
      continue
    }

    const parts = line.split('|').map((part) => part.trim())
    if (parts.length > 6) {
      return { devices: [], error: `第 ${lineNumber} 行最多包含 6 列` }
    }

    const [path, name = '', type = '', serial = '', warningText = '', criticalText = ''] = parts
    if (!path || !path.startsWith('/') || hasControlCharacter(path)) {
      return { devices: [], error: `第 ${lineNumber} 行设备路径必须是绝对路径` }
    }
    if ([name, type, serial].some(hasControlCharacter)) {
      return { devices: [], error: `第 ${lineNumber} 行设备名称、类型和序列号不能包含控制字符` }
    }

    const warning = parseOptionalNonNegativeIntegerCell(warningText, lineNumber, '温度提醒阈值')
    if (warning.error) {
      return { devices: [], error: warning.error }
    }
    const critical = parseOptionalNonNegativeIntegerCell(criticalText, lineNumber, '温度严重阈值')
    if (critical.error) {
      return { devices: [], error: critical.error }
    }
    if (warning.value && critical.value && critical.value < warning.value) {
      return { devices: [], error: `第 ${lineNumber} 行温度严重阈值不能小于提醒阈值` }
    }

    devices.push({
      path,
      ...(name && { name }),
      ...(type && { type }),
      ...(serial && { serial }),
      ...(warning.value !== undefined && { temperature_warning_c: warning.value }),
      ...(critical.value !== undefined && { temperature_critical_c: critical.value }),
    })
  }

  return { devices }
}

function mapDiskHealthSettings(response: Awaited<ReturnType<typeof getSettings>>): DiskHealthDraft {
  const diskHealth = response.data.disk_health
  if (!diskHealth) {
    return { ...defaultDiskHealth }
  }

  return {
    enabled: diskHealth.enabled,
    checkInterval: diskHealth.check_interval,
    probeTimeout: diskHealth.probe_timeout,
    cooldownPeriod: diskHealth.cooldown_period,
    command: diskHealth.command,
    temperatureWarningC: String(diskHealth.temperature_warning_c),
    temperatureCriticalC: String(diskHealth.temperature_critical_c),
    mediaWearWarningPercent: String(diskHealth.media_wear_warning_percent),
    mediaWearCriticalPercent: String(diskHealth.media_wear_critical_percent),
    devices: formatDiskHealthDeviceLines(diskHealth.devices),
  }
}

function diskHealthDraftEqual(left: DiskHealthDraft, right: DiskHealthDraft): boolean {
  return Object.keys(left).every((key) => left[key as keyof DiskHealthDraft] === right[key as keyof DiskHealthDraft])
}

function validationFailure(
  field: DiskHealthDraftField | DiskHealthDraftField[],
  title: string,
  description: string,
): DiskHealthValidationResult {
  const fields = Array.isArray(field) ? field : [field]
  return {
    errors: Object.fromEntries(fields.map((entry) => [entry, description])) as DiskHealthValidationErrors,
    toast: { title, description },
  }
}

function validateDiskHealth(draft: DiskHealthDraft): DiskHealthValidationResult {
  const parsedDevices = parseDiskHealthDeviceLines(draft.devices)
  if (parsedDevices.error) {
    return validationFailure('devices', '磁盘健康设备格式无效', parsedDevices.error)
  }

  if (!isPositiveGoDuration(draft.checkInterval)) {
    return validationFailure('checkInterval', '磁盘健康检查间隔格式无效', '检查间隔必须使用 1h / 30m 这类 Go duration 格式，且大于 0')
  }
  if (!isPositiveGoDuration(draft.probeTimeout)) {
    return validationFailure('probeTimeout', '磁盘健康探测超时格式无效', '探测超时必须使用 15s / 1m 这类 Go duration 格式，且大于 0')
  }
  if (!isPositiveGoDuration(draft.cooldownPeriod)) {
    return validationFailure('cooldownPeriod', '磁盘健康冷却时间格式无效', '冷却时间必须使用 4h / 30m 这类 Go duration 格式，且大于 0')
  }
  if (!isValidDiskHealthCommand(draft.command)) {
    return validationFailure('command', '磁盘健康命令格式无效', '命令必须是单个可执行文件名或绝对路径，不能包含空白或控制字符')
  }

  const temperatureWarning = draft.temperatureWarningC.trim()
  const parsedTemperatureWarning = Number(temperatureWarning)
  if (!/^\d+$/u.test(temperatureWarning) || !Number.isSafeInteger(parsedTemperatureWarning)) {
    return validationFailure('temperatureWarningC', '磁盘温度提醒阈值格式无效', '温度提醒阈值必须是 0 或不超过安全范围的整数')
  }
  const temperatureCritical = draft.temperatureCriticalC.trim()
  const parsedTemperatureCritical = Number(temperatureCritical)
  if (!/^\d+$/u.test(temperatureCritical) || !Number.isSafeInteger(parsedTemperatureCritical)) {
    return validationFailure('temperatureCriticalC', '磁盘温度严重阈值格式无效', '温度严重阈值必须是 0 或不超过安全范围的整数')
  }
  if (parsedTemperatureWarning > 0 && parsedTemperatureCritical > 0 && parsedTemperatureCritical < parsedTemperatureWarning) {
    return validationFailure(['temperatureWarningC', 'temperatureCriticalC'], '磁盘温度阈值关系无效', '温度严重阈值不能小于提醒阈值')
  }

  const mediaWearWarning = draft.mediaWearWarningPercent.trim()
  const parsedMediaWearWarning = Number(mediaWearWarning)
  if (!/^\d+$/u.test(mediaWearWarning) || !Number.isInteger(parsedMediaWearWarning) || parsedMediaWearWarning > 100) {
    return validationFailure('mediaWearWarningPercent', '介质磨损提醒阈值格式无效', '介质磨损提醒阈值必须是 0 到 100 之间的整数')
  }
  const mediaWearCritical = draft.mediaWearCriticalPercent.trim()
  const parsedMediaWearCritical = Number(mediaWearCritical)
  if (!/^\d+$/u.test(mediaWearCritical) || !Number.isInteger(parsedMediaWearCritical) || parsedMediaWearCritical > 100) {
    return validationFailure('mediaWearCriticalPercent', '介质磨损严重阈值格式无效', '介质磨损严重阈值必须是 0 到 100 之间的整数')
  }
  if (parsedMediaWearWarning > 0 && parsedMediaWearCritical > 0 && parsedMediaWearCritical < parsedMediaWearWarning) {
    return validationFailure(['mediaWearWarningPercent', 'mediaWearCriticalPercent'], '介质磨损阈值关系无效', '介质磨损严重阈值不能小于提醒阈值')
  }

  return { errors: {}, devices: parsedDevices.devices }
}

function getLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '磁盘健康设置暂不可用',
      description: '设置服务当前不可用，请检查设备状态或稍后重试。',
    }
  }
  return {
    title: '加载磁盘健康设置失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getSaveErrorToast(error: unknown): { title: string; description: string; color: 'warning' | 'danger' } {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '磁盘健康设置暂不可用',
      description: '设置服务当前不可用，当前修改尚未保存。',
      color: 'warning',
    }
  }
  return {
    title: '保存磁盘健康设置失败',
    description: getUserFacingErrorDescription(error, GENERIC_ACTION_ERROR_DESCRIPTION),
    color: 'danger',
  }
}

function FieldError({ message }: { message?: string }) {
  return message ? <p className="mt-1 text-xs text-danger" role="alert">{message}</p> : null
}

export function DiskHealthSettings() {
  const loadAbortControllerRef = useRef<AbortController | null>(null)
  const saveAbortControllerRef = useRef<AbortController | null>(null)
  const [saved, setSaved] = useState<DiskHealthDraft | null>(null)
  const [draft, setDraft] = useState<DiskHealthDraft>(defaultDiskHealth)
  const [validationErrors, setValidationErrors] = useState<DiskHealthValidationErrors>({})
  const [loadError, setLoadError] = useState<unknown | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)

  const isDirty = saved !== null && !diskHealthDraftEqual(saved, draft)

  const loadSettings = useCallback(async () => {
    loadAbortControllerRef.current?.abort()
    const controller = new AbortController()
    loadAbortControllerRef.current = controller
    setIsLoading(true)
    setLoadError(null)

    try {
      const response = await getSettings({ signal: controller.signal })
      if (controller.signal.aborted || loadAbortControllerRef.current !== controller) {
        return
      }
      const next = mapDiskHealthSettings(response)
      setSaved(next)
      setDraft(next)
      setValidationErrors({})
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error) || loadAbortControllerRef.current !== controller) {
        return
      }
      setLoadError(error)
    } finally {
      if (loadAbortControllerRef.current === controller) {
        loadAbortControllerRef.current = null
        setIsLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    queueMicrotask(() => {
      if (!cancelled) {
        void loadSettings()
      }
    })
    return () => {
      cancelled = true
      loadAbortControllerRef.current?.abort()
      loadAbortControllerRef.current = null
      saveAbortControllerRef.current?.abort()
      saveAbortControllerRef.current = null
    }
  }, [loadSettings])

  const updateDraft = (update: Partial<DiskHealthDraft>) => {
    setDraft((current) => ({ ...current, ...update }))
    setValidationErrors({})
  }

  const handleReset = () => {
    if (!saved || isSaving) {
      return
    }
    setDraft(saved)
    setValidationErrors({})
  }

  const handleSave = async () => {
    if (!saved || !isDirty || isSaving) {
      return
    }

    const validation = validateDiskHealth(draft)
    setValidationErrors(validation.errors)
    if (validation.toast || !validation.devices) {
      if (validation.toast) {
        addToast({ ...validation.toast, color: 'danger' })
      }
      return
    }

    const submitted: DiskHealthDraft = {
      enabled: draft.enabled,
      checkInterval: draft.checkInterval.trim(),
      probeTimeout: draft.probeTimeout.trim(),
      cooldownPeriod: draft.cooldownPeriod.trim(),
      command: draft.command.trim(),
      temperatureWarningC: draft.temperatureWarningC.trim(),
      temperatureCriticalC: draft.temperatureCriticalC.trim(),
      mediaWearWarningPercent: draft.mediaWearWarningPercent.trim(),
      mediaWearCriticalPercent: draft.mediaWearCriticalPercent.trim(),
      devices: draft.devices.trim(),
    }
    saveAbortControllerRef.current?.abort()
    const controller = new AbortController()
    saveAbortControllerRef.current = controller
    setIsSaving(true)

    try {
      const result = await updateSettings({
        disk_health: {
          enabled: submitted.enabled,
          check_interval: submitted.checkInterval,
          probe_timeout: submitted.probeTimeout,
          cooldown_period: submitted.cooldownPeriod,
          command: submitted.command,
          temperature_warning_c: Number(submitted.temperatureWarningC),
          temperature_critical_c: Number(submitted.temperatureCriticalC),
          media_wear_warning_percent: Number(submitted.mediaWearWarningPercent),
          media_wear_critical_percent: Number(submitted.mediaWearCriticalPercent),
          devices: validation.devices,
        },
      }, { signal: controller.signal })
      if (controller.signal.aborted || saveAbortControllerRef.current !== controller) {
        return
      }

      setSaved(submitted)
      setDraft(submitted)
      setValidationErrors({})
      addToast({
        title: result.warning ? '磁盘健康设置已保存，但存在警告' : '磁盘健康设置已保存',
        description: result.warning && result.message.trim() ? result.message.trim() : undefined,
        color: result.warning ? 'warning' : 'success',
      })
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error) || saveAbortControllerRef.current !== controller) {
        return
      }
      addToast(getSaveErrorToast(error))
    } finally {
      if (saveAbortControllerRef.current === controller) {
        saveAbortControllerRef.current = null
        setIsSaving(false)
      }
    }
  }

  const loadErrorPresentation = loadError ? getLoadErrorPresentation(loadError) : null
  const fieldsDisabled = !draft.enabled || isSaving

  const inputClassNames = { inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }

  return (
    <Card className="card-mnemonas" aria-label="磁盘健康设置">
      <CardHeader className="flex flex-col items-start gap-3 pb-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-success/15">
            <HardDrive size={20} className="text-success" />
          </div>
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="font-semibold">磁盘健康监控</h3>
              {isDirty && <Chip size="sm" variant="flat" color="warning">有未保存更改</Chip>}
            </div>
            <p className="text-xs text-default-500">设置 SMART 探测周期、健康阈值和需要监控的磁盘</p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="bordered"
            className="rounded-lg"
            startContent={<RotateCcw size={16} />}
            isDisabled={!isDirty || isSaving}
            onPress={handleReset}
          >
            重置
          </Button>
          <Button
            className="btn-primary rounded-lg"
            startContent={<Save size={16} />}
            isDisabled={!isDirty || isLoading}
            isLoading={isSaving}
            onPress={() => { void handleSave() }}
          >
            保存磁盘健康设置
          </Button>
        </div>
      </CardHeader>
      <Divider />
      <CardBody>
        {isLoading ? (
          <div className="flex items-center justify-center gap-2 py-8 text-sm text-default-500" role="status">
            <RefreshCw size={20} className="animate-spin" />
            加载磁盘健康设置…
          </div>
        ) : loadErrorPresentation ? (
          <div className="flex flex-col items-start gap-4 rounded-lg border border-warning/30 bg-warning/5 p-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-start gap-3">
              <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
              <div>
                <p className="text-sm font-medium text-foreground">{loadErrorPresentation.title}</p>
                <p className="mt-1 text-xs text-default-600">{loadErrorPresentation.description}</p>
              </div>
            </div>
            <Button variant="bordered" className="rounded-lg" onPress={() => { void loadSettings() }}>
              重新加载
            </Button>
          </div>
        ) : (
          <div className="space-y-5">
            <div className="flex flex-col gap-3 rounded-lg border border-divider bg-content2/35 p-4 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p className="text-sm font-medium text-foreground">启用磁盘健康检查</p>
                <p className="mt-1 text-xs text-default-500">定期检查已配置设备的 SMART、温度和介质健康状态。</p>
              </div>
              <Switch
                aria-label="启用磁盘健康检查"
                isSelected={draft.enabled}
                isDisabled={isSaving}
                onValueChange={(enabled) => updateDraft({ enabled })}
              >
                启用磁盘健康检查
              </Switch>
            </div>

            <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
              <div>
                <Input label="检查间隔" aria-label="磁盘健康检查间隔" value={draft.checkInterval} onValueChange={(checkInterval) => updateDraft({ checkInterval })} placeholder="1h" isDisabled={fieldsDisabled} aria-invalid={validationErrors.checkInterval ? 'true' : 'false'} classNames={inputClassNames} />
                <FieldError message={validationErrors.checkInterval} />
              </div>
              <div>
                <Input label="探测超时" aria-label="磁盘健康探测超时" value={draft.probeTimeout} onValueChange={(probeTimeout) => updateDraft({ probeTimeout })} placeholder="15s" isDisabled={fieldsDisabled} aria-invalid={validationErrors.probeTimeout ? 'true' : 'false'} classNames={inputClassNames} />
                <FieldError message={validationErrors.probeTimeout} />
              </div>
              <div>
                <Input label="通知冷却时间" aria-label="磁盘健康冷却时间" value={draft.cooldownPeriod} onValueChange={(cooldownPeriod) => updateDraft({ cooldownPeriod })} placeholder="4h" isDisabled={fieldsDisabled} aria-invalid={validationErrors.cooldownPeriod ? 'true' : 'false'} classNames={inputClassNames} />
                <FieldError message={validationErrors.cooldownPeriod} />
              </div>
              <div>
                <Input label="探测命令" aria-label="磁盘健康探测命令" value={draft.command} onValueChange={(command) => updateDraft({ command })} placeholder="smartctl" isDisabled={fieldsDisabled} aria-invalid={validationErrors.command ? 'true' : 'false'} classNames={inputClassNames} />
                <FieldError message={validationErrors.command} />
              </div>
              <div>
                <Input label="温度提醒阈值 (°C)" aria-label="磁盘温度提醒阈值" type="number" min={0} inputMode="numeric" value={draft.temperatureWarningC} onValueChange={(temperatureWarningC) => updateDraft({ temperatureWarningC })} isDisabled={fieldsDisabled} aria-invalid={validationErrors.temperatureWarningC ? 'true' : 'false'} classNames={inputClassNames} />
                <FieldError message={validationErrors.temperatureWarningC} />
              </div>
              <div>
                <Input label="温度严重阈值 (°C)" aria-label="磁盘温度严重阈值" type="number" min={0} inputMode="numeric" value={draft.temperatureCriticalC} onValueChange={(temperatureCriticalC) => updateDraft({ temperatureCriticalC })} isDisabled={fieldsDisabled} aria-invalid={validationErrors.temperatureCriticalC ? 'true' : 'false'} classNames={inputClassNames} />
                <FieldError message={validationErrors.temperatureCriticalC} />
              </div>
              <div>
                <Input label="介质磨损提醒 (%)" aria-label="介质磨损提醒阈值" type="number" min={0} max={100} step={1} inputMode="numeric" value={draft.mediaWearWarningPercent} onValueChange={(mediaWearWarningPercent) => updateDraft({ mediaWearWarningPercent })} isDisabled={fieldsDisabled} aria-invalid={validationErrors.mediaWearWarningPercent ? 'true' : 'false'} classNames={inputClassNames} />
                <FieldError message={validationErrors.mediaWearWarningPercent} />
              </div>
              <div>
                <Input label="介质磨损严重 (%)" aria-label="介质磨损严重阈值" type="number" min={0} max={100} step={1} inputMode="numeric" value={draft.mediaWearCriticalPercent} onValueChange={(mediaWearCriticalPercent) => updateDraft({ mediaWearCriticalPercent })} isDisabled={fieldsDisabled} aria-invalid={validationErrors.mediaWearCriticalPercent ? 'true' : 'false'} classNames={inputClassNames} />
                <FieldError message={validationErrors.mediaWearCriticalPercent} />
              </div>
            </div>

            <Divider />

            <div>
              <label htmlFor="disk-health-devices" className="mb-1.5 block text-sm font-medium text-default-600">监控设备</label>
              <textarea
                id="disk-health-devices"
                aria-label="磁盘健康设备列表"
                value={draft.devices}
                onChange={(event) => updateDraft({ devices: event.target.value })}
                disabled={fieldsDisabled}
                placeholder="/dev/disk/by-id/ata-data | Data | sat | SER123 | 45 | 55"
                rows={4}
                aria-invalid={validationErrors.devices ? 'true' : 'false'}
                className={cn(
                  'input-shell w-full rounded-medium border border-transparent bg-transparent px-3 py-2 text-sm outline-none focus:border-accent-primary',
                  fieldsDisabled && 'cursor-not-allowed opacity-60',
                  validationErrors.devices && 'border-danger',
                )}
              />
              <p className="mt-1 text-xs text-default-500">每行格式：设备路径 | 名称 | 类型 | 期望序列号 | 温度提醒阈值 | 温度严重阈值。后五列可留空。</p>
              <FieldError message={validationErrors.devices} />
            </div>
          </div>
        )}
      </CardBody>
    </Card>
  )
}
