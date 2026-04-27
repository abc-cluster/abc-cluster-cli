// ---------------------------------------------------------------------------
// validate.ts — Eager validation of the workspace YAML spec.
//
// Runs once at the top of index.ts so problems surface before any Pulumi
// resource registration. Throwing here fails `pulumi preview` and `pulumi up`
// with a clear message; nothing partially provisioned.
// ---------------------------------------------------------------------------

import { WorkspacesSpec, WorkspaceSpec, OrgSpec, Role, MemberSpec } from "./types";
import { NAME_RE } from "./naming";

const VALID_ROLES: ReadonlySet<Role> = new Set<Role>(["group-admin", "group-member"]);

/** Normalise a member's role field into a deduped list of valid roles. */
export function memberRoles(spec: MemberSpec): Role[] {
  const raw = Array.isArray(spec.role) ? spec.role : [spec.role];
  const seen = new Set<Role>();
  for (const r of raw) {
    if (!VALID_ROLES.has(r)) {
      throw new SpecError(`member "${spec.name}" has invalid role "${r}" (allowed: group-admin, group-member)`);
    }
    seen.add(r);
  }
  if (seen.size === 0) {
    throw new SpecError(`member "${spec.name}" must declare at least one role`);
  }
  return Array.from(seen);
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const ISO_DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

export class SpecError extends Error {
  constructor(message: string) {
    super(`workspaces.yaml: ${message}`);
  }
}

export function validateSpec(spec: WorkspacesSpec): void {
  if (spec.version !== "v1") {
    throw new SpecError(`expected version: v1, got ${(spec as any)?.version}`);
  }
  if (!Array.isArray(spec.organizations) || spec.organizations.length === 0) {
    throw new SpecError(`organizations[] is required and must be non-empty`);
  }

  const seenWorkspaces = new Set<string>();
  const seenOrgs = new Set<string>();

  for (const org of spec.organizations) {
    validateOrg(org, seenOrgs);
    for (const ws of org.workspaces ?? []) {
      const fq = `${org.name}-${ws.name}`;
      if (seenWorkspaces.has(fq)) {
        throw new SpecError(`duplicate workspace ${fq}`);
      }
      seenWorkspaces.add(fq);
      validateWorkspace(org, ws, fq);
    }
  }
}

function validateOrg(org: OrgSpec, seen: Set<string>): void {
  if (!org.name || !NAME_RE.test(org.name)) {
    throw new SpecError(
      `org.name "${org.name}" must match ${NAME_RE} (lowercase alphanumerics + hyphens, 1–32 chars)`,
    );
  }
  if (seen.has(org.name)) {
    throw new SpecError(`duplicate org name "${org.name}"`);
  }
  seen.add(org.name);
  if (!Array.isArray(org.workspaces) || org.workspaces.length === 0) {
    throw new SpecError(`org "${org.name}" has no workspaces[]`);
  }
}

function validateWorkspace(org: OrgSpec, ws: WorkspaceSpec, fq: string): void {
  if (!ws.name || !NAME_RE.test(ws.name)) {
    throw new SpecError(`workspace.name "${ws.name}" in org "${org.name}" must match ${NAME_RE}`);
  }

  // Combined org-ws name length: Nomad namespace names cap at 128 but MinIO
  // users land in `<ns>_<member>` and IAM policy names like `ns-<ns>-user-<member>`.
  // 64 leaves comfortable headroom under MinIO's user-name limits.
  if (fq.length > 64) {
    throw new SpecError(`workspace "${fq}" full name exceeds 64 chars`);
  }

  const state = ws.state ?? "active";
  if (state === "deleted") return; // tombstone — no further checks

  if (ws.contact && !EMAIL_RE.test(ws.contact)) {
    throw new SpecError(`workspace "${fq}" contact "${ws.contact}" is not a valid email`);
  }

  // Members
  const memberNames = new Set<string>();
  let groupAdminCount = 0;
  for (const m of ws.members ?? []) {
    if (!m.name || !NAME_RE.test(m.name)) {
      throw new SpecError(`member.name "${m.name}" in "${fq}" must match ${NAME_RE}`);
    }
    if (memberNames.has(m.name)) {
      throw new SpecError(`duplicate member "${m.name}" in "${fq}"`);
    }
    memberNames.add(m.name);
    const roles = memberRoles(m); // throws SpecError on invalid role
    if (roles.includes("group-admin")) groupAdminCount++;
    if (m.email && !EMAIL_RE.test(m.email)) {
      throw new SpecError(`member "${m.name}" in "${fq}" email "${m.email}" is invalid`);
    }
  }
  if (state === "active" && groupAdminCount === 0) {
    throw new SpecError(`active workspace "${fq}" must have at least one member with role: group-admin`);
  }

  // Collaborators
  const collabNames = new Set<string>();
  for (const c of ws.collaborators ?? []) {
    if (!c.name || !NAME_RE.test(c.name)) {
      throw new SpecError(`collaborator.name "${c.name}" in "${fq}" must match ${NAME_RE}`);
    }
    if (collabNames.has(c.name)) {
      throw new SpecError(`duplicate collaborator "${c.name}" in "${fq}"`);
    }
    if (memberNames.has(c.name)) {
      throw new SpecError(`collaborator name "${c.name}" collides with a member name in "${fq}"`);
    }
    collabNames.add(c.name);
    if (!c.expiresAt || !ISO_DATE_RE.test(c.expiresAt)) {
      throw new SpecError(
        `collaborator "${c.name}" in "${fq}" expiresAt "${c.expiresAt}" must be ISO date YYYY-MM-DD`,
      );
    }
    const d = new Date(c.expiresAt + "T00:00:00Z");
    if (Number.isNaN(d.getTime())) {
      throw new SpecError(`collaborator "${c.name}" in "${fq}" expiresAt "${c.expiresAt}" is not a real date`);
    }
    if (c.email && !EMAIL_RE.test(c.email)) {
      throw new SpecError(`collaborator "${c.name}" in "${fq}" email "${c.email}" is invalid`);
    }
    if (c.role !== undefined && c.role !== "group-collaborator") {
      throw new SpecError(`collaborator "${c.name}" in "${fq}" role must be "group-collaborator"`);
    }
  }
}
