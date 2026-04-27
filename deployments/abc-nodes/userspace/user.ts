// ---------------------------------------------------------------------------
// user.ts — Per-person Nomad token + RustFS IAM user + policy attachments.
//
// Owns the resources that follow a *person* across all the workspaces they
// touch. For each user in the spec we emit:
//
//   • One Nomad ACL token, name = user.spec.name. Policies attached =
//     union of (workspace, role)-derived policies, deduplicated. Skipped
//     entirely if the user has no member memberships in active workspaces
//     (e.g. a pure collaborator).
//
//   • One RustFS IAM user, access key = user.spec.name. Password = the
//     Nomad token's SecretID for users who have a token; for pure
//     collaborators, password is read from
//     `pulumi config set --secret collabPassword_<user> <pw>`.
//
//   • One `local.Command` per attached policy (member / group-admin /
//     per-collab) running `mc admin policy attach` + IAM cache flush.
//     Compatible with both MinIO and RustFS. See minio.ts for rationale.
//
//   • One Nomad variable per workspace at
//     nomad/jobs/abc-nodes-minio-iam/<user> (write-only itemsWo) carrying
//     the IAM credential blob — same path consumers expect from the legacy
//     bootstrap script.
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as minio from "@pulumi/minio";
import * as nomad from "@pulumi/nomad";
import { ResolvedUser, UserMembership, ResolvedWorkspace } from "./types";
import {
  iamVarPath,
  minioPolicyForRole,
  minioPolicyCollab,
  nomadPolicyForRole,
  policyGroupAdmin,
  policyMember,
} from "./naming";
import { ALLOW_DESTROY, mkAttachCommand } from "./minio";
import { isCollabActive } from "./validate";

// Per-workspace IAM policy resources, keyed by ns. Passed in from index.ts.
export interface WorkspaceIamHandles {
  /** RustFS IAM policies. */
  groupAdminMinio: minio.IamPolicy;
  memberMinio: minio.IamPolicy;
  collabMinio: Record<string, minio.IamPolicy>;
  /** Nomad ACL policies. */
  groupAdminNomad: nomad.AclPolicy;
  memberNomad: nomad.AclPolicy;
}

export interface UserOutputs {
  principal: string;
  /** undefined for pure collaborators (no Nomad token issued). */
  nomadToken?: nomad.AclToken;
  /** undefined for users with zero active memberships. */
  iamUser?: minio.IamUser;
}

/** Pulumi config — used for collaborator passwords. */
const cfg = new pulumi.Config();

const NOMAD_IAM_NAMESPACE  = cfg.get("nomadIamNamespace")  ?? "abc-services";
const NOMAD_IAM_VAR_PREFIX = cfg.get("nomadIamVarPrefix") ?? "nomad/jobs/abc-nodes-minio-iam";

/**
 * Provision all resources owned by a single user.
 *
 * @param user      Spec + resolved memberships
 * @param workspaceHandles  Map ns → IAM/ACL policy resources for that workspace
 * @param opts      Pulumi options (parent etc.)
 */
export function provisionUser(
  user: ResolvedUser,
  workspaceHandles: Map<string, WorkspaceIamHandles>,
  opts: pulumi.ComponentResourceOptions,
): UserOutputs {
  const principal = user.spec.name;

  // ---- partition memberships --------------------------------------------
  const activeMembers: UserMembership[] = [];
  const activeCollabs: UserMembership[] = [];
  for (const m of user.memberships) {
    if ((m.workspace.spec.state ?? "active") !== "active") continue;
    if (m.kind === "member") activeMembers.push(m);
    else if (m.kind === "collab" && isCollabActive(m)) activeCollabs.push(m);
  }

  if (activeMembers.length === 0 && activeCollabs.length === 0) {
    pulumi.log.info(`User ${principal} has no active memberships; skipping resource creation.`);
    return { principal };
  }

  // ---- 1. Nomad token (members only) ------------------------------------
  // Build a deduped list of policy NAMES; the AclToken resource takes string
  // names rather than resource references so `dependsOn` is how we order.
  let nomadToken: nomad.AclToken | undefined;
  let tokenSecretId: pulumi.Output<string> | undefined;

  if (activeMembers.length > 0) {
    const policyNames = new Set<string>();
    const policyDeps: pulumi.Resource[] = [];
    for (const m of activeMembers) {
      const ns = m.workspace.resourceName;
      const handles = workspaceHandles.get(ns)!;
      // group-admin auto-implies group-member: attach the policy matching
      // the role only. Higher privilege (group-admin) is a strict superset.
      if (m.role === "group-admin") {
        policyNames.add(policyGroupAdmin(ns));
        policyDeps.push(handles.groupAdminNomad);
      } else {
        policyNames.add(policyMember(ns));
        policyDeps.push(handles.memberNomad);
      }
    }

    nomadToken = new nomad.AclToken(
      `user-${principal}-token`,
      {
        name: principal,
        type: "client",
        policies: Array.from(policyNames).sort(),
        global: false,
      },
      { ...opts, dependsOn: policyDeps },
    );
    tokenSecretId = nomadToken.secretId;
  }

  // ---- 2. RustFS IAM user password resolution ---------------------------
  // Members: password = Nomad token SecretID (one credential pair per user).
  // Pure collaborators: must be pre-seeded via Pulumi config —
  //   pulumi config set --secret collabPassword_<user> <password>
  let iamPassword: pulumi.Output<string>;
  if (tokenSecretId) {
    iamPassword = pulumi.secret(tokenSecretId);
  } else {
    iamPassword = cfg.requireSecret(`collabPassword_${principal}`);
  }

  // ---- 3. RustFS IAM user -----------------------------------------------
  const iamUser = new minio.IamUser(
    `user-${principal}-iam`,
    {
      name: principal,
      secret: iamPassword,
      forceDestroy: ALLOW_DESTROY,
    },
    opts,
  );

  // ---- 4. Policy attachments via mc admin policy attach -----------------
  // One local.Command per (user, policy) pair. We attach exactly the
  // role-appropriate policies — for a group-admin we attach the admin policy
  // ONLY (it's a superset of the member policy on RustFS); for a member we
  // attach the member policy.
  const attachDeps: pulumi.Resource[] = [];
  for (const m of activeMembers) {
    const ns = m.workspace.resourceName;
    const handles = workspaceHandles.get(ns)!;
    const policyResource = m.role === "group-admin" ? handles.groupAdminMinio : handles.memberMinio;
    const slug = `${ns}-${m.role === "group-admin" ? "ga" : "m"}`;
    const cmd = mkAttachCommand(
      `user-${principal}-attach-${slug}`,
      iamUser.name,
      policyResource.name,
      [iamUser, policyResource],
      opts,
    );
    attachDeps.push(cmd);
  }
  for (const c of activeCollabs) {
    const ns = c.workspace.resourceName;
    const handles = workspaceHandles.get(ns)!;
    const policyResource = handles.collabMinio[principal];
    if (!policyResource) {
      throw new Error(
        `internal: collab policy missing for user ${principal} in ${ns}`,
      );
    }
    const cmd = mkAttachCommand(
      `user-${principal}-attach-${ns}-c`,
      iamUser.name,
      policyResource.name,
      [iamUser, policyResource],
      opts,
    );
    attachDeps.push(cmd);
  }

  // ---- 5. Nomad credential variables (one per workspace touched) --------
  // Path matches the legacy SYNC_NOMAD_VARS format so consumers (jurist,
  // nf-nomad plugins) that read these variables continue to work.
  const writeIamVar = (
    ns: string,
    role: UserMembership["role"],
    scope: string,
  ) => {
    const safeId = ns.replace(/_/g, "-");
    new nomad.Variable(
      `user-${principal}-iamvar-${safeId}`,
      {
        namespace: NOMAD_IAM_NAMESPACE,
        path: iamVarPath(NOMAD_IAM_VAR_PREFIX, principal),
        itemsWo: pulumi.interpolate`{"access_key":"${principal}","secret_key":"${iamPassword}","role":"${role}","scope":"${scope}","bucket":"${ns}"}`,
        itemsWoVersion: 1,
      },
      { ...opts, additionalSecretOutputs: ["itemsWo"], dependsOn: attachDeps },
    );
  };

  // The Nomad var path doesn't include the namespace, so for a multi-workspace
  // user we'd overwrite the var on each call. Instead, write a SINGLE var
  // covering all workspace bucket scopes the user has access to. The role
  // string is "member" / "group-admin" depending on the highest privilege
  // they hold across any workspace; scope lists every (bucket, role) pair.
  const allWorkspaces = new Set<string>();
  for (const m of activeMembers) allWorkspaces.add(m.workspace.resourceName);
  for (const c of activeCollabs) allWorkspaces.add(c.workspace.resourceName);

  if (allWorkspaces.size > 0) {
    const scope = [
      ...activeMembers.map(
        (m) => `${m.workspace.resourceName}:${m.role === "group-admin" ? "admin" : "member"}`,
      ),
      ...activeCollabs.map((c) => `${c.workspace.resourceName}:collab/${principal}`),
    ].join(",");
    const role =
      activeMembers.some((m) => m.role === "group-admin") ? "group-admin" :
      activeMembers.length > 0                            ? "member"      :
                                                            "collab";
    const buckets = Array.from(allWorkspaces).sort().join(",");

    new nomad.Variable(
      `user-${principal}-iamvar`,
      {
        namespace: NOMAD_IAM_NAMESPACE,
        path: iamVarPath(NOMAD_IAM_VAR_PREFIX, principal),
        itemsWo: pulumi.interpolate`{"access_key":"${principal}","secret_key":"${iamPassword}","role":"${role}","scope":"${scope}","buckets":"${buckets}"}`,
        itemsWoVersion: 1,
      },
      { ...opts, additionalSecretOutputs: ["itemsWo"], dependsOn: attachDeps },
    );
  }

  // (writeIamVar above is unused — the per-workspace path overwrite issue
  // forced the single-var approach. Kept the partition logic for clarity.)
  void writeIamVar;

  return { principal, nomadToken, iamUser };
}
