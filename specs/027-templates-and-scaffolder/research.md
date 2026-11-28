# Spec 027 research notes (2026-11-28, oss-researcher)

These notes are the input to `spec.md` and later `plan.md`. They're from
OpenChoreo's v1.3.x docs and its `backstage-plugins` repo, not read from
the v1.3.1 tag's source. The Azure part is partly from general knowledge,
and the Template Specs and Deployment Environments pages weren't
fetched.

## OpenChoreo's portal: no application scaffolder

- **Its scaffolder templates are platform-admin tools.** They are
  Backstage Software Templates (`scaffolder.backstage.io/v1beta3`) with
  `openchoreo:*` actions. 14 `create-openchoreo-*` templates ship:
  - namespace, project, projecttype, environment, deploymentpipeline,
    notification-channel, componenttype, resourcetype, trait and workflow;
  - cluster-scoped variants of componenttype, projecttype, resourcetype,
    trait and workflow.

  No "service / web app / Go / Ballerina" application template ships.
- **Developers create Components through a built-in wizard,** not a
  scaffolder template:
  1. Namespace, project, component name, and an optional display name and
     description.
  2. One build or deploy choice:
     - **Build from Source:** workflow, Git URL, branch, app path, an
       optional Git secret;
     - **Container Image:** an image reference, plus an auto-deploy
       toggle;
     - **External CI:** Jenkins, GitHub Actions or GitLab CI, plus an
       identifier.
  3. A form generated from the chosen **ComponentType's parameter schema**:
     replicas, port, resources, endpoints (HTTP, gRPC, WebSocket), env vars
     and file mounts (image deploys only), and traits.
     - Example ComponentTypes: `deployment/service`,
       `deployment/web-application`, `deployment/worker`,
       `cronjob/scheduled-task`.
     - For a source build, env vars and file mounts come from a
       `workload.yaml` in the repo.
- **No template creates a repo.** Components point at an existing Git repo
  or image. A custom template could combine `publish:github` or
  `fetch:template`, but none ships.
- **The scaffolder actions call `openchoreo-api`.** Serendip bypasses that
  API (ADR 0019), so only the idea transfers, not the code:
  - `openchoreo:component:create` takes `namespaceName`, `projectName`,
    `componentName`, `componentType`, plus inputs forwarded to the
    ComponentType schema;
  - `project`, `environment`, `deployment-pipeline`, `namespace` and
    `resource` have `:create` actions;
  - the `*-definition:create` actions take `yamlContent`.
- **No CRD exists for "create from a template".** ComponentType and
  ClusterComponentType are the platform's templates for *rendering*
  workloads. Trait, Workflow, ResourceType and ProjectType are CRDs too.
  Scaffolder templates are only Backstage catalog entities kept in Git.
- **`samples/` (v1.3.1)** are plain YAML starters to apply, with no
  parameters:
  - getting-started, component-types, from-image, from-source and
    workflows;
  - gcp-microservices-demo, mcp, platform-config, local-development,
    component-alerts, and occ-cli/go-greeter-manual-deploy.
- **Catalogs:** platform engineers register Template URLs, either through
  the Catalog Import UI (kept in the Backstage DB, lost on reset) or in
  `catalog.locations`. No template versioning was found. The real
  customisation points are org-defined ComponentTypes, Traits and
  Workflows.

Sources:
- https://openchoreo.dev/docs/platform-engineer-guide/backstage-scaffolder-templates/
- https://openchoreo.dev/docs/developer-guide/projects-and-components/creating-a-component/
- https://github.com/openchoreo/backstage-plugins (`templates/`)
- https://github.com/openchoreo/openchoreo/tree/v1.3.1/samples

## Azure's equivalents

| Azure | What a template is | Who writes it | Parameters | Makes a repo? |
|---|---|---|---|---|
| **azd templates / Awesome azd** | A Git repo with app code, Bicep or Terraform, and `azure.yaml` | Microsoft and the community (a gallery taking PRs) | `azd env` values and prompts | `azd init -t` copies it into a new local project; `azd pipeline config` can set up a GitHub repo and CI |
| **App Service / Container Apps quickstarts** | Portal or CLI flows, plus ARM/Bicep "Deploy to Azure" samples | Microsoft | Portal form fields or ARM parameters | No (Deployment Center connects an existing repo) |
| **Template Specs** | A versioned ARM/Bicep template stored as a resource (`Microsoft.Resources/templateSpecs`), shared through RBAC | Platform teams | ARM parameters | No |
| **Azure Deployment Environments** | An environment definition (`environment.yaml` plus IaC) in a catalog Git repo attached to a dev center | Platform engineers | Declared in the manifest, filled in by developers | No |

Sources: https://learn.microsoft.com/en-us/azure/developer/azure-developer-cli/azd-template-galleries,
https://azure.github.io/awesome-azd/

## Take-away for the spec

- OpenChoreo has **no application scaffolder** to copy. The closest
  equivalents are its **Create Component wizard** (driven by a
  ComponentType's schema, plus a choice of source, image or external CI)
  and its plain-YAML `samples/`.
- A Serendip "template" is therefore Serendip's own design. The nearest
  Azure models:
  - **Template Specs:** a versioned, RBAC-shared resource that
    platform teams write;
  - **Deployment Environments:** a catalog that platform engineers
    write and developers fill in.
  - azd's "a template creates a repo" would be a further, separate step.
- **Constitution 10:** Serendip doesn't build a Git server. Creating repos
  on GitHub or another host would be an integration, if it's in scope at
  all.
