import { describe, expect, it } from 'vitest';
import { genWireguardConfig, wireguardConfigFromLink } from '@/lib/xray/inbound-link';
import { buildWireguardClientConfig } from '@/pages/clients/wireguardConfig';
import { WireguardInboundSettingsSchema } from '@/schemas/protocols/inbound/wireguard';
import { upgradeLegacyXdnsSettings } from '@/lib/xray/xdns-mask';
import { formValuesToWirePayload, rawOutboundToFormValues } from '@/lib/xray/outbound-form-adapter';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';

const secretKey = 'yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=';
const publicKey = 'xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=';

describe('WireGuard config line safety', () => {
  it.each(['\nPostUp = unsafe', '\rPostDown = unsafe', '\u0000', '\u007f'])(
    'refuses a control character in every export path: %s', (injected) => {
      const settings = WireguardInboundSettingsSchema.parse({ secretKey, dns: `1.1.1.1${injected}`,
        peers: [{ publicKey, privateKey: secretKey, allowedIPs: ['10.0.0.2/32'] }] });
      expect(genWireguardConfig({ settings, address: 'vpn.example.com', port: 51820, peerIndex: 0 })).toBe('');
      const client = { email: 'peer', privateKey: secretKey, allowedIPs: '10.0.0.2/32', comment: injected } as ClientRecord;
      const inbound = { id: 1, port: 51820, protocol: 'wireguard', wgPublicKey: publicKey } as InboundOption;
      expect(buildWireguardClientConfig(client, inbound, 'vpn.example.com')).toBe('');
      const link = new URL(`wireguard://${encodeURIComponent(secretKey)}@vpn.example.com:51820`);
      link.searchParams.set('publickey', publicKey);
      link.searchParams.set('address', `10.0.0.2/32${injected}`);
      expect(wireguardConfigFromLink(link.toString())).toBe('');
    },
  );
});

describe('XDNS core compatibility', () => {
  it('upgrades old strings to current names/addrs lists and retains the old EDNS0 size', () => {
    expect(upgradeLegacyXdnsSettings({ domains: ['example.com:txt'], resolvers: ['example.com:txt+udp://1.1.1.1:53'] })).toEqual({
      changed: true, next: { domains: [{ names: ['example.com'], types: [16], edns0: 1232 }], resolvers: [{ addrs: ['udp://1.1.1.1:53'] }] },
    });
  });
  it('preserves modern keys while lifting intermediate objects', () => {
    const modern = { domains: [{ names: ['modern.example'], edns0: 1400 }], extraPoll: 2 };
    expect(upgradeLegacyXdnsSettings(modern)).toEqual({ next: modern, changed: false });
    expect(upgradeLegacyXdnsSettings({ domains: [{ name: 'old.example', types: [28] }], resolvers: [{ type: 'tcp', settings: { addr: '1.1.1.1:53' } }] }).next).toEqual({
      domains: [{ names: ['old.example'], types: [28] }], resolvers: [{ addrs: ['tcp://1.1.1.1:53'] }],
    });
  });
});

describe('WireGuard strategy compatibility', () => {
  const raw = { protocol: 'wireguard', tag: 'warp', settings: { secretKey, address: ['10.0.0.2/32'], peers: [{ publicKey, endpoint: 'vpn.example.com:51820' }] } };
  it('moves a legacy family preference to socket and target strategies', () => {
    const wire = formValuesToWirePayload(rawOutboundToFormValues({ ...raw, settings: { ...raw.settings, domainStrategy: 'ForceIPv4v6', remoteDNS: ['local'] } }));
    expect(wire.targetStrategy).toBe('ForceIPv4v6');
    expect(wire.streamSettings).toMatchObject({ sockopt: { domainStrategy: 'ForceIPv4v6' } });
    expect(wire.settings).not.toHaveProperty('domainStrategy');
    expect(wire.settings).not.toHaveProperty('remoteDNS', ['local']);
  });
  it('preserves explicit target/socket strategies and IP-only DNS', () => {
    const wire = formValuesToWirePayload(rawOutboundToFormValues({ ...raw, targetStrategy: 'ForceIPv6', streamSettings: { sockopt: { domainStrategy: 'ForceIPv4' } }, settings: { ...raw.settings, domainStrategy: 'ForceIPv4v6', remoteDNS: ['1.1.1.1'] } }));
    expect(wire.targetStrategy).toBe('ForceIPv6');
    expect(wire.streamSettings).toMatchObject({ sockopt: { domainStrategy: 'ForceIPv4' } });
    expect(wire.settings).toHaveProperty('remoteDNS', ['1.1.1.1']);
  });
});
