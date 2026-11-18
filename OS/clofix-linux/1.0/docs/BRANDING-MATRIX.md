# CloFix Linux 1.0 - Branding matrix

| Location | Change | Owner |
|----------|--------|--------|
| `/etc/os-release` | CloFix Linux identity; keep `UBUNTU_CODENAME` / `ID_LIKE=ubuntu debian` | `clofix-release` / brand script |
| `/usr/lib/os-release` | Same content (real file, not Ubuntu symlink) | brand script |
| `/etc/lsb-release` | Legacy CloFixLinux identity | brand script |
| `/etc/debian_version` | **Unchanged** (dpkg/apt compatibility) | Ubuntu `base-files` |
| `/etc/issue` | Local login banner | brand script |
| `/etc/issue.net` | SSH banner | brand script |
| `/etc/motd` | Static welcome + ASCII | brand script |
| `/etc/update-motd.d/00-clofix-header` | Dynamic MOTD header | brand script |
| `/etc/update-motd.d/*` | Ubuntu fragments renamed `*.clofix-disabled` | brand script |
| `/etc/hostname` | Default `clofix-linux` (instance may override) | brand script |
| `/etc/hosts` | `127.0.1.1 clofix-linux` | brand script |
| `/etc/profile.d/clofix-prompt.sh` | Prompt + `CLOFIX_*` env | brand script |
| `/etc/skel/.bashrc` | New-user defaults | brand script |
| `/root/.bashrc` | Root defaults | brand script |
| `/usr/bin/clofix` | CloFix CLI | `clofix-release` |
| `/etc/clofix/*` | Version + upgrade policy | `clofix-release` |
| `/etc/apt/apt.conf.d/*clofix*` | Update cadence / release-upgrade posture | brand script |
| `/etc/update-manager/release-upgrades` | `Prompt=never` if present | brand script |
| `/etc/default/grub` | `GRUB_DISTRIBUTOR` if GRUB present | brand script |
| `/boot/grub/` | No forced theme unless assets shipped | optional |
| `/etc/cloud/build.info` | CloFix product build metadata (replaces ubuntu-oci) | brand script |
| `/etc/cloud/cloud.cfg.d/99-clofix.cfg` | CloFix defaults; `distro: ubuntu` = cloud-init module only | brand script |
| Kernel `uname` | **Not** rebranded | - |

Website in release metadata: **https://clofix.com**

Duplicate banners: Ubuntu `update-motd.d` scripts disabled so only CloFix header + `/etc/motd` show.
