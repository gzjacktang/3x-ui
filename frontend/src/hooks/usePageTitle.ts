import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

const TITLE_KEYS: Record<string, string> = {
  '/inbounds': 'menu.inbounds',
  '/clients': 'menu.clients',
  '/settings': 'menu.settings',
  '/xray': 'menu.xray',
  '/outbound': 'menu.outbounds',
  '/routing': 'menu.routing',
};

export function usePageTitle() {
  const { pathname } = useLocation();
  const { t } = useTranslation();

  useEffect(() => {
    const key = TITLE_KEYS[pathname];
    const title = key ? t(key) : '3X-UI';
    const host = window.location.hostname;
    document.title = host ? `${host} - ${title}` : title;
  }, [pathname, t]);
}
