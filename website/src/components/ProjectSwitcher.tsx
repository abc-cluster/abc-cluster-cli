import React from 'react';
import {useLocation} from '@docusaurus/router';
import DropdownNavbarItem from '@theme/NavbarItem/DropdownNavbarItem';
import {PROJECTS, projectByPath} from 'abc-site-kit/projects';

// Project-switcher navbar dropdown. Items come from the single-source
// abc-site-kit registry; the dropdown LABEL reflects the active project
// (projectByPath on the current path) so it reads "CLI" / "Concepts"
// instead of a generic "Docs". Falls back to "Docs" on the hub / unknown.
export default function ProjectSwitcher(props: Record<string, unknown>) {
  const {pathname} = useLocation();
  const active = projectByPath(pathname);
  return (
    <DropdownNavbarItem
      {...props}
      label={active ? active.label : 'Docs'}
      items={PROJECTS.map((p) => ({label: p.label, href: p.path}))}
    />
  );
}
