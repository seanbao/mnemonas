import { useCallback, useEffect, useRef, useState } from 'react'
import { addToast, Button, Card, CardBody, CardHeader, Chip, Divider, Input, Switch } from '@heroui/react'
import { AlertCircle, Clock, RefreshCw, RotateCcw, Save } from 'lucide-react'
import { getSettings, SettingsError, updateSettings } from '@/api/settings'
import {
  GENERIC_ACTION_ERROR_DESCRIPTION,
  GENERIC_LOAD_ERROR_DESCRIPTION,
  getUserFacingErrorDescription,
} from '@/lib/apiMessages'

interface ScrubScheduleDraft {
  enabled: boolean
  scheduleInterval: string
  retryInterval: string
  maxRetries: string
}

interface ScrubScheduleValidationErrors {
  scheduleInterval?: string
  retryInterval?: string
  maxRetries?: string
}

const defaultScrubSchedule: ScrubScheduleDraft = {
  enabled: false,
  scheduleInterval: '168h',
  retryInterval: '1h',
  maxRetries: '1',
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

function mapScrubScheduleSettings(response: Awaited<ReturnType<typeof getSettings>>): ScrubScheduleDraft {
  const scrub = response.data.maintenance?.scrub
  if (!scrub) {
    return { ...defaultScrubSchedule }
  }

  return {
    enabled: scrub.enabled,
    scheduleInterval: scrub.schedule_interval,
    retryInterval: scrub.retry_interval,
    maxRetries: String(scrub.max_retries),
  }
}

function scrubScheduleDraftEqual(left: ScrubScheduleDraft, right: ScrubScheduleDraft): boolean {
  return left.enabled === right.enabled
    && left.scheduleInterval === right.scheduleInterval
    && left.retryInterval === right.retryInterval
    && left.maxRetries === right.maxRetries
}

function validateScrubSchedule(draft: ScrubScheduleDraft): ScrubScheduleValidationErrors {
  const errors: ScrubScheduleValidationErrors = {}
  if (!isPositiveGoDuration(draft.scheduleInterval)) {
    errors.scheduleInterval = '常规间隔必须使用 168h、1h30m 这类 Go duration 格式，且大于 0。'
  }
  if (!isPositiveGoDuration(draft.retryInterval)) {
    errors.retryInterval = '失败重试间隔必须使用 1h、30m 这类 Go duration 格式，且大于 0。'
  }

  const maxRetries = draft.maxRetries.trim()
  const parsedMaxRetries = Number(maxRetries)
  if (!/^\d+$/u.test(maxRetries) || !Number.isSafeInteger(parsedMaxRetries)) {
    errors.maxRetries = '最大重试次数必须是 0 或不超过安全范围的整数。'
  }
  return errors
}

function getLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '周期校验设置暂不可用',
      description: '设置服务当前不可用，请检查设备状态或稍后重试。',
    }
  }
  return {
    title: '加载周期校验设置失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getSaveErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '周期校验设置暂不可用',
      description: '设置服务当前不可用，当前修改尚未保存。',
      color: 'warning',
    }
  }
  return {
    title: '保存周期校验设置失败',
    description: getUserFacingErrorDescription(error, GENERIC_ACTION_ERROR_DESCRIPTION),
    color: 'danger',
  }
}

export function ScrubScheduleSettings() {
  const loadAbortControllerRef = useRef<AbortController | null>(null)
  const saveAbortControllerRef = useRef<AbortController | null>(null)
  const [saved, setSaved] = useState<ScrubScheduleDraft | null>(null)
  const [draft, setDraft] = useState<ScrubScheduleDraft>(defaultScrubSchedule)
  const [validationErrors, setValidationErrors] = useState<ScrubScheduleValidationErrors>({})
  const [loadError, setLoadError] = useState<unknown | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)

  const isDirty = saved !== null && !scrubScheduleDraftEqual(saved, draft)

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
      const next = mapScrubScheduleSettings(response)
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

  const updateDraft = (update: Partial<ScrubScheduleDraft>) => {
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

    const errors = validateScrubSchedule(draft)
    setValidationErrors(errors)
    if (Object.keys(errors).length > 0) {
      addToast({
        title: '周期校验设置格式无效',
        description: '请修正标记的字段后再保存。',
        color: 'danger',
      })
      return
    }

    const submitted: ScrubScheduleDraft = {
      enabled: draft.enabled,
      scheduleInterval: draft.scheduleInterval.trim(),
      retryInterval: draft.retryInterval.trim(),
      maxRetries: draft.maxRetries.trim(),
    }
    saveAbortControllerRef.current?.abort()
    const controller = new AbortController()
    saveAbortControllerRef.current = controller
    setIsSaving(true)

    try {
      const result = await updateSettings({
        maintenance: {
          scrub: {
            enabled: submitted.enabled,
            schedule_interval: submitted.scheduleInterval,
            retry_interval: submitted.retryInterval,
            max_retries: Number(submitted.maxRetries),
          },
        },
      }, { signal: controller.signal })
      if (controller.signal.aborted || saveAbortControllerRef.current !== controller) {
        return
      }

      setSaved(submitted)
      setDraft(submitted)
      setValidationErrors({})
      addToast({
        title: result.warning ? '周期校验设置已保存，但存在警告' : '周期校验设置已保存',
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

  return (
    <Card className="card-mnemonas" aria-label="周期校验计划">
      <CardHeader className="flex flex-col items-start gap-3 pb-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-accent-primary/15">
            <Clock size={20} className="text-accent-primary" />
          </div>
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="font-semibold">周期校验计划</h3>
              {isDirty && <Chip size="sm" variant="flat" color="warning">有未保存更改</Chip>}
            </div>
            <p className="text-xs text-default-500">按计划运行完整性校验，并限制失败后的自动重试</p>
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
            保存计划
          </Button>
        </div>
      </CardHeader>
      <Divider />
      <CardBody>
        {isLoading ? (
          <div className="flex items-center justify-center gap-2 py-8 text-sm text-default-500" role="status">
            <RefreshCw size={20} className="animate-spin" />
            加载周期校验设置…
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
                <p className="text-sm font-medium text-foreground">启用周期校验</p>
                <p className="mt-1 text-xs text-default-500">关闭后仍可从本页手动开始校验。</p>
              </div>
              <Switch
                aria-label="启用周期校验"
                isSelected={draft.enabled}
                isDisabled={isSaving}
                onValueChange={(enabled) => updateDraft({ enabled })}
              >
                启用周期校验
              </Switch>
            </div>

            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
              <div>
                <label htmlFor="scrub-schedule-interval" className="mb-1.5 block text-sm font-medium text-default-600">常规间隔</label>
                <Input
                  id="scrub-schedule-interval"
                  aria-label="常规间隔"
                  value={draft.scheduleInterval}
                  isDisabled={isSaving}
                  onValueChange={(scheduleInterval) => updateDraft({ scheduleInterval })}
                  placeholder="168h"
                  aria-invalid={validationErrors.scheduleInterval ? 'true' : 'false'}
                  classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                />
                <p className="mt-1 text-xs text-default-500">例如 168h（每 7 天）。</p>
                {validationErrors.scheduleInterval && <p className="mt-1 text-xs text-danger" role="alert">{validationErrors.scheduleInterval}</p>}
              </div>
              <div>
                <label htmlFor="scrub-retry-interval" className="mb-1.5 block text-sm font-medium text-default-600">失败重试间隔</label>
                <Input
                  id="scrub-retry-interval"
                  aria-label="失败重试间隔"
                  value={draft.retryInterval}
                  isDisabled={isSaving}
                  onValueChange={(retryInterval) => updateDraft({ retryInterval })}
                  placeholder="1h"
                  aria-invalid={validationErrors.retryInterval ? 'true' : 'false'}
                  classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                />
                <p className="mt-1 text-xs text-default-500">校验失败后再次尝试前的等待时间。</p>
                {validationErrors.retryInterval && <p className="mt-1 text-xs text-danger" role="alert">{validationErrors.retryInterval}</p>}
              </div>
              <div>
                <label htmlFor="scrub-max-retries" className="mb-1.5 block text-sm font-medium text-default-600">最大重试次数</label>
                <Input
                  id="scrub-max-retries"
                  aria-label="最大重试次数"
                  type="number"
                  min={0}
                  step={1}
                  inputMode="numeric"
                  value={draft.maxRetries}
                  isDisabled={isSaving}
                  onValueChange={(maxRetries) => updateDraft({ maxRetries })}
                  aria-invalid={validationErrors.maxRetries ? 'true' : 'false'}
                  classNames={{ inputWrapper: 'input-shell group-data-[focus=true]:border-accent-primary' }}
                />
                <p className="mt-1 text-xs text-default-500">0 表示失败后不自动重试。</p>
                {validationErrors.maxRetries && <p className="mt-1 text-xs text-danger" role="alert">{validationErrors.maxRetries}</p>}
              </div>
            </div>
          </div>
        )}
      </CardBody>
    </Card>
  )
}
