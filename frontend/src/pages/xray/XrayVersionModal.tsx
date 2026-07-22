import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Modal, Select, Space, Spin, Typography } from 'antd';
import { DownloadOutlined, ReloadOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';

interface XrayVersionModalProps {
  open: boolean;
  currentVersion: string;
  onClose: () => void;
  onInstalled: () => Promise<void>;
}

function versionTag(version: string): string {
  if (!version || version.toLowerCase() === 'unknown') return 'Unknown';
  return version.startsWith('v') ? version : `v${version}`;
}

export default function XrayVersionModal({ open, currentVersion, onClose, onInstalled }: XrayVersionModalProps) {
  const { t } = useTranslation();
  const [versions, setVersions] = useState<string[]>([]);
  const [selected, setSelected] = useState('');
  const [loading, setLoading] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [error, setError] = useState('');
  const [confirm, confirmContextHolder] = Modal.useModal();

  const currentTag = useMemo(() => versionTag(currentVersion), [currentVersion]);

  const fetchVersions = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const msg = await HttpUtil.get<string[]>('/panel/api/server/getXrayVersion', undefined, { silent: true });
      const fetchedVersions = msg.obj;
      if (!msg.success || !Array.isArray(fetchedVersions)) {
        setError(msg.msg || t('pages.xray.versionListFailed'));
        return;
      }
      setVersions(fetchedVersions);
      setSelected((previous) => previous || fetchedVersions[0] || '');
    } catch (e) {
      setError((e as Error).message || t('pages.xray.versionListFailed'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    if (open) void fetchVersions();
  }, [open, fetchVersions]);

  function installSelected() {
    if (!selected || installing) return;
    confirm.confirm({
      title: t('pages.xray.versionSwitchConfirmTitle'),
      content: t('pages.xray.versionSwitchConfirmDesc', { version: selected }),
      okText: t('confirm'),
      cancelText: t('cancel'),
      onOk: async () => {
        setInstalling(true);
        try {
          const msg = await HttpUtil.post(`/panel/api/server/installXray/${encodeURIComponent(selected)}`);
          if (!msg.success) return;
          await onInstalled();
          onClose();
        } finally {
          setInstalling(false);
        }
      },
    });
  }

  return (
    <Modal
      open={open}
      title={t('pages.xray.versionManager')}
      onCancel={onClose}
      destroyOnClose
      footer={null}
      closable={!installing}
      keyboard={!installing}
      maskClosable={!installing}
    >
      {confirmContextHolder}
      <Spin spinning={loading || installing} description={installing ? t('pages.xray.versionInstalling') : t('loading')}>
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <Alert
            type="info"
            showIcon
            title={t('pages.xray.versionCurrent', { version: currentTag })}
            description={t('pages.xray.versionManagerDesc')}
          />
          {error && <Alert type="error" showIcon title={error} />}
          <Space.Compact style={{ width: '100%' }}>
            <Select
              showSearch
              value={selected || undefined}
              placeholder={t('pages.xray.versionSelectPlaceholder')}
              options={versions.map((version) => ({
                value: version,
                label: version === currentTag ? `${version} (${t('pages.xray.versionCurrentShort')})` : version,
              }))}
              onChange={setSelected}
              style={{ flex: 1 }}
              notFoundContent={loading ? t('loading') : t('pages.xray.versionListEmpty')}
            />
            <Button icon={<ReloadOutlined />} aria-label={t('refresh')} title={t('refresh')} onClick={() => void fetchVersions()} />
          </Space.Compact>
          <Typography.Text type="secondary">{t('pages.xray.versionRestartHint')}</Typography.Text>
          <Button type="primary" icon={<DownloadOutlined />} disabled={!selected} loading={installing} onClick={installSelected}>
            {t('pages.xray.versionInstall')}
          </Button>
        </Space>
      </Spin>
    </Modal>
  );
}
