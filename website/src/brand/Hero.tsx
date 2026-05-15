import type {SVGProps} from 'react';
import HeroSvg from './logo/hero.svg';

/**
 * `<Hero/>` — the centerpiece mark used in landing heroes.
 * Africa silhouette + dot field + A/B/C nodes + wordmark.
 * Uses `--abc-*` CSS variables so it themes with the docs site.
 */
export default function Hero(props: SVGProps<SVGSVGElement>) {
  return <HeroSvg {...props} />;
}
