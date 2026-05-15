// Docusaurus pipes .svg imports through SVGR — default export is a
// React component, named export `url` is the asset URL. Mirrors the
// shape Docusaurus's own examples assume.
declare module '*.svg' {
  import type {ComponentType, SVGProps} from 'react';
  const ReactComponent: ComponentType<SVGProps<SVGSVGElement>>;
  export default ReactComponent;
  export const url: string;
}
