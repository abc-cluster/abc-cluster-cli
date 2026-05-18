import ComponentTypes from '@theme-original/NavbarItem/ComponentTypes';
import ProjectSwitcher from '@site/src/components/ProjectSwitcher';

// Register the project-switcher as a custom navbar item type. Used in
// docusaurus.config.ts as {type: 'custom-projectSwitcher'} so the dropdown
// LABEL can reflect the active project (CLI / Concepts) rather than a
// static "Docs".
export default {
  ...ComponentTypes,
  'custom-projectSwitcher': ProjectSwitcher,
};
