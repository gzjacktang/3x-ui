import { useTranslation } from 'react-i18next';
import { Button, Form, Input, InputNumber, Space, Switch } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

import { FormField } from '@/components/form/rhf';

interface WireguardFieldsProps {
  wgPubKey: string;
  regenInboundWg: () => void;
}

export default function WireguardFields({ wgPubKey, regenInboundWg }: WireguardFieldsProps) {
  const { t } = useTranslation();
  return (
    <>
      <Form.Item label={t('pages.xray.wireguard.secretKey')}>
        <Space.Compact block>
          <FormField name={['settings', 'secretKey']} noStyle>
            <Input style={{ width: 'calc(100% - 32px)' }} />
          </FormField>
          <Button aria-label={t('regenerate')} icon={<ReloadOutlined />} onClick={regenInboundWg} />
        </Space.Compact>
      </Form.Item>
      <Form.Item label={t('pages.xray.wireguard.publicKey')}>
        <Input value={wgPubKey} disabled />
      </Form.Item>
      <FormField name={['settings', 'mtu']} label="MTU">
        <InputNumber />
      </FormField>
      <FormField name={['settings', 'dns']} label={t('pages.inbounds.info.dns')}>
        <Input placeholder="1.1.1.1, 1.0.0.1" />
      </FormField>
      <FormField
        name={['settings', 'noKernelTun']}
        label={t('pages.inbounds.info.noKernelTun')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
    </>
  );
}
