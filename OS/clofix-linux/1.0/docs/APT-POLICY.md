# Ubuntu APT policy (CloFix Linux)

- Package source: **official Ubuntu repositories** for the detected `UBUNTU_CODENAME` only.
- GPG verification remains enabled.
- `apt update|install|remove|search` must work.
- Security updates within the same LTS codename are allowed (`clofix update`).
- `do-release-upgrade` prompts are disabled (`Prompt=never` + CloFix apt snippets).

**Important:** Disabling release-upgrade *notifications* is **not** an absolute lock. A privileged user can still manually edit sources. CloFix Guard validates that configured APT sources do not silently introduce another Ubuntu suite (`apt_codename` in security scan). Major bumps are shipped as a new CloFix rootfs/image.
