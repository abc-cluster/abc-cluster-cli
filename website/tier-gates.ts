/**
 * Feature / tier / deployment gates for the docs.
 *
 * One structure, many deployments. The same docs tree is built for every
 * tier; which pages appear is gated here, driven by env so a deployment or
 * a dev/release cycle can widen/narrow the surface WITHOUT editing content.
 *
 * Resolution (most specific wins, all merged into one hide-set):
 *   1. TIER_HIDE[ABC_TIER]      — the tier's baseline hidden pages
 *   2. ABC_DOCS_HIDE            — extra comma-separated globs (per-deploy)
 *   3. ABC_DOCS_SHOW            — globs to force-show (un-hide), wins last
 *
 * `ABC_TIER` unset → tier `all` → hide nothing → identical to today, so
 * the base config / aither deploy is unaffected. Only the tier build
 * (docusaurus.tier.config.ts) and any deploy that sets ABC_TIER changes.
 *
 * Glob support is deliberately tiny (no minimatch dependency): an exact
 * doc id, or a trailing `*` prefix match. e.g. `reference/admin*` matches
 * `reference/admin` and `reference/admin-tools`.
 */

export type Tier = 'all' | 'seedling' | 'grove' | 'cloud';

/**
 * Per-tier baseline of HIDDEN doc ids (Docusaurus doc `id`, e.g.
 * `reference/infra`). Hide-list, not allow-list: the safe default is
 * "show everything", and a tier opts pages OUT. Edit these lists to
 * curate a tier; or override per deployment via ABC_DOCS_HIDE/SHOW.
 *
 * seedling is intentionally empty by default — the seedling CLI
 * reference subset is a product decision. Example of what a trimmed
 * seedling might hide (operator/infra surface a single-lab researcher
 * does not need) is in the commented block; uncomment/adjust to taste.
 */
export const TIER_HIDE: Record<Tier, string[]> = {
  all: [],
  seedling: [
    // --- example trim (commented; this is the knob) -----------------
    // 'reference/infra',        // cluster provisioning = Pulumi, not CLI
    // 'reference/cluster',      // fleet ops — operator tier
    // 'reference/admin*',       // admin services + admin tools
    // 'reference/abc-accounting',
    // ----------------------------------------------------------------
  ],
  grove: [],
  cloud: [],
};

function envList(name: string): string[] {
  return (process.env[name] || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

export function resolveTier(): Tier {
  const t = (process.env.ABC_TIER || 'all') as Tier;
  return (['all', 'seedling', 'grove', 'cloud'] as const).includes(t)
    ? t
    : 'all';
}

/** The effective hidden-glob set for the current env. */
export function resolveHidden(): string[] {
  const tier = resolveTier();
  const hide = new Set<string>([
    ...TIER_HIDE[tier],
    ...envList('ABC_DOCS_HIDE'),
  ]);
  for (const show of envList('ABC_DOCS_SHOW')) hide.delete(show);
  return [...hide];
}

/** Does doc `id` match any hide glob? */
export function isHidden(id: string, hidden: string[]): boolean {
  return hidden.some((g) =>
    g.endsWith('*') ? id.startsWith(g.slice(0, -1)) : id === g,
  );
}

/** Doc-id globs → docs-preset `exclude` file globs (so hidden pages
 *  are not even built — no orphan HTML, sidebar + build stay in sync). */
export function hiddenToExcludeGlobs(hidden: string[]): string[] {
  return hidden.map((g) =>
    g.endsWith('*') ? `${g.slice(0, -1)}*.{md,mdx}` : `${g}.{md,mdx}`,
  );
}
