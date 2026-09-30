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
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { Markdown } from '@/components/ui/markdown'
import { PublicLayout } from '@/components/layout'
import { Footer } from '@/components/layout/components/footer'
import type { TopNavLink } from '@/components/layout/types'
import { CTA, Features, Hero, HowItWorks } from './components'
import { useHomePageContent } from './hooks'

const homeNavLinks: TopNavLink[] = [
  { title: '首页', href: '/' },
  { title: '控制台', href: '/dashboard', requiresAuth: true },
  { title: '模型广场', href: '/pricing' },
  { title: '文档', href: 'https://docs.newapi.pro', external: true },
  { title: '关于', href: '/about' },
]

const homeHeaderLogo = (
  <span
    aria-label='OC STUDIO'
    className='flex size-full items-center justify-center rounded-lg bg-zinc-950 text-[10px] leading-none font-black tracking-normal text-white shadow-sm dark:bg-white dark:text-zinc-950'
  >
    OC
  </span>
)

export function Home() {
  const { t } = useTranslation()
  const { auth } = useAuthStore()
  const isAuthenticated = !!auth.user
  const { content, isLoaded, isUrl } = useHomePageContent()

  if (!isLoaded) {
    return (
      <PublicLayout
        showMainContainer={false}
        navLinks={homeNavLinks}
        siteName='OC STUDIO'
        logo={homeHeaderLogo}
      >
        <main className='flex min-h-screen items-center justify-center'>
          <div className='text-muted-foreground'>{t('Loading...')}</div>
        </main>
      </PublicLayout>
    )
  }

  if (content) {
    return (
      <PublicLayout
        showMainContainer={false}
        navLinks={homeNavLinks}
        siteName='OC STUDIO'
        logo={homeHeaderLogo}
      >
        <main className='overflow-x-hidden'>
          {isUrl ? (
            <iframe
              src={content}
              className='h-screen w-full border-none'
              title={t('Custom Home Page')}
            />
          ) : (
            <div className='container mx-auto py-8'>
              <Markdown className='custom-home-content'>{content}</Markdown>
            </div>
          )}
        </main>
      </PublicLayout>
    )
  }

  return (
    <PublicLayout
      showMainContainer={false}
      navLinks={homeNavLinks}
      siteName='OC STUDIO'
      logo={homeHeaderLogo}
    >
      <Hero isAuthenticated={isAuthenticated} />
      <Features />
      <HowItWorks />
      <CTA isAuthenticated={isAuthenticated} />
      <Footer />
    </PublicLayout>
  )
}
