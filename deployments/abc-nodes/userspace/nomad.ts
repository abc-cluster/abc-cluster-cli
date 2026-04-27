// ---------------------------------------------------------------------------
// nomad.ts — Workspace-level Nomad resources (per-workspace).
//
// Per-workspace this module emits:
//   • Namespace (description + meta via @pulumi/nomad; capabilities applied
//     via `local.Command` invoking `abc admin services nomad cli -- namespace
//     apply -json …` since the provider doesn't expose the capabilities block)
//   • ACL policies: <ns>-group-admin | <ns>-member | <ns>-submit (optional)
//   • Submit account ACL token (one per workspace, if defined)
//   • <ns>/meta variable (non-secret inventory)
//
// Per-user ACL tokens live in user.ts so a multi-workspace user has a single
// Nomad token with the union of every workspace policy they hold.
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
  principalSubmit,
  workspaceMetaVarPath,
} from "./naming";
import {
  nomadGroupAdminPolicy,
  nomadMemberPolicy,
  nomadSubmitPolicy,
} from "./policies";

// ---- exported types --------------------------------------------------------

export interface WorkspaceNomadOutputs {
  /** Nomad namespace name (= resourceName). */
  namespaceName: pulumi.Output<string>;
  /** ACL policies (referenced by per-user tokens emitted in user.ts). */
  groupAdminPolicy: nomad.AclPolicy;
  memberPolicy: nomad.AclPolicy;
  submitPolicy?: nomad.AclPolicy;
  /** Submit account ACL token, if the workspace defined a submitAccount. */
  submitToken?: nomad.AclToken;
}

// ---- main provisioner ------------------------------------------------------

export function provisionWorkspaceNomad(
  resourceName: string,
  org: OrgSpec,
  spec: WorkspaceSpec,
  opts: pulumi.ComponentResourceOptions,
): WorkspaceNomadOutputs {
  const ns = resourceName;
  const isActive = (spec.state ?? "active") === "active";

  const drivers = { ...DEFAULT_TASK_DRIVERS, ...(spec.taskDrivers ?? {}) };
  const jobPriority = JOB_PRIORITY[spec.priority ?? "normal"];
  const ntfyTopic = spec.ntfyTopic ?? `${ns}-jobs`;
  const description =
    spec.description ??
    `${org.displayName ?? org.name} / ${spec.name} — pipelines and ad-hoc batch jobs`;

  // ------------------------------------------------------------------
  // 1. Namespace (description + meta only)
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
  let submitToken: nomad.AclToken | undefined;
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

    if (isActive) {
      const submitTokenName = principalSubmit(ns);
      submitToken = new nomad.AclToken(
        `${ns}-token-submit`,
        {
          name: submitTokenName,
          type: "client",
          policies: [submitPolicy.name],
          global: false,
        },
        { ...opts, dependsOn: [submitPolicy] },
      );
    }
  }

  // ------------------------------------------------------------------
  // 3. <ns>/meta — non-secret inventory variable (in workspace namespace)
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
          (spec.members ?? []).map((m) => ({ user: m.user, role: m.role })),
        ),
        collaborators: JSON.stringify(
          (spec.collaborators ?? []).map((c) => ({ user: c.user, expiresAt: c.expiresAt })),
        ),
        hasSubmitAccount: spec.submitAccount ? "true" : "false",
      },
    },
    { ...opts, dependsOn: [namespace] },
  );

  if (!isActive) {
    pulumi.log.info(
      `Workspace ${ns} is in state=${spec.state}; per-user ACL tokens and IAM users will not be issued, namespace + bucket preserved.`,
    );
  }

  return {
    namespaceName: namespace.name,
    groupAdminPolicy,
    memberPolicy,
    submitPolicy,
    submitToken,
  };
}
