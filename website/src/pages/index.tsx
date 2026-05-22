/**
 * abc-cluster docs hub — project picker.
 *
 * The root of the shared docs site (/ → /docs/ on the tier build). Renders
 * the canonical project registry from abc-site-kit/projects as cards. Each
 * card links to a project's docs base path. Adding a project is a one-line
 * change in abc-site-kit/projects — this page and the navbar switcher both
 * pick it up automatically.
 *
 * Supersedes the legacy "aither gateway" landing that used to own this
 * route; that gateway is no longer part of the docs build.
 */

import Layout from '@theme/Layout';
import Link from '@docusaurus/Link';
import {PROJECTS} from 'abc-site-kit/projects';

export default function Hub(): React.ReactElement {
  return (
    <Layout
      title="Documentation"
      description="abc-cluster documentation — pick a project.">
      <main
        style={{
          maxWidth: '960px',
          margin: '0 auto',
          padding: '4rem 1.5rem 5rem',
        }}>
        <header style={{marginBottom: '2.5rem'}}>
          <h1 style={{marginBottom: '0.5rem'}}>Documentation</h1>
          <p
            style={{
              color: 'var(--abc-text-dim)',
              fontSize: '1.05rem',
              margin: 0,
            }}>
            Pick a project. Operator setup docs live under <strong>Cluster Setup</strong>; CLI reference under <strong>Cluster CLI</strong>.
          </p>
        </header>

        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))',
            gap: '1.25rem',
          }}>
          {PROJECTS.map((p) => (
            <Link
              key={p.id}
              to={p.path}
              className="abc-hub-card"
              style={{
                display: 'block',
                padding: '1.5rem',
                border: '1px solid var(--abc-rule)',
                borderRadius: '6px',
                background: 'var(--abc-bg-1)',
                color: 'inherit',
                textDecoration: 'none',
                transition: 'border-color 0.15s ease',
              }}>
              <h2
                style={{
                  fontSize: '1.25rem',
                  marginBottom: '0.5rem',
                  color: 'var(--abc-ink)',
                }}>
                {p.label}
              </h2>
              <p
                style={{
                  margin: 0,
                  fontSize: '0.95rem',
                  color: 'var(--abc-text-dim)',
                  lineHeight: 1.5,
                }}>
                {p.blurb}
              </p>
            </Link>
          ))}
        </div>
      </main>
    </Layout>
  );
}
