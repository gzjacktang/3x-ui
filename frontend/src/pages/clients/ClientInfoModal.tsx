import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Divider, Modal, Popover, Tag, Tooltip, message } from 'antd';
import { CopyOutlined, EyeOutlined, QrcodeOutlined, ReloadOutlined } from '@ant-design/icons';

import { ClipboardManager, HttpUtil, IntlUtil, SizeFormatter } from '@/utils';
import { formatInboundLabel } from '@/lib/inbounds/label';
import { normalizeClientIps, type ClientIpInfo } from '@/lib/clients/ip-log';
import { loadClientShareLinks } from '@/lib/clients/share-links';
import { useDatepicker } from '@/hooks/useDatepicker';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';
import { isPostQuantumLink } from '@/lib/xray/inbound-link';
import { LinkTags, linkMetaText, parseLinkParts } from '@/lib/xray/link-label';
import { QrPanel } from '@/pages/inbounds/qr';
import ConfigBlock from '@/components/clients/ConfigBlock';
import { buildWireguardClientConfig, findWireguardInbound, isWireguardClient } from './wireguardConfig';
import './ClientInfoModal.css';

const INBOUND_PROTOCOL_COLORS: Record<string, string> = {
  vless: 'blue',
  vmess: 'geekblue',
  trojan: 'volcano',
  shadowsocks: 'magenta',
  hysteria: 'cyan',
  hysteria2: 'green',
  wireguard: 'gold',
  http: 'purple',
  mixed: 'lime',
  tunnel: 'orange',
};

const INBOUND_CHIP_LIMIT = 1;

interface ClientInfoModalProps {
  open: boolean;
  client: ClientRecord | null;
  inboundsById: Record<number, InboundOption>;
  isOnline: boolean;
  onOpenChange: (open: boolean) => void;
}

interface ApiMsg<T = unknown> {
  success?: boolean;
  obj?: T;
}

export default function ClientInfoModal({
  open,
  client,
  inboundsById,
  isOnline,
  onOpenChange,
}: ClientInfoModalProps) {
  const { datepicker } = useDatepicker();
  const { t } = useTranslation();
  const expiryLabel = (ts?: number) => {
    if (!ts) return '∞';
    if (ts < 0) {
      const days = Math.round(ts / -86400000);
      return `${t('pages.clients.delayedStart')}: ${days}d`;
    }
    return IntlUtil.formatDate(ts, datepicker);
  };
  const dateLabel = (ts?: number) => (!ts || ts <= 0 ? '-' : IntlUtil.formatDate(ts, datepicker));
  const [messageApi, messageContextHolder] = message.useMessage();
  const [links, setLinks] = useState<string[]>([]);
  const [clientIps, setClientIps] = useState<ClientIpInfo[]>([]);
  const [ipsLoading, setIpsLoading] = useState(false);
  const [ipsClearing, setIpsClearing] = useState(false);
  const [ipsModalOpen, setIpsModalOpen] = useState(false);

  useEffect(() => {
    if (!open) {
      setLinks([]);
      setClientIps([]);
      setIpsModalOpen(false);
      return;
    }
    if (!client) return;
    let cancelled = false;
    (async () => {
      const next = await loadClientShareLinks(client, inboundsById);
      if (!cancelled) setLinks(next);
    })();
    return () => { cancelled = true; };
  }, [open, client, inboundsById]);

  const traffic = client?.traffic || null;
  const totalBytes = client?.totalGB || 0;
  const used = (traffic?.up || 0) + (traffic?.down || 0);
  const remaining = useMemo(() => {
    if (totalBytes <= 0) return -1;
    const r = totalBytes - used;
    return r > 0 ? r : 0;
  }, [totalBytes, used]);

  const wgInbound = useMemo(() => findWireguardInbound(client, inboundsById), [client, inboundsById]);
  const wgConfigText = useMemo(() => {
    if (!client || !wgInbound || !isWireguardClient(client)) return '';
    return buildWireguardClientConfig(client, wgInbound, window.location.hostname, '');
  }, [client, wgInbound]);

  async function copyValue(text: string) {
    if (!text) return;
    const ok = await ClipboardManager.copyText(String(text));
    if (ok) messageApi.success(t('copied'));
  }

  async function loadIps() {
    if (!client?.email) return;
    setIpsLoading(true);
    try {
      const msg = await HttpUtil.post(`/panel/api/clients/ips/${encodeURIComponent(client.email)}`) as ApiMsg<unknown[]>;
      if (!msg?.success) { setClientIps([]); return; }
      setClientIps(normalizeClientIps(msg.obj));
    } finally {
      setIpsLoading(false);
    }
  }

  async function clearIps() {
    if (!client?.email) return;
    setIpsClearing(true);
    try {
      const msg = await HttpUtil.post(`/panel/api/clients/clearIps/${encodeURIComponent(client.email)}`) as ApiMsg;
      if (msg?.success) setClientIps([]);
    } finally {
      setIpsClearing(false);
    }
  }

  function openIpsModal() {
    setIpsModalOpen(true);
    if (clientIps.length === 0) void loadIps();
  }

  return (
    <>
      {messageContextHolder}
      <Modal
        open={open}
        title={client ? `${t('pages.clients.clientInfo')} — ${client.email}` : t('pages.clients.clientInfo')}
        footer={null}
        width={640}
        onCancel={() => onOpenChange(false)}
      >
        {client && (
          <>
            <table className="info-table block">
              <tbody>
                <tr>
                  <td>{t('pages.clients.online')}</td>
                  <td>
                    {client.enable && isOnline
                      ? <Tag color="green">{t('pages.clients.online')}</Tag>
                      : <Tag>{t('pages.clients.offline')}</Tag>}
                    <span className="hint">{t('lastOnline')}: {dateLabel(traffic?.lastOnline)}</span>
                  </td>
                </tr>
                <tr>
                  <td>{t('status')}</td>
                  <td>
                    <Tag color={client.enable ? 'green' : 'default'}>
                      {client.enable ? t('enabled') : t('disabled')}
                    </Tag>
                  </td>
                </tr>
                <tr>
                  <td>{t('pages.clients.email')}</td>
                  <td>
                    {client.email
                      ? <Tag color="green">{client.email}</Tag>
                      : <Tag color="red">{t('none')}</Tag>}
                  </td>
                </tr>
                {client.uuid && (
                  <tr>
                    <td>{t('pages.clients.uuid')}</td>
                    <td>
                      <Tag className="info-large-tag">{client.uuid}</Tag>
                      <Button size="small" type="text" icon={<CopyOutlined />} aria-label={t('copy')} onClick={() => copyValue(client.uuid!)} />
                    </td>
                  </tr>
                )}
                {client.password && (
                  <tr>
                    <td>{t('password')}</td>
                    <td>
                      <Tag className="info-large-tag">{client.password}</Tag>
                      <Button size="small" type="text" icon={<CopyOutlined />} aria-label={t('copy')} onClick={() => copyValue(client.password!)} />
                    </td>
                  </tr>
                )}
                {client.auth && (
                  <tr>
                    <td>{t('pages.clients.auth')}</td>
                    <td>
                      <Tag className="info-large-tag">{client.auth}</Tag>
                      <Button size="small" type="text" icon={<CopyOutlined />} aria-label={t('copy')} onClick={() => copyValue(client.auth!)} />
                    </td>
                  </tr>
                )}
                <tr>
                  <td>{t('pages.clients.flow')}</td>
                  <td>
                    {client.flow ? <Tag>{client.flow}</Tag> : <Tag color="orange">{t('none')}</Tag>}
                  </td>
                </tr>
                <tr>
                  <td>{t('pages.inbounds.traffic')}</td>
                  <td>
                    <Tag>
                      ↑ {SizeFormatter.sizeFormat(traffic?.up || 0)}
                      {' '}/ ↓ {SizeFormatter.sizeFormat(traffic?.down || 0)}
                    </Tag>
                    <span className="hint">
                      {SizeFormatter.sizeFormat(used)} / {totalBytes > 0 ? SizeFormatter.sizeFormat(totalBytes) : '∞'}
                    </span>
                  </td>
                </tr>
                <tr>
                  <td>{t('remained')}</td>
                  <td>
                    {remaining < 0
                      ? <Tag color="purple">∞</Tag>
                      : <Tag color={remaining > 0 ? '' : 'red'}>{SizeFormatter.sizeFormat(remaining)}</Tag>}
                  </td>
                </tr>
                <tr>
                  <td>{t('pages.inbounds.expireDate')}</td>
                  <td>
                    {!client.expiryTime
                      ? <Tag color="purple">∞</Tag>
                      : <Tag color={client.expiryTime < 0 ? 'blue' : undefined}>{expiryLabel(client.expiryTime)}</Tag>}
                    {(client.expiryTime ?? 0) > 0 && (
                      <span className="hint">{IntlUtil.formatRelativeTime(client.expiryTime)}</span>
                    )}
                  </td>
                </tr>
                <tr>
                  <td>{t('pages.clients.ipLimit')}</td>
                  <td>{!client.limitIp ? <Tag>∞</Tag> : <Tag>{client.limitIp}</Tag>}</td>
                </tr>
                <tr>
                  <td>{t('pages.inbounds.IPLimitlog')}</td>
                  <td>
                    <Button size="small" icon={<EyeOutlined />} aria-label={t('pages.clients.ipLog')} loading={ipsLoading} onClick={openIpsModal}>
                      {clientIps.length > 0 ? clientIps.length : ''}
                    </Button>
                  </td>
                </tr>
                <tr>
                  <td>{t('pages.inbounds.createdAt')}</td>
                  <td><Tag>{dateLabel(client.createdAt)}</Tag></td>
                </tr>
                <tr>
                  <td>{t('pages.inbounds.updatedAt')}</td>
                  <td><Tag>{dateLabel(client.updatedAt)}</Tag></td>
                </tr>
                {client.comment && (
                  <tr>
                    <td>{t('pages.clients.comment')}</td>
                    <td><Tag className="info-large-tag">{client.comment}</Tag></td>
                  </tr>
                )}
                <tr>
                  <td>{t('pages.clients.attachedInbounds')}</td>
                  <td>
                    {(() => {
                      const ids = client.inboundIds || [];
                      if (ids.length === 0) return <span className="hint">—</span>;
                      const visible = ids.slice(0, INBOUND_CHIP_LIMIT);
                      const overflow = ids.slice(INBOUND_CHIP_LIMIT);
                      const inboundChip = (id: number) => {
                        const ib = inboundsById[id];
                        const proto = (ib?.protocol || '').toLowerCase();
                        const color = INBOUND_PROTOCOL_COLORS[proto] ?? 'default';
                        const label = formatInboundLabel(ib?.tag, ib?.remark);
                        return (
                          <Tooltip key={id} title={label}>
                            <Tag color={color}>{label}</Tag>
                          </Tooltip>
                        );
                      };
                      return (
                        <div className="chips">
                          {visible.map((id) => inboundChip(id))}
                          {overflow.length > 0 && (
                            <Popover
                              trigger="click"
                              placement="bottomRight"
                              content={
                                <div className="chips chips-stack">
                                  {overflow.map((id) => inboundChip(id))}
                                </div>
                              }
                            >
                              <Tag color="default" className="chip-more">
                                +{overflow.length} {t('more') !== 'more' ? t('more') : 'more'}
                              </Tag>
                            </Popover>
                          )}
                        </div>
                      );
                    })()}
                  </td>
                </tr>
              </tbody>
            </table>

            {links.length > 0 && (
              <>
                <Divider>{t('pages.inbounds.copyLink')}</Divider>
                {links.map((link, idx) => {
                  const parts = parseLinkParts(link);
                  const fallback = `${t('pages.clients.link')} ${idx + 1}`;
                  const rowTitle = (parts && linkMetaText(parts)) || fallback;
                  const qrRemark = parts?.remark || rowTitle;
                  const canQr = !isPostQuantumLink(link);
                  return (
                    <div key={idx} className="link-row">
                      {parts
                        ? <LinkTags parts={parts} />
                        : <Tag className="link-row-tag">LINK</Tag>}
                      <span className="link-row-title" title={rowTitle}>{rowTitle}</span>
                      <div className="link-row-actions">
                        <Tooltip title={t('copy')}>
                          <Button size="small" icon={<CopyOutlined />} aria-label={t('copy')} onClick={() => copyValue(link)} />
                        </Tooltip>
                        {canQr && (
                          <Popover
                            trigger="click"
                            placement="left"
                            destroyOnHidden
                            content={<QrPanel value={link} remark={qrRemark} size={220} />}
                          >
                            <Tooltip title={t('pages.clients.qrCode')}>
                              <Button size="small" icon={<QrcodeOutlined />} aria-label={t('pages.clients.qrCode')} />
                            </Tooltip>
                          </Popover>
                        )}
                      </div>
                    </div>
                  );
                })}
              </>
            )}

            {wgConfigText && client && (
              <>
                <Divider>{t('pages.clients.wireguardConfig')}</Divider>
                <ConfigBlock
                  label={t('pages.clients.config')}
                  text={wgConfigText}
                  fileName={`${client.email}.conf`}
                  qrRemark={client.email || 'peer'}
                />
              </>
            )}
          </>
        )}
      </Modal>

      <Modal
        open={ipsModalOpen}
        title={`${t('pages.inbounds.IPLimitlog')}${client?.email ? ` — ${client.email}` : ''}`}
        width={440}
        onCancel={() => setIpsModalOpen(false)}
        footer={[
          <Button key="refresh" icon={<ReloadOutlined />} loading={ipsLoading} onClick={loadIps}>
            {t('refresh')}
          </Button>,
          <Button key="clear" danger loading={ipsClearing} disabled={clientIps.length === 0} onClick={clearIps}>
            {t('pages.clients.clearAll')}
          </Button>,
          <Button key="close" type="primary" onClick={() => setIpsModalOpen(false)}>
            {t('close')}
          </Button>,
        ]}
      >
        {clientIps.length > 0 ? (
          <div style={{ maxHeight: 360, overflowY: 'auto' }}>
            {clientIps.map((entry, idx) => (
              <Tag
                key={idx}
                color="blue"
                style={{
                  display: 'block',
                  width: 'fit-content',
                  maxWidth: '100%',
                  marginBottom: 6,
                  padding: '2px 8px',
                  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
                }}
              >
                {entry.ip}{entry.time ? ` (${entry.time})` : ''}
              </Tag>
            ))}
          </div>
        ) : (
          <Tag>{t('noData')}</Tag>
        )}
      </Modal>
    </>
  );
}
