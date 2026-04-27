// ---------------------------------------------------------------------------
// policies.ts — Nomad ACL policy HCL and MinIO IAM JSON generators.
//
// These policies are authoritative for *userspace* (per-workspace member
// access). Cluster-tier ACL bootstrap (root/agent/operator policies, the
// abc-services namespace itself, etc.) lives in `acl/` under the
// abc-nodes-basic / abc-nodes-enhanced services and is NOT mirrored here.
// ---------------------------------------------------------------------------

// Re-exported here so existing callers can keep importing policy-name helpers
// from "./policies" without churn. New code should import them directly from
// "./naming".
export {
  minioPolicyGroupAdmin as minioGroupAdminPolicyName,
  minioPolicyUser       as minioUserPolicyName,
  minioPolicyCollab     as minioCollaboratorPolicyName,
} from "./naming";

// ============================================================
// Nomad ACL policies (HCL)
// ============================================================

export interface GroupAdminPolicyOpts {
  /** Include `alloc-node-exec` (host-level exec). Default: false. */
  allocNodeExec: boolean;
}

/**
 * group-admin: full write to namespace, can cancel/scale jobs and exec into
 * allocs. `alloc-node-exec` (host-level exec) is opt-in via opts.
 * Cannot touch other namespaces or cluster operator/agent config.
 */
export function nomadGroupAdminPolicy(namespace: string, opts: GroupAdminPolicyOpts): string {
  const caps = [
    "alloc-exec",
    "alloc-lifecycle",
    ...(opts.allocNodeExec ? ["alloc-node-exec"] : []),
    "dispatch-job",
    "list-jobs",
    "parse-job",
    "read-fs",
    "read-job",
    "read-logs",
    "scale-job",
    "submit-job",
  ];
  return `
# group-admin: full control in ${namespace}
# Can do everything in the group namespace including cancelling other members' jobs,
# reading all allocations, and submitting raw_exec tasks for debugging.
# Cannot touch other namespaces or cluster-level operator/agent config.
namespace "${namespace}" {
  policy = "write"
  capabilities = [
${caps.map((c) => `    "${c}",`).join("\n")}
  ]
}

# parse-job in default namespace allows abc CLI HCL validation step.
namespace "default" {
  capabilities = [
    "parse-job",
  ]
}

# Group admin can see nodes to diagnose placement failures.
node  { policy = "read" }
agent { policy = "read" }
`.trim();
}

/**
 * member: submit and inspect jobs; cannot cancel others' jobs (honour system).
 * Also gets parse-job in default namespace so abc CLI HCL validation works.
 */
export function nomadMemberPolicy(namespace: string): string {
  return `
# member: standard researcher token for ${namespace}
# Can submit and manage their OWN jobs; cannot see or cancel others' jobs
# (Nomad OSS does not enforce per-user job ownership — honour system + group-admin oversight).
# Can read logs and alloc filesystem for any job in the namespace.
namespace "${namespace}" {
  capabilities = [
    "submit-job",
    "parse-job",
    "dispatch-job",
    "list-jobs",
    "read-job",
    "read-logs",
    "read-fs",
    "alloc-lifecycle",
    "alloc-exec",
  ]
}

# parse-job in default namespace allows abc CLI to validate HCL without submitting.
namespace "default" {
  capabilities = [
    "parse-job",
  ]
}

node  { policy = "read" }
agent { policy = "read" }
`.trim();
}

/**
 * submit: service-account policy for the nf-nomad Nextflow plugin.
 * One shared token per group; embedded in the group's nextflow config.
 * DO NOT issue this token to individual users.
 */
export function nomadSubmitPolicy(namespace: string): string {
  return `
# submit: service-account policy for nf-nomad Nextflow plugin in ${namespace}
# One shared token per group; embedded in the group's nextflow config.
# DO NOT issue this token to individual users.
namespace "${namespace}" {
  capabilities = [
    "submit-job",
    "dispatch-job",
    "read-job",
    "list-jobs",
    "alloc-lifecycle",
    "read-logs",
    "read-fs",
    "alloc-exec",
  ]
}

node  { policy = "read" }
agent { policy = "read" }
`.trim();
}

// ============================================================
// S3 IAM policies (JSON) — applied to the rustfs/MinIO backend
//
// Bucket layout:
//   users/<username>/            — member private workspace
//   shared/                      — read-only for everyone (references, datasets)
//   shared/users/<username>/     — member's own contribution to the shared area
//   collab/<name>/               — collaborator scoped r/w
//   samplesheets/                — read by members + submit; managed by group-admin
//   pipelines/                   — written by submit SA; read by members + group-admin
//
// ListBucket scoping note:
//   These policies grant unconditional `s3:ListBucket` rather than scoping it
//   via `s3:prefix` Condition. RustFS (the S3 backend in use) does not yet
//   evaluate the `s3:prefix` condition variable — adding any Condition denies
//   ListBucket entirely. Members can therefore enumerate peer file *names*
//   inside the bucket, but `s3:GetObject`/`s3:PutObject`/`s3:DeleteObject`
//   are still scoped to the member's own prefixes via Resource patterns,
//   which RustFS does evaluate correctly. The threat model accepts a
//   filename-disclosure leak in exchange for portability across MinIO and
//   RustFS; data isolation is still enforced.
// ============================================================

const rw = [
  "s3:GetObject",
  "s3:PutObject",
  "s3:DeleteObject",
  "s3:AbortMultipartUpload",
  "s3:ListMultipartUploadParts",
] as const;

const ro = ["s3:GetObject"] as const;

function arn(bucket: string, prefix: string): string {
  return `arn:aws:s3:::${bucket}/${prefix}*`;
}
function bucketArn(bucket: string): string {
  return `arn:aws:s3:::${bucket}`;
}

/** group-admin: unrestricted access to the entire namespace bucket + tusd staging. */
export function minioGroupAdminPolicy(bucket: string): string {
  return JSON.stringify(
    {
      Version: "2012-10-17",
      Statement: [
        {
          Sid: "GroupAdminFullBucketAccess",
          Effect: "Allow",
          Action: ["s3:*"],
          Resource: [bucketArn(bucket), `${bucketArn(bucket)}/*`],
        },
        {
          Sid: "TusdUploadAreaReadAll",
          Effect: "Allow",
          Action: ["s3:GetObject", "s3:ListBucket", "s3:DeleteObject"],
          Resource: ["arn:aws:s3:::tusd", "arn:aws:s3:::tusd/uploads/*"],
        },
      ],
    },
    null,
    2,
  );
}

/**
 * member: scoped access reflecting the bucket layout.
 *
 * R/W:  users/<username>/, shared/users/<username>/
 * R/O:  shared/, samplesheets/, pipelines/
 * tusd: own uploads/<username>/ put + get
 */
export function minioMemberPolicy(bucket: string, username: string): string {
  const privatePrefix    = `users/${username}/`;
  const sharedUserPrefix = `shared/users/${username}/`;

  return JSON.stringify(
    {
      Version: "2012-10-17",
      Statement: [
        {
          Sid: "GetBucketLocation",
          Effect: "Allow",
          Action: ["s3:GetBucketLocation"],
          Resource: [bucketArn(bucket)],
        },
        {
          Sid: "ListBucket",
          Effect: "Allow",
          Action: ["s3:ListBucket"],
          Resource: [bucketArn(bucket)],
          // Unconditional — see policies.ts header comment on RustFS
          // s3:prefix Condition unsupported.
        },
        {
          Sid: "OwnPrefixReadWrite",
          Effect: "Allow",
          Action: [...rw],
          Resource: [
            arn(bucket, privatePrefix),
            arn(bucket, sharedUserPrefix),
          ],
        },
        {
          Sid: "SharedSamplesheetsAndPipelinesReadOnly",
          Effect: "Allow",
          Action: [...ro],
          Resource: [
            arn(bucket, "shared/"),
            arn(bucket, "samplesheets/"),
            arn(bucket, "pipelines/"),
          ],
        },
        {
          Sid: "TusdUploadOwnFiles",
          Effect: "Allow",
          Action: ["s3:PutObject", "s3:GetObject"],
          Resource: [`arn:aws:s3:::tusd/uploads/${username}/*`],
        },
      ],
    },
    null,
    2,
  );
}

/** collaborator: time-bounded; r/w collab/<name>/ + r/o shared/. */
export function minioCollaboratorPolicy(bucket: string, collabName: string): string {
  const collabPrefix = `collab/${collabName}/`;

  return JSON.stringify(
    {
      Version: "2012-10-17",
      Statement: [
        {
          Sid: "GetBucketLocation",
          Effect: "Allow",
          Action: ["s3:GetBucketLocation"],
          Resource: [bucketArn(bucket)],
        },
        {
          Sid: "ListBucket",
          Effect: "Allow",
          Action: ["s3:ListBucket"],
          Resource: [bucketArn(bucket)],
          // Unconditional — see policies.ts header note on RustFS.
        },
        {
          Sid: "CollabPrefixReadWrite",
          Effect: "Allow",
          Action: [...rw],
          Resource: [arn(bucket, collabPrefix)],
        },
        {
          Sid: "SharedReadOnly",
          Effect: "Allow",
          Action: [...ro],
          Resource: [arn(bucket, "shared/")],
        },
      ],
    },
    null,
    2,
  );
}

/** pipeline submit SA: r/w pipelines/, r/o samplesheets/ + shared/. */
export function minioPipelinePolicy(bucket: string): string {
  return JSON.stringify(
    {
      Version: "2012-10-17",
      Statement: [
        {
          Sid: "GetBucketLocation",
          Effect: "Allow",
          Action: ["s3:GetBucketLocation"],
          Resource: [bucketArn(bucket)],
        },
        {
          Sid: "ListBucket",
          Effect: "Allow",
          Action: ["s3:ListBucket"],
          Resource: [bucketArn(bucket)],
          // Unconditional — see policies.ts header note on RustFS.
        },
        {
          Sid: "PipelinesReadWrite",
          Effect: "Allow",
          Action: [...rw],
          Resource: [arn(bucket, "pipelines/")],
        },
        {
          Sid: "SamplesheetsAndSharedReadOnly",
          Effect: "Allow",
          Action: [...ro],
          Resource: [
            arn(bucket, "samplesheets/"),
            arn(bucket, "shared/"),
          ],
        },
      ],
    },
    null,
    2,
  );
}
