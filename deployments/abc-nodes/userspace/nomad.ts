// ---------------------------------------------------------------------------
// nomad.ts — Nomad resource provisioning per workspace.
//
// Creates per workspace:
//   • Namespace (description + meta via @pulumi/nomad; capabilities block
//     applied via local.Command since the provider does not expose it)
//   • ACL policies: <ns>-group-admin | <ns>-member | <ns>-submit (optional)
//   • ACL tokens for each member + optional submit account (active state only)
//   • Non-secret inventory variable at <ns>/meta in the workspace namespace
//
// Lifecycle gating:
//   - active:    everything provisioned
//   - suspended: ACL tokens + member token resources skipped (existing tokens
//                are destroyed by Pulumi diff). Namespace + ACL policies kept.
//   - archived:  same as suspended for now.
//   - deleted:   workspace skipped entirely upstream (index.ts).
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as nomad from "@pulumi/nomad";
import * as command from "@pulumi/command";
import {
  WorkspaceSpec,
  OrgSpec,
  DEFAULT_TASK_DRIVERS,
  JOB_PRIORITY,
} from "./types";
import {
  policyGroupAdmin,
  policyMember,
  policySubmit,
  memberPrincipal,
  memberResourceSlug,
  principalSubmit,
  workspaceMetaVarPath,
} from "./naming";
import {
  nomadGroupAdminPolicy,
  nomadMemberPolicy,
  nomadSubmitPolicy,
} from "./policies";
import { memberRoles } from "./validate";

// ---- exported types --------------------------------------------------------

export interface NomadWorkspaceOutputs {
  /** Nomad namespace name (= resourceName). */
  namespaceName: pulumi.Output<string>;
  /**
   * Map of MinIO principal name → Nomad ACL token SecretID.
   * The SecretID IS the MinIO password (one credential pair per identity).
   * Wrapped as a Pulumi Secret — never stored in plaintext state.
   */
  tokenSecrets: pulumi.Output<Record<string, string>>;
}

// ---- main provisioner ------------------------------------------------------

export function provisionNomadWorkspace(
  resourceName: string,
  org: OrgSpec,
  spec: WorkspaceSpec,
  opts: pulumi.ComponentResourceOptions,
): NomadWorkspaceOutputs {
  const ns = resourceName; // "su-mbhg-bioinformatics"
  const isActive = (spec.state ?? "active") === "active";

  const drivers = { ...DEFAULT_TASK_DRIVERS, ...(spec.taskDrivers ?? {}) };
  const jobPriority = JOB_PRIORITY[spec.priority ?? "normal"];
  const ntfyTopic = spec.ntfyTopic ?? `${ns}-jobs`;
  const description =
    spec.description ??
    `${org.displayName ?? org.name} / ${spec.name} — pipelines and ad-hoc batch jobs`;

  // ------------------------------------------------------------------
  // 1. Namespace (description + meta only — capabilities applied below)
  // ------------------------------------------------------------------
  const namespace = new nomad.Namespace(
    `${ns}-ns`,
    {
      name: ns,
      description,
      meta: {
        group: ns,
        priority: spec.priority ?? "normal",
        job_priority: String(jobPriority),
        s3_bucket: ns,
        ntfy_topic: ntfyTopic,
        managedBy: "abc-userspace-pulumi",
        ...(spec.contact ? { contact: spec.contact } : {}),
      },
    },
    opts,
  );

  // ------------------------------------------------------------------
  // 1b. Apply task driver capabilities via the abc nomad CLI wrapper.
  //
  // The @pulumi/nomad Namespace resource does not expose the capabilities
  // block, so we shell out through `abc admin services nomad cli` to keep
  // the same auth/context (Nomad address, token, region) as the provider.
  //
  // Triggers: re-run when the driver allow-list, ntfy topic, contact, or
  // description changes. Destroy is a no-op — the namespace lifecycle is
  // owned by the @pulumi/nomad resource above; deleting the workspace
  // removes the namespace entirely.
  // ------------------------------------------------------------------
  const namespaceJson = JSON.stringify({
    Name: ns,
    Description: description,
    Capabilities: {
      EnabledTaskDrivers: drivers.enabled,
      DisabledTaskDrivers: drivers.disabled,
    },
    Meta: {
      group: ns,
      priority: spec.priority ?? "normal",
      job_priority: String(jobPriority),
      s3_bucket: ns,
      ntfy_topic: ntfyTopic,
      managedBy: "abc-userspace-pulumi",
      ...(spec.contact ? { contact: spec.contact } : {}),
    },
  });
  // Nomad's `namespace apply -json -` (stdin) panics in current versions; we
  // stage the spec into a per-resource temp file and pass its path instead.
  const applyCmd =
    `set -eu; T=$(mktemp -t ns-${ns}.XXXXXX); ` +
    `printf '%s' "$NS_JSON" > "$T"; ` +
    `abc admin services nomad cli -- namespace apply -json "$T"; ` +
    `rm -f "$T"`;
  new command.local.Command(
    `${ns}-ns-capabilities`,
    {
      create: applyCmd,
      update: applyCmd,
      environment: { NS_JSON: namespaceJson },
      triggers: [namespaceJson],
    },
    { ...opts, dependsOn: [namespace] },
  );

  // ------------------------------------------------------------------
  // 2. ACL Policies (always created so resume from suspended is cheap)
  // ------------------------------------------------------------------
  const groupAdminPolicy = new nomad.AclPolicy(
    `${ns}-policy-group-admin`,
    {
      name: policyGroupAdmin(ns),
      description: `Group admin — ${ns}`,
      rulesHcl: nomadGroupAdminPolicy(ns, {
        allocNodeExec: spec.groupAdminNodeExec === true,
      }),
    },
    { ...opts, dependsOn: [namespace] },
  );

  const memberPolicy = new nomad.AclPolicy(
    `${ns}-policy-member`,
    {
      name: policyMember(ns),
      description: `Member — ${ns}`,
      rulesHcl: nomadMemberPolicy(ns),
    },
    { ...opts, dependsOn: [namespace] },
  );

  let submitPolicy: nomad.AclPolicy | undefined;
  if (spec.submitAccount) {
    submitPolicy = new nomad.AclPolicy(
      `${ns}-policy-submit`,
      {
        name: policySubmit(ns),
        description: `nf-nomad submit — ${ns}`,
        rulesHcl: nomadSubmitPolicy(ns),
      },
      { ...opts, dependsOn: [namespace] },
    );
  }

  // ------------------------------------------------------------------
  // 3. Member ACL Tokens (active state only)
  // ------------------------------------------------------------------
  const secretOutputs: pulumi.Output<{ principal: string; secret: string }>[] = [];

  if (!isActive) {
    pulumi.log.info(
      `Workspace ${ns} is in state=${spec.state}; ACL tokens and MinIO users will be destroyed (data preserved).`,
    );
  }

  if (isActive) {
    for (const member of spec.members ?? []) {
      // A member with multiple roles (e.g. ["group-member", "group-admin"]) gets
      // one Nomad token per role — `abc --sudo` selects the group-admin token
      // for elevated submissions while the default token stays at group-member.
      const roles = memberRoles(member);
      for (const role of roles) {
        const policyResource =
          role === "group-admin" ? groupAdminPolicy : memberPolicy;
        const tokenName = memberPrincipal(ns, role, member.name);
        const slug = memberResourceSlug(role, member.name);

        const token = new nomad.AclToken(
          `${ns}-token-${slug}`,
          {
            name: tokenName,
            type: "client",
            policies: [policyResource.name],
            global: false,
          },
          { ...opts, dependsOn: [policyResource] },
        );

        secretOutputs.push(
          token.secretId.apply((secret) => ({ principal: tokenName, secret })),
        );
      }
    }

    if (spec.submitAccount && submitPolicy) {
      const submitTokenName = principalSubmit(ns);
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
  // 4. <ns>/meta — non-secret inventory variable in the workspace namespace
  // ------------------------------------------------------------------
  new nomad.Variable(
    `${ns}-var-meta`,
    {
      namespace: ns,
      path: workspaceMetaVarPath(ns),
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
          (spec.members ?? []).map((m) => {
            const roles = memberRoles(m);
            return {
              name: m.name,
              roles,
              principals: roles.map((r) => memberPrincipal(ns, r, m.name)),
            };
          }),
        ),
        hasSubmitAccount: spec.submitAccount ? "true" : "false",
      },
    },
    { ...opts, dependsOn: [namespace] },
  );

  // ------------------------------------------------------------------
  // 5. Merge token secrets (Pulumi Secret)
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
