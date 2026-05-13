/**
 * abc-cluster gateway — landing page.
 *
 * Native Docusaurus port of the canonical Caddy landing page that lives at
 * deployments/abc-nodes/caddy/landing/index.html. The two pages render the
 * same hero / install / do-next / CLI / services / URL-toggle structure;
 * the React port owns the docs site root (/) so the landing is part of the
 * docs build and deploy pipeline rather than a static-file copy hack.
 *
 * Notable porting decisions:
 *  - Layout is wrapped in <Layout noNavbar noFooter> to opt out of
 *    Docusaurus' chrome — the landing has its own topbar and foot row
 *    and the two would otherwise collide.
 *  - Network/format toggle, audience filter, search query, theme, and
 *    visual system all live as useState — initial state is mirrored from
 *    window.location.hostname inside a useEffect so SSR doesn't break.
 *  - The hero SVG (Africa silhouette + A/B/C rings) is drawn imperatively
 *    into a <g> ref because the dot pattern depends on a runtime
 *    point-in-polygon test the simplest port preserves verbatim.
 *  - Theme attribute writes to document.documentElement.dataset.theme so
 *    it interoperates with Docusaurus' colorMode without colliding.
 */

import {useEffect, useMemo, useRef, useState} from 'react';
import Layout from '@theme/Layout';
import Head from '@docusaurus/Head';

import './landing.css';

// ── Network config — single source of truth for hostnames + IPs ────────────
const NETWORK_CONFIG = {
  domain: 'aither',
  lanHost: 'aither.mb.sun.ac.za',
  lanIP: '146.232.174.77',
  tsIP: '100.70.185.46',
} as const;

type NetworkSurface = 'tailscale' | 'lan';
type URLFormat = 'dns' | 'ip';
type ViewNetwork = 'auto' | NetworkSurface;
type Audience = 'all' | 'researcher' | 'operator';
type ServiceTier = 'featured' | 'mini';
type ModeOnly = 'domain' | 'subpath';

interface ServiceDef {
  name: string;
  audience: 'researcher' | 'operator';
  tier: ServiceTier;
  href: string;          // canonical *.aither href
  hostLabel: string;     // canonical *.aither host label shown in card foot
  desc: string;
  searchTerms: string;
  status: 'up' | 'degraded' | 'down';
  glyph: JSX.Element;    // SVG path
  modeOnly?: ModeOnly;   // hide outside this network mode
  noteSuffix?: string;   // optional inline em note appended to desc
}

const SERVICES: ServiceDef[] = [
  // Researcher — featured tile grid
  {
    name: 'nomad-ui', audience: 'researcher', tier: 'featured',
    href: 'http://nomad.aither/ui/', hostLabel: 'nomad.aither/ui',
    desc: 'Job scheduler — full UI. Submit, monitor, drain, and re-run workloads; manage tokens.',
    searchTerms: 'job scheduler full ui allocations submit monitor', status: 'up',
    glyph: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
        <rect x="3" y="3" width="8" height="8" rx="1"/><rect x="13" y="3" width="8" height="8" rx="1"/>
        <rect x="3" y="13" width="8" height="8" rx="1"/><rect x="13" y="13" width="8" height="8" rx="1"/>
      </svg>
    ),
  },
  {
    name: 'uppy', audience: 'researcher', tier: 'featured',
    href: 'http://uppy.aither/', hostLabel: 'uppy.aither',
    desc: 'Browser-based, resumable file upload. tusd backend; chunks straight into RustFS.',
    searchTerms: 'browser file upload tusd resumable', status: 'up',
    glyph: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
        <path d="M12 16V4"/><path d="M7 9l5-5 5 5"/><path d="M4 17v3h16v-3"/>
      </svg>
    ),
  },
  {
    name: 'rustfs', audience: 'researcher', tier: 'featured',
    href: 'http://rustfs.aither/rustfs/console/', hostLabel: 'rustfs.aither/rustfs/console',
    desc: 'S3 object storage — browse buckets, manage policies, share files with users.',
    searchTerms: 's3 object storage buckets files rustfs', status: 'up',
    glyph: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
        <ellipse cx="12" cy="6" rx="8" ry="2.5"/><path d="M4 6v6c0 1.4 3.6 2.5 8 2.5s8-1.1 8-2.5V6"/>
        <path d="M4 12v6c0 1.4 3.6 2.5 8 2.5s8-1.1 8-2.5v-6"/>
      </svg>
    ),
  },
  {
    name: 'ntfy', audience: 'researcher', tier: 'featured',
    href: 'http://ntfy.aither/', hostLabel: 'ntfy.aither',
    desc: 'Job event notifications — subscribe to topics, receive push alerts when batch runs finish or fail.',
    noteSuffix: '(Tailscale only — generated links use the *.aither origin.)',
    searchTerms: 'job event notifications push pub sub topic subscribe', status: 'up',
    modeOnly: 'domain',
    glyph: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
        <path d="M18 16v-5a6 6 0 0 0-12 0v5l-2 2h16z"/><path d="M10 20a2 2 0 0 0 4 0"/>
      </svg>
    ),
  },

  // Operator — featured tile grid
  {
    name: 'grafana', audience: 'operator', tier: 'featured',
    href: 'http://grafana.aither/', hostLabel: 'grafana.aither',
    desc: 'Dashboards — metrics, logs, traces. Prometheus + Loki data sources wired in.',
    searchTerms: 'dashboards metrics logs observability', status: 'up',
    glyph: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
        <path d="M3 18l5-7 4 4 4-6 5 5"/><line x1="3" y1="20" x2="21" y2="20"/>
      </svg>
    ),
  },
  {
    name: 'traefik', audience: 'operator', tier: 'featured',
    href: 'http://traefik.aither/dashboard/', hostLabel: 'traefik.aither/dashboard',
    desc: 'Load balancer & service catalog. Live routing table from Consul.',
    searchTerms: 'load balancer service catalog routing', status: 'up',
    glyph: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
        <path d="M12 4v6"/><path d="M6 14l6-4 6 4"/>
        <circle cx="6" cy="18" r="2"/><circle cx="12" cy="18" r="2"/><circle cx="18" cy="18" r="2"/>
      </svg>
    ),
  },
  {
    name: 'garage', audience: 'operator', tier: 'featured',
    href: 'http://garage-webui.aither/', hostLabel: 'garage-webui.aither',
    desc: 'Long-term archive + cluster backups. zstd compression, block-level dedup, geo-replicable. Restic repo lives here.',
    searchTerms: 'garage long-term archive backups compression dedup s3', status: 'up',
    modeOnly: 'domain',
    glyph: (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
        <path d="M3 11l9-7 9 7"/><path d="M5 10v10h14V10"/><path d="M9 20v-6h6v6"/>
      </svg>
    ),
  },

  // Operator — compact mini list
  { name: 'prometheus', audience: 'operator', tier: 'mini',
    href: 'http://prometheus.aither/', hostLabel: 'prometheus.aither',
    desc: 'metrics store & query', searchTerms: 'metrics store query tsdb', status: 'up', glyph: <></> },
  { name: 'loki', audience: 'operator', tier: 'mini',
    href: 'http://loki.aither/ready', hostLabel: 'loki.aither/ready',
    desc: 'log aggregation (API)', searchTerms: 'log aggregation api', status: 'up', glyph: <></> },
  { name: 'alloy', audience: 'operator', tier: 'mini',
    href: 'http://alloy.aither/', hostLabel: 'alloy.aither',
    desc: 'telemetry collector UI', searchTerms: 'telemetry collector ui', status: 'up', glyph: <></> },
  { name: 'consul', audience: 'operator', tier: 'mini',
    href: 'http://consul.aither/ui/', hostLabel: 'consul.aither/ui',
    desc: 'service discovery & health', searchTerms: 'service discovery health catalog', status: 'up', glyph: <></> },
  { name: 'vault', audience: 'operator', tier: 'mini',
    href: 'http://vault.aither/ui/', hostLabel: 'vault.aither/ui',
    desc: 'secrets & SSH CA', searchTerms: 'secrets ssh certificate authority pki', status: 'up', glyph: <></> },
  { name: 'boundary', audience: 'operator', tier: 'mini',
    href: 'http://boundary.aither/', hostLabel: 'boundary.aither',
    desc: 'SSH session broker', searchTerms: 'ssh session broker access node', status: 'up', glyph: <></> },
];

// A-word rotator suffixes. Drop a new entry here to add another rotating
// word. Keep the leading "A" implicit — only the suffix goes in the array
// (e.g. "ccessible" renders as "Accessible"). The animation auto-sizes
// widths and cycles every 2.2s.
//
// Keep in sync with `<span class="ac-track">` in the apex landing at
// abc-deployments/abc-cloud-prod/landing/index.html.
const ACRONYM_WORDS = ['frican', 'wesome', 'utomated', 'ffordable', 'daptable', 'vailable'];

// ── URL helpers ────────────────────────────────────────────────────────────
function detectInitialState(): {network: NetworkSurface; format: URLFormat} {
  if (typeof window === 'undefined') return {network: 'lan', format: 'dns'};
  const h = window.location.hostname;
  if (/\.aither$/.test(h)) return {network: 'tailscale', format: 'dns'};
  if (h === NETWORK_CONFIG.tsIP) return {network: 'tailscale', format: 'ip'};
  if (h === NETWORK_CONFIG.lanIP) return {network: 'lan', format: 'ip'};
  return {network: 'lan', format: 'dns'};
}

function subpathHost(network: NetworkSurface, format: URLFormat): string {
  if (network === 'tailscale') return NETWORK_CONFIG.tsIP;
  return format === 'ip' ? NETWORK_CONFIG.lanIP : NETWORK_CONFIG.lanHost;
}

function isDomainMode(network: NetworkSurface, format: URLFormat): boolean {
  return network === 'tailscale' && format === 'dns';
}

// Rewrite a canonical *.aither href into subpath form for the given host.
// Services whose path already contains the svc segment (nomad, docs, rustfs)
// are kept as-is to avoid doubling the prefix.
function toSubpathHref(origHref: string, host: string): string {
  return origHref.replace(/^http:\/\/([^./]+)\.aither(\/[^]*)?$/, (_m, svc: string, path?: string) => {
    if (svc === 'nomad' || svc === 'docs' || svc === 'rustfs') {
      return 'http://' + host + (path || '/');
    }
    return 'http://' + host + '/' + svc + (path || '/');
  });
}

function toSubpathLabel(origText: string, host: string): string {
  return origText.replace(/^([^./]+)\.aither(\/[^]*)?$/, (_m, svc: string, path?: string) => {
    if (svc === 'nomad') return host + (path || '/ui');
    if (svc === 'docs') return host + (path || '/docs');
    if (svc === 'rustfs') return host + (path || '/');
    return host + '/' + svc + (path || '');
  });
}

function applyNetworkToHref(orig: string, network: NetworkSurface, format: URLFormat): string {
  if (!orig.includes('.aither')) return orig;
  if (isDomainMode(network, format)) return orig;
  return toSubpathHref(orig, subpathHost(network, format));
}

function applyNetworkToLabel(orig: string, network: NetworkSurface, format: URLFormat): string {
  if (!orig.includes('.aither')) return orig;
  if (isDomainMode(network, format)) return orig;
  return toSubpathLabel(orig, subpathHost(network, format));
}

// ── Hero SVG: Africa silhouette + A/B/C glyphs ─────────────────────────────
const AFRICA_PATH =
  'M -18 -78 L 18 -76 L 34 -64 L 40 -48 L 48 -30 L 60 -18 L 52 -6 L 46 14 ' +
  'L 40 36 L 28 58 L 10 74 L -6 78 L -22 72 L -30 52 L -28 32 L -20 12 ' +
  'L -14 2 L -32 -2 L -50 -4 L -58 -18 L -54 -36 L -40 -52 L -32 -68 Z';
const AFRICA_POLY: ReadonlyArray<readonly [number, number]> = [
  [-18,-78],[18,-76],[34,-64],[40,-48],[48,-30],[60,-18],[52,-6],[46,14],
  [40,36],[28,58],[10,74],[-6,78],[-22,72],[-30,52],[-28,32],[-20,12],
  [-14,2],[-32,-2],[-50,-4],[-58,-18],[-54,-36],[-40,-52],[-32,-68],
];

function isInsideAfrica(px: number, py: number): boolean {
  let inside = false;
  for (let i = 0, j = AFRICA_POLY.length - 1; i < AFRICA_POLY.length; j = i++) {
    const [xi, yi] = AFRICA_POLY[i];
    const [xj, yj] = AFRICA_POLY[j];
    const intersect = ((yi > py) !== (yj > py)) && (px < (xj - xi) * (py - yi) / (yj - yi) + xi);
    if (intersect) inside = !inside;
  }
  return inside;
}

const SVG_NS = 'http://www.w3.org/2000/svg';
function svgEl(tag: string, attrs: Record<string, string | number>): SVGElement {
  const n = document.createElementNS(SVG_NS, tag);
  for (const k in attrs) n.setAttribute(k, String(attrs[k]));
  return n;
}

function drawHeroMark(g: SVGGElement, system: 'grid' | 'brush'): void {
  while (g.firstChild) g.removeChild(g.firstChild);
  if (system === 'brush') {
    g.appendChild(svgEl('path', {
      d: AFRICA_PATH, fill: 'none', stroke: 'url(#hero-brush-grad)',
      'stroke-width': 4.2, 'stroke-linejoin': 'round', 'stroke-linecap': 'round',
    }));
    ([['A',-32,98],['B',0,98],['C',32,98]] as const).forEach(([letter, cx, cy]) => {
      g.appendChild(svgEl('circle', {
        cx, cy, r: 9, fill: 'var(--abc-bg)', stroke: 'var(--abc-ink)', 'stroke-width': 1.6,
      }));
      const t = svgEl('text', {
        x: cx, y: cy + 3.5, 'text-anchor': 'middle',
        'font-family': 'JetBrains Mono', 'font-size': 9, 'font-weight': 700, fill: 'var(--abc-ink)',
      });
      t.textContent = letter;
      g.appendChild(t);
    });
  } else {
    g.appendChild(svgEl('path', {
      d: AFRICA_PATH, fill: 'none', stroke: 'var(--ink-dim)', 'stroke-width': 0.8, opacity: 0.5,
    }));
    for (let gy = -78; gy <= 78; gy += 6) {
      for (let gx = -60; gx <= 60; gx += 6) {
        if (isInsideAfrica(gx, gy)) {
          g.appendChild(svgEl('circle', {cx: gx, cy: gy, r: 1.3, fill: 'var(--abc-ink)', opacity: 0.7}));
        }
      }
    }
    ([['A',-32,20],['B',0,30],['C',32,20]] as const).forEach(([letter, cx, cy]) => {
      g.appendChild(svgEl('circle', {
        cx, cy, r: 9, fill: 'var(--abc-bg)', stroke: 'var(--abc-accent)', 'stroke-width': 1.4,
      }));
      const t = svgEl('text', {
        x: cx, y: cy + 3, 'text-anchor': 'middle',
        'font-family': 'JetBrains Mono', 'font-size': 9, 'font-weight': 700, fill: 'var(--abc-accent)',
      });
      t.textContent = letter;
      g.appendChild(t);
    });
  }
}

// ── Copy button ────────────────────────────────────────────────────────────
async function copyText(txt: string): Promise<void> {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try { await navigator.clipboard.writeText(txt); return; } catch {}
  }
  if (typeof document === 'undefined') return;
  const ta = document.createElement('textarea');
  ta.value = txt;
  ta.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none;';
  document.body.appendChild(ta);
  ta.focus();
  ta.select();
  try { document.execCommand('copy'); } finally { document.body.removeChild(ta); }
}

interface CopyButtonProps { label: string; getText: () => string }
function CopyButton({label, getText}: CopyButtonProps) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      className={'cmd-copy' + (copied ? ' copied' : '')}
      type="button"
      onClick={async () => {
        await copyText(getText());
        setCopied(true);
        setTimeout(() => setCopied(false), 1400);
      }}
    >
      {copied ? 'Copied' : label}
    </button>
  );
}

// ── Main page ──────────────────────────────────────────────────────────────
export default function Home(): JSX.Element {
  const detected = useMemo(detectInitialState, []);
  // Docusaurus' colorMode owns data-theme on <html>; the landing CSS keys
  // entirely off that attribute. The brush-system variant was dropped to
  // avoid a hydration race where --bg was undefined until the JS effect
  // ran — the page now resolves to the right colours from first paint.
  const [viewNetwork, setViewNetwork] = useState<ViewNetwork>('auto');
  const [network, setNetwork] = useState<NetworkSurface>(detected.network);
  const [format, setFormat] = useState<URLFormat>(detected.format);
  const [audience, setAudience] = useState<Audience>('all');
  const [query, setQuery] = useState<string>('');

  const heroGRef = useRef<SVGGElement | null>(null);
  const acRotatorRef = useRef<HTMLSpanElement | null>(null);
  const acTrackRef = useRef<HTMLSpanElement | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);

  const dom = isDomainMode(network, format);
  const subHost = subpathHost(network, format);
  const lanHost = NETWORK_CONFIG.lanHost;
  const tsIP = NETWORK_CONFIG.tsIP;
  const domain = NETWORK_CONFIG.domain;

  // No useEffect for system — landing.css keys off data-theme alone now,
  // so the variables resolve from the server-rendered HTML's first paint.

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName;
      const editable = target?.isContentEditable;
      if (e.key === '/' && document.activeElement !== searchRef.current && !e.metaKey && !e.ctrlKey) {
        if (tag === 'INPUT' || tag === 'TEXTAREA' || editable) return;
        e.preventDefault();
        searchRef.current?.focus();
      } else if (e.key === 'Escape' && document.activeElement === searchRef.current) {
        setQuery('');
        searchRef.current?.blur();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  // ── Hero SVG draw — runs once on mount. The dot/ring colours come
  // from CSS variables, so theme changes pick up without a redraw.
  useEffect(() => {
    if (!heroGRef.current) return;
    drawHeroMark(heroGRef.current, 'grid');
  }, []);

  // ── Acronym rotator: cycles through ACRONYM_WORDS every 2.2s ────────────
  useEffect(() => {
    const track = acTrackRef.current;
    const rotator = acRotatorRef.current;
    if (!track || !rotator) return;
    const words = Array.from(track.children) as HTMLSpanElement[];
    if (!words.length) return;
    const lineH = parseFloat(getComputedStyle(track).lineHeight)
      || parseFloat(getComputedStyle(track).fontSize) * 1.18;
    let i = 0;
    function tick() {
      const widths = words.map((w) => Math.ceil(w.getBoundingClientRect().width) + 2);
      words.forEach((w, n) => w.classList.toggle('live', n === i));
      rotator.style.setProperty('--w', widths[i] + 'px');
      track.style.transform = 'translateY(' + (-i * lineH) + 'px)';
      i = (i + 1) % words.length;
    }
    tick();
    const id = window.setInterval(tick, 2200);
    return () => window.clearInterval(id);
  }, []);

  // ── Network toggle handlers ─────────────────────────────────────────────
  function handleNetwork(n: ViewNetwork) {
    setViewNetwork(n);
    setNetwork(n === 'auto' ? detected.network : n);
  }

  // ── Service filter — client-side, single pass ───────────────────────────
  const visibleServices = useMemo(() => {
    const q = query.trim().toLowerCase();
    const netMode: ModeOnly = network === 'tailscale' ? 'domain' : 'subpath';
    return SERVICES.filter((s) => {
      if (s.modeOnly && s.modeOnly !== netMode) return false;
      if (audience !== 'all' && s.audience !== audience) return false;
      if (!q) return true;
      return s.name.toLowerCase().includes(q) || s.searchTerms.toLowerCase().includes(q);
    });
  }, [query, audience, network]);

  const visibleResearcher = visibleServices.filter((s) => s.audience === 'researcher');
  const visibleOperator = visibleServices.filter((s) => s.audience === 'operator');
  const researcherFeatured = visibleResearcher.filter((s) => s.tier === 'featured');
  const operatorFeatured = visibleOperator.filter((s) => s.tier === 'featured');
  const operatorMini = visibleOperator.filter((s) => s.tier === 'mini');

  const fmtLabel = dom ? '*.' + domain : subHost + '/<service>';
  const netLabel = network === 'tailscale' ? 'Tailscale' : 'LAN';
  const autoNote = viewNetwork === 'auto' ? ' (auto-detected from this hostname)' : '';

  // ── Install commands (click-to-copy) ────────────────────────────────────
  const installSystem =
    `curl -fsSL -H "Accept: application/vnd.github.raw+json" \\
    "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" \\
  | sh -s -- --sudo`;
  const installUser =
    `curl -fsSL -H "Accept: application/vnd.github.raw+json" \\
    "https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main" \\
  | sh -s --
export PATH="$PWD:$PATH"`;
  const firstRun = `# 1. bootstrap a placeholder config
abc config init

# 2. drop in the credentials you were handed (your name, token, bucket)
cp ~/Downloads/<your-name>.yaml ~/.abc/config.yaml
abc context show

# 3. verify — randomised stress-ng across CPU / VM / I/O
abc job run hello-abc
abc job run hello-abc --sleep=120s`;

  return (
    <Layout noFooter title="abc-cluster · gateway"
            description="African Bioinformatics Computing Cluster — install the abc CLI, explore services, submit jobs.">
      <Head>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        <link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600;700&display=swap" />
      </Head>

      <div className="abc-landing">

        {/* No custom topbar — Docusaurus' navbar (configured in
            docusaurus.config.ts) handles brand, docs link, GitHub link,
            cluster link, releases link, and the theme toggle. Having a
            second header underneath would just duplicate the chrome. */}

        <main className="page" id="top">

          {/* ── Hero ─────────────────────────────────────────────────────── */}
          <section className="hero">
            <div className="hero-headline">
              <div className="hero-mark" aria-label="abc-cluster mark">
                <svg viewBox="0 0 200 200" aria-hidden="true">
                  {/* Brush gradient was for an unused alternate visual system;
                      removed when the system constant was dropped. */}
                  <g transform="translate(100 85)" ref={heroGRef} />
                  <g>
                    <text x="100" y="178" textAnchor="middle"
                          style={{font: "600 13px 'JetBrains Mono',monospace", fill: 'var(--abc-ink)', letterSpacing: '-0.01em'}}>abc-cluster</text>
                    <text x="100" y="192" textAnchor="middle"
                          style={{font: "500 6px 'JetBrains Mono',monospace", fill: 'var(--abc-text-dim)', letterSpacing: '0.22em'}}>OPEN · SOVEREIGN · FEDERATED · COMPUTING</text>
                  </g>
                </svg>
              </div>
              <div className="hero-headline-text">
                <div className="eyebrow">abc-cluster <span className="dot">·</span> gateway</div>
                <h1 className="acronym-h1" aria-label="African Bioinformatics Computing Cluster">
                  <span className="acronym-line">
                    <span className="ac ac-a">
                      <span className="ac-letter">A</span>
                      <span className="ac-rotator" ref={acRotatorRef}>
                        <span className="ac-track" ref={acTrackRef}>
                          {ACRONYM_WORDS.map((w) => (<span key={w}>{w}</span>))}
                        </span>
                      </span>
                    </span>
                    <span className="ac"><span className="ac-letter">B</span><span className="ac-rest">ioinformatics</span></span>
                    <span className="ac"><span className="ac-letter">C</span><span className="ac-rest">omputing</span></span>
                    <span className="ac-rest">Cluster</span>
                  </span>
                  <span className="acronym-sub">a.k.a. <i>Abhi&apos;s Baby Cluster</i></span>
                </h1>
              </div>
            </div>

            <div className="hero-cols">
              <div className="hero-body">
                <p className="lede">
                  The <code>abc</code> CLI is the single entrypoint to the cluster and it works on Linux, macOS, and Windows machines.{' '}
                  <b>Researchers</b> use it to run job scripts, execute pipelines, and conduct data operations.{' '}
                  <b>Operators</b> use it to manage the underlying cluster.
                </p>
                <div className="install-row">
                  <div className="cmd">
                    <div className="cmd-head">
                      <div className="cmd-title">install · system-wide<span className="path">/usr/local/bin/abc</span></div>
                      <CopyButton label="Copy" getText={() => installSystem} />
                    </div>
                    <pre>
                      <span className="prompt">$ </span>curl <span className="flag">-fsSL</span> <span className="flag">-H</span> <span className="str">&quot;Accept: application/vnd.github.raw+json&quot;</span> <span className="cont">\</span>{'\n'}
                      {'    '}<span className="str">&quot;https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main&quot;</span> <span className="cont">\</span>{'\n'}
                      {'  '}| sh <span className="flag">-s</span> -- <span className="flag">--sudo</span>
                    </pre>
                  </div>
                  <div className="cmd">
                    <div className="cmd-head">
                      <div className="cmd-title">install · current directory<span className="path">$PWD/abc</span></div>
                      <CopyButton label="Copy" getText={() => installUser} />
                    </div>
                    <pre>
                      <span className="prompt">$ </span>curl <span className="flag">-fsSL</span> <span className="flag">-H</span> <span className="str">&quot;Accept: application/vnd.github.raw+json&quot;</span> <span className="cont">\</span>{'\n'}
                      {'    '}<span className="str">&quot;https://api.github.com/repos/abc-cluster/abc-cluster-cli/contents/scripts/install-abc.sh?ref=main&quot;</span> <span className="cont">\</span>{'\n'}
                      {'  '}| sh <span className="flag">-s</span> --{'\n'}
                      <span className="prompt">$ </span>export PATH=<span className="str">&quot;$PWD:$PATH&quot;</span>
                    </pre>
                  </div>
                </div>
                <p className="post-install">After install, verify with <span className="kbd">abc --version</span>.</p>
              </div>
            </div>
          </section>

          {/* ── Do-next ──────────────────────────────────────────────────── */}
          <section className="do-next" id="do-next" aria-labelledby="do-next-title">
            <div className="page" style={{maxWidth: 'none', padding: 0}}>
              <div className="do-next-head">
                <div>
                  <div className="do-next-eyebrow"><span className="dot" />After install · what you can do</div>
                  <h2 className="do-next-title" id="do-next-title">Three consoles cover 90% of day-to-day work.</h2>
                  <p className="do-next-sub">Click straight into the web UIs. Or drive everything from the <code>abc</code> CLI you just installed — see the quick-start below.</p>
                </div>
                <div className="do-next-meta">
                  <span className="kbd">abc&nbsp;--help</span>
                  <span>for the full surface</span>
                </div>
              </div>

              <div className="do-grid">
                <a className="do-card" href={applyNetworkToHref('http://nomad.aither/ui/jobs', network, format)} target="_blank" rel="noreferrer">
                  <div className="do-card-head">
                    <span className="do-step">01</span>
                    <span className="do-glyph" aria-hidden="true">
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
                        <path d="M3 12h4l2-6 4 12 2-6h6"/>
                      </svg>
                    </span>
                  </div>
                  <div className="do-name">Monitor Jobs <span className="arrow">→</span></div>
                  <p className="do-desc">Submit batches, track allocations, tail logs, and re-run failures. Live status from the Nomad scheduler.</p>
                </a>

                <a className="do-card" href={applyNetworkToHref('http://uppy.aither/', network, format)} target="_blank" rel="noreferrer">
                  <div className="do-card-head">
                    <span className="do-step">02</span>
                    <span className="do-glyph" aria-hidden="true">
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
                        <path d="M12 16V4"/><path d="M7 9l5-5 5 5"/><path d="M4 17v3h16v-3"/>
                      </svg>
                    </span>
                  </div>
                  <div className="do-name">Upload Data <span className="arrow">→</span></div>
                  <p className="do-desc">Resumable, chunked transfers straight into your bucket. Works for terabyte-scale FASTQs.</p>
                </a>

                <a className="do-card" href={applyNetworkToHref('http://rustfs.aither/rustfs/console/', network, format)} target="_blank" rel="noreferrer">
                  <div className="do-card-head">
                    <span className="do-step">03</span>
                    <span className="do-glyph" aria-hidden="true">
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round">
                        <ellipse cx="12" cy="6" rx="8" ry="2.5"/><path d="M4 6v12c0 1.4 3.6 2.5 8 2.5s8-1.1 8-2.5V6"/>
                        <path d="M4 12c0 1.4 3.6 2.5 8 2.5s8-1.1 8-2.5"/>
                      </svg>
                    </span>
                  </div>
                  <div className="do-name">Browse Data <span className="arrow">→</span></div>
                  <p className="do-desc">List buckets, preview objects, and grab presigned URLs to share results across the lab.</p>
                </a>
              </div>
            </div>
          </section>

          {/* ── First-run CLI walkthrough ───────────────────────────────── */}
          <section className="block" id="cli">
            <div className="block-head">
              <div className="block-eyebrow"><span className="num">02</span>CLI</div>
              <div>
                <h2 className="block-title">First run — three steps</h2>
                <p className="block-desc">If you just installed <code>abc</code>, walk through this once. You need a credentials file from your workspace lead (handed out at the talk, or pasted from a DM); everything else is on this page.</p>
              </div>
            </div>
            <div className="block-body">
              <div />
              <div className="cmd">
                <div className="cmd-head">
                  <div className="cmd-title">first run<span className="path">~/.abc/config.yaml</span></div>
                  <CopyButton label="Copy" getText={() => firstRun} />
                </div>
                <pre>
                  <span className="comment"># 1. bootstrap a placeholder config</span>{'\n'}
                  <span className="prompt">$ </span>abc config init{'\n'}{'\n'}
                  <span className="comment"># 2. drop in the credentials you were handed (your name, token, bucket)</span>{'\n'}
                  <span className="prompt">$ </span>cp ~/Downloads/<span className="str">&lt;your-name&gt;.yaml</span> ~/.abc/config.yaml{'\n'}
                  <span className="prompt">$ </span>abc context show{'\n'}{'\n'}
                  <span className="comment"># 3. verify — randomised stress-ng across CPU / VM / I/O</span>{'\n'}
                  <span className="prompt">$ </span>abc job run hello-abc{'\n'}
                  <span className="prompt">$ </span>abc job run hello-abc <span className="flag">--sleep=120s</span>
                </pre>
              </div>
            </div>
          </section>

          {/* ── Services ──────────────────────────────────────────────────── */}
          <section className="block" id="services">
            <div className="block-head">
              <div className="block-eyebrow"><span className="num">03</span>Services</div>
              <div>
                <h2 className="block-title">Consoles &amp; endpoints</h2>
                <p className="block-desc">
                  {dom ? (
                    <span>All consoles resolve over <code>*.{domain}</code> via Tailscale split-DNS.</span>
                  ) : (
                    <span>All consoles are accessible at subpaths of <code>{lanHost}</code> — no split-DNS required.</span>
                  )}
                  {' '}Featured cards are the daily drivers; the compact list below covers everything else.
                </p>
              </div>
            </div>

            <div className="svc-pivot" id="svc-pivot">
              <div className="seg">
                {(['all', 'researcher', 'operator'] as const).map((a) => (
                  <button key={a} className={a === audience ? 'active' : ''}
                          type="button" onClick={() => setAudience(a)}>
                    {a[0].toUpperCase() + a.slice(1)}
                  </button>
                ))}
              </div>
              <label className="svc-search">
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <circle cx="11" cy="11" r="6.5" fill="none" stroke="currentColor" strokeWidth="1.6"/>
                  <line x1="16" y1="16" x2="21" y2="21" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round"/>
                </svg>
                <input
                  ref={searchRef}
                  type="search"
                  placeholder="Filter by name or description…"
                  autoComplete="off"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                />
                <kbd>/</kbd>
              </label>
              <div className="count">
                {visibleServices.length}{visibleServices.length === 1 ? ' service' : ' services'}
              </div>
            </div>

            {visibleServices.length === 0 ? (
              <div className="svc-empty">
                No services match <b>{query ? `“${query}”` : 'your filter'}</b>. Try a different term.
              </div>
            ) : (
              <>
                {researcherFeatured.length > 0 && (
                  <div className="svc-section">
                    <div className="svc-section-head">
                      <div className="svc-section-title">
                        Researcher
                        <span className="pill">run jobs · move data</span>
                      </div>
                      <div className="svc-section-meta">primary</div>
                    </div>
                    <div className="svc-grid">
                      {researcherFeatured.map((s) => (
                        <ServiceCard key={s.name} svc={s} network={network} format={format} />
                      ))}
                    </div>
                  </div>
                )}

                {(operatorFeatured.length > 0 || operatorMini.length > 0) && (
                  <div className="svc-section">
                    <div className="svc-section-head">
                      <div className="svc-section-title">
                        Operator
                        <span className="pill">platform &amp; control plane</span>
                      </div>
                      <div className="svc-section-meta">primary</div>
                    </div>
                    {operatorFeatured.length > 0 && (
                      <div className="svc-grid">
                        {operatorFeatured.map((s) => (
                          <ServiceCard key={s.name} svc={s} network={network} format={format} />
                        ))}
                      </div>
                    )}
                    {operatorMini.length > 0 && (
                      <div className="svc-mini">
                        {operatorMini.map((s) => (
                          <a key={s.name} className="svc-mini-row"
                             href={applyNetworkToHref(s.href, network, format)}
                             target="_blank" rel="noreferrer">
                            <span className={'status-dot ' + s.status} />
                            <span>
                              <span className="name">{s.name}</span>
                              <span className="desc">{s.desc}</span>
                            </span>
                            <span className="svc-mini-host">
                              {applyNetworkToLabel(s.hostLabel, network, format)}
                            </span>
                          </a>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </>
            )}
          </section>

          {/* ── URL view toggle ──────────────────────────────────────────── */}
          <section className="block view-toggle-block">
            <div className="block-head">
              <div className="block-eyebrow"><span className="num">99</span>Preview</div>
              <div>
                <h2 className="block-title">URL view</h2>
                <p className="block-desc">
                  All cards above adapt to the network you&apos;re on. Use these toggles to preview
                  links for any surface — handy for testing without opening a second browser.
                </p>
              </div>
            </div>
            <div className="view-controls">
              <span className="view-toggle-label">Network</span>
              <div className="seg view-toggle" role="tablist" aria-label="Network surface">
                {(['auto', 'lan', 'tailscale'] as const).map((n) => (
                  <button key={n} role="tab"
                          className={n === viewNetwork ? 'active' : ''}
                          type="button" onClick={() => handleNetwork(n)}>
                    {n === 'auto' ? 'Auto' : n === 'lan' ? 'LAN' : 'Tailscale'}
                  </button>
                ))}
              </div>
              <div className="view-toggle-sep" />
              <span className="view-toggle-label">Format</span>
              <div className="seg view-toggle" role="tablist" aria-label="URL format">
                {(['dns', 'ip'] as const).map((f) => (
                  <button key={f} role="tab"
                          className={f === format ? 'active' : ''}
                          type="button" onClick={() => setFormat(f)}>
                    {f.toUpperCase()}
                  </button>
                ))}
              </div>
            </div>
            <p className="view-toggle-status">
              Showing <b>{netLabel} — {fmtLabel}</b>{autoNote}.
            </p>
          </section>

          {/* ── Foot / colophon ──────────────────────────────────────────── */}
          <footer className="foot">
            <div className="row">
              <div className="block-eyebrow">colophon</div>
              <div>
                <p style={{margin: '0 0 14px'}}>
                  Traffic enters via <b>{lanHost}</b> (LAN) and is proxied by Caddy
                  through Traefik to backend services on private / Tailscale addresses.
                  Service vhosts are at <code>{dom ? `*.${domain}` : `${subHost}/<service>`}</code> — add
                  {' '}<span className="mono-ip">{tsIP}</span> as a split-DNS nameserver for
                  {' '}<code>{dom ? `.${domain}` : '/<service>'}</code> in the Tailscale admin console to resolve them on any
                  tailnet device. <a href="/docs/">abc CLI docs →</a>
                </p>
                <div style={{color: 'var(--abc-text-mute)', fontSize: 11.5, lineHeight: 1.7, borderTop: '1px solid var(--abc-rule-soft)', paddingTop: 12}}>
                  <b style={{color: 'var(--abc-text-dim)'}}>Stack</b>
                  <span className="sep">·</span> Tailscale (overlay)
                  <span className="sep">→</span> Caddy (vhost / TLS)
                  <span className="sep">→</span> Traefik (Consul-catalog LB)
                  <span className="sep">→</span> Nomad services
                  <span className="sep">·</span> discovery via <b style={{color: 'var(--abc-text-dim)'}}>Consul</b>
                  <span className="sep">·</span> secrets &amp; SSH CA via <b style={{color: 'var(--abc-text-dim)'}}>Vault</b>
                  <span className="sep">·</span> SSH sessions via <b style={{color: 'var(--abc-text-dim)'}}>Boundary</b>
                </div>
              </div>
            </div>
          </footer>

        </main>
      </div>
    </Layout>
  );
}

// ── Service card subcomponent ──────────────────────────────────────────────
interface ServiceCardProps { svc: ServiceDef; network: NetworkSurface; format: URLFormat }
function ServiceCard({svc, network, format}: ServiceCardProps): JSX.Element {
  return (
    <a className="svc-card"
       href={applyNetworkToHref(svc.href, network, format)}
       target="_blank" rel="noreferrer">
      <div className="svc-card-head">
        <span className="svc-card-glyph">{svc.glyph}</span>
        <span className="svc-card-name">{svc.name} <span className="arrow">→</span></span>
      </div>
      <p className="svc-card-desc">
        {svc.desc}
        {svc.noteSuffix && <em style={{color: 'var(--abc-text-mute)'}}> {svc.noteSuffix}</em>}
      </p>
      <div className="svc-card-foot">
        <span className="svc-card-host">{applyNetworkToLabel(svc.hostLabel, network, format)}</span>
        <span className="svc-card-status"><span className={'status-dot ' + svc.status} />Up</span>
      </div>
    </a>
  );
}
