import type { Plugin } from 'vite';

// Storybook 10.5's file guard only decodes %20. Its browser module URL contains
// encoded Unicode while Vitest supplies a decoded filesystem path.
export function patchStorybookTestPath(code: string): string | undefined {
  const guard = /convertToFilePath\(import\.meta\.url\)\.includes\((?=globalThis\.__vitest_worker__\.filepath\b)/g;
  if (!guard.test(code)) return;
  // Decode the module URL exactly once; the worker path is already a raw
  // filesystem path and can legitimately contain literal "%" or "%20".
  return code.replaceAll(
    guard,
    '((url) => { try { return decodeURIComponent(url); } catch { return url; } })(import.meta.url).includes(',
  );
}

export function storybookTestPathCompat(): Plugin {
  return {
    name: 'storybook-test-path-compat',
    enforce: 'post',
    transform(code, id) {
      if (!/\.stories\.[jt]sx?(?:\?|$)/.test(id)) return;
      const rewritten = patchStorybookTestPath(code);
      if (rewritten !== undefined && rewritten !== code) return { code: rewritten, map: null };
    },
  };
}
