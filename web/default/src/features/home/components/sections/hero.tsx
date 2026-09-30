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
import { useEffect, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ArrowRight, BookOpen, Sparkles } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { HeroParticleNetwork } from '../hero-particle-network'

interface HeroProps {
  className?: string
  isAuthenticated?: boolean
}

const HERO_TITLE = 'OC STUDIO'

function useTypewriterText(text: string) {
  const [value, setValue] = useState('')

  useEffect(() => {
    const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
    if (reduceMotion.matches) {
      const timer = window.setTimeout(() => setValue(text), 0)
      return () => window.clearTimeout(timer)
    }

    let index = 0
    const resetTimer = window.setTimeout(() => setValue(''), 0)
    const timer = window.setInterval(() => {
      index += 1
      setValue(text.slice(0, index))
      if (index >= text.length) {
        window.clearInterval(timer)
      }
    }, 95)

    return () => {
      window.clearTimeout(resetTimer)
      window.clearInterval(timer)
    }
  }, [text])

  return value
}

export function Hero(props: HeroProps) {
  const title = useTypewriterText(HERO_TITLE)
  const models = useMemo(
    () => ['OpenAI', 'DeepSeek', 'Claude', 'Gemini', 'SD2', 'Image2'],
    []
  )
  const stats = useMemo(
    () => [
      { value: '50+', label: '上游服务适配' },
      { value: '100+', label: '模型计费支持' },
      { value: '50+', label: '兼容 API 路由' },
      { value: '10+', label: '调度控制能力' },
    ],
    []
  )

  return (
    <section className='relative isolate flex min-h-svh flex-col overflow-hidden bg-[#edfaff] text-zinc-950 dark:bg-zinc-950 dark:text-white'>
      <div
        aria-hidden='true'
        className='absolute inset-0 -z-20'
        style={{
          background: [
            'linear-gradient(105deg, rgba(91, 206, 226, 0.7) 0%, rgba(243, 251, 255, 0.96) 36%, rgba(246, 246, 255, 0.96) 54%, rgba(207, 169, 255, 0.76) 100%)',
            'radial-gradient(circle at 46% 92%, rgba(255, 184, 94, 0.58), transparent 25%)',
            'radial-gradient(circle at 56% 55%, rgba(255, 255, 255, 0.88), transparent 28%)',
            'radial-gradient(circle at 88% 52%, rgba(177, 96, 255, 0.3), transparent 34%)',
          ].join(', '),
        }}
      />
      <div
        aria-hidden='true'
        className='absolute inset-0 -z-10 bg-[radial-gradient(ellipse_at_center,rgba(255,255,255,0.72),rgba(255,255,255,0.18)_34%,transparent_68%)] dark:bg-[radial-gradient(circle_at_center,rgba(15,23,42,0.22),transparent_52%)]'
      />
      <HeroParticleNetwork className='z-0 opacity-80 dark:opacity-60' />

      <div className='relative z-10 mx-auto flex w-full max-w-6xl flex-1 flex-col items-center justify-center px-5 pt-28 pb-10 text-center md:px-8 md:pt-36 md:pb-12'>
        <p className='mb-7 inline-flex items-center gap-2 text-sm font-semibold text-zinc-600 dark:text-zinc-200'>
          <Sparkles className='size-4 text-blue-600 dark:text-blue-300' />
          AI 应用基础设施
        </p>

        <h1 className='min-h-[4rem] max-w-4xl text-5xl leading-[1.08] font-black tracking-normal text-zinc-950 sm:min-h-[4.6rem] sm:text-6xl md:min-h-[5.4rem] md:text-7xl dark:text-white'>
          {title}
        </h1>

        <p className='mt-7 max-w-2xl text-base leading-8 font-medium text-zinc-600 md:text-lg dark:text-zinc-300'>
          通过统一、标准的接口协议接入文本、图像与视频模型，承载 AI 应用，
          高效管理数字资产、计费审计与存证链路。
        </p>

        <div className='mt-10 flex flex-wrap items-center justify-center gap-3'>
          <Button
            size='lg'
            className='h-11 rounded-full bg-black px-7 text-white shadow-[0_18px_38px_-22px_rgba(0,0,0,0.65)] hover:bg-zinc-800 dark:bg-white dark:text-zinc-950 dark:hover:bg-zinc-200'
            render={
              <Link to={props.isAuthenticated ? '/dashboard' : '/sign-in'} />
            }
          >
            {props.isAuthenticated ? '前往仪表板' : '立即登录'}
            <ArrowRight className='ml-1 size-4' />
          </Button>
          <Button
            size='lg'
            variant='outline'
            className='h-11 rounded-full border-white/80 bg-white/70 px-5 text-zinc-900 shadow-sm backdrop-blur-xl hover:bg-white dark:border-white/15 dark:bg-white/10 dark:text-white dark:hover:bg-white/15'
            render={
              <a
                href='https://docs.newapi.pro'
                target='_blank'
                rel='noopener noreferrer'
              />
            }
          >
            <BookOpen className='mr-1 size-4' />
            文档
          </Button>
        </div>

        <div className='mt-16 flex w-full max-w-xl flex-wrap items-center justify-center gap-x-7 gap-y-3 text-sm font-medium text-zinc-500 dark:text-zinc-300'>
          {models.map((model) => (
            <span key={model}>{model}</span>
          ))}
        </div>
      </div>

      <div className='relative z-10 mx-auto grid w-full max-w-4xl grid-cols-2 gap-x-6 gap-y-6 px-5 pb-10 md:grid-cols-4 md:pb-14'>
        {stats.map((item) => (
          <div key={item.label} className='text-center'>
            <div className='text-4xl font-black text-zinc-950 dark:text-white'>
              {item.value}
            </div>
            <div className='mt-2 text-xs font-medium text-zinc-500 dark:text-zinc-300'>
              {item.label}
            </div>
          </div>
        ))}
      </div>
    </section>
  )
}
