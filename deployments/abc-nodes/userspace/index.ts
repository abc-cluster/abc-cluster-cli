// ---------------------------------------------------------------------------
// index.ts — Pulumi entry point for abc-userspace.
//
// Reads workspaces.yaml (or the path set in `pulumi config set specFile ...`),
// validates it, then iterates over orgs → workspaces and instantiates a
// WorkspaceComponent for each non-deleted workspace.
//
// Quick-start:
//   cd deployments/abc-nodes/userspace
//   npm install
//   npm run build
//   pulumi stack init dev
//   pulumi config set nomadAddress http://nomad.example.com:4646
//   pulumi config set minioEndpoint minio.example.com:9000
//   pulumi config set --secret nomadToken <management-token>
//   pulumi config set --secret minioAccessKey <access-key>
//   pulumi config set --secret minioSecretKey <secret-key>
//   pulumi up
//
// Optional config:
//   nomadIamNamespace  — namespace for IAM cred variables (default: abc-services)
//   nomadIamVarPrefix  — variable path prefix (default: nomad/jobs/abc-nodes-minio-iam)
//   allowDestroy       — boolean (default: false). When true, IAM users and S3
//                        buckets are created with forceDestroy=true so
//                        `pulumi destroy` removes everything including all
//                        object versions and delete markers. Set this and run
//                        `pulumi up` once to flip the flag, then `pulumi
//                        destroy`. Without it, destroy fails on non-empty
//                        buckets to prevent accidental data loss.
//
// Per-collaborator passwords (no fallback — required when state=active and
// collaborator is not expired):
//   pulumi config set --secret collabPassword_<ns>_collab-<name> <password>
// ---------------------------------------------------------------------------

import * as pulumi from "@pulumi/pulumi";
import * as fs from "fs";
import * as path from "path";
import * as yaml from "js-yaml";

import { WorkspacesSpec, ResolvedWorkspace } from "./types";
import { WorkspaceComponent } from "./workspace";
import { validateSpec, memberRoles } from "./validate";
import { memberPrincipal } from "./naming";

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

const spec = loadSpec(resolvedSpecPath);
validateSpec(spec);

// ---- resolve workspaces ----------------------------------------------------

function resolveWorkspaces(spec: WorkspacesSpec): ResolvedWorkspace[] {
  const resolved: ResolvedWorkspace[] = [];
  for (const org of spec.organizations) {
    for (const ws of org.workspaces) {
      if (ws.state === "deleted") {
        pulumi.log.info(
          `Skipping ${org.name}-${ws.name} (state=deleted); remove from YAML after confirming destruction.`,
        );
        continue;
      }
      resolved.push({
        resourceName: `${org.name}-${ws.name}`,
        org,
        spec: ws,
      });
    }
  }
  return resolved;
}

const workspaces = resolveWorkspaces(spec);

// ---- instantiate components ------------------------------------------------

const workspaceComponents: Record<string, WorkspaceComponent> = {};

for (const ws of workspaces) {
  workspaceComponents[ws.resourceName] = new WorkspaceComponent(
    ws.resourceName,
    { org: ws.org, spec: ws.spec },
  );
}

// ---- stack outputs ---------------------------------------------------------

/**
 * Audit-friendly summary for `pulumi stack output workspaceSummary --json`.
 * Lists members, collaborators (with expiry status), and submit account
 * presence per workspace — usable as an inventory without reading the YAML.
 */
const now = new Date();
const summary: Record<string, pulumi.Output<unknown>> = {};

for (const ws of workspaces) {
  const ns = ws.resourceName;
  const comp = workspaceComponents[ns];

  const members = (ws.spec.members ?? []).map((m) => {
    const roles = memberRoles(m);
    return {
      name: m.name,
      roles,
      principals: roles.map((r) => ({
        role: r,
        principal: memberPrincipal(ns, r, m.name),
      })),
    };
  });

  const collaborators = (ws.spec.collaborators ?? []).map((c) => {
    const expiry = new Date(c.expiresAt + "T00:00:00Z");
    return {
      name: c.name,
      expiresAt: c.expiresAt,
      expired: expiry <= now,
    };
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
