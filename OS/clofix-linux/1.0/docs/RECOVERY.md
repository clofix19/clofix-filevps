# Operational recovery

## Branding rollback

`brand-rootfs.sh` writes `*.clofix-bak` beside replaced files when backups are enabled.

```bash
# Example restore
cp -a /etc/os-release.clofix-bak /etc/os-release
```

## Firewall rollback

```bash
clofix security rollback
# restores last nft ruleset backup under /var/lib/clofix/guard/firewall/
```

Never run destructive `nft flush ruleset` except via explicit rollback with a known-good backup.

## Guard data

- Events: `/var/lib/clofix/guard/events.jsonl`
- FIM baselines: `/var/lib/clofix/guard/fim-baseline*.json`
- Last scan: `/var/lib/clofix/guard/last-scan.json`

## Immutable mode

Guest flag only until host-worker enforces overlays/snapshots. Disable:

```bash
clofix security mode enable standard
# then host API: immutable_disable
```

## Admin lockout prevention

SSH Guard never auto-disables the only working admin login. Apply `PasswordAuthentication no` only after verifying key login.
