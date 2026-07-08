import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'abc CLI',
  tagline: 'African Bioinformatics Computing — command-line tool',
  favicon: 'img/favicon.svg',

  future: {
    v4: true,
  },

  url: 'https://seedling.abc-cluster.cloud',
  baseUrl: '/docs/',

  organizationName: 'abc-cluster',
  projectName: 'abc-cluster-cli',

  onBrokenLinks: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    format: 'md',
    mermaid: true,
  },

  themes: ['@docusaurus/theme-mermaid'],

  // Browser-theme-aware favicon: dark variant when the user's OS is in dark
  // mode, light variant otherwise. The plain `favicon: 'img/favicon.svg'`
  // above still applies as a fallback for browsers that ignore `media`.
  headTags: [
    {
      tagName: 'link',
      attributes: {
        rel: 'icon',
        type: 'image/svg+xml',
        href: '/docs/img/favicon.svg',
        media: '(prefers-color-scheme: light)',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'icon',
        type: 'image/svg+xml',
        href: '/docs/img/favicon-dark.svg',
        media: '(prefers-color-scheme: dark)',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'apple-touch-icon',
        sizes: '180x180',
        href: '/docs/img/apple-touch-icon.png',
      },
    },
  ],

  presets: [
    [
      'classic',
      {
        // CLI docs instance (default plugin id). Served at /cli (under
        // baseUrl that is /docs/cli on the tier site).
        docs: {
          path: '../docs',
          routeBasePath: 'cli',
          exclude: ['design/**'],
          sidebarPath: './sidebars.ts',
          editUrl: 'https://github.com/abc-cluster/abc-cluster-cli/tree/main/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  plugins: [
    // Cluster Setup docs — operator guides for private abc-seedling deployments.
    // Served at /concepts (path kept stable; label changed in projects registry).
    [
      '@docusaurus/plugin-content-docs',
      {
        id: 'concepts',
        path: './concepts-docs',
        routeBasePath: 'concepts',
        sidebarPath: './sidebars-setup.ts',
      },
    ],
  ],

  themeConfig: {
    image: 'img/social-card.png',
    colorMode: {
      defaultMode: 'light',
      disableSwitch: false,
      // Deterministic light default across all abc-cluster sites; users can
      // still toggle to dark (persisted). OS preference intentionally not
      // honoured so first paint is consistent everywhere.
      respectPrefersColorScheme: false,
    },
    navbar: {
      title: 'abc',
      logo: {
        alt: 'abc-cluster ABC mark',
        src: 'img/logo.svg',
        srcDark: 'img/logo-dark.svg',
        // 32px to match the shared chrome.css brand-mark lockup (the
        // tightened viewBox reads fine at this size).
        style: { width: '32px', height: '32px' },
        href: 'https://seedling.abc-cluster.cloud/',
        target: '_self',
      },
      items: [
        {
          type: 'custom-projectSwitcher',
          position: 'left',
        },
        {
          href: 'https://seedling.abc-cluster.cloud/',
          label: 'Cluster',
          position: 'right',
        },
        {
          href: 'https://github.com/abc-cluster/abc-cluster-cli/releases',
          label: 'Releases',
          position: 'right',
        },
        {
          href: 'https://github.com/abc-cluster/abc-cluster-cli',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'All projects',      to: '/'},
            {label: 'CLI — overview',    to: '/cli/'},
            {label: 'CLI — quick start', to: '/cli/quickstart'},
            {label: 'CLI — reference',   to: '/cli/reference'},
            {label: 'Cluster Setup',     to: '/concepts/'},
          ],
        },
        {
          title: 'Platform',
          items: [
            {label: 'Cluster gateway', href: 'https://seedling.abc-cluster.cloud/'},
            {label: 'Nomad jobs',      href: 'https://nomad.seedling.abc-cluster.cloud/'},
            {label: 'MinIO storage',   href: 'https://minio.seedling.abc-cluster.cloud/'},
            {label: 'GitHub releases', href: 'https://github.com/abc-cluster/abc-cluster-cli/releases'},
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} abc-cluster · Built with Docusaurus`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'yaml', 'hcl', 'json'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
