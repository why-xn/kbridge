---
name: release
description: Use when cutting, tagging, verifying, or hotfixing a kbridge release (vX.Y.Z-alpha.N), bumping version references, or shipping a fix that must not carry master's unreleased work.
---

# Cutting a release

Releases are built by `.github/workflows/release.yml` when a `v*` tag is
pushed: goreleaser archives and checksums, images
`ghcr.io/why-xn/kbridge-{control-plane,agent,cli}:<tag>` for amd64 and arm64,
and OCI charts at `oci://ghcr.io/why-xn/charts`. Nothing is built locally.

## Decide the base

- Everything on `master` ships: tag on `master`.
- A fix must ship without master's unreleased work: branch from the last tag,
  `git switch -c release/vX.Y.Z-alpha.N vPREV`, bring the fix in (merge the
  feature branch if it was cut from the tag, cherry-pick otherwise), and check
  `git diff vPREV --stat` shows only the fix.

## Version references

One commit, `release: vX.Y.Z-alpha.N`, changes all of these. Find them with
`grep -rn 'X.Y.Z-alpha.N' --include='*.md' --include='*.yaml' --include='*.sh' --include=Makefile .`
using the previous version without its `v`, so `Chart.yaml` `version` is caught too:

- `CHANGELOG.md`: rename `[Unreleased]` to `[X.Y.Z-alpha.N] - YYYY-MM-DD`,
  add an empty `[Unreleased]` above it, and update the compare links at the
  bottom (`[Unreleased]: ...compare/vNEW...HEAD`, `[NEW]: ...compare/vPREV...vNEW`).
- `charts/*/Chart.yaml` `version` (no `v`) and `appVersion` (with `v`).
- `charts/*/values.yaml` `image.tag`. The workflow sets chart versions at
  package time but not this tag, so a missed bump ships charts that pull the
  old images.
- `README.md` status line, `install.sh` example, `Makefile` comment,
  `docs/installation.md`, `docs/operations.md`, `docs/production-install.md`.

## Tag and ship

```bash
make test && go vet ./...
git tag -a vNEW -m vNEW
git push origin <branch> vNEW
gh run watch
```

## Verify, every time

- `gh release view vNEW`: marked pre-release for `-alpha`, archives plus
  `checksums.txt` attached.
- `KBRIDGE_VERSION=vNEW sh install.sh` into a temp dir, then `kb version`
  shows the tag and commit.
- `docker buildx imagetools inspect ghcr.io/why-xn/kbridge-agent:vNEW` lists
  amd64 and arm64; same for control-plane and cli.
- `helm show values oci://ghcr.io/why-xn/charts/kbridge-agent --version NEW`
  shows `tag: vNEW`; same for control-plane.

## After a release-branch release

Merge back so `master` carries the fix and the changelog section:

```bash
git switch master && git merge --no-ff release/vNEW
```

Resolve `CHANGELOG.md` by keeping master's `[Unreleased]` entries above the
new section. If master's references still name the previous version, commit
`chore: point version references at vNEW`. Then `git push origin master`.
Never cherry-pick onto master what was merged into a release branch; the next
merge would conflict.

## Known traps

- The workflow always moves `:latest` to the pushed tag. Patching an older
  line rolls `latest` back; do not release an old line without deciding that.
- `ROADMAP.md` marks items shipped only once the tag exists.
