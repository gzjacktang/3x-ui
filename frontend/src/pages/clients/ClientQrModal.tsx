import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Collapse, Modal, Spin, Tag } from 'antd';
import { isPostQuantumLink } from '@/lib/xray/inbound-link';
import { loadClientShareLinks } from '@/lib/clients/share-links';
import { LinkTags, linkMetaText, parseLinkParts } from '@/lib/xray/link-label';
import { QrPanel } from '@/pages/inbounds/qr';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';
import { buildWireguardClientConfig, findWireguardInbound, isWireguardClient } from './wireguardConfig';

interface ClientQrModalProps {
  open: boolean;
  client: ClientRecord | null;
  inboundsById: Record<number, InboundOption>;
  onOpenChange: (open: boolean) => void;
}

export default function ClientQrModal({
  open,
  client,
  inboundsById,
  onOpenChange,
}: ClientQrModalProps) {
  const { t } = useTranslation();
  const [links, setLinks] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);

  const wgInbound = useMemo(() => findWireguardInbound(client, inboundsById), [client, inboundsById]);
  const wgConfigText = useMemo(() => {
    if (!client || !wgInbound || !isWireguardClient(client)) return '';
    return buildWireguardClientConfig(client, wgInbound, window.location.hostname, '');
  }, [client, wgInbound]);

  const hasAnything = !!wgConfigText || links.length > 0;

  useEffect(() => {
    if (!open || !client) {
      setLinks([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    (async () => {
      try {
        const next = await loadClientShareLinks(client, inboundsById);
        if (!cancelled) setLinks(next);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [open, client, inboundsById]);

  const [activeKey, setActiveKey] = useState<string[]>([]);

  const items = useMemo(() => {
    const out: { key: string; label: React.ReactNode; children: React.ReactNode }[] = [];
    links.forEach((link, idx) => {
      const parts = parseLinkParts(link);
      const meta = parts ? linkMetaText(parts) : '';
      const label: React.ReactNode = parts ? (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
          <LinkTags parts={parts} />
          {meta && <span style={{ opacity: 0.6, fontSize: 12 }}>({meta})</span>}
        </span>
      ) : `${t('pages.clients.link')} ${idx + 1}`;
      out.push({
        key: `l${idx}`,
        label,
        children: (
          <QrPanel
            value={link}
            remark={parts?.remark || `${client?.email || ''} #${idx + 1}`}
            showQr={!isPostQuantumLink(link)}
          />
        ),
      });
    });
    if (wgConfigText) {
      out.push({
        key: 'wg-config',
        label: <Tag color="cyan" style={{ margin: 0 }}>{t('pages.clients.wireguardConfig')}</Tag>,
        children: (
          <QrPanel
            value={wgConfigText}
            remark={client?.email || 'peer'}
            downloadName={`${client?.email || 'peer'}.conf`}
          />
        ),
      });
    }
    return out;
  }, [wgConfigText, links, client?.email, t]);

  useEffect(() => {
    if (!open) {
      setActiveKey([]);
      return;
    }
    setActiveKey(items.length > 0 ? [items[0].key] : []);
  }, [open, items]);

  return (
    <Modal
      open={open}
      title={client ? `${t('qrCode')} — ${client.email}` : t('qrCode')}
      footer={null}
      width={520}
      centered
      onCancel={() => onOpenChange(false)}
    >
      <Spin spinning={loading}>
        {client && !hasAnything && !loading && (
          <div style={{ padding: 24, textAlign: 'center', opacity: 0.6 }}>{t('pages.clients.noLinks')}</div>
        )}
        {hasAnything && (
          <Collapse
            activeKey={activeKey}
            onChange={(keys) => setActiveKey(typeof keys === 'string' ? [keys] : (keys as string[]))}
            items={items}
          />
        )}
      </Spin>
    </Modal>
  );
}
