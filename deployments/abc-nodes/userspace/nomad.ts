// ---------------------------------------------------------------------------
// nomad.ts — Nomad resource provisioning per workspace.
//
// Creates:
//   • Nomad namespace  (with capabilities + meta matching acl/namespaces/*.hcl)
//   • ACL policies:   <ns>-group-admin  |  <ns>-member  |  <ns>-submit (optional)
//   • ACL tokens:     <ns>_<username>  per member  +  <ns>_submit  for submit account
//   • Nomad variable: <ns>/meta in the workspace namespace (non-secret inventory)
//
// Credential variables (access_key / secret_key per MinIO principal) are written
// to  nomad/jobs/abc-nodes-minio-iam/<principal>  in the  abc-services  namespace
// by minio.ts after MinIO resources are created — matching the path used by the
// setup-minio-namespace-buckets.sh bootstrap script.
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as nomad from "@pulumi/nomad";
import {
  WorkspaceSpec,
  OrgSpec,
  MemberSpec,
  DEFAULT_TASK_DRIVERS,
  JOB_PRIORITY,
} from "./types";
import {
  nomadGroupAdminPolicy,
  nomadMemberPolicy,
  nomadSubmitPolicy,
} from "./policies";

// ---- exported types --------------------------------------------------------

export interface NomadWorkspaceOutputs {
  /** Nomad namespace name (= resourceName). */
  namespaceName: pulumi.Output<string>;
  /**
   * Map of MinIO principal name → Nomad ACL token SecretID.
   * The SecretID IS the MinIO password (one credential pair per identity).
   * Wrapped as a Pulumi Secret — never stored in plaintext state.
   *
   * Key format: <namespace>_<username>  (matches MinIO user naming convention)
   */
  tokenSecrets: pulumi.Output<Record<string, string>>;
}

// ---- main provisioner ------------------------------------------------------

/**
 * Provisions all Nomad resources for a single workspace.
 */
export function provisionNomadWorkspace(
  resourceName: string,
  org: OrgSpec,
  spec: WorkspaceSpec,
  opts: pulumi.ComponentResourceOptions,
): NomadWorkspaceOutputs {
  const ns = resourceName; // "su-mbhg-bioinformatics"
  const isActive = (spec.state ?? "active") === "active";

  const drivers = spec.taskDrivers ?? DEFAULT_TASK_DRIVERS;
  const jobPriority = JOB_PRIORITY[spec.priority ?? "normal"];
  const ntfyTopic = spec.ntfyTopic ?? `${ns}-jobs`;

  // ------------------------------------------------------------------
  // 1. Namespace
  // Mirrors the structure of acl/namespaces/su-mbhg-bioinformatics.hcl
  // ------------------------------------------------------------------
  const namespace = new nomad.Namespace(
    `${ns}-ns`,
    {
      name: ns,
      description:
        spec.description ??
        `${org.displayName ?? org.name} / ${spec.name} — pipelines and ad-hoc batch jobs`,
      meta: {
        group: ns,
        priority: spec.priority ?? "normal",
        job_priority: String(jobPriority),
        s3_bucket: ns,
        ntfy_topic: ntfyTopic,
        managedBy: "abc-userspace-pulumi",
        ...(spec.contact ? { contact: spec.contact } : {}),
      },
      // Note: capabilities block (enabled/disabled task drivers) is not yet
      // supported as a first-class field in the @pulumi/nomad Namespace resource.
      // Until the provider exposes it, apply acl/namespaces/<ns>.hcl separately
      // or extend this with a CustomResource once the provider adds support.
    },
    opts,
  );

  // ------------------------------------------------------------------
  // 2. ACL Policies
  // Naming matches acl/policies/<ns>-{group-admin,member,submit}.hcl
  // ------------------------------------------------------------------
  const groupAdminPolicy = new nomad.AclPolicy(
    `${ns}-policy-group-admin`,
    {
      name: `${ns}-group-admin`,
      description: `Group admin — ${ns}`,
      rulesHcl: nomadGroupAdminPolicy(ns),
    },
    { ...opts, dependsOn: [namespace] },
  );

  const memberPolicy = new nomad.AclPolicy(
    `${ns}-policy-member`,
    {
      name: `${ns}-member`,
      description: `Member — ${ns}`,
      rulesHcl: nomadMemberPolicy(ns),
    },
    { ...opts, dependsOn: [namespace] },
  );

  // Submit policy is optional — only if the workspace has a submitAccount.
  let submitPolicy: nomad.AclPolicy | undefined;
  if (spec.submitAccount && isActive) {
    submitPolicy = new nomad.AclPolicy(
      `${ns}-policy-submit`,
      {
        name: `${ns}-submit`,
        description: `nf-nomad submit — ${ns}`,
        rulesHcl: nomadSubmitPolicy(ns),
      },
      { ...opts, dependsOn: [namespace] },
    );
  }

  // ------------------------------------------------------------------
  // 3. Member ACL Tokens
  // Token Name convention: <namespace>_<username>
  // The token SecretID becomes the MinIO user password.
  // ------------------------------------------------------------------
  const secretOutputs: pulumi.Output<{ principal: string; secret: string }>[] = [];

  if (isActive) {
    for (const member of spec.members ?? []) {
      const policyResource =
        member.role === "group-admin" ? groupAdminPolicy : memberPolicy;
      // MinIO admin user is <ns>_admin; regular member is <ns>_<username>
      const tokenName = member.role === "group-admin"
        ? `${ns}_admin`
        : `${ns}_${member.name}`;

      const token = new nomad.AclToken(
        `${ns}-token-${member.name}`,
        {
          name: tokenName,
          type: "client",
          policies: [policyResource.name],
          global: false,
        },
        { ...opts, dependsOn: [policyResource] },
      );

      const principal = tokenName;
      secretOutputs.push(
        token.secretId.apply((secret) => ({ principal, secret })),
      );
    }

    // Submit service account token
    if (spec.submitAccount && submitPolicy) {
      const submitTokenName = `${ns}_submit`;
      const submitToken = new nomad.AclToken(
        `${ns}-token-submit`,
        {
          name: submitTokenName,
          type: "client",
          policies: [submitPolicy.name],
          global: false,
        },
        { ...opts, dependsOn: [submitPolicy] },
      );
      secretOutputs.push(
        submitToken.secretId.apply((secret) => ({
          principal: submitTokenName,
          secret,
        })),
      );
    }
  }

  // ------------------------------------------------------------------
  // 4. Nomad variable: <ns>/meta  (non-secret inventory, in workspace namespace)
  // ------------------------------------------------------------------
  new nomad.Variable(
    `${ns}-var-meta`,
    {
      namespace: ns,
      path: `${ns}/meta`,
      items: {
        org: org.name,
        workspace: spec.name,
        description: spec.description ?? "",
        state: spec.state ?? "active",
        priority: spec.priority ?? "normal",
        job_priority: String(jobPriority),
        ntfy_topic: ntfyTopic,
        s3_bucket: ns,
        managedBy: "abc-userspace-pulumi",
        members: JSON.stringify(
          (spec.members ?? []).map((m) => ({
            name: m.name,
            role: m.role,
            minioUser: m.role === "group-admin" ? `${ns}_admin` : `${ns}_${m.name}`,
          })),
        ),
        hasSubmitAccount: spec.submitAccount ? "true" : "false",
      },
    },
    { ...opts, dependsOn: [namespace] },
  );

  // ------------------------------------------------------------------
  // 5. Merge token secret outputs (wrapped as Pulumi Secret)
  // ------------------------------------------------------------------
  const tokenSecrets: pulumi.Output<Record<string, string>> =
    secretOutputs.length > 0
      ? pulumi.secret(
          pulumi.all(secretOutputs).apply((entries) =>
            entries.reduce(
              (acc, { principal, secret }) => {
                acc[principal] = secret;
                return acc;
              },
              {} as Record<string, string>,
            ),
          ),
        )
      : pulumi.output({} as Record<string, string>);

  return {
    namespaceName: namespace.name,
    tokenSecrets,
  };
}
