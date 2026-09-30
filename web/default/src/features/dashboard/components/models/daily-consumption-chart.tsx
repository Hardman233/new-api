/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useEffect, useMemo, useRef, useState } from 'react'
import { VChart } from '@visactor/react-vchart'
import { TrendingUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Skeleton } from '@/components/ui/skeleton'
import { useTheme } from '@/context/theme-provider'
import { formatNumber, formatQuota } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'
import { useAuthStore } from '@/stores/auth-store'
import { getDailyConsumptionDates } from '@/features/dashboard/api'
import {
  buildQueryParams,
  getDefaultDays,
} from '@/features/dashboard/lib'
import type {
  DailyConsumptionItem,
  DashboardFilters,
} from '@/features/dashboard/types'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

interface DailyConsumptionChartProps {
  filters?: DashboardFilters
}

export function DailyConsumptionChart(props: DailyConsumptionChartProps) {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = !!(user?.role && user.role >= 10)
  const [data, setData] = useState<DailyConsumptionItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)

  useEffect(() => {
    const updateTheme = async () => {
      setThemeReady(false)
      if (!themeManagerPromise) {
        themeManagerPromise = import('@visactor/vchart').then(
          (m) => m.ThemeManager
        )
      }
      const ThemeManager = await themeManagerPromise
      themeManagerRef.current = ThemeManager
      ThemeManager.setCurrentTheme(resolvedTheme === 'dark' ? 'dark' : 'light')
      setThemeReady(true)
    }

    updateTheme()
  }, [resolvedTheme])

  useEffect(() => {
    const abortController = new AbortController()
    setLoading(true)
    setError(false)

    const timeRange = computeTimeRange(
      getDefaultDays(props.filters?.time_granularity),
      props.filters?.start_timestamp,
      props.filters?.end_timestamp
    )

    getDailyConsumptionDates(buildQueryParams(timeRange, props.filters), isAdmin)
      .then((res) => {
        if (abortController.signal.aborted) return
        setData(res?.data || [])
      })
      .catch(() => {
        if (abortController.signal.aborted) return
        setData([])
        setError(true)
      })
      .finally(() => {
        if (!abortController.signal.aborted) setLoading(false)
      })

    return () => {
      abortController.abort()
    }
  }, [props.filters, isAdmin])

  const chart = useMemo(() => {
    const values = data.map((item) => ({
      Date: item.date,
      rawQuota: item.quota || 0,
      Requests: item.request_count || 0,
      Tokens: item.total_tokens || 0,
    }))
    const totalQuota = values.reduce((sum, item) => sum + item.rawQuota, 0)

    return {
      totalQuota,
      totalRequests: values.reduce((sum, item) => sum + item.Requests, 0),
      totalTokens: values.reduce((sum, item) => sum + item.Tokens, 0),
      spec: {
        type: 'line',
        data: [{ id: 'dailyConsumptionData', values }],
        xField: 'Date',
        yField: 'rawQuota',
        legends: { visible: false },
        axes: [
          {
            orient: 'left',
            label: {
              formatMethod: (value: number) => formatQuota(value),
            },
          },
        ],
        line: { style: { lineWidth: 2 } },
        point: { visible: true, style: { size: 4 } },
        tooltip: {
          mark: {
            content: [
              {
                key: t('Consume'),
                value: (datum: { rawQuota?: number }) =>
                  formatQuota(datum.rawQuota || 0),
              },
              {
                key: t('Requests'),
                value: (datum: { Requests?: number }) =>
                  formatNumber(datum.Requests || 0),
              },
              {
                key: t('Tokens'),
                value: (datum: { Tokens?: number }) =>
                  formatNumber(datum.Tokens || 0),
              },
            ],
          },
        },
        color: ['#2563eb'],
      },
    }
  }, [data, t])

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex flex-col gap-1.5 border-b px-3 py-2 sm:px-5 sm:py-3 lg:flex-row lg:items-center lg:justify-between'>
        <div className='flex items-center gap-2'>
          <TrendingUp className='text-muted-foreground/60 size-4' />
          <div className='text-sm font-semibold'>{t('Daily Spend')}</div>
          <span className='text-muted-foreground text-xs'>
            {t('Total:')} {formatQuota(chart.totalQuota)}
          </span>
        </div>
        <div className='text-muted-foreground flex gap-3 text-xs'>
          <span>
            {t('Requests')}: {formatNumber(chart.totalRequests)}
          </span>
          <span>
            {t('Tokens')}: {formatNumber(chart.totalTokens)}
          </span>
        </div>
      </div>
      <div className='h-[300px] p-1.5 sm:h-96 sm:p-2'>
        {loading ? (
          <Skeleton className='h-full w-full' />
        ) : error ? (
          <div className='text-muted-foreground flex h-full items-center justify-center text-sm'>
            {t('Failed to load data')}
          </div>
        ) : themeReady ? (
          <VChart
            key={`${resolvedTheme}-${data.length}`}
            spec={{
              ...chart.spec,
              theme: resolvedTheme === 'dark' ? 'dark' : 'light',
              background: 'transparent',
            }}
            option={VCHART_OPTION}
          />
        ) : null}
      </div>
    </div>
  )
}
