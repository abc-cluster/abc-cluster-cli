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

  url: 'https://abc-cluster.io',
  baseUrl: '/',

  organizationName: 'abc-cluster',
  projectName: 'abc-cluster-cli',

  onBrokenLinks: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    format: 'md',
  },

  // Browser-theme-aware favicon: dark variant when the user's OS is in dark
  // mode, light variant otherwise. The plain `favicon: 'img/favicon.svg'`
  // above still applies as a fallback for browsers that ignore `media`.
  headTags: [
    {
      tagName: 'link',
      attributes: {
        rel: 'icon',
        type: 'image/svg+xml',
        href: '/img/favicon.svg',
        media: '(prefers-color-scheme: light)',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'icon',
        type: 'image/svg+xml',
        href: '/img/favicon-dark.svg',
        media: '(prefers-color-scheme: dark)',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'apple-touch-icon',
        sizes: '180x180',
        href: '/img/apple-touch-icon.png',
      },
    },
  ],

  presets: [
    [
      'classic',
      {
        docs: {
          path: '../docs',
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

  themeConfig: {
    image: 'img/social-card.png',
    colorMode: {
      defaultMode: 'dark',
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'abc',
      logo: {
        alt: 'abc-cluster ABC mark',
        src: 'img/logo.svg',
        srcDark: 'img/logo-dark.svg',
        // Trio rings need ~28px to read; 22px crushed the A/B/C glyphs.
        style: { width: '28px', height: '28px' },
        href: 'http://aither.mb.sun.ac.za/',
        target: '_self',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'mainSidebar',
          position: 'left',
          label: 'Docs',
        },
        {
          href: 'http://aither.mb.sun.ac.za/',
          label: 'Cluster',
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
            {label: 'Overview',      to: '/docs/'},
            {label: 'Quick start',   to: '/docs/quickstart'},
            {label: 'Command reference', to: '/docs/reference'},
            {label: 'Tutorials',     to: '/docs/tutorials'},
          ],
        },
        {
          title: 'Platform',
          items: [
            {label: 'Cluster gateway', href: 'http://aither.mb.sun.ac.za/'},
            {label: 'Nomad jobs',      href: 'http://nomad.aither/ui/'},
            {label: 'MinIO storage',   href: 'http://minio-console.aither/'},
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
