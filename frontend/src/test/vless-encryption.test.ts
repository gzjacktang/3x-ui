import { describe, it, expect } from 'vitest';

import { vlessEncryptionAuthKind } from '@/lib/xray/vless-encryption';
import { canEnableTlsFlow } from '@/lib/xray/protocol-capabilities';

const x25519Key = 'kO9pIKKPtoUCzo3ZWfWfp0lQoWCyJC1TqL8oz1hpsFM';
const mlkem768Key = 'A'.repeat(1590);

describe('vlessEncryptionAuthKind', () => {
  const cases: { name: string; encryption: string; want: ReturnType<typeof vlessEncryptionAuthKind> }[] = [
    { name: 'empty string', encryption: '', want: null },
    { name: 'none', encryption: 'none', want: null },
    { name: 'only dots', encryption: '...', want: null },
    { name: 'x25519 native', encryption: `mlkem768x25519plus.native.600s.${x25519Key}`, want: 'x25519' },
    { name: 'x25519 xorpub', encryption: `mlkem768x25519plus.xorpub.600s.${x25519Key}`, want: 'x25519_xorpub' },
    { name: 'x25519 random', encryption: `mlkem768x25519plus.random.600s.${x25519Key}`, want: 'x25519_random' },
    { name: 'mlkem768 native', encryption: `mlkem768x25519plus.native.600s.${mlkem768Key}`, want: 'mlkem768' },
    { name: 'mlkem768 xorpub', encryption: `mlkem768x25519plus.xorpub.600s.${mlkem768Key}`, want: 'mlkem768_xorpub' },
    { name: 'mlkem768 random', encryption: `mlkem768x25519plus.random.600s.${mlkem768Key}`, want: 'mlkem768_random' },
    { name: 'two-segment value treated as native', encryption: `mlkem768x25519plus.${x25519Key}`, want: 'x25519' },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(vlessEncryptionAuthKind(c.encryption)).toBe(c.want);
    });
  }
});

describe('VLESS ENC Vision availability', () => {
  it('allows RAW + ENC + Vision without transport TLS', () => {
    expect(canEnableTlsFlow({
      protocol: 'vless',
      settings: { decryption: `mlkem768x25519plus.native.600s.${x25519Key}` },
      streamSettings: { network: 'tcp', security: 'none' },
    })).toBe(true);
  });

  it('still hides Vision on unencrypted RAW', () => {
    expect(canEnableTlsFlow({
      protocol: 'vless',
      settings: { encryption: 'none', decryption: 'none' },
      streamSettings: { network: 'tcp', security: 'none' },
    })).toBe(false);
  });
});
