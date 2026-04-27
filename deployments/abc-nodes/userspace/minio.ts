// ---------------------------------------------------------------------------
// minio.ts — Workspace-level S3 / IAM resources (per-workspace).
//
// Per-workspace this module emits:
//   • S3 bucket + versioning
//   • Top-level folder placeholders (users/, collab/, shared/, …)
//   • IAM policies: group-admin, member (uses ${aws:username}), collab-<user>,
//     pipeline-submit
//   • Submit account (one IamUser + policy attach per workspace, if defined)
//
// Per-user IAM resources (the IAM users themselves and their policy
// attachments) live in user.ts so a multi-workspace user has a single IAM
// user with multiple policies attached — that's the whole point of v2.
//
// Compatible with both MinIO and RustFS:
//   • IAM policy creation uses @pulumi/minio (works on both)
//   • Policy attachment uses `mc admin policy attach` via local.Command
//     (provider's IamUserPolicyAttachment fails on RustFS due to a
//     Content-Length header strictness difference)
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as minio from "@pulumi/minio";
import * as command from "@pulumi/command";
import { WorkspaceSpec, OrgSpec, UserSpec } from "./types";
import {
  minioPolicyGroupAdmin,
  minioPolicyMember,
  minioPolicyCollab,
  minioPolicySubmit,
  principalSubmit,
} from "./naming";
import {
  minioGroupAdminPolicy,
  minioMemberPolicy,
  minioCollaboratorPolicy,
  minioPipelinePolicy,
} from "./policies";

// ---- config ----------------------------------------------------------------

const cfg = new pulumi.Config();
/**
 * When true, IAM users and S3 buckets are created with forceDestroy=true so
 * `pulumi destroy` removes them along with their attached policies, all
 * object versions, and delete markers. Default false.
 */
export const ALLOW_DESTROY = cfg.getBoolean("allowDestroy") ?? false;

// MinIO/RustFS admin credentials — read from the same `minio:` config the
// @pulumi/minio provider uses. Reused by local.Command's that call
// `mc admin …` (policy attach, user enable cycle).
const minioCfg          = new pulumi.Config("minio");
const MINIO_ADMIN_HOST  = minioCfg.require("minioServer");
const MINIO_ADMIN_USER  = minioCfg.require("minioUser");
const MINIO_ADMIN_PASS  = minioCfg.requireSecret("minioPassword");
const MINIO_ADMIN_SSL   = minioCfg.getBoolean("minioSsl") ?? false;
const MINIO_ADMIN_SCHEME = MINIO_ADMIN_SSL ? "https" : "http";

/** mc-style URL with admin creds, suitable for `MC_HOST_<alias>` env var. */
export const MC_HOST_URL: pulumi.Output<string> = pulumi
  .all([MINIO_ADMIN_USER, MINIO_ADMIN_PASS])
  .apply(([u, p]) =>
    `${MINIO_ADMIN_SCHEME}://${encodeURIComponent(u)}:${encodeURIComponent(p)}@${MINIO_ADMIN_HOST}`,
  );

// ---- exported types --------------------------------------------------------

/** Resources produced by provisioning a single workspace's S3 + IAM bits. */
export interface WorkspaceMinioOutputs {
  /** Bucket name (= ns). */
  bucketName: pulumi.Output<string>;
  /** group-admin IAM policy (one per workspace). */
  groupAdminPolicy: minio.IamPolicy;
  /** Single member IAM policy with ${aws:username} substitution. */
  memberPolicy: minio.IamPolicy;
  /** Per-collaborator IAM policies, keyed by collaborator user name. */
  collabPolicies: Record<string, minio.IamPolicy>;
  /** Submit account resources (if the workspace defined a submitAccount). */
  submit?: SubmitAccountOutputs;
}

export interface SubmitAccountOutputs {
  /** IAM user resource for the submit account principal. */
  user: minio.IamUser;
  /** local.Command that runs after policy attach + IAM cache flush. */
  ready: pulumi.Resource;
  /** Principal name — used by writeNomadIamVar in the submit credential write. */
  principal: string;
  /** The pipeline-submit IAM policy. */
  policy: minio.IamPolicy;
  /** Password used for the IAM user (= Nomad token SecretID). */
  password: pulumi.Output<string>;
}

// ---- provisioner -----------------------------------------------------------

export function provisionWorkspaceMinio(
  resourceName: string,
  org: OrgSpec,
  spec: WorkspaceSpec,
  /** Map of user name → user spec, for collab policy generation. */
  users: Map<string, UserSpec>,
  /** Submit-account password (Nomad token SecretID); empty if none. */
  submitPassword: pulumi.Output<string>,
  opts: pulumi.ComponentResourceOptions,
): WorkspaceMinioOutputs {
  const ns = resourceName;
  const isActive = (spec.state ?? "active") === "active";

  // ------------------------------------------------------------------
  // 1. Bucket + versioning
  // ------------------------------------------------------------------
  const bucket = new minio.S3Bucket(
    `${ns}-bucket`,
    {
      bucket: ns,
      objectLocking: false,
      forceDestroy: ALLOW_DESTROY,
    },
    opts,
  );

  const versioningStatus = isActive ? "Enabled" : "Suspended";
  new minio.S3BucketVersioning(
    `${ns}-versioning`,
    { bucket: bucket.bucket, versioningConfiguration: { status: versioningStatus } },
    { ...opts, dependsOn: [bucket] },
  );

  // ------------------------------------------------------------------
  // 2. Top-level folder placeholders
  // ------------------------------------------------------------------
  const mkPlaceholder = (slug: string, key: string) =>
    new minio.S3Object(
      `${ns}-ph-${slug}`,
      {
        bucketName: bucket.bucket,
        objectName: key,
        contentType: "application/octet-stream",
        content: "\n",
      },
      { ...opts, dependsOn: [bucket], ignoreChanges: ["content"] },
    );

  mkPlaceholder("users",        "users/.keep");
  mkPlaceholder("collab",       "collab/.keep");
  mkPlaceholder("shared",       "shared/.keep");
  mkPlaceholder("shared-refs",  "shared/references-and-databases/.keep");
  mkPlaceholder("shared-users", "shared/users/.keep");
  mkPlaceholder("samplesheets", "samplesheets/.keep");
  mkPlaceholder("pipelines",    "pipelines/.keep");

  // Per-member folder placeholders (users/<name>/ and shared/users/<name>/)
  // need to exist for each group-member who shows up in this workspace.
  // group-admins also write there sometimes — pre-create for any user with
  // any role in the workspace.
  const seenUserFolders = new Set<string>();
  for (const m of spec.members ?? []) {
    if (seenUserFolders.has(m.user)) continue;
    seenUserFolders.add(m.user);
    mkPlaceholder(`users-${m.user}`,        `users/${m.user}/.keep`);
    mkPlaceholder(`shared-users-${m.user}`, `shared/users/${m.user}/.keep`);
  }
  for (const c of spec.collaborators ?? []) {
    mkPlaceholder(`collab-${c.user}`, `collab/${c.user}/.keep`);
  }

  // ------------------------------------------------------------------
  // 3. IAM policies — group-admin + member (one each, per workspace)
  // ------------------------------------------------------------------
  const groupAdminPolicy = new minio.IamPolicy(
    `${ns}-iam-group-admin`,
    { name: minioPolicyGroupAdmin(ns), policy: minioGroupAdminPolicy(ns) },
    opts,
  );

  const memberPolicy = new minio.IamPolicy(
    `${ns}-iam-member`,
    { name: minioPolicyMember(ns), policy: minioMemberPolicy(ns) },
    opts,
  );

  // ------------------------------------------------------------------
  // 4. Per-collaborator IAM policies (one per active collaborator)
  // ------------------------------------------------------------------
  const collabPolicies: Record<string, minio.IamPolicy> = {};
  if (isActive) {
    for (const c of spec.collaborators ?? []) {
      collabPolicies[c.user] = new minio.IamPolicy(
        `${ns}-iam-collab-${c.user}`,
        {
          name: minioPolicyCollab(ns, c.user),
          policy: minioCollaboratorPolicy(ns, c.user),
        },
        opts,
      );
    }
  }

  // ------------------------------------------------------------------
  // 5. Submit account (per-workspace; remains a single IAM user
  //    because there's no top-level user counterpart).
  // ------------------------------------------------------------------
  let submit: SubmitAccountOutputs | undefined;
  if (spec.submitAccount && isActive) {
    const submitPolicy = new minio.IamPolicy(
      `${ns}-iam-submit`,
      { name: minioPolicySubmit(ns), policy: minioPipelinePolicy(ns) },
      opts,
    );

    const submitMinioUser = principalSubmit(ns);
    const submitUser = new minio.IamUser(
      `${ns}-user-submit`,
      { name: submitMinioUser, secret: submitPassword, forceDestroy: ALLOW_DESTROY },
      opts,
    );

    const submitReady = mkAttachCommand(
      `${ns}-attach-submit`,
      submitMinioUser,
      submitPolicy.name,
      [submitPolicy, submitUser],
      opts,
    );

    submit = { user: submitUser, ready: submitReady, principal: submitMinioUser, policy: submitPolicy, password: submitPassword };
  }

  return {
    bucketName: bucket.bucket,
    groupAdminPolicy,
    memberPolicy,
    collabPolicies,
    submit,
  };
}

// ---- helpers shared with user.ts ------------------------------------------

/**
 * Run `mc admin policy attach` (and a disable→enable cycle to flush IAM
 * cache) against the configured MinIO/RustFS endpoint. Idempotent on re-runs;
 * `mc admin policy detach` runs on destroy.
 *
 * Used by both the per-workspace submit account and per-user attachments
 * emitted by user.ts. Returns the local.Command for use as a dependency.
 */
export function mkAttachCommand(
  resourceName: string,
  username: pulumi.Input<string>,
  policyName: pulumi.Input<string>,
  dependsOn: pulumi.Resource[],
  opts: pulumi.ComponentResourceOptions,
): command.local.Command {
  return new command.local.Command(
    resourceName,
    {
      create:
        `mc admin policy attach userspace "$POLICY" --user "$TARGET_USER" && ` +
        `mc admin user disable userspace "$TARGET_USER" && ` +
        `mc admin user enable userspace "$TARGET_USER"`,
      delete: `mc admin policy detach userspace "$POLICY" --user "$TARGET_USER" || true`,
      triggers: [pulumi.all([username, policyName]).apply(([u, p]) => `${u}|${p}`)],
      environment: {
        TARGET_USER: pulumi.output(username) as unknown as pulumi.Input<string>,
        POLICY: pulumi.output(policyName) as unknown as pulumi.Input<string>,
        MC_HOST_userspace: MC_HOST_URL,
      },
    },
    { ...opts, dependsOn },
  );
}
