#!/usr/bin/env python3
"""make-report.py — merge results/*.tsv into report.md and report.html
Usage: python3 make-report.py [results_dir] [output.md]
"""
import sys, glob, os, re, statistics as st, datetime, collections, html

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import test_catalog

RES = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(__file__), "results")
OUT = sys.argv[2] if len(sys.argv) > 2 else os.path.join(RES, "report.md")
HTML_OUT = os.path.splitext(OUT)[0] + ".html"
NAMES = {"fx": "CloFix FileVPS", "incus": "Incus", "lxc": "LXC"}
ORDER = ["fx", "incus", "lxc"]


def load_bench():
    data = {}
    for p in glob.glob(os.path.join(RES, "bench-*.tsv")):
        b = os.path.basename(p)[6:-4]
        d = collections.defaultdict(list)
        for line in open(p, encoding="utf-8"):
            k, _, v = line.rstrip("\n").partition("\t")
            d[k].append(v)
        data[b] = d
    return data


def load_audit():
    data = {}
    for p in glob.glob(os.path.join(RES, "audit-*.tsv")):
        b = os.path.basename(p)[6:-4]
        rows = []
        for line in open(p, encoding="utf-8"):
            parts = line.rstrip("\n").split("\t")
            if len(parts) >= 4:
                rows.append(parts + [""] * (5 - len(parts)))
        data[b] = rows
    return data


def nums(vals):
    out = []
    for v in vals:
        try:
            out.append(float(v))
        except ValueError:
            pass
    return out


def med(d, k):
    n = nums(d.get(k, []))
    return st.median(n) if n else None


def first(d, k):
    v = d.get(k, [])
    return v[0] if v else None


def fmt(v, unit=""):
    if v is None:
        return "—"
    return f"{v:,.1f}{unit}" if isinstance(v, float) else f"{v}{unit}"


def cell(d, k, unit=""):
    n = med(d, k)
    if n is not None:
        return fmt(n, unit)
    v = first(d, k)
    return v if v else "—"


def backends_used(bench, audit):
    present = set(bench) | set(audit)
    return [b for b in ORDER if b in present]


def ratio(a, b, lower_better=True):
    if a is None or b in (None, 0):
        return "—"
    r = a / b
    if lower_better:
        return f"{(1 - r) * 100:+.0f}% {'better' if r < 0.95 else ('worse' if r > 1.05 else 'same')}".replace("+-", "-")
    return f"{(r - 1) * 100:+.0f}% {'better' if r > 1.05 else ('worse' if r < 0.95 else 'same')}"


BENCH_ROWS = [
    ("create_ms", "Create time (median)", " ms", True),
    ("start_ready_ms", "Start → ready (median)", " ms", True),
    ("total_ms", "Total launch (median)", " ms", True),
    ("idle_mem_per_vps_mb", "Idle RAM overhead / VPS", " MB", True),
    ("idle_disk_create_per_vps_mb", "Disk / VPS after create", " MB", True),
    ("idle_disk_running_per_vps_mb", "Disk / VPS running", " MB", True),
    ("idle_host_procs_per_vps", "Host processes / VPS", "", True),
    ("kernel_shared_mem_per_vps_mb", "Kernel memory / VPS (slab+stack+pgtables)", " MB", True),
    ("anon_mem_per_vps_mb", "Process (anon) memory / VPS", " MB", True),
    ("page_cache_per_vps_mb", "Page cache / VPS (low = shared base OS)", " MB", True),
    ("rootfs_apparent_mb", "Rootfs apparent size", " MB", True),
    ("os_share_ratio", "OS share ratio (apparent ÷ real disk; >1 = shared)", "x", False),
    ("cpu_idle_overhead_pct", "Idle host CPU overhead (all VPS)", " %", True),
    ("cpu_loop_ms", "In-VPS CPU loop (median)", " ms", True),
    ("exec_latency_ms", "Exec latency (median)", " ms", True),
    ("density_vps_count", "Max VPS reached (test cap)", "", False),
    ("density_mem_per_vps_mb", "RAM / VPS at max density", " MB", True),
    ("density_avg_launch_s", "Avg launch time during density", " s", True),
    ("projected_idle_density", "Projected idle VPS on this host", "", False),
]


def bench_section(bench):
    bs = [b for b in ORDER if b in bench]
    L = ["## 1. Performance Benchmark", ""]
    if not bs:
        return L + [
            "_No `bench-*.tsv` files were found. Measured numbers are unverified until the scripts run on a dedicated Linux host._",
            "",
        ]
    host = bench[bs[0]]
    L += [f"Host: {first(host,'host_cores')} cores, {first(host,'host_mem_total_mb')} MB RAM, kernel {first(host,'kernel')}", ""]
    compare = "fx" in bs and len(bs) > 1
    hdr = "| Metric | " + " | ".join(NAMES[b] for b in bs) + " |"
    if compare:
        hdr += " " + " | ".join(f"FileVPS vs {NAMES[o]}" for o in bs if o != "fx") + " |"
    L += [hdr, "|" + "---|" * (hdr.count("|") - 1)]
    for key, label, unit, lower in BENCH_ROWS:
        vals = {b: med(bench[b], key) for b in bs}
        row = f"| {label} | " + " | ".join(cell(bench[b], key, unit) for b in bs) + " |"
        if compare:
            row += " " + " | ".join(ratio(vals["fx"], vals[o], lower) for o in bs if o != "fx") + " |"
        L.append(row)
    L += ["", "OS share mode: " + ", ".join(f"{NAMES[b]}={first(bench[b],'os_share_mode') or '—'}" for b in bs)]
    L += ["", "Stop reasons (density): " + ", ".join(f"{NAMES[b]}={first(bench[b],'density_stop_reason') or '—'}" for b in bs), ""]
    if compare:
        L += ["_better = FileVPS better by >5%, worse = FileVPS worse by >5%, same = within ±5%. For lower-is-better metrics, a positive percent means FileVPS uses less._", ""]
    return L


def catalog_section(bench, audit):
    fx_bench = bench.get("fx", {})
    fx_audit = audit.get("fx", [])
    L = [
        "## 3. Master test catalog",
        "",
        "These are the P0/P1 cases from the FileVPS QA checklist. Status comes from the last `bench-fx.tsv` / `audit-fx.tsv` on this host. Cases without a probe stay **NOT RUN**. Skipped items were not executed on purpose (density cap, no `--destructive`, no compare backends). Later engine fixes are not treated as a new measurement.",
        "",
    ]
    counts = collections.Counter()
    body = []
    for title, pri, cases in test_catalog.SUITES:
        body += [f"### {title} ({pri})", "", "| ID | Case | Status | Evidence |", "|---|---|---|---|"]
        for cid, name, spec in cases:
            status, detail = test_catalog.resolve(name, spec, fx_bench, fx_audit)
            counts[status] += 1
            body.append(f"| {cid} | {name} | {status} | {detail.replace('|', '/')} |")
        body.append("")
    total = sum(counts.values())
    L += [
        "| Status | Count |",
        "|---|---|",
        f"| FAIL | {counts['FAIL']} |",
        f"| WARN | {counts['WARN']} |",
        f"| PASS | {counts['PASS']} |",
        f"| MEASURED | {counts['MEASURED']} |",
        f"| INFO | {counts['INFO']} |",
        f"| SKIPPED | {counts['SKIPPED']} |",
        f"| NOT RUN | {counts['NOT RUN']} |",
        f"| **Total** | **{total}** |",
        "",
    ]
    L += body
    return L


def audit_section(audit):
    bs = [b for b in ORDER if b in audit]
    L = ["## 2. Security Audit", ""]
    if not bs:
        return L + [
            "_No `audit-*.tsv` files were found. The expected FileVPS results below come from engine code, not a live host run._",
            "",
        ]
    cats = {"ISO": "Root isolation", "NS": "Namespace escape", "XV": "Cross-VPS access",
            "FS": "Filesystem security", "RES": "Resource exhaustion", "KOS": "Kernel & OS sharing"}
    L += ["### Summary (FAIL / WARN / PASS)", "", "| Category | " + " | ".join(NAMES[b] for b in bs) + " |", "|" + "---|" * (len(bs) + 1)]
    tot = {b: collections.Counter() for b in bs}
    for c, cn in cats.items():
        cells = []
        for b in bs:
            cnt = collections.Counter(r[2] for r in audit[b] if r[0] == c)
            tot[b].update(cnt)
            cells.append(f"{cnt['FAIL']} / {cnt['WARN']} / {cnt['PASS']}")
        L.append(f"| {cn} | " + " | ".join(cells) + " |")
    L.append("| **Total** | " + " | ".join(f"**{tot[b]['FAIL']} / {tot[b]['WARN']} / {tot[b]['PASS']}**" for b in bs) + " |")
    L.append("")
    for b in bs:
        L += [f"### Findings — {NAMES[b]}", ""]
        icon = {"FAIL": "FAIL", "WARN": "WARN", "PASS": "PASS", "INFO": "INFO", "SKIP": "SKIP"}
        rows = sorted(audit[b], key=lambda r: ["FAIL", "WARN", "INFO", "SKIP", "PASS"].index(r[2]) if r[2] in icon else 9)
        L += ["| Status | ID | Check | Detail |", "|---|---|---|---|"]
        for cat, cid, status, title, detail in rows:
            L.append(f"| {icon.get(status, status)} | {cid} | {title} | {detail.replace('|','/')} |")
        L.append("")
    return L


CODE_NOTES = """## 4. FileVPS code-backed expectations

These rows describe what the current worker engine is written to do. They are **not** live measurements.

| ID | Expected | Why (code) | Verification |
|---|---|---|---|
| I01 | PASS when `user_ns=true` | `CLONE_NEWUSER` maps guest 0 to `uid_base` (default 100000) | Unverified on this Mac workspace |
| I02 | PASS | Guest allowlist drops SYS_MODULE/RAWIO/BOOT/TIME/PTRACE/SYS_ADMIN | Unverified |
| I03 | PASS | `fx-init` installs a seccomp filter and `no_new_privs` | Unverified |
| I04 | WARN | AppArmor is detected on the host; no per-guest profile is applied | Unverified |
| I05 I06 | PASS | Guest `/dev` is a private tmpfs; mount/kexec/module syscalls are denied | Unverified |
| I07 | PASS | `mount` is in the seccomp deny list | Unverified |
| N01 N02 | PASS | New mount, PID, UTS, IPC, net, cgroup (and user when enabled) namespaces | Unverified |
| N05 | PASS | After the process is placed in `/clofix-filevps/<id>`, `CLONE_NEWCGROUP` makes the guest see `0::/`. Host `pids.max` / `memory.max` / `cpu.max` still apply. | Unverified |
| X01 X03 X04 | PASS | Separate OverlayFS upper layers and namespaces | Unverified |
| X05 | PASS when `user_ns=true` | Each guest gets a unique `host_uid` range (`uid_base + index * 65536`) | Unverified |
| X06 | PASS | Each veth is `bridge link set isolated on`; nftables default-drop forward | Unverified |
| R01 R02 R03 | PASS | cgroup v2 sets `pids.max`, `memory.max`/`memory.high`, `cpu.max` | Unverified |
| R04 R12 | PASS | `disk_mb` is a loop+ext4 backing for overlay upper/work; guest `df` shows the limit | Unverified |
| K05 | PASS | `fx-init` bind-mounts a per-guest `/proc/meminfo` from `ram_mb` | Unverified |
| K01 | INFO | FileVPS shares the host kernel; it is not KVM/QEMU | Documented |
| O01 O02 | PASS | Shared read-only image + per-guest OverlayFS upper layer | Unverified |

Production workers default `user_ns=true`. Local Docker Desktop Compose sets `CLOFIX_USER_NS=false`, which makes I01 FAIL and is development-only.

## 5. Methodology and limitations

- Same host, one backend at a time. FileVPS uses bundled Ubuntu 24.04. Incus/LXC default to Debian 12 unless you override `INCUS_IMAGE` or `BASE_DISTRO`/`BASE_REL`.
- FileVPS create requires an SSH public key, cgroup limits, and (for networked guests) a `10.231.0.0/16` address plus an SSH host port in 22000–22099. Density tests skip guest networking because only 100 SSH ports exist.
- Protection Mode stays off during the audit so writes inside the guest can be tested. That is not the default for UI-created VPS instances.
- RAM overhead is the host `MemTotal−MemAvailable` delta. Disk is a `df` delta on `STORAGE_PATH`. OverlayFS should show a high OS-share ratio.
- Density is bounded by `DENSITY_MAX` and a memory reserve. It is idle density, not workload density.
- The audit is a configuration/behaviour check, not a kernel exploit assessment. A PASS means that probe did not cross the boundary. Shared-kernel isolation can still fail if the host kernel is buggy.
- Resource probes are bounded. Re-run with `--destructive` only on a throw-away host.
- Run each benchmark at least three times and compare medians before drawing business conclusions.
"""


def methodology():
    return ["## 6. How to produce measured results", "",
            "On a dedicated Linux worker that already has `clofix-worker`, Incus, and/or LXC:",
            "",
            "```bash",
            "sudo ./run-compare.sh            # backends that are installed",
            "sudo ./clofix-bench.sh fx",
            "sudo ./clofix-audit.sh fx",
            "python3 make-report.py results results/report.md",
            "```",
            "",
            "Do not run density or `--destructive` probes on a production host.",
            ""]


def md_inline(s: str) -> str:
    s = html.escape(s)
    s = re.sub(r"`([^`]+)`", r"<code>\1</code>", s)
    s = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", s)
    return s


def md_to_html(md: str, title: str) -> str:
    rows = []
    in_table = False
    in_pre = False
    in_ul = False

    def close_lists():
        nonlocal in_ul
        if in_ul:
            rows.append("</ul>")
            in_ul = False

    for line in md.splitlines():
        if line.startswith("```"):
            close_lists()
            if in_pre:
                rows.append("</pre>")
                in_pre = False
            else:
                rows.append("<pre>")
                in_pre = True
            continue
        if in_pre:
            rows.append(html.escape(line))
            continue
        if line.startswith("|") and "---" not in line:
            close_lists()
            cells = [c.strip() for c in line.strip("|").split("|")]
            tag = "th" if not in_table else "td"
            if not in_table:
                rows.append("<table>")
                in_table = True
            rows.append("<tr>" + "".join(f"<{tag}>{md_inline(c)}</{tag}>" for c in cells) + "</tr>")
            continue
        if in_table:
            if line.startswith("|"):
                continue
            rows.append("</table>")
            in_table = False
        if line.startswith("# "):
            close_lists()
            rows.append(f"<h1>{md_inline(line[2:])}</h1>")
        elif line.startswith("## "):
            close_lists()
            rows.append(f"<h2>{md_inline(line[3:])}</h2>")
        elif line.startswith("### "):
            close_lists()
            rows.append(f"<h3>{md_inline(line[4:])}</h3>")
        elif line.startswith("- "):
            if not in_ul:
                rows.append("<ul>")
                in_ul = True
            rows.append(f"<li>{md_inline(line[2:])}</li>")
        elif line.startswith("_") and line.endswith("_") and len(line) > 2:
            close_lists()
            rows.append(f"<p class='note'>{md_inline(line[1:-1])}</p>")
        elif line.strip() == "":
            close_lists()
            rows.append("")
        else:
            close_lists()
            rows.append(f"<p>{md_inline(line)}</p>")
    if in_table:
        rows.append("</table>")
    close_lists()
    if in_pre:
        rows.append("</pre>")
    body = "\n".join(rows)
    return f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{html.escape(title)}</title>
<style>
body {{ font-family: Georgia, serif; max-width: 1100px; margin: 2rem auto; padding: 0 1.5rem; color: #1a1a1a; }}
h1,h2,h3 {{ font-family: system-ui, sans-serif; }}
table {{ border-collapse: collapse; width: 100%; margin: 1rem 0; font-size: 0.95rem; }}
th,td {{ border: 1px solid #ccc; padding: 0.4rem 0.55rem; text-align: left; vertical-align: top; }}
th {{ background: #f3f3f3; }}
.note {{ color: #444; font-style: italic; }}
pre {{ background: #f6f6f6; padding: 0.8rem; overflow: auto; }}
code {{ background: #f0f0f0; padding: 0 0.25rem; font-size: 0.9em; }}
ul {{ margin: 0.6rem 0 0.6rem 1.2rem; }}
</style>
</head>
<body>
{body}
</body>
</html>
"""


def main():
    os.makedirs(os.path.dirname(OUT) or ".", exist_ok=True)
    bench, audit = load_bench(), load_audit()
    measured = bool(bench or audit)
    status = "Measured results from this host" if measured else "Code-backed report; runtime results not yet collected"
    used = backends_used(bench, audit)
    title = "CloFix FileVPS — Benchmark and Security Report"
    if len(used) > 1:
        title = "CloFix FileVPS vs Incus/LXC — Benchmark and Security Report"
    L = [
        f"# {title}",
        "",
        f"Generated: {datetime.datetime.now():%Y-%m-%d %H:%M}",
        "",
        f"**Status:** {status}.",
        "",
        "The bench and audit scripts now call `clofix-worker fx` (not the old placeholder `clofix-fx` CLI) and use `/var/lib/clofix-filevps/instances/<id>/rootfs-mount`. FileVPS create uses Ubuntu 24.04, a throw-away SSH key, cgroup limits, and Protection Mode off. Density skips guest networking because only SSH host ports 22000–22099 exist.",
        "",
    ]
    notes = os.path.join(RES, "run-notes.md")
    if os.path.isfile(notes):
        L += open(notes, encoding="utf-8").read().splitlines() + [""]
    L += bench_section(bench) + audit_section(audit) + catalog_section(bench, audit)
    L += CODE_NOTES.splitlines() + [""] + methodology()
    text = "\n".join(L) + "\n"
    open(OUT, "w", encoding="utf-8").write(text)
    open(HTML_OUT, "w", encoding="utf-8").write(md_to_html(text, title))
    print("wrote", OUT)
    print("wrote", HTML_OUT)


if __name__ == "__main__":
    main()
