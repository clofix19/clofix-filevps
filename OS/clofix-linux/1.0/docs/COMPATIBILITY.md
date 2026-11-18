# CloFix Linux 1.0 - Compatibility & upgrade policy

## Package origin

- Underlying userspace: **Ubuntu** (default build: `ubuntu:24.04` / codename `noble`).
- APT continues to use **official Ubuntu repositories** for that codename.
- `ID_LIKE=ubuntu debian` and `UBUNTU_CODENAME` remain for tooling that keys off Ubuntu.

## What stays Ubuntu

| Item | Reason |
|------|--------|
| `/etc/debian_version` | dpkg / multiarch expectations |
| APT sources / keyrings | Package authenticity |
| Package names & libraries | Application ABI |
| systemd, PAM, OpenSSH, DNS stacks | Runtime |

## What is CloFix

- User-facing name: **CloFix Linux 1.0**
- CLI: `clofix` (`version`, `info`, `status`, `update`, `security`, `support`)
- Branding files under `/etc/clofix`, MOTD, issue, prompt
- Policy: security + same-release upgrades allowed; `do-release-upgrade` discouraged (`Prompt=never`)

## Upgrade policy

See `/etc/clofix/upgrade-policy`:

- `allow_security_updates=yes`
- `allow_same_release_upgrade=yes`
- `allow_do_release_upgrade=no`

Major bumps ship as a **new CloFix Linux rootfs / FileVPS image**, not an in-guest Ubuntu release upgrade.

## FileVPS notes

- Guests share host kernel; do not rewrite kernel branding.
- GRUB/Plymouth changes apply only when those packages exist in the rootfs (often absent in CloFix overlay guests).
- Default login remains **root**; stock `ubuntu` user is removed.

## Licenses / trademarks

- Ubuntu packages remain under their upstream licenses.
- “Ubuntu” marks belong to Canonical; CloFix branding must not imply Canonical endorsement.
- Redistribute Ubuntu components only under applicable open-source terms.

## Validation

Run after branding:

```bash
./scripts/validate-rootfs.sh /path/to/rootfs
```

Optional APT smoke (Linux + network):

```bash
chroot /path/to/rootfs apt-get update
chroot /path/to/rootfs apt-get install -y --no-install-recommends curl
```
