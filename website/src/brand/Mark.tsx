import type {SVGProps} from 'react';
import MarkSvg from './logo/mark.svg';

/**
 * `<Mark/>` — the abc-cluster trio-rings glyph (top-left navbar / brand area).
 * Renders via Docusaurus's built-in SVGR, so `currentColor` and the
 * `--abc-bg` CSS variable flow through from the surrounding DOM.
 */
export default function Mark(props: SVGProps<SVGSVGElement>) {
  return <MarkSvg width={28} height={28} {...props} />;
}
