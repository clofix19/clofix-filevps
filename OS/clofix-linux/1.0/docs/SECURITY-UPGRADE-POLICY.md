# CloFix Linux - security & upgrade policy

## Goals

1. Keep **security patches** flowing from Ubuntu for the pinned codename.
2. Allow normal `apt upgrade` within that release.
3. Block accidental `do-release-upgrade` / codename jumps.
4. Ship major CloFix bumps as new rootfs / FileVPS images.

## Guest policy files

| File | Purpose |
|------|---------|
| `/etc/clofix/upgrade-policy` | Human/machine readable CloFix policy |
| `/etc/apt/apt.conf.d/20auto-upgrades-clofix` | Periodic lists + unattended-upgrade enabled |
| `/etc/apt/apt.conf.d/99clofix-release-upgrade` | Release-upgrade posture |
| `/etc/update-manager/release-upgrades` | `Prompt=never` when present |

## Operator commands

```bash
clofix update      # apt-get update && apt-get upgrade
clofix security    # policy + pending upgrade simulation
```

## What we do not do

- Do not rewrite Ubuntu package origins or disable GPG verification.
- Do not change `/etc/debian_version` for branding.
- Do not bake SSH host keys into distributable archives.
- Do not claim validation without `.build/reports/` from `scripts/build-rootfs.sh`.
