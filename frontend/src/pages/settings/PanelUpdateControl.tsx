import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, Space, Tag, Typography, Alert } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { SettingListItem } from '@/components/ui';

interface UpdateInfo {
  channel: string;
  currentVersion: string;
  latestVersion: string;
  updateAvailable: boolean;
}

interface UpdateStatus {
  runId: string;
  state: 'pending' | 'success' | 'failed';
  exitCode: number;
}

const RUN_ID_KEY = 'panelUpdateRunId';

export default function PanelUpdateControl() {
  const { t } = useTranslation();
  const [confirm, confirmContextHolder] = Modal.useModal();
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const [checking, setChecking] = useState(false);
  const [starting, setStarting] = useState(false);
  const [runId, setRunId] = useState(() => sessionStorage.getItem(RUN_ID_KEY) || '');
  const [error, setError] = useState('');
  const [result, setResult] = useState<'success' | 'failed' | ''>('');

  const checkInfo = useCallback(async () => {
    setChecking(true);
    setError('');
    try {
      const msg = await HttpUtil.get<UpdateInfo>('/panel/api/server/getPanelUpdateInfo', { dev: true }, { silent: true, timeout: 10000 });
      if (msg.success && msg.obj) setInfo(msg.obj);
      else {
        setInfo(null);
        setError(msg.msg || t('pages.settings.panelUpdateCheckFailed'));
      }
    } finally {
      setChecking(false);
    }
  }, [t]);

  useEffect(() => { void checkInfo(); }, [checkInfo]);

  useEffect(() => {
    if (!runId) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let attempts = 0;

    const poll = async () => {
      attempts += 1;
      const msg = await HttpUtil.get<UpdateStatus>('/panel/api/server/getUpdateStatus', undefined, { silent: true, timeout: 5000 });
      if (!active) return;
      const status = msg.success ? msg.obj : null;
      if (status?.runId === runId && status.state !== 'pending') {
        sessionStorage.removeItem(RUN_ID_KEY);
        setRunId('');
        setResult(status.state);
        if (status.state === 'success') {
          window.setTimeout(() => window.location.reload(), 2000);
        } else {
          setError(t('pages.settings.panelUpdateFailed', { code: status.exitCode }));
        }
        return;
      }
      if (attempts < 200) timer = setTimeout(() => { void poll(); }, 3000);
      else {
        sessionStorage.removeItem(RUN_ID_KEY);
        setRunId('');
        setError(t('pages.settings.panelUpdateTimeout'));
      }
    };
    void poll();
    return () => { active = false; if (timer) clearTimeout(timer); };
  }, [runId, t]);

  const startUpdate = () => {
    if (!info || starting || runId) return;
    confirm.confirm({
      title: t('pages.settings.panelUpdateConfirmTitle'),
      content: t('pages.settings.panelUpdateConfirmDesc', { version: info.latestVersion }),
      okText: t('pages.settings.panelUpdateButton'),
      cancelText: t('cancel'),
      onOk: async () => {
        setStarting(true);
        setError('');
        setResult('');
        try {
          const msg = await HttpUtil.post<{ runId: string }>('/panel/api/server/updatePanel', { dev: true }, { silent: true });
          if (!msg.success || !msg.obj?.runId) {
            setError(msg.msg || t('pages.settings.panelUpdateStartFailed'));
            return;
          }
          sessionStorage.setItem(RUN_ID_KEY, msg.obj.runId);
          setRunId(msg.obj.runId);
        } finally {
          setStarting(false);
        }
      },
    });
  };

  return (
    <SettingListItem
      paddings="small"
      title={t('pages.settings.panelUpdateTitle')}
      description={t('pages.settings.panelUpdateDesc')}
    >
      <Space direction="vertical" size="small" style={{ width: '100%' }}>
        <Space wrap>
          <Typography.Text>{t('pages.settings.panelUpdateCurrent', { version: info?.currentVersion || '—' })}</Typography.Text>
          <Typography.Text>{t('pages.settings.panelUpdateLatest', { version: info?.latestVersion || '—' })}</Typography.Text>
          {info?.updateAvailable && <Tag color="blue">{t('pages.settings.panelUpdateAvailable')}</Tag>}
        </Space>
        <Space wrap>
          <Button onClick={startUpdate} loading={starting} disabled={!info?.updateAvailable || Boolean(runId)} type="primary">
            {t('pages.settings.panelUpdateButton')}
          </Button>
          <Button icon={<ReloadOutlined />} loading={checking} onClick={() => void checkInfo()} aria-label={t('pages.settings.panelUpdateCheck')}>
            {t('pages.settings.panelUpdateCheck')}
          </Button>
        </Space>
        {runId && <Alert type="info" showIcon title={t('pages.settings.panelUpdateRunning')} />}
        {result === 'success' && <Alert type="success" showIcon title={t('pages.settings.panelUpdateSucceeded')} />}
        {error && <Alert type="error" showIcon title={error} />}
        {confirmContextHolder}
      </Space>
    </SettingListItem>
  );
}
