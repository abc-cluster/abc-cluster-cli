// Tier docs-site build profile.
//
// The base docusaurus.config.ts serves the gateway landing at `/` and docs
// at `/docs` with baseUrl `/` (used for the aither deploy). A *tier* site
// (e.g. seedling.abc-cluster.cloud/docs) wants docs-only, no gateway
// landing, with everything under `/docs/`. This profile overrides exactly
// those three things and reuses the rest of the base config (brand webpack
// alias, headTags, themeConfig, mermaid, etc.) unchanged.
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

const basePreset = (base.presets as any[])[0][1];

const config: Config = {
  ...base,
  baseUrl: '/docs/',
  // Internal-link resolution shifts with routeBasePath; for the interim
  // tier build a broken internal link must not block shipping docs.
  onBrokenLinks: 'warn',
  presets: [
    [
      'classic',
      {
        ...basePreset,
        // No gateway landing page on a tier docs site.
        pages: false,
        docs: {
          ...basePreset.docs,
          // Docs ARE the site root → under baseUrl that is /docs/.
          routeBasePath: '/',
        },
      },
    ],
  ],
};

export default config;
