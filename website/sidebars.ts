import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';
import {resolveHidden, isHidden} from 'abc-site-kit/tier-gates';

// Authored, full sidebar. Tier/deployment gating filters this down via
// tier-gates.ts (env-driven). With ABC_TIER unset nothing is hidden and
// the output is identical to the hand-authored tree below.
const fullSidebar: SidebarsConfig = {
  mainSidebar: [
    {type: 'doc', id: 'index', label: 'Overview'},
    {type: 'doc', id: 'quickstart', label: 'Quick start'},
    {
      type: 'category',
      label: 'Tutorials',
      link: {type: 'doc', id: 'tutorials/index'},
      items: [
        {type: 'doc', id: 'tutorials/project-management', label: 'Project management'},
        {type: 'doc', id: 'tutorials/demo',               label: 'Interacting with the cluster'},
      ],
    },
    {
      type: 'category',
      label: 'Reference',
      link: {type: 'doc', id: 'reference/index'},
      items: [
        {type: 'doc', id: 'reference/global-flags',      label: 'Global flags'},
        {type: 'doc', id: 'reference/auth-context',      label: 'auth context / config'},
        {type: 'doc', id: 'reference/auth',              label: 'auth'},
        {type: 'doc', id: 'reference/secrets',           label: 'secrets'},
        {type: 'doc', id: 'reference/abc-project',       label: 'project'},
        {type: 'doc', id: 'reference/abc-investigation', label: 'investigation'},
        {type: 'doc', id: 'reference/abc-report',        label: 'report (showback)'},
        {type: 'doc', id: 'reference/abc-accounting',    label: 'accounting (budget)'},
        {type: 'doc', id: 'reference/jobs',              label: 'job run'},
        {type: 'doc', id: 'reference/pipeline',          label: 'pipeline run'},
        {type: 'doc', id: 'reference/module',            label: 'module run'},
        {type: 'doc', id: 'reference/data',              label: 'data'},
        {type: 'doc', id: 'reference/infra',             label: 'infra'},
        {type: 'doc', id: 'reference/admin',             label: 'admin services'},
        {type: 'doc', id: 'reference/admin-tools',       label: 'admin tools'},
        {type: 'doc', id: 'reference/cluster',           label: 'cluster'},
        {type: 'doc', id: 'reference/doctor',            label: 'doctor (preflight)'},
        {type: 'doc', id: 'reference/local-state',       label: 'local state (~/.abc/local.db)'},
      ],
    },
  ],
};

const hidden = resolveHidden();

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function gate(items: any[]): any[] {
  const out: any[] = [];
  for (const it of items) {
    if (it && it.type === 'doc') {
      if (isHidden(it.id, hidden)) continue;
    } else if (it && it.type === 'category') {
      const kids = gate(it.items || []);
      const linkHidden =
        it.link?.type === 'doc' && isHidden(it.link.id, hidden);
      // Drop a category if its link page is gated out or it has no
      // visible children left.
      if (linkHidden || kids.length === 0) continue;
      out.push({...it, items: kids});
      continue;
    }
    out.push(it);
  }
  return out;
}

const sidebars: SidebarsConfig = hidden.length
  ? {mainSidebar: gate((fullSidebar.mainSidebar as any[]) ?? [])}
  : fullSidebar;

export default sidebars;
