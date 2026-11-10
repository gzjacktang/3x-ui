import { describe, expect, it } from 'vitest';

import { patchStorybookTestPath, storybookTestPathCompat } from './vitest-story-path-compat';

const guard = 'const _isRunningFromThisFile = convertToFilePath(import.meta.url).includes(globalThis.__vitest_worker__.filepath ?? _expect.getState().testPath);';

function runGuard(code: string, url: string, filepath?: string, testPath?: string): boolean {
  const execute = new Function(
    'moduleUrl', 'globalThis', '_expect', 'convertToFilePath',
    code.replace('import.meta.url', 'moduleUrl') + '; return _isRunningFromThisFile;',
  );
  return execute(
    url, { __vitest_worker__: { filepath } }, { getState: () => ({ testPath }) },
    (value: string) => value.replace(/^file:\/\//, '').replace(/%20/g, ' '),
  ) as boolean;
}

describe('Storybook current-file guard compatibility', () => {
  it('reproduces the upstream Unicode-path miss before the shim', () => {
    const filepath = '/workspace/ai修图/frontend/src/Icon.stories.tsx';
    expect(runGuard(guard, 'http://localhost' + encodeURI(filepath), filepath)).toBe(false);
  });

  it.each([
    '/workspace/frontend/src/Icon.stories.tsx',
    '/workspace/ai修图/frontend/src/Icon.stories.tsx',
    '/workspace/with spaces/frontend/src/Icon.stories.tsx',
    '/workspace/literal%20-and-100%/frontend/src/Icon.stories.tsx',
    '/workspace/with#hash/frontend/src/Icon.stories.tsx',
  ])('registers the current story at %s', (filepath) => {
    const rewritten = patchStorybookTestPath(guard);
    expect(rewritten).toBeDefined();
    const url = 'http://localhost' + filepath.split('/').map(encodeURIComponent).join('/');
    expect(runGuard(rewritten!, url, filepath)).toBe(true);
  });

  it('keeps the fallback testPath and rejects imported stories', () => {
    const rewritten = patchStorybookTestPath(guard)!;
    const filepath = '/workspace/修图/Icon.stories.tsx';
    const url = 'file://' + encodeURI(filepath);
    expect(runGuard(rewritten, url, undefined, filepath)).toBe(true);
    expect(runGuard(rewritten, url, '/workspace/修图/Other.stories.tsx')).toBe(false);
  });

  it('does not throw on a malformed percent escape', () => {
    const rewritten = patchStorybookTestPath(guard)!;
    const filepath = '/workspace/literal%/Icon.stories.tsx';
    expect(runGuard(rewritten, 'http://localhost' + filepath, filepath)).toBe(true);
  });

  it('does not modify ordinary component modules or unrelated calls', () => {
    expect(patchStorybookTestPath('export const Example = {};')).toBeUndefined();
    expect(storybookTestPathCompat().name).toBe('storybook-test-path-compat');
  });
});
