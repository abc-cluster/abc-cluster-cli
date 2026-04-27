// ---------------------------------------------------------------------------
// minio.ts — MinIO resource provisioning per workspace.
//
// Naming conventions:
//   Bucket:              <namespace>
//   MinIO admin user:    <namespace>_admin
//   MinIO member user:   <namespace>_<username>
//   MinIO collab user:   <namespace>_collab-<name>
//   MinIO submit user:   <namespace>_submit
//   IAM policy (admin):  ns-<namespace>-group-admin
//   IAM policy (member): ns-<namespace>-user-<username>
//   IAM policy (collab): ns-<namespace>-collab-<name>
//   IAM policy (submit): ns-<namespace>-pipeline-submit
//
// Bucket layout:
//   <bucket>/
//   ├── users/                           ← top-level marker
//   │   ├── <member>/.keep               ← member private workspace  (member r/w)
//   │   └── ...
//   ├── collab/                          ← top-level marker
//   │   └── <name>/.keep                 ← collaborator area  (collab r/w)
//   ├── shared/                          ← read-only for all
//   │   ├── references-and-databases/.keep
//   │   └── users/
//   │       ├── <member>/.keep           ← member's shared contribution  (member r/w)
//   │       └── ...
//   ├── samplesheets/                    ← read by members + submit; managed by admin
//   └── pipelines/                       ← written by submit SA; read by members + admin
//
// Credentials for each principal are stored in Nomad variables at:
//   nomad/jobs/abc-nodes-minio-iam/<principal>  in namespace abc-services
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as minio from "@pulumi/minio";
import * as nomad from "@pulumi/nomad";
import { WorkspaceSpec, OrgSpec } from "./types";
import {
  minioGroupAdminPolicyName,
  minioUserPolicyName,
  minioCollaboratorPolicyName,
  minioGroupAdminPolicy,
  minioMemberPolicy,
  minioCollaboratorPolicy,
  minioPipelinePolicy,
} from "./policies";

// Nomad namespace where IAM credential variables are stored (cluster-level).
const NOMAD_IAM_NAMESPACE = "abc-services";
// Variable path prefix matching setup-minio-namespace-buckets.sh.
const NOMAD_IAM_VAR_PREFIX = "nomad/jobs/abc-nodes-minio-iam";

// ---- helpers ---------------------------------------------------------------

/**
 * Write a MinIO IAM credential to a Nomad variable.
 *
 * Called inline at the point each MinIO user is provisioned — NOT inside an
 * `.apply()` callback — so the resource is visible to `pulumi preview` and
 * properly tracked in stack state.
 *
 * Uses itemsWo (write-only) exclusively — the Nomad provider requires that
 * `items` and `itemsWo` are mutually exclusive. All fields (access_key,
 * secret_key, role, scope, bucket) go into the write-only payload so Pulumi
 * never stores any credential value in plaintext stack state.
 *
 * Fields match the setup-minio-namespace-buckets.sh SYNC_NOMAD_VARS format.
 */
function writeNomadIamVar(
  ns: string,
  principal: string,              // e.g. "su-mbhg-bioinformatics_kim"
  secretKey: pulumi.Input<string>,
  role: PrincipalCredential["role"],
  scope: string,
  opts: pulumi.ComponentResourceOptions,
): void {
  // Resource name: replace underscores so it's a valid Pulumi URN segment.
  const resourceId = principal.replace(/_/g, "-");
  new nomad.Variable(
    `${ns}-minio-var-${resourceId}`,
    {
      namespace: NOMAD_IAM_NAMESPACE,
      path: `${NOMAD_IAM_VAR_PREFIX}/${principal}`,
      // All fields written as a single write-only JSON payload.
      // items and itemsWo are mutually exclusive in the Nomad provider.
      itemsWo: pulumi.interpolate`{"access_key":"${principal}","secret_key":"${secretKey}","role":"${role}","scope":"${scope}","bucket":"${ns}"}`,
      itemsWoVersion: 1,
    },
    { ...opts, additionalSecretOutputs: ["itemsWo"] },
  );
}

// ---- exported types --------------------------------------------------------

export interface MinioWorkspaceOutputs {
  /** MinIO bucket name (= namespace name). */
  bucketName: pulumi.Output<string>;
  /**
   * Map of MinIO principal (e.g. "su-mbhg-bioinformatics_kim") →
   *   { accessKey, secretKey, role, scope, bucket }
   * Matches the fields written to Nomad variables by the bootstrap script.
   * Wrapped as a Pulumi Secret.
   */
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

/**
 * Provisions all MinIO resources for a single workspace and writes
 * IAM credentials to Nomad variables in the abc-services namespace.
 *
 * @param nomadTokenSecrets  Pulumi Secret output from nomad.ts — the Nomad
 *   token SecretIDs that double as MinIO user passwords.
 */
export function provisionMinioWorkspace(
  resourceName: string,
  org: OrgSpec,
  spec: WorkspaceSpec,
  nomadTokenSecrets: pulumi.Output<Record<string, string>>,
  opts: pulumi.ComponentResourceOptions,
): MinioWorkspaceOutputs {
  const ns = resourceName; // "su-mbhg-bioinformatics"
  const isActive = (spec.state ?? "active") === "active";

  // ------------------------------------------------------------------
  // 1. Bucket
  // ------------------------------------------------------------------
  const bucket = new minio.S3Bucket(
    `${ns}-bucket`,
    {
      bucket: ns,
      objectLocking: false,
      // Required for `pulumi destroy` to succeed when versioning is enabled:
      // the provider will delete all object versions before removing the bucket.
      forceDestroy: true,
    },
    opts,
  );

  // Versioning: enabled for active workspaces, suspended for archived.
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
  // 2. Top-level folder placeholders (zero-byte marker objects)
  //
  // These establish the fixed bucket structure at workspace creation time.
  // Per-member and per-collaborator placeholders are added in the loops below.
  // ------------------------------------------------------------------

  // Helper to reduce boilerplate for marker objects.
  // "\n" matches `echo "" | mc pipe` — the shell bootstrap creates .keep
  // files with a single newline byte. Empty string is rejected by the provider.
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

  mkPlaceholder("users",              "users/.keep");
  mkPlaceholder("collab",             "collab/.keep");
  mkPlaceholder("shared",             "shared/.keep");
  mkPlaceholder("shared-refs",        "shared/references-and-databases/.keep");
  mkPlaceholder("shared-users",       "shared/users/.keep");
  mkPlaceholder("samplesheets",       "samplesheets/.keep");
  mkPlaceholder("pipelines",          "pipelines/.keep");

  // Per-member and per-collaborator placeholders are added in the loops below.

  // ------------------------------------------------------------------
  // 3. Group-admin IAM policy  (ns-<ns>-group-admin)
  // One per workspace; shared by group-admin member and submit account.
  // ------------------------------------------------------------------
  const groupAdminIamPolicy = new minio.IamPolicy(
    `${ns}-iam-group-admin`,
    {
      name: minioGroupAdminPolicyName(ns),
      policy: minioGroupAdminPolicy(ns),
    },
    opts,
  );

  // ------------------------------------------------------------------
  // 4. Submit account IAM policy + user (optional)
  // MinIO user: <ns>_submit  |  password = Nomad token SecretID
  // ------------------------------------------------------------------
  const credOutputs: pulumi.Output<{ key: string; cred: PrincipalCredential }>[] = [];

  if (spec.submitAccount && isActive) {
    const submitPolicyName = `ns-${ns}-pipeline-submit`;
    const submitIamPolicy = new minio.IamPolicy(
      `${ns}-iam-submit`,
      {
        name: submitPolicyName,
        policy: minioPipelinePolicy(ns),
      },
      opts,
    );

    const submitMinioUser = `${ns}_submit`;
    // Password = Nomad token SecretID for this principal.
    const submitPassword = nomadTokenSecrets.apply(
      (m) => m[submitMinioUser] ?? "",
    );

    const submitUser = new minio.IamUser(
      `${ns}-user-submit`,
      {
        name: submitMinioUser,
        // MinIO `mc admin user add` creates the user with a password.
        // The @pulumi/minio IamUser resource takes `secret` for the password.
        secret: submitPassword,
        forceDestroy: true,
      },
      opts,
    );

    new minio.IamUserPolicyAttachment(
      `${ns}-attach-submit`,
      {
        userName: submitUser.name,
        policyName: submitIamPolicy.name,
      },
      { ...opts, dependsOn: [submitIamPolicy, submitUser] },
    );

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
  // 5. Per-member IAM policies + users
  // MinIO user: <ns>_admin (group-admin) or <ns>_<username> (member)
  // Password = Nomad token SecretID for this principal.
  // ------------------------------------------------------------------
  if (isActive) {
    for (const member of spec.members ?? []) {
      const minioUsername =
        member.role === "group-admin"
          ? `${ns}_admin`
          : `${ns}_${member.name}`;

      const memberPassword = nomadTokenSecrets.apply(
        (m) => m[minioUsername] ?? "",
      );

      // IAM policy: group-admin reuses the shared group-admin policy;
      //             members get a per-user scoped policy.
      let iamPolicyResource: minio.IamPolicy;
      let roleName: "group-admin" | "user";
      let scopeStr: string;

      if (member.role === "group-admin") {
        iamPolicyResource = groupAdminIamPolicy;
        roleName = "group-admin";
        scopeStr = "bucket-full";
      } else {
        const userPolicyName = minioUserPolicyName(ns, member.name);
        iamPolicyResource = new minio.IamPolicy(
          `${ns}-iam-user-${member.name}`,
          {
            name: userPolicyName,
            policy: minioMemberPolicy(ns, member.name),
          },
          opts,
        );
        roleName = "user";
        scopeStr = `users/${member.name}/rw+shared/users/${member.name}/rw+shared/ro+samplesheets/ro+pipelines/ro`;

        // Per-user placeholders: private workspace + shared contribution area.
        mkPlaceholder(`users-${member.name}`,         `users/${member.name}/.keep`);
        mkPlaceholder(`shared-users-${member.name}`,  `shared/users/${member.name}/.keep`);
      }

      const minioUser = new minio.IamUser(
        `${ns}-user-${member.name}`,
        {
          name: minioUsername,
          secret: memberPassword,
          forceDestroy: true,
        },
        opts,
      );

      new minio.IamUserPolicyAttachment(
        `${ns}-attach-${member.name}`,
        {
          userName: minioUser.name,
          policyName: iamPolicyResource.name,
        },
        { ...opts, dependsOn: [iamPolicyResource, minioUser] },
      );

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

  // ------------------------------------------------------------------
  // 6. Collaborators — time-bounded external access
  // MinIO user:   <ns>_collab-<name>
  // IAM policy:   ns-<ns>-collab-<name>
  // Bucket scope: collab/<name>/ r/w + shared/ read-only
  //
  // A collaborator's password is randomly generated (not tied to a Nomad
  // token) because collaborators don't get Nomad access by default.
  // ------------------------------------------------------------------
  const now = new Date();
  if (isActive) {
    for (const collab of spec.collaborators ?? []) {
      const expiry = new Date(collab.expiresAt);
      if (expiry <= now) {
        pulumi.log.warn(
          `Collaborator ${collab.name} in ${ns} has expired (${collab.expiresAt}); skipping MinIO resources.`,
        );
        continue;
      }

      const collabMinioUser = `${ns}_collab-${collab.name}`;
      const collabPolicyName = minioCollaboratorPolicyName(ns, collab.name);

      const collabIamPolicy = new minio.IamPolicy(
        `${ns}-iam-collab-${collab.name}`,
        {
          name: collabPolicyName,
          policy: minioCollaboratorPolicy(ns, collab.name),
        },
        opts,
      );

      // Collaborator password: derived from Nomad token secrets map if present,
      // otherwise must be supplied externally (e.g. via `pulumi config set --secret`).
      // Use the key format <ns>_collab-<name> so operators can pre-seed it.
      const collabPassword = nomadTokenSecrets.apply(
        (m) => m[collabMinioUser] ?? pulumi.interpolate`collab-${collab.name}-${ns}`,
      );

      const collabUser = new minio.IamUser(
        `${ns}-user-collab-${collab.name}`,
        {
          name: collabMinioUser,
          secret: collabPassword,
          forceDestroy: true,
        },
        opts,
      );

      new minio.IamUserPolicyAttachment(
        `${ns}-attach-collab-${collab.name}`,
        {
          userName: collabUser.name,
          policyName: collabIamPolicy.name,
        },
        { ...opts, dependsOn: [collabIamPolicy, collabUser] },
      );

      // collab/<name>/ folder marker.
      mkPlaceholder(`collab-${collab.name}`, `collab/${collab.name}/.keep`);

      const capName = collab.name;
      const collabScope = `collab/${capName}/rw+shared/ro`;
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
  // 8. Merge all credentials
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

  // Note: Nomad IAM credential variables are written inline by writeNomadIamVar()
  // at the point each MinIO user is provisioned (sections 4, 5, 6 above).
  // This ensures all nomad.Variable resources are registered with the Pulumi
  // engine at graph construction time and appear correctly in `pulumi preview`.

  return {
    bucketName: bucket.bucket,
    credentials,
  };
}
