import { describe, it, expect } from 'vitest';
import i18next from 'i18next';

import { INTERPOLATION } from '@/i18n/react';

describe('i18n nesting guard', () => {
  it('leaves $t(...) tokens literal in interpolated values', async () => {
    const instance = i18next.createInstance();
    await instance.init({
      lng: 'en-US',
      resources: {
        'en-US': {
          translation: {
            secret: 'EXPANDED',
            greeting: 'Hello {name}',
          },
        },
      },
      interpolation: INTERPOLATION,
    });

    expect(instance.t('greeting', { name: '$t(secret)' })).toBe('Hello $t(secret)');
    expect(instance.t('greeting', { name: '$t(' + 'a'.repeat(5000) })).toBe(
      'Hello $t(' + 'a'.repeat(5000),
    );
  });
});
