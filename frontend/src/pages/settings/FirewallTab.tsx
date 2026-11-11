import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  InputNumber,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { FireOutlined, ReloadOutlined } from '@ant-design/icons';
import { HttpUtil } from '@/utils';
import { SettingListItem } from '@/components/ui';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { catTabLabel } from './catTabLabel';
import './FirewallTab.css';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;

interface ApiMsg<T = unknown> {
  success?: boolean;
  msg?: string;
  obj?: T;
}

interface FirewallRule {
  number: number;
  to: string;
  action: string;
  direction: string;
  from: string;
  protected: boolean;
}

interface ProtectedPort {
  port: number;
  proto: string;
  label: string;
}

interface ForeignFirewall {
  name: string;
  active: boolean;
  detail?: string;
}

interface FirewallStatus {
  installed: boolean;
  active: boolean;
  available: boolean;
  rules: FirewallRule[];
  protectedPorts: ProtectedPort[];
  foreign?: ForeignFirewall[];
  takeoverRecommended?: boolean;
  message?: string;
}

interface InboundOption {
  tag: string;
  protocol: string;
  port: number;
}

type Proto = 'tcp' | 'udp' | 'both';

function ruleCoversPort(rule: FirewallRule, port: number): boolean {
  const to = rule.to.trim();
  if (/^OpenSSH$/i.test(to)) return port === 22;
  const match = to.match(/^(\d+)(?::(\d+))?(?:\/(tcp|udp))?$/i);
  if (!match) return false;
  const start = Number(match[1]);
  const end = match[2] ? Number(match[2]) : start;
  return port >= start && port <= end;
}

export default function FirewallTab() {
  const { t } = useTranslation();
  const { isMobile } = useMediaQuery();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();

  const [loading, setLoading] = useState(false);
  const [acting, setActing] = useState(false);
  const [status, setStatus] = useState<FirewallStatus | null>(null);
  const [inbounds, setInbounds] = useState<InboundOption[]>([]);

  const [portStart, setPortStart] = useState<number | null>(443);
  const [portEnd, setPortEnd] = useState<number | null>(null);
  const [proto, setProto] = useState<Proto>('tcp');

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const msg = await HttpUtil.get<FirewallStatus>('/panel/api/server/firewall/status', undefined, { silent: true });
      if (msg?.success && msg.obj) {
        setStatus(msg.obj);
      } else {
        setStatus(null);
        messageApi.error(msg?.msg || t('pages.settings.firewall.statusFailed'));
      }
    } finally {
      setLoading(false);
    }
  }, [messageApi, t]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const msg = await HttpUtil.get('/panel/api/inbounds/options', undefined, { silent: true }) as ApiMsg<InboundOption[]>;
      if (cancelled) return;
      if (msg?.success && Array.isArray(msg.obj)) {
        setInbounds(msg.obj.filter((ib) => Number.isFinite(ib.port) && ib.port > 0));
      }
    })();
    return () => { cancelled = true; };
  }, []);

  const missingInboundPorts = useMemo(() => {
    if (!status?.installed) return [];
    const rules = status.rules || [];
    const seen = new Set<number>();
    const missing: { port: number; tags: string[] }[] = [];
    for (const ib of inbounds) {
      if (seen.has(ib.port)) {
        const row = missing.find((m) => m.port === ib.port);
        if (row && !row.tags.includes(ib.tag)) row.tags.push(ib.tag);
        continue;
      }
      seen.add(ib.port);
      const covered = rules.some((rule) => rule.action === 'ALLOW' && ruleCoversPort(rule, ib.port));
      if (!covered) {
        missing.push({ port: ib.port, tags: [ib.tag] });
      }
    }
    return missing.sort((a, b) => a.port - b.port);
  }, [inbounds, status]);

  async function runAction(fn: () => Promise<ApiMsg>) {
    setActing(true);
    try {
      const msg = await fn();
      if (msg?.success) {
        await refresh();
      }
    } finally {
      setActing(false);
    }
  }

  function onInstall() {
    void runAction(() => HttpUtil.post('/panel/api/server/firewall/install'));
  }

  function onTakeover() {
    modal.confirm({
      title: t('pages.settings.firewall.takeoverConfirmTitle'),
      content: t('pages.settings.firewall.takeoverConfirmDesc'),
      okText: t('pages.settings.firewall.takeoverConfirmOk'),
      okButtonProps: { danger: true },
      cancelText: t('cancel'),
      onOk: () => runAction(() =>
        HttpUtil.post('/panel/api/server/firewall/takeover', { confirm: true }, JSON_HEADERS),
      ),
    });
  }

  function onEnable() {
    modal.confirm({
      title: t('pages.settings.firewall.enableConfirmTitle'),
      content: t('pages.settings.firewall.enableConfirmDesc'),
      okText: t('pages.settings.firewall.enable'),
      cancelText: t('cancel'),
      onOk: () => runAction(() => HttpUtil.post('/panel/api/server/firewall/enable')),
    });
  }

  function onDisable() {
    modal.confirm({
      title: t('pages.settings.firewall.disableConfirmTitle'),
      content: t('pages.settings.firewall.disableConfirmDesc'),
      okText: t('pages.settings.firewall.disable'),
      okButtonProps: { danger: true },
      cancelText: t('cancel'),
      onOk: () => runAction(() => HttpUtil.post('/panel/api/server/firewall/disable')),
    });
  }

  function onAllow(start: number, end: number | null | undefined, protocol: Proto) {
    const payload = {
      start,
      end: end && end !== start ? end : 0,
      proto: protocol,
    };
    void runAction(() => HttpUtil.post('/panel/api/server/firewall/allow', payload, JSON_HEADERS));
  }

  function onDelete(rule: FirewallRule) {
    modal.confirm({
      title: t('pages.settings.firewall.deleteConfirmTitle'),
      content: t('pages.settings.firewall.deleteConfirmDesc', { number: rule.number, to: rule.to }),
      okText: t('pages.settings.firewall.delete'),
      okButtonProps: { danger: true },
      cancelText: t('cancel'),
      onOk: () => runAction(() =>
        HttpUtil.post('/panel/api/server/firewall/delete', { number: rule.number }, JSON_HEADERS),
      ),
    });
  }

  const columns: ColumnsType<FirewallRule> = [
    { title: t('pages.settings.firewall.colNumber'), dataIndex: 'number', width: 64 },
    { title: t('pages.settings.firewall.colTo'), dataIndex: 'to' },
    {
      title: t('pages.settings.firewall.colAction'),
      key: 'actionDir',
      render: (_, row) => `${row.action} ${row.direction}`,
    },
    { title: t('pages.settings.firewall.colFrom'), dataIndex: 'from' },
    {
      title: t('pages.settings.firewall.colProtected'),
      dataIndex: 'protected',
      width: isMobile ? 72 : 110,
      render: (value: boolean) => (
        value
          ? <Tag color="processing">{t('pages.settings.firewall.protectedBadge')}</Tag>
          : <span>{t('pages.settings.firewall.no')}</span>
      ),
    },
    {
      title: t('pages.settings.actions'),
      key: 'ops',
      width: 100,
      render: (_, row) => (
        <Button
          danger
          size="small"
          disabled={row.protected || acting}
          onClick={() => onDelete(row)}
        >
          {t('pages.settings.firewall.delete')}
        </Button>
      ),
    },
  ];

  return (
    <>
      {messageContextHolder}
      {modalContextHolder}

      <div className="firewall-tab">
        <div className="firewall-header">
          {catTabLabel(<FireOutlined />, t('pages.settings.firewall.title'), false)}
          <Button icon={<ReloadOutlined />} onClick={() => void refresh()} loading={loading} disabled={acting}>
            {t('pages.settings.firewall.refresh')}
          </Button>
        </div>

        <Typography.Paragraph type="secondary" className="firewall-desc">
          {t('pages.settings.firewall.desc')}
        </Typography.Paragraph>

        {status?.message && !status.installed && (
          <Alert type="warning" showIcon className="firewall-alert" title={status.message || t('pages.settings.firewall.notInstalled')} />
        )}

        {status?.takeoverRecommended && (
          <Alert
            type="error"
            showIcon
            className="firewall-alert"
            title={t('pages.settings.firewall.takeoverTitle')}
            description={(
              <Space direction="vertical" size={8} style={{ width: '100%' }}>
                <span>{t('pages.settings.firewall.takeoverDesc')}</span>
                {(status.foreign || []).filter((fw) => fw.active).length > 0 && (
                  <Space wrap>
                    {(status.foreign || []).filter((fw) => fw.active).map((fw) => (
                      <Tag key={fw.name} color="error">{fw.name}: {fw.detail || t('pages.settings.firewall.foreignActive')}</Tag>
                    ))}
                  </Space>
                )}
                <Button danger type="primary" loading={acting} onClick={onTakeover}>
                  {t('pages.settings.firewall.takeoverButton')}
                </Button>
              </Space>
            )}
          />
        )}

        {(status?.foreign || []).length > 0 && !status?.takeoverRecommended && (
          <SettingListItem paddings="small" title={t('pages.settings.firewall.foreignTitle')}>
            <Space wrap>
              {(status?.foreign || []).map((fw) => (
                <Tag key={fw.name} color={fw.active ? 'error' : 'default'}>
                  {fw.name}: {fw.active ? t('pages.settings.firewall.foreignActive') : t('pages.settings.firewall.foreignInactive')}
                </Tag>
              ))}
            </Space>
          </SettingListItem>
        )}

        <SettingListItem paddings="small" title={t('pages.settings.firewall.installed')}>
          <Space wrap>
            <Tag color={status?.installed ? 'success' : 'default'}>
              {status?.installed ? t('pages.settings.firewall.installed') : t('pages.settings.firewall.missing')}
            </Tag>
            <Tag color={status?.active ? 'success' : 'default'}>
              {status?.active ? t('pages.settings.firewall.active') : t('pages.settings.firewall.inactive')}
            </Tag>
            {!status?.installed && (
              <Button type="primary" loading={acting} onClick={onInstall}>
                {t('pages.settings.firewall.install')}
              </Button>
            )}
            {status?.installed && !status.active && (
              <Button type="primary" loading={acting} onClick={onEnable}>
                {t('pages.settings.firewall.enable')}
              </Button>
            )}
            {status?.installed && status.active && (
              <Button danger loading={acting} onClick={onDisable}>
                {t('pages.settings.firewall.disable')}
              </Button>
            )}
          </Space>
        </SettingListItem>

        <SettingListItem
          paddings="small"
          title={t('pages.settings.firewall.protectedTitle')}
          description={t('pages.settings.firewall.protectedDesc')}
        >
          <Space wrap>
            {(status?.protectedPorts || []).map((p) => (
              <Tag key={p.label}>{p.label}</Tag>
            ))}
          </Space>
        </SettingListItem>

        <SettingListItem paddings="small" title={t('pages.settings.firewall.allowTitle')}>
          <Space wrap align="start">
            <div className="firewall-field">
              <div className="firewall-field-label">{t('pages.settings.firewall.port')}</div>
              <InputNumber
                min={1}
                max={65535}
                value={portStart ?? undefined}
                onChange={(v) => setPortStart(typeof v === 'number' ? v : null)}
              />
            </div>
            <div className="firewall-field">
              <div className="firewall-field-label">{t('pages.settings.firewall.portEnd')}</div>
              <InputNumber
                min={1}
                max={65535}
                value={portEnd ?? undefined}
                onChange={(v) => setPortEnd(typeof v === 'number' ? v : null)}
              />
            </div>
            <div className="firewall-field">
              <div className="firewall-field-label">{t('pages.settings.firewall.proto')}</div>
              <Select
                style={{ width: 140 }}
                value={proto}
                onChange={(v) => setProto(v)}
                options={[
                  { value: 'tcp', label: t('pages.settings.firewall.protoTcp') },
                  { value: 'udp', label: t('pages.settings.firewall.protoUdp') },
                  { value: 'both', label: t('pages.settings.firewall.protoBoth') },
                ]}
              />
            </div>
            <Button
              type="primary"
              disabled={!status?.installed || !portStart || acting}
              loading={acting}
              onClick={() => {
                if (!portStart) return;
                onAllow(portStart, portEnd, proto);
              }}
            >
              {t('pages.settings.firewall.allow')}
            </Button>
          </Space>
        </SettingListItem>

        <div className="firewall-section">
          <Typography.Title level={5}>{t('pages.settings.firewall.inboundHintsTitle')}</Typography.Title>
          <Typography.Paragraph type="secondary">
            {t('pages.settings.firewall.inboundHintsDesc')}
          </Typography.Paragraph>
          {missingInboundPorts.length === 0 ? (
            <Typography.Text type="secondary">{t('pages.settings.firewall.inboundHintsEmpty')}</Typography.Text>
          ) : (
            <Space direction="vertical" style={{ width: '100%' }}>
              {missingInboundPorts.map((row) => (
                <div key={row.port} className="firewall-inbound-row">
                  <span>
                    <Tag>{row.port}</Tag>
                    {row.tags.join(', ')}
                  </span>
                  <Button
                    size="small"
                    disabled={!status?.installed || acting}
                    onClick={() => onAllow(row.port, null, 'both')}
                  >
                    {t('pages.settings.firewall.allowInbound')}
                  </Button>
                </div>
              ))}
            </Space>
          )}
        </div>

        <div className="firewall-section">
          <Typography.Title level={5}>{t('pages.settings.firewall.rulesTitle')}</Typography.Title>
          <Table
            size="small"
            rowKey="number"
            loading={loading}
            pagination={false}
            columns={columns}
            dataSource={status?.rules || []}
            locale={{ emptyText: t('pages.settings.firewall.rulesEmpty') }}
            scroll={isMobile ? { x: 720 } : undefined}
          />
        </div>
      </div>
    </>
  );
}
