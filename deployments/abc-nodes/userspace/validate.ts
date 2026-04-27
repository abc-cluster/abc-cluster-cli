// ---------------------------------------------------------------------------
// validate.ts — Eager validation of the workspaces.yaml v2 spec.
//
// Runs once at the top of index.ts so problems surface before any Pulumi
// resource registration. Throwing here fails `pulumi preview` and `pulumi up`
// with a clear message; nothing partially provisioned.
// ---------------------------------------------------------------------------

import {
  WorkspacesSpec,
  WorkspaceSpec,
  OrgSpec,
  UserSpec,
  Role,
  WorkspaceMember,
  CollaboratorRef,
  ResolvedUser,
  ResolvedWorkspace,
  UserMembership,
} from "./types";
import { NAME_RE } from "./naming";

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const ISO_DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

const VALID_ROLES: ReadonlySet<Role> = new Set<Role>(["group-admin", "group-member"]);

export class SpecError extends Error {
  constructor(message: string) {
    super(`workspaces.yaml: ${message}`);
  }
}

/**
 * Parse + validate the spec, returning the resolved view used by the rest of
 * the program (one pass over the structure; no second walk needed).
 */
export interface Resolved {
  spec: WorkspacesSpec;
  workspaces: ResolvedWorkspace[];
  /** Keyed by user name. Includes users with zero memberships (will get no resources). */
  users: Map<string, ResolvedUser>;
}

export function validateSpec(spec: WorkspacesSpec): Resolved {
  if (spec.version !== "v2") {
    throw new SpecError(
      `expected version: v2, got ${(spec as any)?.version}. ` +
        `v1 schemas need migration — see workspaces.yaml header comments for the new layout.`,
    );
  }

  // ---- top-level users[] ----
  if (!Array.isArray(spec.users) || spec.users.length === 0) {
    throw new SpecError(`users[] is required and must be non-empty`);
  }
  const users = new Map<string, ResolvedUser>();
  for (const u of spec.users) {
    validateUser(u);
    if (users.has(u.name)) throw new SpecError(`duplicate user "${u.name}"`);
    users.set(u.name, { spec: u, memberships: [] });
  }

  // ---- organisations + workspaces ----
  if (!Array.isArray(spec.organizations) || spec.organizations.length === 0) {
    throw new SpecError(`organizations[] is required and must be non-empty`);
  }
  const seenWorkspaces = new Set<string>();
  const seenOrgs = new Set<string>();
  const workspaces: ResolvedWorkspace[] = [];

  for (const org of spec.organizations) {
    validateOrg(org, seenOrgs);
    for (const ws of org.workspaces ?? []) {
      const fq = `${org.name}-${ws.name}`;
      if (seenWorkspaces.has(fq)) throw new SpecError(`duplicate workspace ${fq}`);
      seenWorkspaces.add(fq);
      validateWorkspace(org, ws, fq, users);
      const resolved: ResolvedWorkspace = { resourceName: fq, org, spec: ws };
      workspaces.push(resolved);
      attachMemberships(resolved, users);
    }
  }

  return { spec, workspaces, users };
}

// ---- helpers --------------------------------------------------------------

function validateUser(u: UserSpec): void {
  if (!u.name || !NAME_RE.test(u.name)) {
    throw new SpecError(
      `user.name "${u.name}" must match ${NAME_RE} (lowercase alphanumerics + hyphens, 1–32 chars)`,
    );
  }
  if (u.email && !EMAIL_RE.test(u.email)) {
    throw new SpecError(`user "${u.name}" email "${u.email}" is invalid`);
  }
}

function validateOrg(org: OrgSpec, seen: Set<string>): void {
  if (!org.name || !NAME_RE.test(org.name)) {
    throw new SpecError(`org.name "${org.name}" must match ${NAME_RE}`);
  }
  if (seen.has(org.name)) throw new SpecError(`duplicate org name "${org.name}"`);
  seen.add(org.name);
  if (!Array.isArray(org.workspaces) || org.workspaces.length === 0) {
    throw new SpecError(`org "${org.name}" has no workspaces[]`);
  }
}

function validateWorkspace(
  org: OrgSpec,
  ws: WorkspaceSpec,
  fq: string,
  users: Map<string, ResolvedUser>,
): void {
  if (!ws.name || !NAME_RE.test(ws.name)) {
    throw new SpecError(`workspace.name "${ws.name}" in org "${org.name}" must match ${NAME_RE}`);
  }
  if (fq.length > 64) {
    throw new SpecError(`workspace "${fq}" full name exceeds 64 chars`);
  }

  const state = ws.state ?? "active";
  if (state === "deleted") return; // tombstone — no further checks

  if (ws.contact && !EMAIL_RE.test(ws.contact)) {
    throw new SpecError(`workspace "${fq}" contact "${ws.contact}" is not a valid email`);
  }

  // Members
  const memberUsers = new Set<string>();
  let groupAdminCount = 0;
  for (const m of ws.members ?? []) {
    validateMember(m, fq, users);
    if (memberUsers.has(m.user)) {
      throw new SpecError(`duplicate member reference "${m.user}" in "${fq}"`);
    }
    memberUsers.add(m.user);
    if (m.role === "group-admin") groupAdminCount++;
  }
  if (state === "active" && groupAdminCount === 0) {
    throw new SpecError(`active workspace "${fq}" must have at least one member with role: group-admin`);
  }

  // Collaborators
  const collabUsers = new Set<string>();
  for (const c of ws.collaborators ?? []) {
    validateCollaborator(c, fq, users);
    if (collabUsers.has(c.user)) {
      throw new SpecError(`duplicate collaborator reference "${c.user}" in "${fq}"`);
    }
    if (memberUsers.has(c.user)) {
      throw new SpecError(`user "${c.user}" cannot be both member and collaborator in "${fq}"`);
    }
    collabUsers.add(c.user);
  }
}

function validateMember(m: WorkspaceMember, fq: string, users: Map<string, ResolvedUser>): void {
  if (!m.user) {
    throw new SpecError(`member entry in "${fq}" missing required field: user`);
  }
  if (!users.has(m.user)) {
    throw new SpecError(
      `member "${m.user}" in "${fq}" not declared in top-level users[] — add an entry there first`,
    );
  }
  if (!VALID_ROLES.has(m.role)) {
    throw new SpecError(
      `member "${m.user}" in "${fq}" has invalid role "${m.role}" (allowed: group-admin, group-member)`,
    );
  }
}

function validateCollaborator(
  c: CollaboratorRef,
  fq: string,
  users: Map<string, ResolvedUser>,
): void {
  if (!c.user) {
    throw new SpecError(`collaborator entry in "${fq}" missing required field: user`);
  }
  if (!users.has(c.user)) {
    throw new SpecError(
      `collaborator "${c.user}" in "${fq}" not declared in top-level users[] — add an entry there first`,
    );
  }
  if (!c.expiresAt || !ISO_DATE_RE.test(c.expiresAt)) {
    throw new SpecError(
      `collaborator "${c.user}" in "${fq}" expiresAt "${c.expiresAt}" must be ISO date YYYY-MM-DD`,
    );
  }
  const d = new Date(c.expiresAt + "T00:00:00Z");
  if (Number.isNaN(d.getTime())) {
    throw new SpecError(`collaborator "${c.user}" in "${fq}" expiresAt "${c.expiresAt}" is not a real date`);
  }
}

function attachMemberships(rw: ResolvedWorkspace, users: Map<string, ResolvedUser>): void {
  if ((rw.spec.state ?? "active") === "deleted") return;
  for (const m of rw.spec.members ?? []) {
    users.get(m.user)!.memberships.push({ workspace: rw, kind: "member", role: m.role });
  }
  for (const c of rw.spec.collaborators ?? []) {
    users.get(c.user)!.memberships.push({
      workspace: rw,
      kind: "collab",
      role: "group-collaborator",
      expiresAt: c.expiresAt,
    });
  }
}

/** Helper for callers that need to know if a collaborator membership is active today. */
export function isCollabActive(m: UserMembership, now: Date = new Date()): boolean {
  if (m.kind !== "collab" || !m.expiresAt) return true;
  return new Date(m.expiresAt + "T00:00:00Z") > now;
}
