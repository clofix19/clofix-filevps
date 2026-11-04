# Master FileVPS test catalog. Status is resolved from the last bench/audit TSV,
# not by inventing results. Cases without a probe stay NOT RUN.

SUITES = [
    (
        "1. VPS Lifecycle",
        "P0",
        [
            ("L01", "VPS create and start", {"bench": ["create_ms", "start_ready_ms"]}),
            ("L02", "Create → guest ready time (fx exec, not SSH handshake)", {"bench": ["start_ready_ms", "total_ms"]}),
            ("L03", "Concurrent VPS creation (10 / 25 / 50)", {"skip": "not_run"}),
            ("L04", "VPS crash and automatic recovery", {"skip": "not_run"}),
            ("L05", "VPS recovery after worker restart", {"skip": "not_run"}),
            ("L06", "VPS clone, rebuild, and snapshot restore", {"skip": "not_run"}),
            ("L07", "Failed provisioning cleanup and rollback", {"skip": "not_run"}),
            ("L08", "Throw-away guest delete after bench/audit", {"skip": "not_run"}),
        ],
    ),
    (
        "2. OS & Kernel Sharing",
        "P0",
        [
            ("O01", "Shared read-only base OS", {"audit": ["O01"]}),
            ("O02", "OverlayFS lower/upper isolation (A write not visible in B)", {"audit": ["O02", "X01"]}),
            ("O03", "Per-VPS hostname / identity files", {"audit": ["O03"]}),
            ("O04", "Base OS image update and rollback", {"skip": "not_run"}),
            ("O05", "Ubuntu 24.04 bundled image (this collection)", {"audit": ["O04"]}),
            ("O06", "Debian / CentOS-compatible image tests", {"skip": "not_run"}),
            ("O07", "Shared kernel information leakage", {"audit": ["K01", "K05", "K06", "I08"]}),
            ("O08", "Guest package install and systemd compatibility", {"skip": "not_run"}),
        ],
    ),
    (
        "3. Root & Namespace Security",
        "P0",
        [
            ("S01", "User namespace UID/GID mapping", {"audit": ["I01"]}),
            ("S02", "Host root versus VPS root isolation", {"audit": ["I01", "X05"]}),
            ("S03", "Unique UID ranges per VPS", {"audit": ["X05"]}),
            ("S04", "PID namespace isolation", {"audit": ["N02", "X03"]}),
            ("S05", "Mount / UTS / IPC / net / cgroup namespaces", {"audit": ["N01", "N05", "X04"]}),
            ("S06", "Dangerous Linux capabilities dropped", {"audit": ["I02"]}),
            ("S07", "Seccomp and no_new_privs", {"audit": ["I03"]}),
            ("S08", "AppArmor / SELinux confinement", {"audit": ["I04"]}),
            ("S09", "Cross-VPS process and filesystem access blocked", {"audit": ["X01", "X03"]}),
        ],
    ),
    (
        "4. Filesystem & Protection Mode",
        "P0",
        [
            ("F01", "Protection Mode on/off (audit runs with protection off)", {"skip": "not_run"}),
            ("F02", "Protected-file modify without admin approval", {"skip": "not_run"}),
            ("F03", "Host filesystem and runtime socket access blocked", {"audit": ["N03", "X02"]}),
            ("F04", "Symlink / path-traversal escape", {"audit": ["F06"]}),
            ("F05", "/tmp and /var/tmp sticky bit", {"audit": ["F02"]}),
            ("F06", "Disk write quota enforcement", {"audit": ["R04"]}),
            ("F07", "Per-VPS storage separation", {"audit": ["X01", "O02"]}),
            ("F08", "Concurrent read/write data integrity", {"skip": "not_run"}),
            ("F09", "Guest /etc/shadow and host instance dir modes", {"audit": ["F03", "F04"]}),
        ],
    ),
    (
        "5. RAM & Memory Engine",
        "P0",
        [
            ("M01", "Idle RAM per VPS", {"bench": ["idle_mem_per_vps_mb"]}),
            ("M02", "RAM limits memory.max / memory.high", {"audit": ["R02"]}),
            ("M03", "ZRAM compression ratio and CPU cost", {"skip": "not_run"}),
            ("M04", "File-backed swap usage", {"skip": "not_run"}),
            ("M05", "Shared page-cache behaviour", {"bench": ["page_cache_per_vps_mb", "os_share_ratio"]}),
            ("M06", "Memory pressure and reclaim", {"skip": "destructive"}),
            ("M07", "Out-of-memory handling", {"skip": "destructive"}),
            ("M08", "One VPS OOM does not take down another", {"skip": "destructive"}),
            ("M09", "Memory overcommit and recovery", {"skip": "not_run"}),
            ("M10", "Guest /proc/meminfo vs host MemTotal", {"audit": ["K05"]}),
        ],
    ),
    (
        "6. CPU, PID & Resource Limits",
        "P0",
        [
            ("C01", "cpu.max quota visible / set", {"audit": ["R03"]}),
            ("C02", "pids.max visible / set", {"audit": ["R01"]}),
            ("C03", "Fork-bomb containment", {"skip": "destructive"}),
            ("C04", "CPU stress isolation", {"skip": "destructive"}),
            ("C05", "CPU scheduling and throttling", {"skip": "destructive"}),
            ("C06", "Disk I/O throttling", {"skip": "not_run"}),
            ("C07", "Noisy-neighbour performance impact", {"skip": "destructive"}),
            ("C08", "In-VPS CPU loop latency", {"bench": ["cpu_loop_ms"]}),
        ],
    ),
    (
        "7. Network & SSH Security",
        "P0",
        [
            ("NET01", "Per-VPS IP and network isolation", {"audit": ["X06"]}),
            ("NET02", "SSH login and authorized keys", {"skip": "not_run"}),
            ("NET03", "Host port mapping and port exhaustion", {"skip": "not_run"}),
            ("NET04", "Cross-VPS networking allow/deny", {"audit": ["X06"]}),
            ("NET05", "Nftables / isolated veth (observed via reachability)", {"audit": ["X06"]}),
            ("NET06", "Inbound / outbound connectivity", {"skip": "not_run"}),
            ("NET07", "IPv4 / IPv6 and DNS", {"skip": "not_run"}),
            ("NET08", "High-concurrency network connections", {"skip": "not_run"}),
            ("NET09", "Port scanning and packet isolation", {"skip": "not_run"}),
        ],
    ),
    (
        "8. VPS Density & Scalability",
        "P0",
        [
            ("D01", "Controlled density 10 / 25 / 50 / 100", {"skip": "density"}),
            ("D02", "Idle VPS density", {"skip": "density"}),
            ("D03", "Nginx application VPS density", {"skip": "not_run"}),
            ("D04", "PHP + MySQL e-commerce VPS density", {"skip": "not_run"}),
            ("D05", "CPU-heavy and memory-heavy mix", {"skip": "not_run"}),
            ("D06", "Provisioning speed under high density", {"skip": "density"}),
            ("D07", "Peak-hour latency and throughput", {"skip": "not_run"}),
            ("D08", "Safe maximum occupancy before SLO violation", {"skip": "not_run"}),
        ],
    ),
    (
        "9. Website & Application Workloads",
        "P1",
        [
            ("W01", "Nginx HTTP benchmark", {"skip": "not_run"}),
            ("W02", "Apache benchmark", {"skip": "not_run"}),
            ("W03", "PHP-FPM and WordPress", {"skip": "not_run"}),
            ("W04", "WooCommerce-like e-commerce workload", {"skip": "not_run"}),
            ("W05", "Node.js application", {"skip": "not_run"}),
            ("W06", "Go API service", {"skip": "not_run"}),
            ("W07", "Concurrent HTTP requests and latency", {"skip": "not_run"}),
            ("W08", "Web application persistence after restart", {"skip": "not_run"}),
        ],
    ),
    (
        "10. Database Services",
        "P1",
        [
            ("DB01", "MySQL create / user / database isolation", {"skip": "not_run"}),
            ("DB02", "PostgreSQL create / user / database isolation", {"skip": "not_run"}),
            ("DB03", "MongoDB access controls", {"skip": "not_run"}),
            ("DB04", "Concurrent database reads and writes", {"skip": "not_run"}),
            ("DB05", "Database backup READY artifact + download API (restore not fully validated)", {"skip": "not_run"}),
            ("DB06", "Database crash recovery", {"skip": "not_run"}),
            ("DB07", "Managed DB disk_mb quota configured (disk-full behaviour not probed)", {"skip": "not_run"}),
            ("DB08", "Tenant database security", {"skip": "not_run"}),
            ("DB09", "TLS required on published DB listeners", {"skip": "unit"}),
            ("DB10", "Encrypted backup + safety-backup gate on restore", {"skip": "unit"}),
            ("DB11", "Engine version pin + worker capability advertise", {"skip": "unit"}),
            ("DB12", "Failed provision retry without duplicate project", {"skip": "unit"}),
            ("DB13", "Reserved admin usernames rejected", {"skip": "unit"}),
            ("DB14", "License quota blocks over-provision", {"skip": "unit"}),
            ("DB15", "Redis logical DB is prefix isolation (no native backup claim)", {"skip": "unit"}),
            ("DB16", "Stop/start bills storage-only while data retained", {"skip": "unit"}),
        ],
    ),
    (
        "11. Worker & Auto Scheduler",
        "P0",
        [
            ("WK01", "Multiple worker registration", {"skip": "not_run"}),
            ("WK02", "Worker secret-key enrollment", {"skip": "not_run"}),
            ("WK03", "CPU / RAM / disk capacity discovery", {"skip": "not_run"}),
            ("WK04", "Automatic VPS placement", {"skip": "not_run"}),
            ("WK05", "Select another worker when one is full", {"skip": "not_run"}),
            ("WK06", "Worker offline detection", {"skip": "not_run"}),
            ("WK07", "Insufficient capacity rejection", {"skip": "not_run"}),
            ("WK08", "Scheduler concurrent placement races", {"skip": "not_run"}),
            ("WK09", "Worker removal and draining", {"skip": "not_run"}),
        ],
    ),
    (
        "12. Backup & Object Storage",
        "P1",
        [
            ("B01", "Full VPS backup and restore", {"skip": "not_run"}),
            ("B02", "Second full snapshot after change (not true incremental)", {"skip": "not_run"}),
            ("B03", "S3-compatible storage operations", {"skip": "not_run"}),
            ("B04", "Backup checksum verification", {"skip": "not_run"}),
            ("B05", "Encrypted backup storage", {"skip": "not_run"}),
            ("B06", "Cross-worker restoration", {"skip": "not_run"}),
            ("B07", "Interrupted backup recovery", {"skip": "not_run"}),
            ("B08", "Backup retention and cleanup", {"skip": "not_run"}),
            ("B09", "Docker volume backup create / restore / delete / redeploy", {"skip": "unit"}),
        ],
    ),
    (
        "13. Cloud Admin & Customer UI",
        "P1",
        [
            ("U01", "Admin login API (MFA enrollment/challenge not proven)", {"skip": "not_run"}),
            ("U02", "RBAC and tenant isolation", {"skip": "not_run"}),
            ("U03", "VPS create / update / delete UI", {"skip": "not_run"}),
            ("U04", "Worker and VPS usage dashboard", {"skip": "not_run"}),
            ("U05", "Admin-only protection controls", {"skip": "not_run"}),
            ("U06", "Audit logs and activity history", {"skip": "not_run"}),
            ("U07", "Billing and payment integration", {"skip": "not_run"}),
            ("U08", "License and worker limits enforcement", {"skip": "not_run"}),
        ],
    ),
    (
        "14. Security Attack Simulation",
        "P0",
        [
            ("A01", "Guest-to-host escape regression probes", {"audit": ["I05", "I06", "I07", "N03", "N04", "F05"]}),
            ("A02", "Cross-tenant access attempts", {"audit": ["X01", "X02", "X03", "X06"]}),
            ("A03", "Command injection input validation", {"skip": "not_run"}),
            ("A04", "Privilege escalation controls", {"audit": ["I02", "I03", "F05"]}),
            ("A05", "Wrong SSH key rejected (rate-limit / brute-force detection not proven)", {"skip": "not_run"}),
            ("A06", "Malicious configuration attempts", {"skip": "not_run"}),
            ("A07", "Secrets and credentials exposure", {"skip": "not_run"}),
            ("A08", "API authorization bypass tests", {"skip": "not_run"}),
            ("A09", "Kernel attack surface hardening", {"audit": ["K02", "K04", "K07"]}),
            ("A10", "Edge OWASP CRS WAF bindings (create / rules / error pages / delete)", {"skip": "unit"}),
            ("A11", "WAF ModSecurity audit stats parse (hits / blocks / attacks / IPs)", {"skip": "unit"}),
        ],
    ),
    (
        "15. Performance Comparison",
        "P1",
        [
            ("P01", "CloFix vs LXC on same hardware", {"skip": "compare"}),
            ("P02", "CloFix vs Incus on same hardware", {"skip": "compare"}),
            ("P03", "CloFix vs Docker on same workload", {"skip": "not_run"}),
            ("P04", "Creation and boot time comparison", {"skip": "compare"}),
            ("P05", "Idle and loaded RAM comparison", {"skip": "compare"}),
            ("P06", "Disk footprint and I/O comparison", {"skip": "compare"}),
            ("P07", "Application throughput and tail latency", {"skip": "not_run"}),
            ("P08", "Security and isolation differences", {"skip": "compare"}),
        ],
    ),
    (
        "16. Monitoring & Alerts",
        "P1",
        [
            ("MO01", "Per-VPS CPU / RAM / disk metrics", {"skip": "not_run"}),
            ("MO02", "Worker node health monitoring", {"skip": "not_run"}),
            ("MO03", "Memory pressure / OOM alerts", {"skip": "not_run"}),
            ("MO04", "Storage almost-full alerts", {"skip": "not_run"}),
            ("MO05", "Network traffic metrics", {"skip": "not_run"}),
            ("MO06", "Worker down and VPS crash alerts", {"skip": "not_run"}),
            ("MO07", "Audit / event log integrity", {"skip": "not_run"}),
            ("MO08", "Alert delivery and deduplication", {"skip": "not_run"}),
        ],
    ),
    (
        "17. Reliability & Failure Recovery",
        "P0",
        [
            ("REL01", "Unexpected VPS process termination", {"skip": "not_run"}),
            ("REL02", "Worker reboot / power-loss recovery", {"skip": "not_run"}),
            ("REL03", "Network disconnect / reconnect", {"skip": "not_run"}),
            ("REL04", "Disk full and filesystem read-only", {"skip": "not_run"}),
            ("REL05", "Interrupted OS image update", {"skip": "not_run"}),
            ("REL06", "Control-plane database outage", {"skip": "not_run"}),
            ("REL07", "Backup / restore after simulated failure", {"skip": "not_run"}),
            ("REL08", "Rolling worker upgrade and rollback", {"skip": "not_run"}),
        ],
    ),
    (
        "18. Billing, License & Abuse Controls",
        "P1",
        [
            ("BL01", "Per-VPS resource billing calculation", {"skip": "not_run"}),
            ("BL02", "Usage metering accuracy", {"skip": "not_run"}),
            ("BL03", "Wallet, invoice, and payment state", {"skip": "not_run"}),
            ("BL04", "Subscription expiration enforcement", {"skip": "not_run"}),
            ("BL05", "License worker-count verification", {"skip": "not_run"}),
            ("BL06", "VPS quota enforcement", {"skip": "not_run"}),
            ("BL07", "Duplicate provisioning and double-charge prevention", {"skip": "not_run"}),
            ("BL08", "Suspension / reactivation without data loss", {"skip": "not_run"}),
        ],
    ),
    (
        "19. VPS Production Controls",
        "P0",
        [
            ("VP01", "Protection Mode default-on for new guests", {"skip": "unit"}),
            ("VP02", "SSH key / PAM / Key-Only / Login Block API controls", {"skip": "unit"}),
            ("VP03", "Delete Protection blocks accidental destroy", {"skip": "unit"}),
            ("VP04", "Security Modes + Network Quarantine apply live controls", {"skip": "unit"}),
            ("VP05", "Published port map stays in cloud ranges (SSH/HTTP/HTTPS/custom)", {"skip": "unit"}),
            ("VP06", "Firewall CIDR allow/deny on mapped ports", {"skip": "unit"}),
            ("VP07", "Edge WAF binding attach for FileVPS HTTP path", {"skip": "unit"}),
            ("VP08", "Writable-layer backup only when stopped; restore as new VPS", {"skip": "unit"}),
            ("VP09", "cgroup v2 CPU/RAM/PID/swap limits enforced in placement", {"skip": "unit"}),
            ("VP10", "Cross-tenant instance API isolation", {"skip": "unit"}),
            ("VP11", "Volume / EFS / S3 mount path conflict rejection", {"skip": "unit"}),
            ("VP12", "IAM security:manage required for hardening mutations", {"skip": "unit"}),
        ],
    ),
    (
        "20. Managed Database Production",
        "P0",
        [
            ("MDB01", "MySQL project provision metadata + TLS CA return", {"skip": "unit"}),
            ("MDB02", "PostgreSQL / MongoDB / Redis engines advertised per worker", {"skip": "unit"}),
            ("MDB03", "Job payload not stored plaintext on controller", {"skip": "unit"}),
            ("MDB04", "Quota + license deny without database entitlement", {"skip": "unit"}),
            ("MDB05", "Unknown DB / reserved user operations rejected", {"skip": "unit"}),
            ("MDB06", "FAILED project retry requeues once", {"skip": "unit"}),
            ("MDB07", "Backup READY artifacts carry sha256", {"skip": "unit"}),
            ("MDB08", "Schedules / jobs inventory for ops visibility", {"skip": "unit"}),
            ("MDB09", "Tenant cannot see another account's DB project", {"skip": "unit"}),
            ("MDB10", "Published ports stay in 47000–51999 band", {"skip": "unit"}),
        ],
    ),
    (
        "21. CKS / EKS Production",
        "P0",
        [
            ("CK01", "FileVPS infrastructure_mode rejected for kubelet", {"skip": "unit"}),
            ("CK02", "Placement skips workers without Kubernetes capacity", {"skip": "unit"}),
            ("CK03", "Cluster provision + kubeconfig download", {"skip": "unit"}),
            ("CK04", "Tenant isolation on cluster list/get", {"skip": "unit"}),
            ("CK05", "IAM kubernetes scope required for mutations", {"skip": "unit"}),
            ("CK06", "production_ready false on lab/fake workers", {"skip": "unit"}),
            ("CK07", "production_ready true only with real worker + CLOFIX_K8S_E2E", {"skip": "unit"}),
            ("CK08", "Helm install / StorageClass / etcd backup encrypted", {"skip": "unit"}),
            ("CK09", "Workload catalog + CloFix attach (LB/volume/EFS/S3)", {"skip": "unit"}),
            ("CK10", "VPS group / node pool scale on dedicated CKS workers", {"skip": "unit"}),
            ("CK11", "HA control-plane option recorded (3/5) without public 6443", {"skip": "unit"}),
            ("CK12", "License max_kubernetes_* quota enforced", {"skip": "unit"}),
        ],
    ),
    (
        "22. Docker Engine Production",
        "P0",
        [
            ("DCK01", "Compose policy rejects privileged / host-net / docker.sock", {"skip": "unit"}),
            ("DCK02", "Host publish ports only in 18000–21999", {"skip": "unit"}),
            ("DCK03", "Deploy rejected when Docker engine not ready", {"skip": "unit"}),
            ("DCK04", "Port conflict detection against FileVPS maps", {"skip": "unit"}),
            ("DCK05", "Cross-tenant Docker resource deny", {"skip": "unit"}),
            ("DCK06", "License max_docker_* quota reject", {"skip": "unit"}),
            ("DCK07", "Image / Compose / Git / Quick App create paths", {"skip": "unit"}),
            ("DCK08", "Networks + volumes + delete protection", {"skip": "unit"}),
            ("DCK09", "Volume backup create / restore / redeploy / delete", {"skip": "unit"}),
            ("DCK10", "Edge WAF install path for container/app targets", {"skip": "unit"}),
            ("DCK11", "Security scan job queue (Trivy optional)", {"skip": "unit"}),
            ("DCK12", "FileVPS API still healthy beside Docker product", {"skip": "unit"}),
        ],
    ),
]


def _audit_map(rows):
    """Map audit id -> (status, title, detail)."""
    out = {}
    for row in rows:
        if len(row) < 4:
            continue
        detail = row[4] if len(row) > 4 else ""
        out[row[1]] = (row[2], row[3], detail)
    return out


def normalize_status(st):
    """Catalog statuses are canonical; audit TSV uses SKIP → SKIPPED."""
    if not st:
        return "NOT RUN"
    st = str(st).strip().upper().replace(" ", "_")
    aliases = {
        "SKIP": "SKIPPED",
        "SKIPPED": "SKIPPED",
        "NOT_RUN": "NOT RUN",
        "NOTRUN": "NOT RUN",
        "N/A": "SKIPPED",
        "NA": "SKIPPED",
    }
    return aliases.get(st, st)


def _worst(statuses):
    order = ["FAIL", "WARN", "SKIPPED", "INFO", "PASS"]
    rank = {s: i for i, s in enumerate(order)}
    worst = None
    for s in statuses:
        s = normalize_status(s)
        if s not in rank:
            continue
        if worst is None or rank[s] < rank[worst]:
            worst = s
    return worst


def _fmt_audit_ev(aid, st, title, detail):
    bits = [f"{aid}={st}"]
    title = (title or "").strip()
    detail = (detail or "").strip()
    if title:
        bits.append(title)
    if detail and detail not in title:
        bits.append(detail)
    return " · ".join(bits)


def _thin_evidence(detail):
    d = (detail or "").strip()
    if not d:
        return True
    if d.startswith("http=") and len(d) <= 12:
        return True
    # Circular: only "O01=PASS" style without probe detail.
    if "=" in d and " · " not in d and "pid=" not in d and "rps=" not in d:
        parts = [p.strip() for p in d.split(";")]
        if parts and all(p.endswith("=PASS") or p.endswith("=WARN") or p.endswith("=INFO") for p in parts):
            return True
    return False


def _evidence_from_spec(spec, bench, audit_rows):
    evidence = []
    statuses = []
    amap = _audit_map(audit_rows)
    for aid in spec.get("audit", []):
        row = amap.get(aid)
        if not row:
            continue
        st, title, detail = row
        statuses.append(st)
        evidence.append(_fmt_audit_ev(aid, st, title, detail))
    for key in spec.get("bench", []):
        vals = bench.get(key) or []
        if vals:
            evidence.append(f"{key}={vals[0]}" + (f"…({len(vals)})" if len(vals) > 1 else ""))
    return statuses, evidence


def resolve(cid, case, spec, bench, audit_rows, extra=None):
    statuses, evidence = _evidence_from_spec(spec, bench, audit_rows)

    def done(st, detail):
        return normalize_status(st), detail

    if extra and cid in extra:
        st, detail = extra[cid]
        st = normalize_status(st)
        # Attach concrete audit/bench probe text when PASS evidence is thin or empty.
        if st in ("PASS", "MEASURED", "SMOKE", "UNIT") and (_thin_evidence(detail) or not (detail or "").strip()):
            if evidence:
                merged = (detail or "").strip()
                add = "; ".join(evidence)
                detail = f"{merged} | {add}".strip(" |") if merged else add
            elif not (detail or "").strip():
                # Prefer suite title already stored in detail; else case name + why.
                detail = f"why={case}" if case else "suite row had empty evidence"
        # UI/API http-only smoke must not stay as bare PASS without saying so.
        if st == "PASS" and (detail or "").strip().startswith("http=") and len((detail or "").strip()) <= 12:
            st = "SMOKE"
            detail = f"{detail} · UI/API reachability only (not a live guest/enforcement probe)"
        # Capability / surface-only wording is SMOKE, not production PASS.
        if st == "PASS":
            low = (detail or "").lower()
            if any(x in low for x in (
                "advertised",
                "capability check",
                "api present",
                "api reachable",
                "surface only",
                "not destructive-probed",
                "not cryptographically proven",
                "io.max=n/a",
                "io.max n/a",
            )):
                st = "SMOKE"
                detail = f"{detail} · demoted: surface/capability evidence only"
        return done(st, detail)

    skip = spec.get("skip")
    if skip == "density":
        return done("SKIPPED", "DENSITY_MAX=0 on a worker that already has customer VPS")
    if skip == "destructive":
        return done("SKIPPED", "--destructive not run; live fleet must stay up")
    if skip == "compare":
        return done("SKIPPED", "Incus/LXC/Docker were not measured on this host")
    if skip == "not_run":
        return done("NOT RUN", "No probe in this collection")
    if skip == "unit":
        return done("UNIT", "Go unit/httptest coverage expected; not a live multi-worker or performance probe")

    if statuses:
        return done(_worst(statuses), "; ".join(evidence))
    if spec.get("bench") and evidence:
        return done("MEASURED", "; ".join(evidence))
    if spec.get("audit") or spec.get("bench"):
        return done("NOT RUN", "mapped probe missing from TSV")
    return done("NOT RUN", "No probe in this collection")
