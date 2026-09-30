import { useCallback, useEffect, useMemo, useState } from 'react'
import { Plus, RefreshCw, Save, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { MultiSelect, type Option } from '@/components/multi-select'

type GroupView = {
  name: string
  ratio: number
  configured_models: string[]
  available_models: string[]
}

type Rule = {
  id?: number
  target_group: string
  enabled: boolean
  models: string[]
}

type Config = {
  source_group: string
  enabled: boolean
  retry_enabled: boolean
  retry_max_retries: number
  retry_http_status_codes: string
  reason: string
  rules: Rule[]
}

type AdminData = {
  groups: GroupView[]
  configs: Config[]
}

function emptyConfig(sourceGroup: string): Config {
  return {
    source_group: sourceGroup,
    enabled: false,
    retry_enabled: false,
    retry_max_retries: 3,
    retry_http_status_codes: '429,502-504',
    reason: '',
    rules: [],
  }
}

function hasValidRetryStatusCodes(value: string): boolean {
  if (!value.trim()) return false
  return value.split(',').every((segment) => {
    const token = segment.trim().replace(/\s+/g, '')
    const match = token.match(/^(\d{3})(?:-(\d{3}))?$/)
    if (!match) return false
    const start = Number(match[1])
    const end = Number(match[2] || match[1])
    return (
      start >= 100 && end <= 599 && start <= end && (end < 200 || start > 299)
    )
  })
}

function normalizeConfig(
  config: Config | undefined,
  sourceGroup: string
): Config {
  const value = config || emptyConfig(sourceGroup)
  return {
    source_group: sourceGroup,
    enabled: Boolean(value.enabled),
    retry_enabled: Boolean(value.retry_enabled),
    retry_max_retries: Number(value.retry_max_retries) || 3,
    retry_http_status_codes: value.retry_http_status_codes || '429,502-504',
    reason: value.reason || '',
    rules: (value.rules || []).map((rule) => ({
      id: rule.id,
      target_group: rule.target_group || '',
      enabled: Boolean(rule.enabled),
      models: Array.isArray(rule.models) ? rule.models : [],
    })),
  }
}

export function GroupFallbackEditor() {
  const { t } = useTranslation()
  const [data, setData] = useState<AdminData | null>(null)
  const [sourceGroup, setSourceGroup] = useState('')
  const [config, setConfig] = useState<Config | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const load = useCallback(
    async (preferredSourceGroup = '') => {
      setLoading(true)
      try {
        const response = await api.get('/api/group/fallback')
        if (!response.data?.success)
          throw new Error(
            response.data?.message || t('Failed to load fallback settings')
          )
        const nextData = response.data.data as AdminData
        const firstGroup =
          preferredSourceGroup || nextData.groups[0]?.name || ''
        setData(nextData)
        setSourceGroup(firstGroup)
        setConfig(
          normalizeConfig(
            nextData.configs.find((item) => item.source_group === firstGroup),
            firstGroup
          )
        )
      } catch (error) {
        toast.error(
          error instanceof Error
            ? error.message
            : t('Failed to load fallback settings')
        )
      } finally {
        setLoading(false)
      }
    },
    [t]
  )

  useEffect(() => {
    const timeout = window.setTimeout(() => void load(), 0)
    return () => window.clearTimeout(timeout)
  }, [load])

  const selectSource = (value: string) => {
    setSourceGroup(value)
    setConfig(
      normalizeConfig(
        data?.configs.find((item) => item.source_group === value),
        value
      )
    )
  }

  const source = data?.groups.find((group) => group.name === sourceGroup)
  const targetGroups = useMemo(
    () => (data?.groups || []).filter((group) => group.name !== sourceGroup),
    [data?.groups, sourceGroup]
  )

  const updateRule = (index: number, update: Partial<Rule>) => {
    setConfig((current) => {
      if (!current) return current
      const rules = current.rules.map((rule, itemIndex) =>
        itemIndex === index ? { ...rule, ...update } : rule
      )
      return { ...current, rules }
    })
  }

  const changeTarget = (index: number, targetGroup: string) => {
    updateRule(index, { target_group: targetGroup, models: [] })
  }

  const addRule = () => {
    setConfig((current) =>
      current
        ? {
            ...current,
            rules: [
              ...current.rules,
              { target_group: '', enabled: false, models: [] },
            ],
          }
        : current
    )
  }

  const save = async () => {
    if (!config) return
    if (config.retry_enabled) {
      if (
        !Number.isInteger(config.retry_max_retries) ||
        config.retry_max_retries < 1 ||
        config.retry_max_retries > 10
      ) {
        toast.error(t('Retry count must be between 1 and 10'))
        return
      }
      if (!hasValidRetryStatusCodes(config.retry_http_status_codes)) {
        toast.error(t('Use HTTP status codes such as 429,500-503'))
        return
      }
    }
    setSaving(true)
    try {
      const response = await api.put(
        `/api/group/fallback/${encodeURIComponent(sourceGroup)}`,
        config
      )
      if (!response.data?.success)
        throw new Error(
          response.data?.message || t('Failed to save fallback settings')
        )
      toast.success(t('Fallback settings saved'))
      await load(sourceGroup)
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to save fallback settings')
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card className='border shadow-none'>
      <CardHeader>
        <CardTitle>{t('Group fallback')}</CardTitle>
        <CardDescription>
          {t(
            'Retry-enabled rules use the source group first; other enabled rules route directly to the fallback group.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-5'>
        {loading ? (
          <div className='text-muted-foreground text-sm'>{t('Loading...')}</div>
        ) : null}
        {!loading && data && config ? (
          <>
            <div className='grid gap-4 md:grid-cols-[minmax(0,1fr)_auto] md:items-end'>
              <div className='space-y-2'>
                <Label htmlFor='fallback-source-group'>
                  {t('Source group')}
                </Label>
                <select
                  id='fallback-source-group'
                  value={sourceGroup}
                  onChange={(event) => selectSource(event.target.value)}
                  className='border-input bg-background ring-offset-background focus:ring-ring h-10 w-full rounded-md border px-3 text-sm outline-none focus:ring-2 focus:ring-offset-2'
                >
                  {data.groups.map((group) => (
                    <option key={group.name} value={group.name}>
                      {group.name}
                    </option>
                  ))}
                </select>
              </div>
              <div className='flex items-center gap-3 pb-1'>
                <Label htmlFor='fallback-enabled'>{t('Enable fallback')}</Label>
                <Switch
                  id='fallback-enabled'
                  checked={config.enabled}
                  onCheckedChange={(enabled) =>
                    setConfig({
                      ...config,
                      enabled,
                      retry_enabled: enabled ? config.retry_enabled : false,
                    })
                  }
                />
              </div>
            </div>
            <div className='text-muted-foreground text-xs'>
              {t('Source group ratio')}: {source?.ratio ?? '-'}
            </div>
            <div className='space-y-4 border-y py-4'>
              <div className='flex items-center justify-between gap-3'>
                <div className='flex items-center gap-2'>
                  <RefreshCw className='h-4 w-4' />
                  <Label htmlFor='fallback-retry-enabled'>
                    {t('Retry before fallback')}
                  </Label>
                </div>
                <Switch
                  id='fallback-retry-enabled'
                  checked={config.retry_enabled}
                  disabled={!config.enabled}
                  onCheckedChange={(retry_enabled) =>
                    setConfig({ ...config, retry_enabled })
                  }
                />
              </div>
              {config.retry_enabled ? (
                <div className='grid gap-4 md:grid-cols-2'>
                  <div className='space-y-2'>
                    <Label htmlFor='fallback-retry-count'>
                      {t('Retry Count')}
                    </Label>
                    <Input
                      id='fallback-retry-count'
                      type='number'
                      min={1}
                      max={10}
                      value={config.retry_max_retries}
                      onChange={(event) =>
                        setConfig({
                          ...config,
                          retry_max_retries: Number(event.target.value),
                        })
                      }
                    />
                  </div>
                  <div className='space-y-2'>
                    <Label htmlFor='fallback-retry-status-codes'>
                      {t('Retry HTTP Status Codes')}
                    </Label>
                    <Input
                      id='fallback-retry-status-codes'
                      value={config.retry_http_status_codes}
                      placeholder='429,502-504'
                      onChange={(event) =>
                        setConfig({
                          ...config,
                          retry_http_status_codes: event.target.value,
                        })
                      }
                    />
                  </div>
                </div>
              ) : null}
            </div>
            <div className='space-y-2'>
              <Label htmlFor='fallback-reason'>{t('Reason')}</Label>
              <Input
                id='fallback-reason'
                value={config.reason}
                onChange={(event) =>
                  setConfig({ ...config, reason: event.target.value })
                }
                placeholder={t('Optional emergency switch reason')}
                maxLength={255}
              />
            </div>
            <div className='space-y-3'>
              <div className='flex items-center justify-between gap-3'>
                <div>
                  <Label>{t('Fallback rules')}</Label>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t(
                      'Each model can be assigned to only one enabled fallback group.'
                    )}
                  </p>
                </div>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={addRule}
                >
                  <Plus className='mr-2 h-4 w-4' />
                  {t('Add rule')}
                </Button>
              </div>
              {config.rules.length === 0 ? (
                <div className='text-muted-foreground rounded-md border border-dashed p-4 text-sm'>
                  {t('No fallback rules configured')}
                </div>
              ) : null}
              {config.rules.map((rule, index) => {
                const target = data.groups.find(
                  (group) => group.name === rule.target_group
                )
                const modelOptions: Option[] = (source?.configured_models || [])
                  .filter((modelName) =>
                    target?.configured_models.includes(modelName)
                  )
                  .map((modelName) => ({ label: modelName, value: modelName }))
                const unavailable = rule.models.filter(
                  (modelName) => !target?.available_models.includes(modelName)
                )
                return (
                  <div
                    key={`${rule.id || 'new'}-${index}`}
                    className='space-y-4 rounded-md border p-4'
                  >
                    <div className='grid gap-4 md:grid-cols-[minmax(0,1fr)_auto_auto] md:items-end'>
                      <div className='space-y-2'>
                        <Label>{t('Fallback group')}</Label>
                        <select
                          value={rule.target_group}
                          onChange={(event) =>
                            changeTarget(index, event.target.value)
                          }
                          className='border-input bg-background h-10 w-full rounded-md border px-3 text-sm outline-none'
                        >
                          <option value=''>{t('Select group')}</option>
                          {targetGroups.map((group) => (
                            <option key={group.name} value={group.name}>
                              {group.name} ({group.ratio})
                            </option>
                          ))}
                        </select>
                      </div>
                      <div className='flex items-center gap-2 pb-2'>
                        <Label>{t('Rule enabled')}</Label>
                        <Switch
                          checked={rule.enabled}
                          onCheckedChange={(enabled) =>
                            updateRule(index, { enabled })
                          }
                        />
                      </div>
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        aria-label={t('Remove rule')}
                        onClick={() =>
                          setConfig({
                            ...config,
                            rules: config.rules.filter(
                              (_, itemIndex) => itemIndex !== index
                            ),
                          })
                        }
                      >
                        <Trash2 className='h-4 w-4' />
                      </Button>
                    </div>
                    <div className='space-y-2'>
                      <Label>
                        {t('Models handled by this fallback group')}
                      </Label>
                      <MultiSelect
                        options={modelOptions}
                        selected={rule.models}
                        onChange={(models) => updateRule(index, { models })}
                        placeholder={t('Search and select models...')}
                      />
                      {unavailable.length > 0 ? (
                        <p className='text-destructive text-xs'>
                          {t('Unavailable models in target group')}:{' '}
                          {unavailable.join(', ')}
                        </p>
                      ) : null}
                    </div>
                  </div>
                )
              })}
            </div>
            <div className='flex justify-end'>
              <Button
                type='button'
                onClick={() => void save()}
                disabled={saving || !sourceGroup}
              >
                <Save className='mr-2 h-4 w-4' />
                {saving ? t('Saving...') : t('Save fallback settings')}
              </Button>
            </div>
          </>
        ) : null}
      </CardContent>
    </Card>
  )
}
