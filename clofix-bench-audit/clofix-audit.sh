#!/usr/bin/env bash
# clofix-audit.sh — defensive isolation audit for fx (CloFix FileVPS) | incus | lxc
# Sections: 1 root isolation, 2 namespace escape, 3 cross-VPS, 4 filesystem, 5 resource exhaustion, 6 kernel & OS sharing
# Usage: sudo ./clofix-audit.sh <fx|incus|lxc> [--destructive]
#
# Non-destructive checks run by default (read-only probes inside two throw-away VPSes).
# --destructive adds BOUNDED resource-exhaustion probes (pids/memory/disk/CPU).
# There is NO unbounded fork bomb. Run on a dedicated test host only.
set -uo pipefail
BACKEND="${1:?usage: $0 <fx|incus|lxc> [--destructive]}"
DESTRUCTIVE=0; [ "${2:-}" = "--destructive" ] && DESTRUCTIVE=1
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
need_root
need_backend
# Audit writes inside guests; FileVPS Protection Mode must stay off.
FX_PROTECTION=0
FX_NETWORK=1

PIDS_PROBE="${PIDS_PROBE:-3000}"     # max processes to try spawning
MEM_PROBE_MB="${MEM_PROBE_MB:-1536}" # max MB to try allocating
DISK_PROBE_MB="${DISK_PROBE_MB:-1024}"
CPU_PROBE_S="${CPU_PROBE_S:-15}"
OUT="${OUT:-$HERE/results}"; mkdir -p "$OUT"
RES="$OUT/audit-$BACKEND.tsv"; : > "$RES"
A="cfxa-$BACKEND-a"; B="cfxa-$BACKEND-b"

# rec <category> <id> <PASS|WARN|FAIL|INFO|SKIP> <title> [detail]
rec() {
  printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" \
    "$(printf '%s' "${5:-}" | tr '\t\n' '  ' | cut -c1-300)" >> "$RES"
  printf '  [%-4s] %-5s %s\n' "$3" "$2" "$4"
}
ex() { vps_exec "$@" 2>&1 | grep -v -E '^exit status [0-9]+$'; }

echo "== Security audit: $BACKEND (destructive=$DESTRUCTIVE) =="
for n in "$A" "$B"; do
  track "$n"
  vps_create "$n" && vps_start "$n" && wait_ready "$n" 120 || { echo "cannot create $n — check adapter in lib.sh"; exit 2; }
done

# ============================================================================
# 1. ROOT ISOLATION
# ============================================================================
echo "-- 1. Root isolation"
uidmap=$(ex "$A" "cat /proc/self/uid_map")
kind=$(echo "$uidmap" | awk 'NR==1{print ($1==0&&$2==0)?"identity":"mapped"}')
USERNS=1
if [ "$kind" = "identity" ]; then
  USERNS=0
  rec ISO I01 FAIL "Container root == host root (identity uid_map, no user namespace)" "$uidmap"
else
  rec ISO I01 PASS "Container root is mapped to unprivileged host uid" "$uidmap"
fi

capeff=$(ex "$A" "grep CapEff /proc/self/status | awk '{print \$2}'" | tr -d '[:space:]')
capdec=$((16#${capeff:-0}))
has() { (( (capdec >> $1) & 1 )); }
bad=""
has 16 && bad="$bad SYS_MODULE"; has 17 && bad="$bad SYS_RAWIO"
has 22 && bad="$bad SYS_BOOT";   has 25 && bad="$bad SYS_TIME"
has 19 && bad="$bad SYS_PTRACE"
adm=0; has 21 && adm=1
if [ -n "$bad" ]; then rec ISO I02 FAIL "Dangerous capabilities in container root:$bad" "CapEff=$capeff"
elif [ $adm -eq 1 ] && [ $USERNS -eq 0 ]; then rec ISO I02 FAIL "CAP_SYS_ADMIN held without user namespace" "CapEff=$capeff"
elif [ $adm -eq 1 ]; then rec ISO I02 WARN "CAP_SYS_ADMIN held (only scoped to userns)" "CapEff=$capeff"
else rec ISO I02 PASS "No dangerous capabilities" "CapEff=$capeff"; fi

sc=$(ex "$A" "grep -E '^(Seccomp|NoNewPrivs):' /proc/self/status" | tr '\n' ' ')
case "$sc" in *"Seccomp:	2"*|*"Seccomp: 2"*) rec ISO I03 PASS "Seccomp filter active" "$sc";;
  *) rec ISO I03 WARN "No seccomp filter on container init" "$sc";; esac

lsm=$(ex "$A" "cat /proc/self/attr/current 2>/dev/null")
case "$lsm" in ""|"unconfined"*|*"Invalid"*) rec ISO I04 WARN "No AppArmor/SELinux confinement" "$lsm";;
  *) rec ISO I04 PASS "LSM confinement active" "$lsm";; esac

w=""
for p in /proc/sysrq-trigger /proc/sys/kernel/core_pattern /sys/kernel/uevent_helper /proc/sys/kernel/modprobe /sys/kernel/security; do
  ex "$A" "test -w $p" >/dev/null && w="$w $p"
done
[ -n "$w" ] && rec ISO I05 FAIL "Sensitive kernel interfaces writable:$w" || rec ISO I05 PASS "Sensitive /proc,/sys paths not writable"

dev=$(ex "$A" "ls /dev/mem /dev/kmem /dev/port 2>/dev/null; find /dev -maxdepth 2 -type b 2>/dev/null" | tr '\n' ' ')
[ -n "${dev// /}" ] && rec ISO I06 FAIL "Raw device nodes visible in container" "$dev" || rec ISO I06 PASS "No /dev/mem,kmem,port or block devices"

if ex "$A" "mkdir -p /tmp/m && mount -t tmpfs none /tmp/m && umount /tmp/m" >/dev/null; then
  rec ISO I07 WARN "Container root can mount filesystems" "mount(tmpfs) succeeded"
else rec ISO I07 PASS "mount() denied inside container"; fi

dr=$(ex "$A" "cat /proc/sys/kernel/dmesg_restrict 2>/dev/null"); dm=$(ex "$A" "dmesg 2>&1 | head -1")
case "$dm" in *"not permitted"*|*"denied"*|*"Operation"*|"") rec ISO I08 PASS "dmesg not readable" "restrict=$dr";;
  *) rec ISO I08 WARN "Kernel log readable from container (info leak)" "restrict=$dr";; esac

# ============================================================================
# 2. NAMESPACE ESCAPE SURFACE
# ============================================================================
echo "-- 2. Namespace escape"
shared=""
for ns in pid net mnt uts ipc user cgroup; do
  h=$(readlink /proc/1/ns/$ns 2>/dev/null); c=$(ex "$A" "readlink /proc/self/ns/$ns" | tr -d '[:space:]')
  [ -n "$h" ] && [ -n "$c" ] && [ "$h" = "$c" ] && shared="$shared $ns"
done
[ -n "$shared" ] && rec NS N01 FAIL "Namespaces shared with host:$shared" || rec NS N01 PASS "All checked namespaces differ from host"

inside=$(ex "$A" "ls /proc | grep -c '^[0-9]'" | tr -d '[:space:]'); hostp=$(proc_count)
if [ "${inside:-0}" -ge $((hostp*9/10)) ] && [ "$hostp" -gt 20 ]; then
  rec NS N02 FAIL "Container sees (nearly) all host PIDs" "inside=$inside host=$hostp"
else rec NS N02 PASS "PID namespace isolated" "inside=$inside host=$hostp"; fi

# The guest OverlayFS root lists host lowerdir/upperdir under /var/lib/clofix-filevps.
# That is the VPS's own root, not a leaked runtime socket. Still flag docker.sock
# and other host runtime paths.
leak=$(ex "$A" "cat /proc/self/mountinfo" | awk '
  /docker\.sock|\/run\/host|containerd|\/var\/lib\/(incus|lxc|docker)(\/|[[:space:]])/ { print; next }
  /\/var\/lib\/clofix/ {
    if ($0 ~ / - overlay overlay / && $0 ~ /lowerdir=\/var\/lib\/clofix-filevps\// && $0 ~ /upperdir=\/var\/lib\/clofix-filevps\//) next
    print
  }
' | head -3)
[ -n "$leak" ] && rec NS N03 FAIL "Host paths / runtime sockets mounted in container" "$leak" || rec NS N03 PASS "No host runtime paths in mountinfo"

ra=$(ex "$A" "find /sys/fs/cgroup -name release_agent -writable 2>/dev/null | head -1")
[ -n "$ra" ] && rec NS N04 FAIL "Writable cgroup release_agent (classic escape vector)" "$ra" || rec NS N04 PASS "No writable release_agent"

cg=$(ex "$A" "cat /proc/self/cgroup | head -1")
case "$cg" in *"::/"|*":/") rec NS N05 PASS "cgroup path hidden by cgroup namespace" "$cg";;
  *) rec NS N05 WARN "Host cgroup path visible to container" "$cg";; esac

if ex "$A" "ls /proc/1/root/ >/dev/null && readlink /proc/1/root" | grep -qx '/'; then
  rec NS N06 PASS "/proc/1/root resolves inside container root"
else rec NS N06 WARN "/proc/1/root probe inconclusive"; fi

# ============================================================================
# 3. CROSS-VPS ACCESS
# ============================================================================
echo "-- 3. Cross-VPS access"
ex "$B" "echo secretB > /root/.cfx_marker_B; echo secretB > /tmp/.cfx_marker_B; echo secretB > /dev/shm/.cfx_marker_B; setsid sh -c 'exec sleep 7351' >/dev/null 2>&1 &" >/dev/null
sleep 1
f=$(ex "$A" "find / -xdev -name .cfx_marker_B 2>/dev/null; find /dev/shm /mnt /media /srv /var/lib -maxdepth 5 -name .cfx_marker_B 2>/dev/null")
[ -n "$f" ] && rec XV X01 FAIL "VPS-A can see VPS-B files" "$f" || rec XV X01 PASS "VPS-B marker files invisible from VPS-A"

e=$(ex "$A" "ls -d /var/lib/incus /var/lib/lxc /var/lib/clofix-fx /var/lib/clofix-filevps /var/lib/docker 2>/dev/null")
[ -n "$e" ] && rec XV X02 FAIL "Runtime state directories visible in VPS-A" "$e" || rec XV X02 PASS "Host runtime directories not visible"

p=$(ex "$A" "ps -eo args 2>/dev/null | grep '[s]leep 7351' || (for d in /proc/[0-9]*; do tr '\0' ' ' < \$d/cmdline 2>/dev/null; echo; done | grep '[s]leep 7351')")
[ -n "$p" ] && rec XV X03 FAIL "VPS-B process visible from VPS-A" "$p" || rec XV X03 PASS "VPS-B processes invisible from VPS-A"

shared=""
for ns in pid net mnt uts ipc cgroup; do
  a=$(ex "$A" "readlink /proc/self/ns/$ns" | tr -d '[:space:]'); b=$(ex "$B" "readlink /proc/self/ns/$ns" | tr -d '[:space:]')
  [ -n "$a" ] && [ "$a" = "$b" ] && shared="$shared $ns"
done
[ -n "$shared" ] && rec XV X04 FAIL "VPS-A and VPS-B share namespaces:$shared" || rec XV X04 PASS "No namespace shared between VPSes"

ua=$(ex "$A" "awk 'NR==1{print \$2}' /proc/self/uid_map" | tr -d '[:space:]'); ub=$(ex "$B" "awk 'NR==1{print \$2}' /proc/self/uid_map" | tr -d '[:space:]')
if [ "$ua" = "$ub" ] && [ "$ua" != "0" ]; then rec XV X05 WARN "VPSes share the same host uid range (no per-VPS uid isolation)" "base=$ua"
elif [ "$ua" = "0" ]; then rec XV X05 FAIL "Identity uid mapping — root in A equals root in B on host"
else rec XV X05 PASS "Distinct host uid ranges per VPS" "A=$ua B=$ub"; fi

ipb=$(ex "$B" "hostname -I 2>/dev/null | awk '{print \$1}'" | tr -d '[:space:]')
if [ -n "$ipb" ]; then
  if ex "$A" "ping -c1 -W2 $ipb >/dev/null 2>&1 || timeout 3 bash -c 'echo > /dev/tcp/$ipb/22'" >/dev/null; then
    rec XV X06 WARN "VPS-A can reach VPS-B over the network (verify this is intended policy)" "B=$ipb"
  else rec XV X06 PASS "VPS-A cannot reach VPS-B ($ipb) — inter-VPS traffic blocked"; fi
else rec XV X06 SKIP "VPS-B has no IP"; fi

# ============================================================================
# 4. FILESYSTEM SECURITY
# ============================================================================
echo "-- 4. Filesystem"
suid=$(ex "$A" "find / -xdev -perm -4000 -type f 2>/dev/null" | wc -l)
[ "$suid" -gt 12 ] && rec FS F01 WARN "Many SUID binaries ($suid)" || rec FS F01 PASS "SUID binary count acceptable ($suid)"

ww=$(ex "$A" "find / -xdev -type d -perm -0002 ! -perm -1000 2>/dev/null | head -5")
[ -n "$ww" ] && rec FS F02 FAIL "World-writable dirs without sticky bit" "$ww" || rec FS F02 PASS "No world-writable dirs lacking sticky bit"

sh=$(ex "$A" "stat -c %a /etc/shadow" | tr -d '[:space:]')
case "$sh" in 600|640|000|400|0) rec FS F03 PASS "/etc/shadow mode $sh";; *) rec FS F03 FAIL "/etc/shadow mode $sh";; esac

rf=$(vps_rootfs "$A"); pd=$(dirname "$rf")
if [ -d "$pd" ]; then
  m=$(stat -c %a "$pd")
  [ "${m: -1}" != "0" ] && rec FS F04 WARN "VPS dir on host is world-accessible (mode $m)" "$pd" || rec FS F04 PASS "VPS dir on host not world-accessible (mode $m)" "$pd"
else rec FS F04 SKIP "Rootfs path not found — set FX_ROOTFS / INCUS_ROOTFS" "$pd"; fi

if ex "$A" "mknod /tmp/blk b 8 0 && rm -f /tmp/blk" >/dev/null; then rec FS F05 FAIL "Container root can create block device nodes"
else rec FS F05 PASS "mknod of block devices denied"; fi

if [ -n "$FX_FILE_READ" ] && [ "$BACKEND" = fx ]; then
  ex "$A" "ln -sf ../../../../../../etc/hostname /root/esc" >/dev/null
  hh=$(hostname); got=$(tpl "${FX_FILE_READ}" "$A" 2>&1)
  case "$got" in *"$hh"*) rec FS F06 FAIL "Control-plane file API followed symlink out of the VPS root" "$got";;
    *) rec FS F06 PASS "File API did not follow escaping symlink";; esac
else rec FS F06 SKIP "Symlink-escape test needs FX_FILE_READ template (control-plane file read)"; fi

# ============================================================================
# 5. RESOURCE EXHAUSTION (bounded; needs --destructive)
# ============================================================================
echo "-- 5. Resource exhaustion"
# After CLONE_NEWCGROUP the guest may not see cgroupfs. Read host limits instead.
cgr() {
  local id=$1 file=$2 legacy=$3
  if [ "$BACKEND" = fx ]; then
    cat "/sys/fs/cgroup/clofix-filevps/$id/$file" 2>/dev/null
  else
    ex "$id" "cat /sys/fs/cgroup/$file 2>/dev/null || cat /sys/fs/cgroup/$legacy 2>/dev/null"
  fi
}
pm=$(cgr "$A" pids.max pids/pids.max | tr -d '[:space:]'); mm=$(cgr "$A" memory.max memory/memory.limit_in_bytes | tr -d '[:space:]')
cm=$(cgr "$A" cpu.max cpu/cpu.cfs_quota_us | tr -d '[:space:]')
[ -z "$pm" ] || [ "$pm" = max ] && rec RES R01 FAIL "No pids.max visible — fork bomb can exhaust host PIDs" "pids.max=${pm:-n/a}" || rec RES R01 PASS "pids.max set" "$pm"
case "$mm" in ""|max|9223372036854*) rec RES R02 FAIL "No memory limit visible" "memory.max=${mm:-n/a}";; *) rec RES R02 PASS "Memory limit set" "$mm";; esac
case "$cm" in ""|max*|-1) rec RES R03 WARN "No CPU quota visible" "cpu.max=${cm:-n/a}";; *) rec RES R03 PASS "CPU quota set" "$cm";; esac
inkb=$(ex "$A" "df -P / | awk 'NR==2{print \$2}'" | tr -d '[:space:]'); htot=$(( $(disk_total_b)/1024 ))
if [ -n "$inkb" ] && [ "$inkb" -ge $((htot*9/10)) ]; then rec RES R04 WARN "VPS sees ~whole host disk — no visible per-VPS disk quota" "vps=${inkb}KB host=${htot}KB"
else rec RES R04 PASS "Per-VPS disk size limited" "vps=${inkb}KB host=${htot}KB"; fi

if [ $DESTRUCTIVE -ne 1 ]; then
  rec RES R10 SKIP "Active exhaustion probes skipped (use --destructive)"
else
  # --- pids ---
  l0=$(cut -d' ' -f1 /proc/loadavg)
  spawned=$(ex "$A" "i=0; while [ \$i -lt $PIDS_PROBE ]; do sleep 40 >/dev/null 2>&1 & [ \$? -eq 0 ] || break; i=\$((i+1)); done; jobs -p | wc -l; kill \$(jobs -p) 2>/dev/null" | tail -1 | tr -d '[:space:]')
  ex "$A" "pkill sleep" >/dev/null
  if [ "${spawned:-0}" -ge "$PIDS_PROBE" ]; then rec RES R10 FAIL "VPS spawned $PIDS_PROBE processes with no limit hit" "load before=$l0"
  else rec RES R10 PASS "Process limit enforced at ~${spawned:-?}"; fi
  sleep 3

  # --- memory ---
  a0=$(mem_avail_kb); cap=$(( $(mem_avail_kb)/1024 - 1024 )); [ "$cap" -gt "$MEM_PROBE_MB" ] && cap=$MEM_PROBE_MB
  ex "$A" "awk 'BEGIN{for(i=0;i<$cap;i++){s[i]=sprintf(\"%1048576s\",\"x\")}; system(\"sleep 3\")}'" >/dev/null; rc=$?
  a1=$(mem_avail_kb)
  if [ $rc -ne 0 ]; then rec RES R11 PASS "Allocation of ${cap}MB was killed/denied inside VPS (rc=$rc)"
  else rec RES R11 FAIL "VPS allocated ${cap}MB without being limited" "mem.max=$mm"; fi
  sleep 3

  # --- disk ---
  ex "$A" "dd if=/dev/zero of=/root/fill bs=1M count=$DISK_PROBE_MB 2>&1 | tail -1; sync" >/dev/null; rc=$?
  sz=$(ex "$A" "stat -c %s /root/fill 2>/dev/null" | tr -d '[:space:]'); ex "$A" "rm -f /root/fill" >/dev/null
  if [ "${sz:-0}" -ge $((DISK_PROBE_MB*1048576)) ]; then rec RES R12 WARN "Wrote ${DISK_PROBE_MB}MB with no quota stop (need ≥ quota to prove)" "size=$sz"
  else rec RES R12 PASS "Disk write stopped by quota at $sz bytes"; fi

  # --- CPU + noisy neighbour ---
  loop="awk 'BEGIN{for(i=0;i<20000000;i++)s+=i}'"
  t0=$(now_ms); ex "$B" "$loop" >/dev/null; base=$(( $(now_ms)-t0 ))
  ex "$A" "for k in 1 2 3 4 5 6 7 8; do (timeout $CPU_PROBE_S sh -c 'while :; do :; done' &) ; done" >/dev/null
  sleep 2; busy=$(cpu_busy_pct 8)
  t0=$(now_ms); ex "$B" "$loop" >/dev/null; loaded=$(( $(now_ms)-t0 ))
  cores=$(awk -v b="$busy" -v n="$(ncpu)" 'BEGIN{printf "%.2f",b*n/100}')
  rec RES R13 INFO "Host CPU while VPS-A runs 8 busy loops: ${busy}% (~${cores} cores of $(ncpu))"
  r=$(awk -v a="$loaded" -v b="$base" 'BEGIN{printf "%.2f",a/b}')
  if awk -v r="$r" 'BEGIN{exit !(r>1.5)}'; then rec RES R14 WARN "Noisy neighbour: VPS-B loop slowed ${r}x while VPS-A burned CPU" "idle=${base}ms loaded=${loaded}ms"
  else rec RES R14 PASS "VPS-B loop slowdown ${r}x under neighbour load" "idle=${base}ms loaded=${loaded}ms"; fi
  sleep $((CPU_PROBE_S))
  rec RES R15 INFO "Host responsive after probes; load1=$(cut -d' ' -f1 /proc/loadavg)"
fi

# ============================================================================
# 6. KERNEL & OS SHARING
# ============================================================================
echo "-- 6. Kernel & OS sharing"
kin=$(ex "$A" "uname -r" | tr -d '[:space:]'); khost=$(uname -r)
[ "$kin" = "$khost" ] && rec KOS K01 INFO "Kernel shared with host ($khost) — a kernel bug affects ALL VPSes on this worker" || rec KOS K01 PASS "VPS runs its own kernel ($kin)"

hs=""
chk() { v=$(cat "$1" 2>/dev/null); [ -n "$v" ] && [ "$v" != "$2" ] && hs="$hs $(basename $1)=$v(want $2)"; }
chk /proc/sys/kernel/kptr_restrict 2; chk /proc/sys/kernel/dmesg_restrict 1
chk /proc/sys/kernel/unprivileged_bpf_disabled 2; chk /proc/sys/kernel/yama/ptrace_scope 1
chk /proc/sys/kernel/io_uring_disabled 2
pp=$(cat /proc/sys/kernel/perf_event_paranoid 2>/dev/null); [ -n "$pp" ] && [ "$pp" -lt 2 ] && hs="$hs perf_event_paranoid=$pp(want>=2)"
[ -n "$hs" ] && rec KOS K02 WARN "Host kernel hardening gaps (shared-kernel attack surface):$hs" || rec KOS K02 PASS "Host kernel sysctl hardening in place"

mu=$(cat /proc/sys/user/max_user_namespaces 2>/dev/null)
[ "${mu:-1}" = "0" ] && rec KOS K03 INFO "User namespaces disabled on host" || rec KOS K03 INFO "Host allows user namespaces (max=$mu) — larger kernel attack surface, but required for uid mapping"

wr=""
for p in /proc/sys/vm/drop_caches /proc/sys/vm/swappiness /proc/sys/kernel/panic /proc/sys/net/ipv4/ip_forward /proc/sys/kernel/hostname; do
  ex "$A" "test -w $p" >/dev/null && wr="$wr $p"
done
case "$wr" in *drop_caches*|*swappiness*|*kernel/panic*) rec KOS K04 FAIL "VPS can write host-global sysctls:$wr";;
  *) rec KOS K04 PASS "Host-global sysctls not writable (writable namespaced only:${wr:- none})";; esac

mt=$(ex "$A" "awk '/MemTotal/{print \$2}' /proc/meminfo" | tr -d '[:space:]')
if [ "$mt" = "$(mem_total_kb)" ]; then rec KOS K05 WARN "VPS /proc/meminfo shows HOST total RAM (leak + apps mis-size caches); consider lxcfs-style view" "MemTotal=$mt kB"
else rec KOS K05 PASS "VPS sees its own memory view" "MemTotal=$mt kB"; fi

lm=$(ex "$A" "lsmod 2>/dev/null | wc -l" | tr -d '[:space:]')
[ "${lm:-0}" -gt 1 ] && rec KOS K06 WARN "Host kernel module list visible in VPS ($lm lines)" || rec KOS K06 PASS "Kernel module list hidden"

vul=$(grep -l "Vulnerable" /sys/devices/system/cpu/vulnerabilities/* 2>/dev/null | xargs -r -n1 basename | tr '\n' ' ')
smt=$(cat /sys/devices/system/cpu/smt/active 2>/dev/null)
[ -n "$vul" ] && rec KOS K07 WARN "CPU side-channel mitigations missing for: $vul (shared kernel/CPU)" "smt=$smt" || rec KOS K07 PASS "No unmitigated CPU vulnerabilities reported" "smt=$smt (SMT on = cross-VPS sibling-thread risk)"

# --- OS share ---
rfA=$(vps_rootfs "$A"); rfB=$(vps_rootfs "$B")
if [ -d "$rfA" ] && [ -d "$rfB" ]; then
  ia=$(stat -c '%i' "$rfA/usr/bin/true" 2>/dev/null); ib=$(stat -c '%i' "$rfB/usr/bin/true" 2>/dev/null)
  nl=$(stat -c '%h' "$rfA/usr/bin/true" 2>/dev/null)
  if [ -n "$ia" ] && [ "$ia" = "$ib" ] && [ "${nl:-1}" -gt 1 ]; then
    if ex "$A" "test -w /usr/bin/true" >/dev/null; then rec KOS O01 FAIL "Base OS files are hardlinked across VPSes AND writable — one VPS can alter the OS of all others" "inode=$ia links=$nl"
    else rec KOS O01 PASS "Base OS shared via hardlink but read-only in VPS" "inode=$ia links=$nl"; fi
  else rec KOS O01 PASS "Base OS not hardlink-shared (separate copy / COW / overlay)" "A=$ia B=$ib"; fi
else rec KOS O01 SKIP "Rootfs paths not found — set FX_ROOTFS / INCUS_ROOTFS"; fi

ex "$A" "touch /usr/.cfx_osmark; echo A > /etc/cfx_osmark" >/dev/null
if ex "$B" "test -e /usr/.cfx_osmark -o -e /etc/cfx_osmark" >/dev/null; then rec KOS O02 FAIL "Write in VPS-A's OS tree (/usr,/etc) appears in VPS-B"
else rec KOS O02 PASS "OS-tree writes in VPS-A do not leak into VPS-B"; fi

ex "$A" "echo cfx-test > /etc/hostname.cfx; cp /etc/passwd /etc/passwd.cfx" >/dev/null
hb=$(ex "$B" "cat /etc/hostname" | tr -d '[:space:]'); ha=$(ex "$A" "cat /etc/hostname" | tr -d '[:space:]')
[ -n "$ha" ] && [ "$ha" = "$hb" ] && rec KOS O03 WARN "VPS-A and VPS-B have identical hostname/identity files ('$ha') — per-VPS identity not set" || rec KOS O03 PASS "Per-VPS identity files differ"

hostos=$(. /etc/os-release; echo "$ID $VERSION_ID"); vpsos=$(ex "$A" ". /etc/os-release; echo \$ID \$VERSION_ID")
rec KOS O04 INFO "OS share: host='$hostos' VPS='$vpsos' (patching host kernel patches all VPSes; VPS userland patched separately)"
ex "$A" "rm -f /usr/.cfx_osmark /etc/cfx_osmark /etc/hostname.cfx /etc/passwd.cfx" >/dev/null

echo "== done: $RES =="
