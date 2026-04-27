// ---------------------------------------------------------------------------
// types.ts — TypeScript interfaces for the workspaces.yaml v2 spec.
//
// Per-person credential model:
//   • Top-level users[] lists every principal once.
//   • Each user gets ONE Nomad ACL token + ONE RustFS IAM user, named by
//     their global `name` field. Policies are unioned across every
//     (workspace, role) the user holds.
//   • role: group-admin auto-implies group-member (one policy attached, no
//     redundant member attachment).
//   • A user may appear in multiple workspaces — one token covers all.
//   • `--sudo` is a client-side UX prompt; no token swap (see
//     internal/jurist/types.go for the future elevation-token sketch).
//
// Naming conventions:
//   Nomad namespace:   <org>-<workspace>     e.g. su-mbhg-bioinformatics
//   RustFS bucket:     <org>-<workspace>     (same as namespace)
//   Nomad ACL token:   <user>                e.g. abhi
//   RustFS IAM user:   <user>                (same as Nomad token name)
//   Nomad policy:      <ns>-{group-admin|member|submit}
//   RustFS IAM policy: ns-<ns>-group-admin   |  ns-<ns>-member  (uses
//                      ${aws:username})  |  ns-<ns>-collab-<user>  |
//                      ns-<ns>-pipeline-submit
// ---------------------------------------------------------------------------

/** Top-level spec file. Multiple orgs may coexist in one file. */
export interface WorkspacesSpec {
  /** Schema version. Bumped to v2 for the per-person model. */
  version: "v2";
  /** Every principal that appears in any workspace. Each gets one credential pair. */
  users: UserSpec[];
  /** List of organisations that own workspaces. */
  organizations: OrgSpec[];
}

/**
 * A workspace member or external collaborator. Defined once at the top of
 * the spec; referenced from workspaces by `user: <name>`.
 */
export interface UserSpec {
  /** Globally unique principal name. Must match NAME_RE. */
  name: string;
  /** Email for notifications and Seqera Platform user mapping. */
  email?: string;
}

/** Member role within a workspace. */
export type Role = "group-admin" | "group-member";

/** An organisation that owns one or more workspaces. */
export interface OrgSpec {
  /** Short identifier used in resource names, e.g. "su-mbhg". */
  name: string;
  /** Human-readable display name. */
  displayName?: string;
  /** Workspaces that belong to this org. */
  workspaces: WorkspaceSpec[];
}

/** A single workspace — the unit of isolation. */
export interface WorkspaceSpec {
  /** Short identifier; combined with org name as <org>-<workspace>. */
  name: string;
  /** Human-readable description stored in namespace/variable metadata. */
  description?: string;
  /** Scheduler priority tier: high → 70, normal → 50. */
  priority?: "high" | "normal";
  /**
   * Lifecycle state:
   *   active     — fully operational (default)
   *   suspended  — Nomad ACL tokens revoked for users whose ONLY membership
   *                is this workspace; data preserved
   *   archived   — bucket versioning suspended; read-only intent
   *   deleted    — tombstone; remove entry after `pulumi destroy` confirms
   */
  state?: "active" | "suspended" | "archived" | "deleted";
  /**
   * If true, the group-admin Nomad policy includes `alloc-node-exec` (host
   * exec). Default false — only enable for workspaces that genuinely need it.
   */
  groupAdminNodeExec?: boolean;
  /**
   * Nomad task driver allow-list for the namespace.
   * Defaults to enabled=["containerd-driver","docker","exec"], disabled=["raw_exec"].
   * Applied via `local.Command` invoking `abc admin services nomad cli --
   * namespace apply -json …` because the @pulumi/nomad provider does not
   * expose the namespace capabilities block.
   */
  taskDrivers?: TaskDriversSpec;
  /** Contact email for the group PI / tech lead (stored in namespace meta). */
  contact?: string;
  /** ntfy topic for job-completion notifications. Defaults to "<ns>-jobs". */
  ntfyTopic?: string;
  /** Workspace members. Each entry references a top-level user by name. */
  members?: WorkspaceMember[];
  /** Optional nf-nomad pipeline service account (one per workspace). */
  submitAccount?: SubmitAccountSpec;
  /** Time-bounded external collaborators. */
  collaborators?: CollaboratorRef[];
  /** Seqera Platform integration metadata. */
  seqera?: SeqeraSpec;
}

/**
 * A workspace member: a top-level user with a role in this workspace.
 *
 * group-admin auto-implies group-member capabilities — only the group-admin
 * policy is attached (member policy would be redundant).
 */
export interface WorkspaceMember {
  /** References WorkspacesSpec.users[].name. */
  user: string;
  /** Role within this workspace. */
  role: Role;
}

/**
 * A time-bounded external collaborator reference. Collaborators receive a
 * RustFS IAM user (scoped to collab/<user>/ r/w + shared/ ro) but no Nomad
 * token — by default they don't run jobs.
 */
export interface CollaboratorRef {
  /** References WorkspacesSpec.users[].name. */
  user: string;
  /** ISO-8601 date (YYYY-MM-DD); after this the IAM scope is dropped. */
  expiresAt: string;
}

/** Nomad namespace task driver capabilities. */
export interface TaskDriversSpec {
  enabled?: string[];
  disabled?: string[];
}

/** nf-nomad pipeline service account (one per workspace). */
export interface SubmitAccountSpec {
  description?: string;
}

/** Seqera Platform workspace integration settings. */
export interface SeqeraSpec {
  workspaceId?: number;
  computeEnvName?: string;
}

// ---------------------------------------------------------------------------
// Derived / resolved types used internally
// ---------------------------------------------------------------------------

/** Fully-qualified workspace identifier after org prefix expansion. */
export interface ResolvedWorkspace {
  /** e.g. "su-mbhg-bioinformatics" */
  resourceName: string;
  org: OrgSpec;
  spec: WorkspaceSpec;
}

/**
 * Aggregated view of a single user's memberships across all workspaces.
 * Built during spec resolution and consumed by user.ts to emit the user's
 * Nomad token + IAM user with the right policy attachments.
 */
export interface UserMembership {
  /** Workspace this membership applies to. */
  workspace: ResolvedWorkspace;
  /** "member" if joined via members[], "collab" if joined via collaborators[]. */
  kind: "member" | "collab";
  /** Effective role (members only). For collab this is always "group-collaborator". */
  role: Role | "group-collaborator";
  /** Collaborator expiresAt; undefined for members. */
  expiresAt?: string;
}

export interface ResolvedUser {
  spec: UserSpec;
  /** Every workspace this user touches (members[] + collaborators[]). */
  memberships: UserMembership[];
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/** Default task drivers matching the existing namespace HCL pattern. */
export const DEFAULT_TASK_DRIVERS: Required<TaskDriversSpec> = {
  enabled: ["containerd-driver", "docker", "exec"],
  disabled: ["raw_exec"],
};

/** Map priority tier → numeric job priority (Nomad scheduler). */
export const JOB_PRIORITY: Record<"high" | "normal", number> = {
  high: 70,
  normal: 50,
};
