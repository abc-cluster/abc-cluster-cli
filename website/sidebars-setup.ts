import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  setupSidebar: [
    {type: 'doc', id: 'index', label: 'Cluster Setup'},
    {
      type: 'category',
      label: 'Seedling tier',
      link: {type: 'doc', id: 'seedling/index'},
      items: [
        {type: 'doc', id: 'seedling/provision',    label: 'Provision the access pool'},
        {type: 'doc', id: 'seedling/deploy',       label: 'Deploy landing + claim service'},
        {type: 'doc', id: 'seedling/caddy',        label: 'Reverse proxy / TLS (optional)'},
        {type: 'doc', id: 'seedling/handover',     label: 'Issuing access handover files'},
      ],
    },
  ],
};

export default sidebars;
