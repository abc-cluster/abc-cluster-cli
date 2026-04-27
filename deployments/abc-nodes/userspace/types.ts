// ---------------------------------------------------------------------------
// types.ts — TypeScript interfaces for the workspace YAML spec.
//
// Naming conventions (matching existing acl/ bootstrap scripts):
//
//   Nomad namespace:   <org>-<workspace>          e.g. su-mbhg-bioinformatics
//   Nomad policy:      <ns>-{group-admin|member|submit}
//   MinIO bucket:      <org>-<workspace>          (same as namespace)
//   MinIO IAM policy:  ns-<ns>-group-admin  /  ns-<ns>-user-<username>
//   MinIO user:        <ns>_<username>             (underscore separator)
//   MinIO admin user:  <ns>_admin
//   Nomad var path:    nomad/jobs/abc-nodes-minio-iam/<principal>  in abc-services ns
//
// Organisation model:
//   Organization
//     └── Workspace  (1 Nomad namespace + 1 MinIO bucket)
//           ├── Members        (group-admin | member)
//           └── SubmitAccount  (nf-nomad pipeline service account; optional)
// ---------------------------------------------------------------------------

/** Top-level spec file. Multiple orgs may coexist in one file. */
export interface WorkspacesSpec {
  /** Schema version for future migration support. */
  version: "v1";
  /** List of organisations that own workspaces. */
  organizations: OrgSpec[];
}

/** An organisation that owns one or more workspaces. */
export interface OrgSpec {
  /** Short identifier used in resource names, e.g. "su-mbhg". */
  name: string;
  /** Human-readable display name shown in Seqera Platform. */
  displayName?: string;
  /** Workspaces that belong to this org. */
  workspaces: WorkspaceSpec[];
}

/**
 * A single workspace — the unit of isolation.
 *
 * Resource naming:
 *   Nomad namespace / MinIO bucket = `<org.name>-<workspace.name>`
 *   e.g. org="su-mbhg", workspace="bioinformatics"
 *     → "su-mbhg-bioinformatics"
 */
export interface WorkspaceSpec {
  /** Short identifier, combined with org name. */
  name: string;
  /** Human-readable description stored in namespace/variable metadata. */
  description?: string;
  /**
   * Scheduler priority tier:
   *   high   → job_priority=70 (preempts normal batch)
   *   normal → job_priority=50 (default)
   * Maps to the `priority` and `job_priority` meta fields in the namespace HCL.
   */
  priority?: "high" | "normal";
  /**
   * Lifecycle state:
   *   active     — fully operational (default)
   *   suspended  — Nomad ACL tokens, MinIO users, and per-user IAM policies
   *                are destroyed; namespace, bucket, and all S3 data are
   *                preserved. Bucket versioning is suspended. Resume by
   *                flipping back to "active".
   *   archived   — same destructive surface as "suspended" today; versioning
   *                suspended. Reserved for future read-only token issuance.
   *   deleted    — tombstone; remove entry after confirming `pulumi destroy`
   */
  state?: "active" | "suspended" | "archived" | "deleted";
  /**
   * If true, the group-admin Nomad policy includes `alloc-node-exec` (run
   * arbitrary commands on the host node, not just inside the container).
   * Defaults to false — only enable for workspaces that genuinely need
   * host-level debugging access.
   */
  groupAdminNodeExec?: boolean;
  /**
   * Nomad task driver allow-list for the namespace.
   * Defaults to: enabled=["containerd-driver","docker","exec"], disabled=["raw_exec"]
   *
   * The @pulumi/nomad provider does not expose the namespace `capabilities`
   * block, so this is applied via a `local.Command` running
   * `abc admin services nomad cli -- namespace apply -json -` after the
   * namespace resource is created.
   */
  taskDrivers?: TaskDriversSpec;
  /**
   * Contact email for the group PI / tech lead.
   * Stored in namespace meta as `contact`.
   */
  contact?: string;
  /**
   * ntfy topic for job-completion notifications.
   * Stored in namespace meta as `ntfy_topic`.
   * Defaults to "<namespace>-jobs" if not set.
   */
  ntfyTopic?: string;
  /** Human members of the workspace. */
  members?: MemberSpec[];
  /**
   * nf-nomad pipeline service account (optional).
   * Creates Nomad token with submit policy + MinIO user with pipeline policy.
   * MinIO user: <namespace>_submit  |  Nomad token: <namespace>_submit
   * MinIO access: pipelines/ r/w + samplesheets/ read + shared/ read
   */
  submitAccount?: SubmitAccountSpec;
  /**
   * Time-bounded external collaborators (optional).
   * Each gets a MinIO user scoped to collab/<name>/ r/w + shared/ read.
   * No Nomad access by default.
   */
  collaborators?: CollaboratorSpec[];
  /** Seqera Platform integration metadata. */
  seqera?: SeqeraSpec;
}

/** Nomad namespace task driver capabilities. */
export interface TaskDriversSpec {
  /** Drivers allowed in this namespace. */
  enabled?: string[];
  /** Drivers blocked in this namespace. */
  disabled?: string[];
}

/**
 * Member role within a workspace.
 *
 *   group-admin   — full namespace write + all caps; full bucket access.
 *                   Held by the shared "admin" account and by individuals
 *                   who need elevated access via `abc --sudo`.
 *   group-member  — submit/inspect own jobs; users/<name>/ and shared/users/<name>/
 *                   r/w; shared/ read-only.
 */
export type Role = "group-admin" | "group-member";

/**
 * A human workspace member.
 *
 * `role` may be a single role or an array — a member with multiple roles gets
 * one Nomad token + one MinIO user per role, so `abc --sudo` can pick the
 * group-admin token while the default token stays at group-member.
 *
 * Principal naming (= Nomad token Name = MinIO username):
 *   group-member   →  <namespace>_<name>
 *   group-admin    →  <namespace>_<name>-admin
 */
export interface MemberSpec {
  /** Username — base for principal names. Lowercase alphanumerics + hyphens. */
  name: string;
  /** One role or a set of roles. */
  role: Role | Role[];
  /** Email for notifications and Seqera Platform user mapping. */
  email?: string;
}

/**
 * The nf-nomad pipeline service account (one per workspace).
 * Gets submit Nomad policy + group-admin MinIO policy (full bucket access).
 *
 * MinIO user:  <namespace>_submit
 * Nomad token: <namespace>_submit
 */
export interface SubmitAccountSpec {
  /** Optional description stored in Nomad variable metadata. */
  description?: string;
}

/**
 * A time-bounded external collaborator.
 *
 * MinIO access:
 *   collab/<name>/   — full r/w (private collaboration area)
 *   shared/          — read-only
 *
 * MinIO user:  <namespace>_collab-<name>
 * No Nomad token is issued by default.
 */
export interface CollaboratorSpec {
  /** Identifier used as the collab/<name>/ prefix and MinIO username suffix. */
  name: string;
  /**
   * ISO-8601 date (YYYY-MM-DD) after which the MinIO user should be removed.
   * Running `pulumi up` after this date skips / removes the collaborator resources.
   */
  expiresAt: string;
  /** Email for notifications. */
  email?: string;
  /**
   * Always "group-collaborator" — kept as an explicit field so the YAML
   * documents intent and matches the role vocabulary used elsewhere.
   * Optional; defaults to "group-collaborator".
   */
  role?: "group-collaborator";
}

/** Seqera Platform workspace integration settings. */
export interface SeqeraSpec {
  /**
   * Seqera Platform workspace ID (numeric) — set after Platform workspace
   * is created. Used to cross-reference compute environments.
   */
  workspaceId?: number;
  /** Compute environment name registered in the Platform. */
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
