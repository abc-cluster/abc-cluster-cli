// Tier docs-site build profile.
//
// The base docusaurus.config.ts defines the multi-project docs structure:
// the hub (src/pages/index.tsx) at `/`, the CLI docs at `/cli`, and the
// concepts placeholder at `/concepts`, all with baseUrl `/`. A *tier* site
// (e.g. seedling.abc-cluster.cloud/docs) serves the same structure under
// `/docs/` and layers the env-driven tier gate onto the CLI docs so hidden
// pages aren't generated. This profile overrides only baseUrl, headTags,
// onBrokenLinks, and the CLI docs `exclude`; everything else (hub, concepts
// plugin instance, navbar switcher, themeConfig, mermaid) is reused.
//
// Build:
//   npx docusaurus build --config docusaurus.tier.config.ts --out-dir build-tier
//
// Deploy: drop build-tier/ at the edge under
//   /var/www/seedling.abc-cluster.cloud/docs/
// The existing seedling Caddy vhost (root + file_server) serves it; assets
// resolve because baseUrl is /docs/.
//
// This is the interim seedling tier docs site. When abc-site-kit lands the
// CONTENT composition changes (docs-common + tier overlay) but this
// build/serve shape stays.

import type {Config} from '@docusaurus/types';
import base from './docusaurus.config';
import {resolveHidden, hiddenToExcludeGlobs} from 'abc-site-kit/tier-gates';

const basePreset = (base.presets as any[])[0][1];

// Same env-driven gate the sidebar uses, applied to the docs build so
// hidden pages aren't generated at all (no orphan HTML; sidebar + build
// stay in sync; onBrokenLinks won't trip on gated-out pages).
const gateExclude = hiddenToExcludeGlobs(resolveHidden());

const config: Config = {
  ...base,
  baseUrl: '/docs/',
  // The base config's headTags hardcode absolute `/img/favicon*` paths
  // (correct only at baseUrl `/`). Under `/docs/` they 404 and, coming
  // after the baseUrl-aware `favicon` link, the browser prefers the
  // broken media-query ones → no tab icon. Drop them; the plain
  // `favicon: 'img/favicon.svg'` already emits a correct
  // `/docs/img/favicon.svg` link.
  headTags: [],
  // Internal-link resolution shifts with routeBasePath; for the interim
  // tier build a broken internal link must not block shipping docs.
  onBrokenLinks: 'warn',
  presets: [
    [
      'classic',
      {
        ...basePreset,
        // The hub (src/pages/index.tsx) IS the tier site root now;
        // keep the pages plugin enabled (default opts) so /docs/ serves
        // the project picker. The classic preset rejects `true` for
        // `pages` — `{}` is the canonical "enabled with defaults".
        pages: {},
        docs: {
          ...basePreset.docs,
          // CLI docs stay at /cli (from the base config). Only the
          // env-driven tier gate is layered on here so hidden pages
          // aren't generated (no orphan HTML; sidebar + build in sync).
          exclude: [
            ...((basePreset.docs?.exclude as string[]) ?? []),
            ...gateExclude,
          ],
        },
      },
    ],
  ],
};

export default config;
