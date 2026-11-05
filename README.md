# helm-controller

[![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/4784/badge)](https://bestpractices.coreinfrastructure.org/projects/4784)
[![e2e](https://github.com/fluxcd/helm-controller/workflows/e2e/badge.svg)](https://github.com/fluxcd/helm-controller/actions)
[![report](https://goreportcard.com/badge/github.com/fluxcd/helm-controller)](https://goreportcard.com/report/github.com/fluxcd/helm-controller)
[![license](https://img.shields.io/github/license/fluxcd/helm-controller.svg)](https://github.com/fluxcd/helm-controller/blob/main/LICENSE)
[![release](https://img.shields.io/github/release/fluxcd/helm-controller/all.svg)](https://github.com/fluxcd/helm-controller/releases)

## About this fork

This fork is used by the
[qdrant-cloud-agent](https://github.com/qdrant/qdrant-cloud-agent/). The main
reason behind forking the upstream project is to be able to distribute the CRDs of this
project without risk of collision in case the customer is using Flux CD in their
K8s cluster. The main change between the fork and upstream consist of renaming the
API group names, replacing the ones from fluxcd (ex: `helm.toolkit.fluxcd.io`)
by our custom ones.

### Maintaining the fork up to date

The [`sync-upstream`](.github/workflows/sync-upstream.yaml) workflow opens a PR
when upstream publishes a new stable release (`vX.Y.Z`). It runs on Mondays, or
on demand from the Actions tab (for a given tag, or with `dry_run` to stop before
pushing). The PR, `Sync upstream vX.Y.Z`, has up to two commits:

1. `chore: merge upstream tag vX.Y.Z`: upstream's changes, merged automatically.
2. `chore: apply Qdrant adaptations ...`, only when needed: what the fork owns,
   put back or regenerated. Its message lists the files.

The workflow runs [`tools/fork-sync`](tools/fork-sync), a small Go tool. What the
fork owns, the API group rename and the commands that adapt upstream's code
(`make tidy`, and `make manifests`, which regenerates the renamed CRD from the
API types) are set in [`.github/fork-sync.yaml`](.github/fork-sync.yaml).

**Reviewing a sync PR**

- CI is the verdict. Failing builds or tests are fixed on the PR branch.
- With merge conflicts the PR is a draft, with the markers and a list of the
  conflicting files (`.sync-upstream-conflicts`) committed. The
  [`conflict-markers`](.github/workflows/conflict-markers.yaml) check fails until
  the conflicts are resolved and that list is deleted. The rename and the
  adaptations do not run on a conflicted sync: once the conflicts are resolved,
  run `go -C tools/fork-sync run . --repo ../.. rename`, then the commands under
  `adaptations` in `.github/fork-sync.yaml`.
- If `make tidy` or `make manifests` fails (say, the release needs a newer Go)
  the PR still opens, with a note.
- Merge with a merge commit, never squash or rebase: the upstream tag has to stay
  an ancestor of `main`.
- The workflow never merges or tags. Closing a PR without merging tells it to
  skip that release. The agent's own CI is what checks compatibility with it.

**The source-controller fork.** This fork uses the source-controller fork's API
(the `source-controller/api` replace in `go.mod`) and CRDs (`SOURCE_BRANCH` in
the Makefile). The sync does not change either. When a release needs a newer
source-controller API, the build only passes once the source-controller fork
has a matching `-qdrant` tag: update both on the sync branch.

**Porting the adapter.** [`adapter/adapter.go`](adapter/adapter.go) is the fork's
own code: it adds the HelmRelease reconciler to `qdrant-cloud-agent`'s controller
manager, which upstream's `main.go` cannot do (`package main` cannot be
imported). It sets it up the way `main.go` does, so when a release changes that
setup, the `adapter` check fails on the sync PR. Make the same change upstream
made in `main.go`, on the sync branch:

```sh
git fetch --no-tags https://github.com/fluxcd/helm-controller.git "+refs/tags/*:refs/upstream-tags/*"
git diff refs/upstream-tags/v1.6.2 refs/upstream-tags/v1.7.1 -- main.go   # the last synced and the new release
```

**The fork owns `.github/`,** because the sync PR's CI runs the workflows with
this repository's secrets before anyone reviews it. Upstream's changes there are
put back to the fork's version in the adaptations commit (on conflicted syncs
too), and a sync stops if an adaptation changes a workflow.

**Taking an upstream CI change.** When upstream changed workflows the fork
has, the sync PR lists them with the command that applies upstream's changes on
top of the fork's version. Run it on the sync branch when CI needs them (a newer
Go, say), leaving out the files you do not want. By hand, between the last
synced and the new release:

```sh
git fetch --no-tags https://github.com/fluxcd/helm-controller.git "+refs/tags/*:refs/upstream-tags/*"
git diff refs/upstream-tags/vOLD refs/upstream-tags/vNEW -- .github/workflows/e2e.yaml | git apply -3
```

**Credentials.** The workflow pushes and opens PRs as the
[`qdrant-flux-fork-sync`](https://github.com/apps/qdrant-flux-fork-sync) app,
installed only on the Flux forks (not `GITHUB_TOKEN`, whose PRs do not trigger CI
and which cannot push the workflow changes that come with upstream's commits).
Its client ID and private key are the `FORK_SYNC_APP_CLIENT_ID` and
`FORK_SYNC_APP_PRIVATE_KEY` secrets. The workflow has two jobs, so that
upstream's code never runs where they are: `sync` merges and runs upstream's
code, with no credentials, and `publish` pushes and opens the PR from a fresh
runner, with a token that lasts for the job.

**Tests.** `go -C tools/fork-sync test ./...`, run in CI as `fork-tools-test`.
The tool is a copy of the one in
[fluxcd-source-controller](https://github.com/qdrant/fluxcd-source-controller/tree/main/tools/fork-sync):
fixes go to both.

#### Manual sync

From an up-to-date `main` with no local changes (needs `go` and `make`):

```sh
go -C tools/fork-sync run . --repo ../.. merge vX.Y.Z
git push origin sync/upstream-vX.Y.Z
```

Open a PR against `main` and merge it with a merge commit. Resolve conflicts on
that branch and delete `.sync-upstream-conflicts`. Do not use `-X theirs`, it
silently prefers upstream's side of every conflict.

---

The helm-controller is a Kubernetes operator, allowing one to declaratively
manage Helm chart releases. It is part of a composable [GitOps toolkit](https://fluxcd.io/flux/components)
and depends on [source-controller][] to acquire the Helm charts from Helm
repositories.

The desired state of a Helm release is described through a Kubernetes Custom
Resource named `HelmRelease`. Based on the creation, mutation or removal of a
`HelmRelease` resource in the cluster, Helm actions are performed by the
operator.

![overview](docs/diagrams/helm-controller-overview.png)

## Features

* Watches for `HelmRelease` objects and generates `HelmChart` objects
* Supports `HelmChart` artifacts produced from `HelmRepository`,
  `GitRepository` and `Bucket` sources
* Fetches artifacts produced by [source-controller][] from `HelmChart`
  and `OCIRepository` objects
* Watches `HelmChart` objects for revision changes (including semver
  ranges for charts from `HelmRepository` sources)
* Performs automated Helm actions, including Helm tests, rollbacks and
  uninstalls
* Offers extensive configuration options for automated remediation
  (rollback, uninstall, retry) on failed Helm install, upgrade or test
  actions
* Runs Helm install/upgrade in a specific order, taking into account the
  depends-on relationship defined in a set of `HelmRelease` objects
* Reports Helm release statuses (alerting provided by
  [notification-controller][])
* Built-in Kustomize compatible Helm post renderer, providing support
  for strategic merge, JSON 6902 and images patches
* Supports detecting and correcting in-cluster changes compared to the desired
  state of the Helm release

## Guides

* [Get started with Flux](https://fluxcd.io/flux/get-started/)
* [Manage Helm Releases](https://fluxcd.io/flux/guides/helmreleases/)
* [Setup Notifications](https://fluxcd.io/flux/guides/notifications/)

## Specification

[HelmRelease API](docs/spec/v2/helmreleases.md)

[source-controller]: https://github.com/fluxcd/source-controller
[notification-controller]: https://github.com/fluxcd/notification-controller
