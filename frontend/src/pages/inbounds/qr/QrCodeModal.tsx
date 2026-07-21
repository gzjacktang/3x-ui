import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Collapse, Modal } from 'antd';
import type { CollapseProps } from 'antd';

import { Protocols } from '@/schemas/primitives';
import {
  genAllLinks,
  genWireguardConfigs,
  genWireguardLinks,
  isPostQuantumLink,
  preferPublicHost,
} from '@/lib/xray/inbound-link';
import { inboundFromDb, type DbInboundLike } from '@/lib/xray/inbound-from-db';
import QrPanel from './QrPanel';

interface ClientSetting {
  email?: string;
  [k: string]: unknown;
}

interface QrCodeModalProps {
  open: boolean;
  onClose: () => void;
  dbInbound: (DbInboundLike & { remark?: string }) | null;
  client?: ClientSetting | null;
  nodeAddress?: string;
  publicHost?: string;
}

interface QrItem {
  key: string;
  header: string;
  value: string;
  downloadName?: string;
  showQr?: boolean;
}

export default function QrCodeModal({
  open,
  onClose,
  dbInbound,
  client = null,
  nodeAddress = '',
  publicHost = '',
}: QrCodeModalProps) {
  const { t } = useTranslation();
  const [links, setLinks] = useState<{ remark?: string; link: string }[]>([]);
  const [wireguardConfigs, setWireguardConfigs] = useState<string[]>([]);
  const [wireguardLinks, setWireguardLinks] = useState<string[]>([]);
  const [activeKey, setActiveKey] = useState<string[]>([]);

  useEffect(() => {
    if (!open || !dbInbound) return;
    const inbound = inboundFromDb(dbInbound);
    const fallbackHostname = preferPublicHost(window.location.hostname, publicHost);
    if (inbound.protocol === Protocols.WIREGUARD) {
      const peerRemark = client?.email
        ? `${dbInbound.remark}-${client.email}`
        : dbInbound.remark || '';
      setWireguardConfigs(
        genWireguardConfigs({
          inbound,
          remark: peerRemark,
          hostOverride: nodeAddress,
          fallbackHostname,
        }).split('\r\n'),
      );
      setWireguardLinks(
        genWireguardLinks({
          inbound,
          remark: peerRemark,
          hostOverride: nodeAddress,
          fallbackHostname,
        }).split('\r\n'),
      );
      setLinks([]);
    } else {
      setLinks(
        genAllLinks({
          inbound,
          remark: dbInbound.remark || '',
          client: client ?? {},
          hostOverride: nodeAddress,
          fallbackHostname,
        }),
      );
      setWireguardConfigs([]);
      setWireguardLinks([]);
    }

  }, [open, dbInbound, client, nodeAddress, publicHost]);

  const qrItems = useMemo<QrItem[]>(() => {
    const items: QrItem[] = [];
    links.forEach((link, idx) => {
      items.push({ key: `l${idx}`, header: link.remark || `Link ${idx + 1}`, value: link.link });
    });
    wireguardConfigs.forEach((cfg, idx) => {
      items.push({
        key: `wc${idx}`,
        header: `Peer ${idx + 1} config`,
        value: cfg,
        downloadName: `peer-${idx + 1}.conf`,
      });
      if (wireguardLinks[idx]) {
        items.push({ key: `wl${idx}`, header: `Peer ${idx + 1} link`, value: wireguardLinks[idx], showQr: false });
      }
    });
    return items;
  }, [links, wireguardConfigs, wireguardLinks]);

  const collapseItems: CollapseProps['items'] = useMemo(
    () => qrItems.map((item) => ({
      key: item.key,
      label: item.header,
      children: (
        <QrPanel
          value={item.value}
          remark={item.header}
          downloadName={item.downloadName || ''}
          showQr={item.showQr !== false && !isPostQuantumLink(item.value)}
        />
      ),
    })),
    [qrItems],
  );

  useEffect(() => {
    if (!open) {
      setActiveKey([]);
      return;
    }
    setActiveKey(qrItems.length > 0 ? [qrItems[0].key] : []);
  }, [open, qrItems]);

  return (
    <Modal open={open} onCancel={onClose} title={t('qrCode')} footer={null} width={420} destroyOnHidden>
      {dbInbound && collapseItems && collapseItems.length > 0 && (
        <Collapse
          ghost
          activeKey={activeKey}
          onChange={(keys) => setActiveKey(typeof keys === 'string' ? [keys] : (keys as string[]))}
          items={collapseItems}
        />
      )}
    </Modal>
  );
}
