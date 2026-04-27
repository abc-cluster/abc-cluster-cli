// ---------------------------------------------------------------------------
// index.ts — Pulumi entry point for abc-userspace (schema v2).
//
// Order of operations:
//   1. Load + validate workspaces.yaml (must be version: v2)
//   2. For each non-deleted workspace, instantiate WorkspaceComponent
//      → emits namespace, bucket, ACL/IAM policies, submit account
//   3. For each top-level user, instantiate per-user resources
//      → one Nomad token + one IAM user with policies attached for every
//        (workspace, role) the user holds
//   4. Export a workspaceSummary suitable for `pulumi stack output --json`
//
// Quick-start:
//   cd deployments/abc-nodes/userspace
//   npm install
//   npm run build
//   pulumi stack init <name>
//   pulumi config set nomadAddress http://nomad.example.com:4646
//   pulumi config set minioEndpoint <rustfs-host>:9900
//   pulumi config set --secret nomad:secretId <management-token>
//   pulumi config set --secret minio:minioPassword <admin-password>
//   pulumi config set minio:minioUser <admin-user>
//   pulumi up
//
// Optional config:
//   nomadIamNamespace  — namespace for IAM cred variables (default: abc-services)
//   nomadIamVarPrefix  — variable path prefix (default: nomad/jobs/abc-nodes-minio-iam)
//   allowDestroy       — boolean (default: false). When true, IAM users and
//                        S3 buckets are forceDestroy=true so `pulumi destroy`
//                        wipes everything including object versions.
//
// Per-collaborator passwords (no fallback — required when state=active):
//   pulumi config set --secret collabPassword_<user> <password>
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as fs from "fs";
import * as path from "path";
import * as yaml from "js-yaml";

import { WorkspacesSpec, UserSpec } from "./types";
import { WorkspaceComponent } from "./workspace";
import { provisionUser } from "./user";
import { validateSpec, isCollabActive } from "./validate";

// ---- configuration ---------------------------------------------------------

const config = new pulumi.Config();

const specFilePath = config.get("specFile") ?? "workspaces.yaml";
const resolvedSpecPath = path.isAbsolute(specFilePath)
  ? specFilePath
  : path.resolve(process.cwd(), specFilePath);

// ---- load + validate spec --------------------------------------------------

function loadSpec(filePath: string): WorkspacesSpec {
  if (!fs.existsSync(filePath)) {
    throw new Error(
      `Workspace spec file not found: ${filePath}\n` +
        `Set the path with: pulumi config set specFile /path/to/workspaces.yaml`,
    );
  }
  const raw = fs.readFileSync(filePath, "utf8");
  return yaml.load(raw) as WorkspacesSpec;
}

const rawSpec = loadSpec(resolvedSpecPath);
const resolved = validateSpec(rawSpec);

pulumi.log.info(
  `userspace v2: ${resolved.users.size} users, ${resolved.workspaces.length} workspaces`,
);

// ---- instantiate workspace components --------------------------------------

const userSpecMap = new Map<string, UserSpec>();
for (const ru of resolved.users.values()) userSpecMap.set(ru.spec.name, ru.spec);

const workspaceComponents: Record<string, WorkspaceComponent> = {};
for (const ws of resolved.workspaces) {
  if (ws.spec.state === "deleted") {
    pulumi.log.info(
      `Skipping ${ws.resourceName} (state=deleted); remove from YAML after confirming destruction.`,
    );
    continue;
  }
  workspaceComponents[ws.resourceName] = new WorkspaceComponent(
    ws.resourceName,
    { org: ws.org, spec: ws.spec, users: userSpecMap },
  );
}

// ---- instantiate per-user resources ----------------------------------------

const handlesByNs = new Map<string, ReturnType<() => WorkspaceComponent["handles"]>>();
for (const [ns, comp] of Object.entries(workspaceComponents)) {
  handlesByNs.set(ns, comp.handles);
}

for (const user of resolved.users.values()) {
  // skip users with zero memberships across all active workspaces
  const hasAny = user.memberships.some(
    (m) => (m.workspace.spec.state ?? "active") === "active",
  );
  if (!hasAny) {
    pulumi.log.info(`User ${user.spec.name} has no active memberships; skipping.`);
    continue;
  }
  provisionUser(user, handlesByNs as any, {});
}

// ---- stack outputs ---------------------------------------------------------

/**
 * Audit-friendly summary keyed by workspace.
 * `pulumi stack output workspaceSummary --json` returns the full picture.
 */
const now = new Date();
const summary: Record<string, pulumi.Output<unknown>> = {};

for (const ws of resolved.workspaces) {
  if (ws.spec.state === "deleted") continue;
  const ns = ws.resourceName;
  const comp = workspaceComponents[ns];
  const members = (ws.spec.members ?? []).map((m) => ({ user: m.user, role: m.role }));
  const collaborators = (ws.spec.collaborators ?? []).map((c) => {
    const m = { kind: "collab" as const, expiresAt: c.expiresAt, workspace: ws, role: "group-collaborator" as const };
    return { user: c.user, expiresAt: c.expiresAt, expired: !isCollabActive(m, now) };
  });

  summary[ns] = pulumi.all([comp.namespaceName, comp.bucketName]).apply(
    ([namespace, bucket]) => ({
      namespace,
      bucket,
      org: ws.org.name,
      state: ws.spec.state ?? "active",
      priority: ws.spec.priority ?? "normal",
      memberCount: members.length,
      members,
      collaborators,
      hasSubmitAccount: !!ws.spec.submitAccount,
    }),
  );
}

export const workspaceSummary = pulumi.all(summary);

/** Inventory of every user and their memberships, for ops auditing. */
export const userSummary = Array.from(resolved.users.values()).map((u) => ({
  name: u.spec.name,
  email: u.spec.email,
  memberships: u.memberships.map((m) => ({
    ns: m.workspace.resourceName,
    kind: m.kind,
    role: m.role,
    ...(m.expiresAt ? { expiresAt: m.expiresAt } : {}),
  })),
}));
