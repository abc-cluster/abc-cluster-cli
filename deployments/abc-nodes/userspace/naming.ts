// ---------------------------------------------------------------------------
// naming.ts — Single source of truth for resource name conventions (v2).
//
// Per-person model: the principal is just the user's name (no namespace
// prefix). Per-workspace policies and groups still embed the namespace.
// ---------------------------------------------------------------------------

/**
 * Allowed shape for org / workspace / user names.
 * Lowercase alphanumerics + hyphens, 3–32 chars, must start with alphanum.
 * These end up as RustFS IAM usernames (case-sensitive, no underscores in
 * S3 ARN-friendly contexts) and S3 prefixes — keep the alphabet conservative.
 *
 * Minimum length is 3 because RustFS rejects shorter access keys with
 * "create_user err invalid access key length" (MinIO is more permissive).
 * If you need to support 1–2 char identifiers (e.g. initials), pad them in
 * the YAML — `tj` becomes `tjones` or `tj-ceri`.
 */
export const NAME_RE = /^[a-z0-9][a-z0-9-]{2,31}$/;

// ---- Nomad ACL policy names (per workspace × role) -------------------------

export const policyGroupAdmin = (ns: string): string => `${ns}-group-admin`;
export const policyMember     = (ns: string): string => `${ns}-member`;
export const policySubmit     = (ns: string): string => `${ns}-submit`;

// ---- RustFS IAM policy names (per workspace × role) ------------------------

export const minioPolicyGroupAdmin = (ns: string): string => `ns-${ns}-group-admin`;
/** Single per-workspace member policy that uses ${aws:username} substitution. */
export const minioPolicyMember     = (ns: string): string => `ns-${ns}-member`;
export const minioPolicyCollab     = (ns: string, user: string): string => `ns-${ns}-collab-${user}`;
export const minioPolicySubmit     = (ns: string): string => `ns-${ns}-pipeline-submit`;

// ---- Principal names (= Nomad token Name = RustFS IAM username) ------------

/** A user's principal name — same string everywhere across Nomad and RustFS. */
export const principalUser   = (user: string): string => user;
/** Pipeline service account principal: <ns>_submit (per workspace). */
export const principalSubmit = (ns: string): string => `${ns}_submit`;

// ---- Resolve role → policy ------------------------------------------------

/**
 * Map a role to the single Nomad policy that should be attached.
 * group-admin auto-implies group-member; only the higher-privilege policy is
 * attached (member would be redundant).
 */
export function nomadPolicyForRole(ns: string, role: "group-admin" | "group-member"): string {
  return role === "group-admin" ? policyGroupAdmin(ns) : policyMember(ns);
}

/** Same idea on the RustFS side. */
export function minioPolicyForRole(ns: string, role: "group-admin" | "group-member"): string {
  return role === "group-admin" ? minioPolicyGroupAdmin(ns) : minioPolicyMember(ns);
}

// ---- Nomad variable paths --------------------------------------------------

/** IAM credential variable path: <prefix>/<principal>. */
export const iamVarPath = (prefix: string, principal: string): string => `${prefix}/${principal}`;
/** Workspace meta variable path: <ns>/meta. */
export const workspaceMetaVarPath = (ns: string): string => `${ns}/meta`;
