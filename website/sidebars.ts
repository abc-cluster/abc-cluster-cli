import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  mainSidebar: [
    {
      type: 'doc',
      id: 'index',
      label: 'Overview',
    },
    {
      type: 'doc',
      id: 'installation',
      label: 'Installation',
    },
    {
      type: 'doc',
      id: 'quickstart',
      label: 'Quick start',
    },
    {
      type: 'category',
      label: 'Tutorials',
      link: {type: 'doc', id: 'tutorials/index'},
      items: [
        {type: 'doc', id: 'tutorials/demo',      label: 'Hands-on walkthrough'},
      ],
    },
    {
      type: 'category',
      label: 'Reference',
      link: {type: 'doc', id: 'reference/index'},
      items: [
        {type: 'doc', id: 'reference/global-flags', label: 'Global flags'},
        {type: 'doc', id: 'reference/context',      label: 'context / config'},
        {type: 'doc', id: 'reference/auth',         label: 'auth'},
        {type: 'doc', id: 'reference/secrets',      label: 'secrets'},
        {type: 'doc', id: 'reference/jobs',         label: 'job run'},
        {type: 'doc', id: 'reference/data',         label: 'data'},
        {type: 'doc', id: 'reference/infra',        label: 'infra'},
        {type: 'doc', id: 'reference/admin',        label: 'admin services'},
        {type: 'doc', id: 'reference/cluster',      label: 'cluster'},
      ],
    },
  ],
};

export default sidebars;
