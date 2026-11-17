# Hardened Kubernetes deployment (reference)

A locked-down variant of `../` for clusters where script Jobs must not reach
anything they do not need. It relies on the `KUBERNETES_JOB_*` and
`WORKFLOWFIESTA_ALLOW_IN_PROCESS_TOOLS` settings.

## Threat model

Script Jobs run whatever the workflow or chat sends: any command, any image,
with any environment values the Environment injects. Treat a script pod as
untrusted code. The runner pod holds two credentials worth protecting: its
WorkflowFiesta token and a Kubernetes token that can create Jobs.

## What each file does

| File | Control |
|---|---|
| `namespace.yaml` | Dedicated namespace, Pod Security Admission `restricted` enforced |
| `serviceaccounts.yaml` | Separate runner and script identities; script SA and `default` mount no token |
| `rbac.yaml` | Runner may manage Jobs and read pod logs in this namespace only |
| `admission-policy.yaml` | API server rejects runner-created Jobs that use another SA, mount a token, Secrets, ConfigMaps or host paths, skip the deadline, or use an unapproved image |
| `networkpolicies.yaml` | Default deny; runner and script pods get DNS and public HTTPS only; no cluster services, VPC, or IMDS |
| `quota.yaml` | ResourceQuota and LimitRange scoped to this namespace |
| `deployment.yaml` | Digest-pinned runner, read-only root fs, non-root, seccomp, all job controls on |

## Cloud identity

Neither ServiceAccount has an IRSA annotation or an EKS Pod Identity
association, so pods get no AWS credentials from the platform. Also block the
node's instance metadata service: the NetworkPolicies exclude
`169.254.0.0/16`, and node groups should require IMDSv2 with a hop limit of 1
so pods cannot borrow the node role even where policies are not enforced.

## Script image

The restricted profile runs scripts as UID 65532 with a read-only root
filesystem, so `apt-get install` at run time fails. Build a small image with
the tools your scripts need (for example `python3`, `curl`,
`ca-certificates`), push it to a registry you control, and point the
Environment `dockerImage` at it by digest. That also removes package-mirror
traffic from the egress allowlist.

## Apply

```sh
kubectl apply -k deploy/k8s/hardened/
kubectl apply -f deploy/k8s/hardened/admission-policy.yaml   # cluster-admin
kubectl -n workflowfiesta create secret generic wf-runner-creds --from-literal=token="$WF_TOKEN"
```

## Verify

```sh
kubectl auth can-i --list --as=system:serviceaccount:workflowfiesta:wf-script-job -n workflowfiesta
kubectl -n workflowfiesta logs deploy/workflowfiesta-runner | grep -E 'Job (namespace|security profile)'
```

Then run a workflow step that prints `id`, tries
`curl -m 3 http://169.254.169.254/` and a cluster Service, and checks that
`/var/run/secrets/kubernetes.io/serviceaccount` does not exist. The step
should report a non-root UID and fail both connections.
