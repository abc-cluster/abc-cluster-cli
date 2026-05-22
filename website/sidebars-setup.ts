import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  setupSidebar: [
    {type: 'doc', id: 'index', label: 'Cluster Setup'},
    {
      type: 'category',
      label: 'Seedling tier',
      link: {type: 'doc', id: 'seedling/index'},
      items: [
        {type: 'doc', id: 'seedling/nomad',     label: 'Phase 1 — Bare Nomad'},
        {type: 'doc', id: 'seedling/bootstrap', label: 'Phase 2 — Pulumi bootstrap'},
        {type: 'doc', id: 'seedling/provision', label: 'Phase 3 — Provision access pool'},
        {type: 'doc', id: 'seedling/deploy',    label: 'Phase 4 — Deploy landing page'},
        {type: 'doc', id: 'seedling/caddy',     label: 'Reverse proxy / TLS (optional)'},
        {type: 'doc', id: 'seedling/handover',  label: 'Issuing handover files'},
      ],
    },
  ],
};

export default sidebars;
