import { useCallback, useEffect, useRef, useState } from 'react'
import {
  addToast,
  Button,
  Card,
  CardBody,
  CardHeader,
  Chip,
  Divider,
  Switch,
} from '@heroui/react'
import { AlertCircle, RefreshCw, RotateCcw, Save, Star } from 'lucide-react'
import { getSettings, SettingsError, updateSettings } from '@/api/settings'
import {
  GENERIC_ACTION_ERROR_DESCRIPTION,
  GENERIC_LOAD_ERROR_DESCRIPTION,
  getUserFacingErrorDescription,
} from '@/lib/apiMessages'

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

function getLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof SettingsError && error.isUnavailable) {
    return {
      title: '收藏设置暂不可用',
      description: '设置服务当前不可用，请检查设备状态或稍后重试。',
    }
  }

  return {
    title: '加载收藏设置失败',
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
      title: '收藏设置暂不可用',
      description: '设置服务当前不可用，当前修改尚未保存。',
      color: 'warning',
    }
  }

  return {
    title: '保存收藏设置失败',
    description: getUserFacingErrorDescription(error, GENERIC_ACTION_ERROR_DESCRIPTION),
    color: 'danger',
  }
}

export function FavoritesSettings() {
  const loadAbortControllerRef = useRef<AbortController | null>(null)
  const saveAbortControllerRef = useRef<AbortController | null>(null)
  const [savedEnabled, setSavedEnabled] = useState<boolean | null>(null)
  const [draftEnabled, setDraftEnabled] = useState(true)
  const [loadError, setLoadError] = useState<unknown | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [isSaving, setIsSaving] = useState(false)

  const isDirty = savedEnabled !== null && savedEnabled !== draftEnabled

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

      const enabled = response.data.favorites?.enabled ?? true
      setSavedEnabled(enabled)
      setDraftEnabled(enabled)
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

  const handleReset = () => {
    if (savedEnabled === null || isSaving) {
      return
    }
    setDraftEnabled(savedEnabled)
  }

  const handleSave = async () => {
    if (savedEnabled === null || !isDirty || isSaving) {
      return
    }

    saveAbortControllerRef.current?.abort()
    const controller = new AbortController()
    saveAbortControllerRef.current = controller
    setIsSaving(true)

    try {
      const result = await updateSettings({
        favorites: {
          enabled: draftEnabled,
        },
      }, { signal: controller.signal })
      if (controller.signal.aborted || saveAbortControllerRef.current !== controller) {
        return
      }

      setSavedEnabled(draftEnabled)
      addToast({
        title: result.warning ? '收藏设置已保存，但存在警告' : '收藏设置已保存',
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
    <Card className="card-mnemonas shrink-0" aria-label="收藏功能设置">
      <CardHeader className="flex flex-col items-start gap-3 pb-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-accent-primary/15">
            <Star size={20} className="text-accent-primary" />
          </div>
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="font-semibold">收藏功能</h2>
              {isDirty && <Chip size="sm" variant="flat" color="warning">有未保存更改</Chip>}
            </div>
            <p className="text-xs text-default-500">控制所有账户是否可以收藏常用文件和文件夹</p>
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
            保存设置
          </Button>
        </div>
      </CardHeader>
      <Divider />
      <CardBody>
        {isLoading ? (
          <div className="flex items-center justify-center gap-2 py-6 text-sm text-default-500" role="status">
            <RefreshCw size={20} className="animate-spin" />
            加载收藏设置…
          </div>
        ) : loadErrorPresentation ? (
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <AlertCircle size={28} className="text-warning" />
            <div>
              <p className="font-medium">{loadErrorPresentation.title}</p>
              <p className="mt-1 text-sm text-default-500">{loadErrorPresentation.description}</p>
            </div>
            <Button
              variant="bordered"
              className="rounded-lg"
              startContent={<RefreshCw size={16} />}
              onPress={() => { void loadSettings() }}
            >
              重新加载
            </Button>
          </div>
        ) : (
          <Switch
            aria-label="启用收藏功能"
            isSelected={draftEnabled}
            onValueChange={setDraftEnabled}
            isDisabled={isSaving}
          >
            <span className="text-sm font-medium">启用收藏功能</span>
          </Switch>
        )}
      </CardBody>
    </Card>
  )
}
