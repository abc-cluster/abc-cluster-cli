// ---------------------------------------------------------------------------
// naming.ts — Single source of truth for resource name conventions.
//
// Every name embedded into Nomad token names, MinIO usernames, S3 prefixes,
// and Nomad variable paths is constructed here so renames or convention
// changes only need to happen in one place.
// ---------------------------------------------------------------------------

/**
 * Allowed shape for org / workspace / member / collaborator names.
 * Lowercase alphanumerics + hyphens, 1–32 chars, must start with alphanum.
 * These end up in MinIO usernames (which are case-sensitive) and S3 prefixes,
 * so we keep the alphabet conservative to avoid silent IAM breakage.
 */
export const NAME_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;

// ---- Nomad ACL policy names ------------------------------------------------

export const policyGroupAdmin = (ns: string): string => `${ns}-group-admin`;
export const policyMember     = (ns: string): string => `${ns}-member`;
export const policySubmit     = (ns: string): string => `${ns}-submit`;

// ---- MinIO IAM policy names ------------------------------------------------

export const minioPolicyGroupAdmin = (ns: string): string => `ns-${ns}-group-admin`;
export const minioPolicyUser       = (ns: string, name: string): string => `ns-${ns}-user-${name}`;
export const minioPolicyCollab     = (ns: string, name: string): string => `ns-${ns}-collab-${name}`;
export const minioPolicySubmit     = (ns: string): string => `ns-${ns}-pipeline-submit`;

// ---- Principal names (= Nomad token Name = MinIO username) -----------------

/** group-admin principal: <ns>_<name>-admin (always per-person, including name="admin" → <ns>_admin-admin). */
export const principalAdmin  = (ns: string, name: string): string => `${ns}_${name}-admin`;
/** group-member principal: <ns>_<name>. */
export const principalMember = (ns: string, name: string): string => `${ns}_${name}`;
/** Pipeline service account: <ns>_submit. */
export const principalSubmit = (ns: string): string => `${ns}_submit`;
/** External collaborator: <ns>_collab-<name>. */
export const principalCollab = (ns: string, name: string): string => `${ns}_collab-${name}`;

/** Resolve a (member, role) pair to its principal name. */
export function memberPrincipal(
  ns: string,
  role: "group-admin" | "group-member",
  name: string,
): string {
  return role === "group-admin" ? principalAdmin(ns, name) : principalMember(ns, name);
}

/** Resource-name slug for a (name, role) pair: "kim", "abhi-admin". */
export function memberResourceSlug(
  role: "group-admin" | "group-member",
  name: string,
): string {
  return role === "group-admin" ? `${name}-admin` : name;
}

// ---- Nomad variable paths --------------------------------------------------

/** IAM credential variable path: <prefix>/<principal>. */
export const iamVarPath = (prefix: string, principal: string): string => `${prefix}/${principal}`;

/** Workspace meta variable path: <ns>/meta. */
export const workspaceMetaVarPath = (ns: string): string => `${ns}/meta`;
