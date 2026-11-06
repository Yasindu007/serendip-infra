package openchoreo

import (
	"fmt"

	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// Serendip's own build workflow (plan decision 1, T001 item 3): check
// out, then build and push with podman, outputting the commit (on the
// checkout node) and the image (on the build-push node). There's no
// generate-workload step and no ExternalSecret: the provider reads the
// outputs from the Argo Workflow, and the Git credential is a plain
// Secret named <run>-git that the build reconciler creates only when
// there is one.
//
// Templating: ${...} is OpenChoreo's CEL and {{...}} is Argo's. The
// scripts mustn't contain "{{", which Argo would try to expand.
const (
	WorkflowName = "serendip-dockerfile"
	// runTTL is how long a finished run, its pods and their logs are
	// kept (plan decision 5). Completed pods hold their emptyDir until
	// then, which is why it's short.
	runTTL = "1h"

	checkoutImage  = "alpine/git:v2.52.0"
	buildPushImage = "ghcr.io/openchoreo/podman-runner:v1.2"

	// The output parameters T006 reads, and the steps carrying them.
	CheckoutStep    = "checkout"
	BuildPushStep   = "build-push"
	CommitOutput    = "commit"
	ImageOutput     = "image"
	GitSecretSuffix = "-git"
)

// checkoutScript clones the branch's head, or fetches one commit, using
// the mounted <run>-git Secret's username and password through
// GIT_ASKPASS when it exists.
//
// Only this step may see the credential, and nothing of it may reach the
// workspace the build step shares (the askpass script stays in this
// container's /tmp; the remote in .git/config is the plain URL): the API
// lets someone without the Secret's value change which code the build step
// runs (RP-044).
const checkoutScript = `if [ -f /etc/git-secret/password ]; then
  printf '#!/bin/sh\ncase "$1" in Username*) cat /etc/git-secret/username 2>/dev/null || echo git;; *) cat /etc/git-secret/password;; esac\n' > /tmp/askpass
  chmod +x /tmp/askpass
  export GIT_ASKPASS=/tmp/askpass
fi
export GIT_TERMINAL_PROMPT=0
if [ -n "$COMMIT" ]; then
  git init -q /mnt/vol/src && cd /mnt/vol/src
  git remote add origin "$REPO"
  git fetch -q --depth 1 origin "$COMMIT"
  git checkout -q FETCH_HEAD
else
  git clone -q --single-branch --branch "$BRANCH" --depth 1 -- "$REPO" /mnt/vol/src
  cd /mnt/vol/src
fi
git rev-parse HEAD | tr -d '\n' > /tmp/commit
echo "checked out $(cat /tmp/commit)"
`

// buildPushScript builds the Dockerfile and pushes the image tagged with
// the commit's first 12 characters. It runs privileged without user
// namespaces (RP-038): kind's OCI hook fails inside one. A path or
// Dockerfile that a symlink in the repository points outside the checkout
// is refused (RP-040).
const buildPushScript = `mkdir -p /etc/containers
printf '[storage]\ndriver = "overlay"\nrunroot = "/storage/run"\ngraphroot = "/storage/graph"\n' > /etc/containers/storage.conf
TAG=$(echo "$COMMIT" | cut -c1-12)
SRC="$PUSH/$REPO_NAME:$TAG"
cd "/mnt/vol/src/$APP_PATH"
case "$(pwd -P)/" in /mnt/vol/src/*) ;; *) echo "the build path leaves the repository" >&2; exit 1;; esac
[ -f "$DOCKERFILE" ] || { echo "there's no $DOCKERFILE in $APP_PATH" >&2; exit 1; }
case "$(realpath "$DOCKERFILE")" in /mnt/vol/src/*) ;; *) echo "the Dockerfile leaves the repository" >&2; exit 1;; esac
podman build -t "$SRC" -f "$DOCKERFILE" .
podman push --tls-verify=false "$SRC"
echo -n "$PULL/$REPO_NAME:$TAG" > /tmp/image
echo "pushed $SRC"
`

const clusterWorkflowsPath = "/apis/openchoreo.dev/v1alpha1/clusterworkflows"

func env(name, value string) map[string]any { return map[string]any{"name": name, "value": value} }

func param(name, value string) map[string]any { return map[string]any{"name": name, "value": value} }

func str(def string) map[string]any { return map[string]any{"type": "string", "default": def} }

// clusterWorkflow returns the serendip-dockerfile ClusterWorkflow, as
// T001 ran it.
func clusterWorkflow() kubernetes.Object {
	schema := map[string]any{
		"type":     "object",
		"required": []string{"repository", "image"},
		"properties": map[string]any{
			"repository": map[string]any{
				"type":     "object",
				"required": []string{"url"},
				"properties": map[string]any{
					"url":    map[string]any{"type": "string"},
					"branch": str("main"),
					"commit": str(""),
					"path":   str("."),
				},
			},
			"dockerfile": str("Dockerfile"),
			"image": map[string]any{
				"type":     "object",
				"required": []string{"repository"},
				"properties": map[string]any{
					"pushRegistry": str(pushRegistry),
					"pullRegistry": str(pullRegistry),
					"repository":   map[string]any{"type": "string"},
				},
			},
		},
	}
	checkout := map[string]any{
		"name": CheckoutStep,
		"container": map[string]any{
			"image":   checkoutImage,
			"command": []string{"sh", "-ec"},
			"args":    []string{checkoutScript},
			"env": []any{
				env("REPO", "{{workflow.parameters.repo}}"),
				env("BRANCH", "{{workflow.parameters.branch}}"),
				env("COMMIT", "{{workflow.parameters.commit}}"),
			},
			"volumeMounts": []any{
				map[string]any{"name": "workspace", "mountPath": "/mnt/vol"},
				map[string]any{"name": "git-secret", "mountPath": "/etc/git-secret", "readOnly": true},
			},
		},
		// Always named, and optional: an empty secretName makes the pod
		// invalid even with optional set (T001 item 3).
		"volumes": []any{map[string]any{
			"name":   "git-secret",
			"secret": map[string]any{"secretName": "{{workflow.parameters.git-secret}}", "optional": true},
		}},
		"outputs": map[string]any{"parameters": []any{map[string]any{
			"name": CommitOutput, "valueFrom": map[string]any{"path": "/tmp/commit"},
		}}},
	}
	buildPush := map[string]any{
		"name":   BuildPushStep,
		"inputs": map[string]any{"parameters": []any{map[string]any{"name": "commit"}}},
		"container": map[string]any{
			"image":   buildPushImage,
			"command": []string{"sh", "-ec"},
			"args":    []string{buildPushScript},
			"env": []any{
				env("COMMIT", "{{inputs.parameters.commit}}"),
				env("APP_PATH", "{{workflow.parameters.path}}"),
				env("DOCKERFILE", "{{workflow.parameters.dockerfile}}"),
				env("PUSH", "{{workflow.parameters.push-registry}}"),
				env("PULL", "{{workflow.parameters.pull-registry}}"),
				env("REPO_NAME", "{{workflow.parameters.image-repo}}"),
			},
			"securityContext": map[string]any{"privileged": true},
			"volumeMounts": []any{
				map[string]any{"name": "workspace", "mountPath": "/mnt/vol"},
				map[string]any{"name": "storage", "mountPath": "/storage"},
			},
		},
		"volumes": []any{map[string]any{"name": "storage", "emptyDir": map[string]any{"sizeLimit": "10Gi"}}},
		"outputs": map[string]any{"parameters": []any{map[string]any{
			"name": ImageOutput, "valueFrom": map[string]any{"path": "/tmp/image"},
		}}},
	}
	runTemplate := map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Workflow",
		"metadata": map[string]any{
			"name":      "${metadata.workflowRunName}",
			"namespace": "${metadata.namespace}",
		},
		"spec": map[string]any{
			"serviceAccountName": "workflow-sa",
			"entrypoint":         "build",
			"arguments": map[string]any{"parameters": []any{
				param("repo", "${parameters.repository.url}"),
				param("branch", "${parameters.repository.branch}"),
				param("commit", "${parameters.repository.commit}"),
				param("path", "${parameters.repository.path}"),
				param("dockerfile", "${parameters.dockerfile}"),
				param("git-secret", "${metadata.workflowRunName}"+GitSecretSuffix),
				param("push-registry", "${parameters.image.pushRegistry}"),
				param("pull-registry", "${parameters.image.pullRegistry}"),
				param("image-repo", "${parameters.image.repository}"),
			}},
			"volumeClaimTemplates": []any{map[string]any{
				"metadata": map[string]any{"name": "workspace"},
				"spec": map[string]any{
					"accessModes": []string{"ReadWriteOnce"},
					"resources":   map[string]any{"requests": map[string]string{"storage": "2Gi"}},
				},
			}},
			"templates": []any{
				map[string]any{"name": "build", "steps": []any{
					[]any{map[string]any{"name": CheckoutStep, "template": CheckoutStep}},
					[]any{map[string]any{
						"name": BuildPushStep, "template": BuildPushStep,
						"arguments": map[string]any{"parameters": []any{param("commit",
							fmt.Sprintf("{{steps.%s.outputs.parameters.%s}}", CheckoutStep, CommitOutput))}},
					}},
				}},
				checkout,
				buildPush,
			},
		},
	}
	return kubernetes.Object{
		APIVersion: apiVersion, Kind: "ClusterWorkflow",
		Metadata: kubernetes.ObjectMeta{Name: WorkflowName, Labels: managedBy},
		Spec: mustJSON(map[string]any{
			"workflowPlaneRef":   map[string]any{"kind": "ClusterWorkflowPlane", "name": WorkflowPlaneName},
			"ttlAfterCompletion": runTTL,
			"parameters":         map[string]any{"openAPIV3Schema": schema},
			"runTemplate":        runTemplate,
		}),
	}
}
