// ---------------------------------------------------------------------------
// minio.ts — MinIO resource provisioning per workspace.
//
// Per workspace:
//   • Bucket (always; survives suspension/archival)
//   • Versioning: Enabled when active, Suspended otherwise
//   • Top-level folder placeholders: users/, collab/, shared/, …
//   • IAM policies + users for members, optional submit SA, and collaborators
//     (only when active — suspending the workspace destroys the users + per-user
//     IAM policies; data is preserved.)
//   • Per-principal IAM credentials are written back to Nomad variables at
//     <iamVarPrefix>/<principal> in the configured Nomad IAM namespace
//     (defaults: prefix="nomad/jobs/abc-nodes-minio-iam", ns="abc-services").
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as minio from "@pulumi/minio";
import * as nomad from "@pulumi/nomad";
import * as command from "@pulumi/command";
import { WorkspaceSpec, OrgSpec } from "./types";
import {
  minioPolicyGroupAdmin,
  minioPolicyUser,
  minioPolicyCollab,
  minioPolicySubmit,
  memberPrincipal,
  memberResourceSlug,
  principalSubmit,
  principalCollab,
  iamVarPath,
} from "./naming";
import {
  minioGroupAdminPolicy,
  minioMemberPolicy,
  minioCollaboratorPolicy,
  minioPipelinePolicy,
} from "./policies";
import { memberRoles } from "./validate";

// ---- config ----------------------------------------------------------------

const cfg = new pulumi.Config();
/** Nomad namespace where IAM credential variables are stored. */
const NOMAD_IAM_NAMESPACE = cfg.get("nomadIamNamespace") ?? "abc-services";
/** Variable path prefix for IAM creds. */
const NOMAD_IAM_VAR_PREFIX = cfg.get("nomadIamVarPrefix") ?? "nomad/jobs/abc-nodes-minio-iam";
/**
 * When true, IAM users and S3 buckets are created with forceDestroy=true so
 * `pulumi destroy` removes them along with their attached policies, all
 * object versions, and delete markers. Default is false — `pulumi destroy`
 * will fail on non-empty buckets, preventing accidental data loss. Flip with
 *   pulumi config set allowDestroy true
 * before destroying a stack.
 */
export const ALLOW_DESTROY = cfg.getBoolean("allowDestroy") ?? false;

// MinIO admin credentials, read from the same `minio:` config namespace the
// @pulumi/minio provider uses. Re-used by local.Command's that need to call
// `mc admin ...` (e.g. enabling IAM users after attachment lands).
const minioCfg          = new pulumi.Config("minio");
const MINIO_ADMIN_HOST  = minioCfg.require("minioServer");          // host:port
const MINIO_ADMIN_USER  = minioCfg.require("minioUser");
const MINIO_ADMIN_PASS  = minioCfg.requireSecret("minioPassword");
const MINIO_ADMIN_SSL   = minioCfg.getBoolean("minioSsl") ?? false;
const MINIO_ADMIN_SCHEME = MINIO_ADMIN_SSL ? "https" : "http";

// ---- helpers ---------------------------------------------------------------

/**
 * Provision a MinIO IAM user and close the post-create race window where a
 * freshly created IamUser briefly authenticates with overly permissive access
 * before the IamUserPolicyAttachment fully propagates through MinIO's IAM
 * cache.
 *
 * Sequence:
 *   1. IamUser is created (the @pulumi/minio provider does not support
 *      atomic create-with-policy, and disableUser=true on create is broken
 *      upstream so we cannot create disabled).
 *   2. IamUserPolicyAttachment binds the scoped policy.
 *   3. local.Command runs `mc admin user disable && mc admin user enable`
 *      to force MinIO to flush its IAM identity cache, ensuring the
 *      attached policy is the authoritative one for any subsequent request.
 *
 * The disable/enable cycle takes ~100ms; access during that window already
 * fails. After the cycle completes the user authenticates against the
 * attached policy with no leftover permissions.
 *
 * The local.Command depends on the attachment so it runs after the policy
 * binding lands. It re-runs whenever the username changes (idempotent on mc).
 */
function provisionMinioUser(
  ns: string,
  slug: string,
  username: string,
  password: pulumi.Input<string>,
  policyResource: minio.IamPolicy,
  opts: pulumi.ComponentResourceOptions,
): { user: minio.IamUser; ready: pulumi.Resource } {
  const user = new minio.IamUser(
    `${ns}-user-${slug}`,
    {
      name: username,
      secret: password,
      forceDestroy: ALLOW_DESTROY,
    },
    opts,
  );

  const attachment = new minio.IamUserPolicyAttachment(
    `${ns}-attach-${slug}`,
    {
      userName: user.name,
      policyName: policyResource.name,
    },
    { ...opts, dependsOn: [policyResource, user] },
  );

  // mc reads MC_HOST_<alias> from env, so we synthesise an alias URL with the
  // admin creds from `minio:` Pulumi config (same source the provider uses).
  const mcHost = pulumi
    .all([MINIO_ADMIN_USER, MINIO_ADMIN_PASS])
    .apply(([u, p]) =>
      `${MINIO_ADMIN_SCHEME}://${encodeURIComponent(u)}:${encodeURIComponent(p)}@${MINIO_ADMIN_HOST}`,
    );

  const reset = new command.local.Command(
    `${ns}-iam-reset-${slug}`,
    {
      // Toggle disable→enable to flush MinIO's IAM cache for this principal.
      // After this runs, the attached policy is the only one in effect.
      create:
        `mc admin user disable userspace "$TARGET_USER" && ` +
        `mc admin user enable userspace "$TARGET_USER"`,
      triggers: [username],
      environment: {
        TARGET_USER: username,
        MC_HOST_userspace: mcHost,
      },
    },
    { ...opts, dependsOn: [attachment] },
  );

  return { user, ready: reset };
}

/**
 * Write a MinIO IAM credential to a Nomad variable.
 *
 * Uses itemsWo (write-only) exclusively — Pulumi never stores any credential
 * value in plaintext stack state. Fields match the SYNC_NOMAD_VARS format used
 * by setup-minio-namespace-buckets.sh.
 */
function writeNomadIamVar(
  ns: string,
  principal: string,
  secretKey: pulumi.Input<string>,
  role: PrincipalCredential["role"],
  scope: string,
  opts: pulumi.ComponentResourceOptions,
): void {
  const resourceId = principal.replace(/_/g, "-");
  new nomad.Variable(
    `${ns}-minio-var-${resourceId}`,
    {
      namespace: NOMAD_IAM_NAMESPACE,
      path: iamVarPath(NOMAD_IAM_VAR_PREFIX, principal),
      itemsWo: pulumi.interpolate`{"access_key":"${principal}","secret_key":"${secretKey}","role":"${role}","scope":"${scope}","bucket":"${ns}"}`,
      itemsWoVersion: 1,
    },
    { ...opts, additionalSecretOutputs: ["itemsWo"] },
  );
}

// ---- exported types --------------------------------------------------------

export interface MinioWorkspaceOutputs {
  bucketName: pulumi.Output<string>;
  credentials: pulumi.Output<Record<string, PrincipalCredential>>;
}

export interface PrincipalCredential {
  accessKey: string;
  secretKey: string;
  role: "group-admin" | "user" | "collab" | "submit";
  scope: string;
  bucket: string;
}

// ---- main provisioner ------------------------------------------------------

export function provisionMinioWorkspace(
  resourceName: string,
  org: OrgSpec,
  spec: WorkspaceSpec,
  nomadTokenSecrets: pulumi.Output<Record<string, string>>,
  opts: pulumi.ComponentResourceOptions,
): MinioWorkspaceOutputs {
  const ns = resourceName;
  const isActive = (spec.state ?? "active") === "active";

  // ------------------------------------------------------------------
  // 1. Bucket (kept across all non-deleted states — data survives suspension)
  // ------------------------------------------------------------------
  const bucket = new minio.S3Bucket(
    `${ns}-bucket`,
    {
      bucket: ns,
      objectLocking: false,
      // Gate on ALLOW_DESTROY so `pulumi destroy` only nukes data when the
      // operator has explicitly opted in. With versioning enabled the
      // provider deletes every object version + delete marker on destroy.
      forceDestroy: ALLOW_DESTROY,
    },
    opts,
  );

  const versioningStatus = isActive ? "Enabled" : "Suspended";
  new minio.S3BucketVersioning(
    `${ns}-versioning`,
    {
      bucket: bucket.bucket,
      versioningConfiguration: { status: versioningStatus },
    },
    { ...opts, dependsOn: [bucket] },
  );

  // ------------------------------------------------------------------
  // 2. Top-level folder placeholders
  // ------------------------------------------------------------------
  const mkPlaceholder = (name: string, objectName: string) =>
    new minio.S3Object(
      `${ns}-ph-${name}`,
      {
        bucketName: bucket.bucket,
        objectName,
        contentType: "application/octet-stream",
        content: "\n",
      },
      { ...opts, dependsOn: [bucket], ignoreChanges: ["content"] },
    );

  mkPlaceholder("users",         "users/.keep");
  mkPlaceholder("collab",        "collab/.keep");
  mkPlaceholder("shared",        "shared/.keep");
  mkPlaceholder("shared-refs",   "shared/references-and-databases/.keep");
  mkPlaceholder("shared-users",  "shared/users/.keep");
  mkPlaceholder("samplesheets",  "samplesheets/.keep");
  mkPlaceholder("pipelines",     "pipelines/.keep");

  // ------------------------------------------------------------------
  // 3. Group-admin IAM policy (always declared; cheap to keep on suspend)
  // ------------------------------------------------------------------
  const groupAdminIamPolicy = new minio.IamPolicy(
    `${ns}-iam-group-admin`,
    {
      name: minioPolicyGroupAdmin(ns),
      policy: minioGroupAdminPolicy(ns),
    },
    opts,
  );

  const credOutputs: pulumi.Output<{ key: string; cred: PrincipalCredential }>[] = [];

  // ------------------------------------------------------------------
  // 4. Submit account (active only)
  // ------------------------------------------------------------------
  if (spec.submitAccount && isActive) {
    const submitIamPolicy = new minio.IamPolicy(
      `${ns}-iam-submit`,
      {
        name: minioPolicySubmit(ns),
        policy: minioPipelinePolicy(ns),
      },
      opts,
    );

    const submitMinioUser = principalSubmit(ns);
    const submitPassword = nomadTokenSecrets.apply(
      (m) => m[submitMinioUser] ?? "",
    );

    provisionMinioUser(ns, "submit", submitMinioUser, submitPassword, submitIamPolicy, opts);

    const submitScope = "pipelines/rw+samplesheets/ro+shared/ro";
    writeNomadIamVar(ns, submitMinioUser, submitPassword, "submit", submitScope, opts);

    credOutputs.push(
      submitPassword.apply((secret) => ({
        key: submitMinioUser,
        cred: {
          accessKey: submitMinioUser,
          secretKey: secret,
          role: "submit" as const,
          scope: submitScope,
          bucket: ns,
        },
      })),
    );
  }

  // ------------------------------------------------------------------
  // 5. Per-member IAM policies + users (active only)
  // ------------------------------------------------------------------
  if (isActive) {
    // Per-member, per-role: each role gets its own MinIO user, IAM attachment,
    // and Nomad credential variable. group-member roles share one per-user
    // IAM policy (and folder placeholders) regardless of how many other roles
    // the same person holds.
    const memberFolderInitialised = new Set<string>();

    for (const member of spec.members ?? []) {
      const roles = memberRoles(member);

      for (const role of roles) {
        const minioUsername = memberPrincipal(ns, role, member.name);
        const slug = memberResourceSlug(role, member.name);

        const memberPassword = nomadTokenSecrets.apply(
          (m) => m[minioUsername] ?? "",
        );

        let iamPolicyResource: minio.IamPolicy;
        let roleName: "group-admin" | "user";
        let scopeStr: string;

        if (role === "group-admin") {
          iamPolicyResource = groupAdminIamPolicy;
          roleName = "group-admin";
          scopeStr = "bucket-full";
        } else {
          iamPolicyResource = new minio.IamPolicy(
            `${ns}-iam-user-${member.name}`,
            {
              name: minioPolicyUser(ns, member.name),
              policy: minioMemberPolicy(ns, member.name),
            },
            opts,
          );
          roleName = "user";
          scopeStr = `users/${member.name}/rw+shared/users/${member.name}/rw+shared/ro+samplesheets/ro+pipelines/ro`;

          if (!memberFolderInitialised.has(member.name)) {
            mkPlaceholder(`users-${member.name}`,        `users/${member.name}/.keep`);
            mkPlaceholder(`shared-users-${member.name}`, `shared/users/${member.name}/.keep`);
            memberFolderInitialised.add(member.name);
          }
        }

        provisionMinioUser(ns, slug, minioUsername, memberPassword, iamPolicyResource, opts);

        writeNomadIamVar(ns, minioUsername, memberPassword, roleName, scopeStr, opts);

        const capRole = roleName;
        const capScope = scopeStr;
        credOutputs.push(
          memberPassword.apply((secret) => ({
            key: minioUsername,
            cred: {
              accessKey: minioUsername,
              secretKey: secret,
              role: capRole,
              scope: capScope,
              bucket: ns,
            },
          })),
        );
      }
    }
  }

  // ------------------------------------------------------------------
  // 6. Collaborators (active only, expiresAt > now).
  //
  // Expired collaborators are skipped entirely — their IAM user, IAM policy,
  // and `collab/<name>/.keep` placeholder are removed by Pulumi diff on the
  // first `pulumi up` after expiry. Bucket data under collab/<name>/ is
  // preserved (the placeholder `.keep` deletion does not remove the prefix's
  // other objects). Operators still need to remove the YAML entry to keep
  // the spec tidy.
  //
  // Password requirement: collaborators do NOT get a Nomad token; their MinIO
  // password must be pre-seeded by the operator with
  //   pulumi config set --secret collabPassword:<ns>_collab-<name> <password>
  // Missing → hard fail (no weak fallback).
  // ------------------------------------------------------------------
  const now = new Date();
  if (isActive) {
    for (const collab of spec.collaborators ?? []) {
      const expiry = new Date(collab.expiresAt + "T00:00:00Z");
      if (expiry <= now) {
        pulumi.log.warn(
          `Collaborator ${collab.name} in ${ns} expired ${collab.expiresAt}; resources destroyed (collab/${collab.name}/ data preserved).`,
        );
        continue;
      }

      const collabMinioUser = principalCollab(ns, collab.name);

      const collabIamPolicy = new minio.IamPolicy(
        `${ns}-iam-collab-${collab.name}`,
        {
          name: minioPolicyCollab(ns, collab.name),
          policy: minioCollaboratorPolicy(ns, collab.name),
        },
        opts,
      );

      // Pre-seeded via `pulumi config set --secret collabPassword_<principal> <pw>`.
      // Hard-fail if missing — no fallback to a guessable password.
      // Note: Pulumi config keys cannot contain ':' in the name, so we use '_'
      // as the separator after the "collabPassword" prefix.
      const configKey = `collabPassword_${collabMinioUser}`;
      const collabPassword = cfg.requireSecret(configKey);

      provisionMinioUser(
        ns,
        `collab-${collab.name}`,
        collabMinioUser,
        collabPassword,
        collabIamPolicy,
        opts,
      );

      mkPlaceholder(`collab-${collab.name}`, `collab/${collab.name}/.keep`);

      const collabScope = `collab/${collab.name}/rw+shared/ro`;
      writeNomadIamVar(ns, collabMinioUser, collabPassword, "collab", collabScope, opts);

      credOutputs.push(
        collabPassword.apply((secret) => ({
          key: collabMinioUser,
          cred: {
            accessKey: collabMinioUser,
            secretKey: secret,
            role: "collab" as const,
            scope: collabScope,
            bucket: ns,
          },
        })),
      );
    }
  }

  // ------------------------------------------------------------------
  // 7. Merge credentials
  // ------------------------------------------------------------------
  const credentials: pulumi.Output<Record<string, PrincipalCredential>> =
    credOutputs.length > 0
      ? pulumi.secret(
          pulumi.all(credOutputs).apply((entries) =>
            entries.reduce(
              (acc, { key, cred }) => {
                acc[key] = cred;
                return acc;
              },
              {} as Record<string, PrincipalCredential>,
            ),
          ),
        )
      : pulumi.output({} as Record<string, PrincipalCredential>);

  return {
    bucketName: bucket.bucket,
    credentials,
  };
}
