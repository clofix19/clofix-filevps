# CloFix FileVPS OS images

| Image | Version | Notes |
|-------|---------|--------|
| Ubuntu | 24.04 / 22.04 | Built in `worker/Dockerfile` |
| **CloFix Linux** | **1.0.0** | [`clofix-linux/1.0/`](clofix-linux/1.0/) — branding + **CloFix Guard** |

```bash
cd clofix-linux/1.0
./scripts/build-binaries.sh
./scripts/build-rootfs.sh
```

Docs: `TASKS.md`, `SECURITY-MATRIX.md`, `docs/ROOTFS-INVENTORY.md`.  
Worker rebuild is separate (`docker compose build worker`).
