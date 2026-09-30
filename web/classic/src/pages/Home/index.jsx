/*
Copyright (C) 2025 QuantumNous

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

import React, { useContext, useEffect, useMemo, useState } from 'react';
import { Button } from '@douyinfe/semi-ui';
import {
  ArrowRight,
  Bell,
  BookOpen,
  Languages,
  Sparkles,
  Sun,
} from 'lucide-react';
import { Link } from 'react-router-dom';
import { marked } from 'marked';
import { useTranslation } from 'react-i18next';
import NoticeModal from '../../components/layout/NoticeModal';
import { API, showError } from '../../helpers';
import { StatusContext } from '../../context/Status';
import { useActualTheme } from '../../context/Theme';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import OcParticleNetwork from './OcParticleNetwork';

const HERO_TITLE = 'OC STUDIO';

const navLinks = [
  { label: '首页', to: '/' },
  { label: '控制台', to: '/console' },
  { label: '模型广场', to: '/pricing' },
  { label: '文档', external: true },
  { label: '关于', to: '/about' },
];

function useTypewriterText(text) {
  const [value, setValue] = useState('');

  useEffect(() => {
    const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
    if (reduceMotion.matches) {
      const timer = window.setTimeout(() => setValue(text), 0);
      return () => window.clearTimeout(timer);
    }

    let index = 0;
    const resetTimer = window.setTimeout(() => setValue(''), 0);
    const timer = window.setInterval(() => {
      index += 1;
      setValue(text.slice(0, index));
      if (index >= text.length) {
        window.clearInterval(timer);
      }
    }, 95);

    return () => {
      window.clearTimeout(resetTimer);
      window.clearInterval(timer);
    };
  }, [text]);

  return value;
}

function DefaultHomeContent({ docsLink, isMobile }) {
  const title = useTypewriterText(HERO_TITLE);
  const docsHref = docsLink || 'https://docs.newapi.pro';
  const models = useMemo(
    () => ['OpenAI', 'DeepSeek', 'Claude', 'Gemini', 'SD2', 'Image2'],
    [],
  );
  const stats = useMemo(
    () => [
      { value: '50+', label: '上游服务适配' },
      { value: '100+', label: '模型计费支持' },
      { value: '50+', label: '兼容 API 路由' },
      { value: '10+', label: '调度控制能力' },
    ],
    [],
  );

  return (
    <section className='relative isolate flex min-h-screen flex-col overflow-hidden bg-[#edfaff] text-zinc-950 dark:bg-zinc-950 dark:text-white'>
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
      <OcParticleNetwork />

      <nav className='fixed left-1/2 top-0 z-30 hidden h-16 w-[min(80rem,calc(100%-3rem))] -translate-x-1/2 items-center justify-between bg-transparent px-0 md:flex'>
        <Link to='/' className='flex items-center gap-2'>
          <span
            aria-label='OC STUDIO'
            className='flex size-6 shrink-0 items-center justify-center rounded-md bg-zinc-950 text-[9px] leading-none font-black tracking-normal text-white shadow-sm dark:bg-white dark:text-zinc-950'
          >
            OC
          </span>
          <span className='text-sm font-black tracking-tight text-zinc-950 dark:text-white'>
            OC STUDIO
          </span>
        </Link>
        <div className='flex items-center gap-3'>
          {navLinks.map((item) =>
            item.external ? (
              <button
                key={item.label}
                type='button'
                onClick={() => window.open(docsHref, '_blank')}
                className='rounded-md px-2 py-1.5 text-[13px] font-bold text-zinc-700 transition-colors hover:text-zinc-950 dark:text-white/80 dark:hover:text-white'
              >
                {item.label}
              </button>
            ) : (
              <Link
                key={item.label}
                to={item.to}
                className='rounded-md px-2 py-1.5 text-[13px] font-bold text-zinc-700 transition-colors hover:text-zinc-950 dark:text-white/80 dark:hover:text-white'
              >
                {item.label}
              </Link>
            ),
          )}
          <span className='mx-1 h-4 w-px bg-zinc-950/15 dark:bg-white/20' />
          <Languages className='size-4 text-zinc-950 dark:text-white' />
          <Sun className='size-4 text-zinc-950 dark:text-white' />
          <Bell className='size-4 text-zinc-950 dark:text-white' />
        </div>
      </nav>

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
          <Link to='/console'>
            <Button
              theme='solid'
              type='tertiary'
              size={isMobile ? 'default' : 'large'}
              className='!h-11 !rounded-full !bg-black !px-7 !text-white !shadow-[0_18px_38px_-22px_rgba(0,0,0,0.65)] hover:!bg-zinc-800 dark:!bg-white dark:!text-zinc-950 dark:hover:!bg-zinc-200'
            >
              <span className='inline-flex items-center gap-2'>
                前往仪表板
                <ArrowRight className='size-4' />
              </span>
            </Button>
          </Link>
          <Button
            size={isMobile ? 'default' : 'large'}
            className='!h-11 !rounded-full !border-white/80 !bg-white/70 !px-5 !text-zinc-900 !shadow-sm backdrop-blur-xl hover:!bg-white dark:!border-white/15 dark:!bg-white/10 dark:!text-white dark:hover:!bg-white/15'
            onClick={() => window.open(docsHref, '_blank')}
          >
            <span className='inline-flex items-center gap-2'>
              <BookOpen className='size-4' />
              文档
            </span>
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
  );
}

const Home = () => {
  const { i18n } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const actualTheme = useActualTheme();
  const [homePageContentLoaded, setHomePageContentLoaded] = useState(false);
  const [homePageContent, setHomePageContent] = useState('');
  const [noticeVisible, setNoticeVisible] = useState(false);
  const isMobile = useIsMobile();
  const docsLink = statusState?.status?.docs_link || '';

  const displayHomePageContent = async () => {
    setHomePageContent(localStorage.getItem('home_page_content') || '');
    try {
      const res = await API.get('/api/home_page_content');
      const { success, message, data } = res.data;
      if (success) {
        let content = data || '';
        if (content && !content.startsWith('https://')) {
          content = marked.parse(content);
        }
        setHomePageContent(content);
        localStorage.setItem('home_page_content', content);

        if (data && data.startsWith('https://')) {
          const iframe = document.querySelector('iframe');
          if (iframe) {
            iframe.onload = () => {
              iframe.contentWindow.postMessage({ themeMode: actualTheme }, '*');
              iframe.contentWindow.postMessage({ lang: i18n.language }, '*');
            };
          }
        }
      } else {
        showError(message);
        setHomePageContent('加载首页内容失败...');
      }
    } catch (error) {
      console.error('加载首页内容失败:', error);
      setHomePageContent('');
    }
    setHomePageContentLoaded(true);
  };

  useEffect(() => {
    const checkNoticeAndShow = async () => {
      const lastCloseDate = localStorage.getItem('notice_close_date');
      const today = new Date().toDateString();
      if (lastCloseDate !== today) {
        try {
          const res = await API.get('/api/notice');
          const { success, data } = res.data;
          if (success && data && data.trim() !== '') {
            setNoticeVisible(true);
          }
        } catch (error) {
          console.error('获取公告失败:', error);
        }
      }
    };

    checkNoticeAndShow();
  }, []);

  useEffect(() => {
    displayHomePageContent().then();
  }, []);

  return (
    <div className='w-full overflow-x-hidden'>
      <NoticeModal
        visible={noticeVisible}
        onClose={() => setNoticeVisible(false)}
        isMobile={isMobile}
      />
      {homePageContentLoaded && homePageContent === '' ? (
        <DefaultHomeContent docsLink={docsLink} isMobile={isMobile} />
      ) : (
        <div className='overflow-x-hidden w-full'>
          {homePageContent.startsWith('https://') ? (
            <iframe
              src={homePageContent}
              className='w-full h-screen border-none'
              title='Custom Home Page'
            />
          ) : (
            <div
              className='mt-[60px]'
              dangerouslySetInnerHTML={{ __html: homePageContent }}
            />
          )}
        </div>
      )}
    </div>
  );
};

export default Home;
