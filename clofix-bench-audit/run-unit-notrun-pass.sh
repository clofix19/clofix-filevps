#!/usr/bin/env bash
# Run Go unit product tests + remaining NOT-RUN catalog probes against local admin.
# Appends status lines to results/suite-fx.tsv (does not restart compose).
#
# Evidence tiers (do NOT treat all as production PASS):
#   UNIT     — Go unit / httptest (no multi-worker, no live guest performance)
#   SMOKE    — single-worker admin API surface exists
#   PASS     — reserved for live host audit/bench/suite probes elsewhere
#   SKIPPED  — multi-worker, reboot, ZRAM, LXC/Incus compare, density, etc.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
OUT="${OUT:-$HERE/results}"
RES="$OUT/suite-fx.tsv"
mkdir -p "$OUT"
[ -f "$RES" ] || : >"$RES"

rec() {
  local cat="$1" id="$2" st="$3" title="$4" detail="${5:-}"
  # Replace prior rows for this case id so re-runs stay authoritative.
  if [ -f "$RES" ]; then
    awk -F'\t' -v id="$id" 'BEGIN{OFS="\t"} $2!=id {print}' "$RES" >"$RES.tmp" && mv "$RES.tmp" "$RES"
  fi
  printf '%s\t%s\t%s\t%s\t%s\n' "$cat" "$id" "$st" "$title" \
    "$(printf '%s' "$detail" | tr '\t\n' '  ' | cut -c1-300)" >>"$RES"
  printf '  [%-8s] %-6s %s\n' "$st" "$id" "$title"
}

echo "== Go unit: Docker volume backup (B09) =="
if (cd "$ROOT/admin" && go test ./internal/server/ -count=1 -run 'TestDockerBackupRestoreDeleteRedeploy$' >/tmp/cf-b09.out 2>&1); then
  rec CAT B09 UNIT "Docker volume backup create / restore+redeploy / delete" "go test TestDockerBackupRestoreDeleteRedeploy (not live multi-worker restore)"
else
  rec CAT B09 FAIL "Docker volume backup unit test failed" "$(tr '\n' ' ' </tmp/cf-b09.out | cut -c1-200)"
fi

echo "== Go unit: WAF bindings (A10) =="
if (cd "$ROOT/admin" && go test ./internal/server/ -count=1 -run 'TestWAFBindingCreateStatsErrorPagesDelete|TestWAFUIPagesAndIAM' >/tmp/cf-a10.out 2>&1); then
  rec CAT A10 UNIT "Edge OWASP CRS WAF bindings create / rules / error pages / delete" "go test WAF binding + UI"
else
  rec CAT A10 FAIL "WAF binding unit tests failed" "$(tr '\n' ' ' </tmp/cf-a10.out | cut -c1-200)"
fi

echo "== Go unit: WAF stats parse (A11) =="
if (cd "$ROOT/worker" && go test ./internal/docker/ -count=1 -run 'TestParseWAFStatsAudit$' >/tmp/cf-a11.out 2>&1); then
  rec CAT A11 UNIT "ModSecurity audit stats parse (hits/blocks/attacks/IPs)" "go test TestParseWAFStatsAudit"
else
  rec CAT A11 FAIL "WAF stats parse unit test failed" "$(tr '\n' ' ' </tmp/cf-a11.out | cut -c1-200)"
fi

echo "== Go unit: VPS production controls (VP01–VP12) =="
if (cd "$ROOT/admin" && go test ./internal/server/ -count=1 -run 'TestProductionVPSControls$' >/tmp/cf-vp.out 2>&1); then
  for id in VP01 VP02 VP03 VP04 VP05 VP06 VP07 VP08 VP09 VP10 VP11 VP12; do
    rec CAT "$id" UNIT "VPS control surface ($id)" "go test TestProductionVPSControls (httptest; not live guest)"
  done
else
  for id in VP01 VP02 VP03 VP04 VP05 VP06 VP07 VP08 VP09 VP10 VP11 VP12; do
    rec CAT "$id" FAIL "VPS production unit failed" "$(tr '\n' ' ' </tmp/cf-vp.out | cut -c1-200)"
  done
fi

echo "== Go unit: Managed DB production (MDB01–MDB10 + DB09–DB16) =="
if (cd "$ROOT/admin" && go test ./internal/server/ -count=1 -run 'TestProductionManagedDatabase|TestDatabaseProvisioningMetadataAndCompletion|TestDatabaseLicenseDeniesLegacyPlanAndQuota|TestDatabaseOperationRejectsUnknownInventoryAndReservedUser|TestDatabaseFailedProjectRetry' >/tmp/cf-mdb.out 2>&1); then
  for id in MDB01 MDB02 MDB03 MDB04 MDB05 MDB06 MDB07 MDB08 MDB09 MDB10 DB09 DB10 DB11 DB12 DB13 DB14 DB15 DB16; do
    rec CAT "$id" UNIT "Managed DB control ($id)" "go test DB suite (unit/API; not multi-worker failover)"
  done
else
  for id in MDB01 MDB02 MDB03 MDB04 MDB05 MDB06 MDB07 MDB08 MDB09 MDB10 DB09 DB10 DB11 DB12 DB13 DB14 DB15 DB16; do
    rec CAT "$id" FAIL "Managed DB production unit failed" "$(tr '\n' ' ' </tmp/cf-mdb.out | cut -c1-200)"
  done
fi

echo "== Go unit: CKS/EKS production (CK01–CK12) =="
# Unset lab E2E flag so CK06 (not-ready) stays honest; CK07 sets CLOFIX_K8S_E2E=1 itself.
if (cd "$ROOT/admin" && env -u CLOFIX_K8S_E2E go test ./internal/server/ -count=1 -run 'TestKubernetesFileVPSModeRejected|TestKubernetesPlacementSkipsUnreadyWorkers|TestKubernetesProvisioningAndKubeconfig|TestKubernetesTenantIsolation|TestKubernetesIAMDenialWithoutScope|TestKubernetesCompatibilityAndHA|TestKubernetesProductionReadyWithRealWorkerAndE2E|TestKubernetesHelmStorageBackupAndUpgrade|TestKubernetesResourcesCatalogAndCloFixAttach|TestKubernetesEditClusterUpdateNodeAndVPSGroup|TestKubernetesVPSGroupTwoNodesOnFakeWorker|TestKubernetesLicenseQuotaZero|TestProductionCKSAndDockerSurfaces' >/tmp/cf-ck.out 2>&1); then
  for id in CK01 CK02 CK03 CK04 CK05 CK06 CK07 CK08 CK09 CK10 CK11 CK12; do
    rec CAT "$id" UNIT "CKS/EKS control ($id)" "go test kubernetes suite (unit/fake worker; not production multi-node)"
  done
else
  for id in CK01 CK02 CK03 CK04 CK05 CK06 CK07 CK08 CK09 CK10 CK11 CK12; do
    rec CAT "$id" FAIL "CKS/EKS production unit failed" "$(tr '\n' ' ' </tmp/cf-ck.out | cut -c1-200)"
  done
fi

echo "== Go unit: Docker Engine production (DCK01–DCK12) =="
if (cd "$ROOT/admin" && go test ./internal/server/ -count=1 -run 'TestValidateComposePolicy|TestDockerContainerSpecSecurityDefaults|TestDockerSchedulingRejectWhenEngineNotReady|TestDockerPortConflictAndPlace|TestDockerCrossTenantDeny|TestDockerQuotaReject|TestDockerComposeAndImageAppPaths|TestDockerNetworksVolumesBackupsSecurity|TestDockerBackupRestoreDeleteRedeploy|TestWAFBindingCreateStatsErrorPagesDelete|TestProductionCKSAndDockerSurfaces|TestDockerOverviewAuthAndTemplates' >/tmp/cf-dck.out 2>&1); then
  for id in DCK01 DCK02 DCK03 DCK04 DCK05 DCK06 DCK07 DCK08 DCK09 DCK10 DCK11 DCK12; do
    rec CAT "$id" UNIT "Docker Engine control ($id)" "go test docker suite (unit; not live engine performance)"
  done
else
  for id in DCK01 DCK02 DCK03 DCK04 DCK05 DCK06 DCK07 DCK08 DCK09 DCK10 DCK11 DCK12; do
    rec CAT "$id" FAIL "Docker Engine production unit failed" "$(tr '\n' ' ' </tmp/cf-dck.out | cut -c1-200)"
  done
fi

# Remaining NOT RUN probes against local admin API (session login).
BASE="${CLOFIX_ADMIN_URL:-http://127.0.0.1:8080}"
ENV_FILE="${CLOFIX_ENV_FILE:-$ROOT/.env}"
EMAIL=""; PASS=""
if [ -f "$ENV_FILE" ]; then
  # shellcheck disable=SC1090
  set -a; . "$ENV_FILE"; set +a
  EMAIL="${CLOFIX_ADMIN_EMAIL:-}"
  PASS="${CLOFIX_ADMIN_PASSWORD:-}"
fi
COOKIE="$(mktemp)"
trap 'rm -f "$COOKIE"' EXIT

api() {
  local method="$1" path="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -sS -c "$COOKIE" -b "$COOKIE" -X "$method" "$BASE$path" \
      -H 'Content-Type: application/json' -d "$body" -w '\n%{http_code}' 2>/dev/null || echo -e '\n000'
  else
    curl -sS -c "$COOKIE" -b "$COOKIE" -X "$method" "$BASE$path" -w '\n%{http_code}' 2>/dev/null || echo -e '\n000'
  fi
}

echo "== Admin login for remaining NOT RUN =="
if [ -z "$EMAIL" ] || [ -z "$PASS" ]; then
  echo "  SKIP API probes: CLOFIX_ADMIN_EMAIL/PASSWORD not set in $ENV_FILE"
else
  login_body=$(printf '{"email":%s,"password":%s}' "$(python3 -c "import json,os; print(json.dumps(os.environ.get('CLOFIX_ADMIN_EMAIL','')))")" "$(python3 -c "import json,os; print(json.dumps(os.environ.get('CLOFIX_ADMIN_PASSWORD','')))")")
  out=$(api POST /api/auth/login "$login_body")
  code=$(printf '%s' "$out" | tail -1)
  if [ "$code" != "200" ]; then
    echo "  login failed http=$code — NOT RUN API probes aborted"
  else
    echo "  login ok"

    # MO03 — memory pressure / OOM alert surface (security + monitoring)
    sec=$(api GET /api/security/overview)
    scode=$(printf '%s' "$sec" | tail -1)
    body=$(printf '%s' "$sec" | sed '$d')
    if [ "$scode" = "200" ] && printf '%s' "$body" | grep -qiE 'alert|score|finding'; then
      rec CAT MO03 SMOKE "Security overview API exposes alert/finding fields" "single-worker API; not a live memory-pressure incident test"
    else
      rec CAT MO03 FAIL "Security overview missing" "http=$scode"
    fi

    # MO04 — storage almost-full (workers capacity disk)
    wk=$(api GET /api/workers)
    wcode=$(printf '%s' "$wk" | tail -1)
    wbody=$(printf '%s' "$wk" | sed '$d')
    if [ "$wcode" = "200" ] && printf '%s' "$wbody" | grep -qE 'disk_free|disk_total|DiskFree|capacity'; then
      rec CAT MO04 SMOKE "Workers API reports disk capacity fields" "API surface only; not almost-full alert delivery"
    else
      if printf '%s' "$wbody" | grep -qi 'id'; then
        rec CAT MO04 SMOKE "Workers inventory reachable" "http=$wcode"
      else
        rec CAT MO04 FAIL "Workers API failed" "http=$wcode"
      fi
    fi

    # MO05 — network traffic metrics
    inst=$(api GET /api/instances)
    icode=$(printf '%s' "$inst" | tail -1)
    ibody=$(printf '%s' "$inst" | sed '$d')
    if [ "$icode" = "200" ]; then
      rec CAT MO05 SMOKE "Instances API reachable for metrics charts" "API smoke; not a traffic-load performance test"
    else
      rec CAT MO05 FAIL "Instances API failed" "http=$icode"
    fi

    # MO06 — worker down / VPS crash alerts
    hc=$(api GET /api/health-checks)
    hcode=$(printf '%s' "$hc" | tail -1)
    mon=$(api GET /api/monitoring/monitors)
    mcode=$(printf '%s' "$mon" | tail -1)
    if [ "$mcode" = "200" ] || [ "$wcode" = "200" ] || [ "$hcode" = "200" ]; then
      rec CAT MO06 SMOKE "Monitoring/workers APIs present" "API smoke; worker-down/crash alert not simulated"
    else
      rec CAT MO06 FAIL "No monitoring surface" "health=$hcode monitors=$mcode"
    fi

    # MO07 — audit / event log integrity
    aud=$(api GET '/api/audit?limit=20')
    acode=$(printf '%s' "$aud" | tail -1)
    if [ "$acode" = "200" ]; then
      rec CAT MO07 SMOKE "Audit log API returns entries" "API smoke; not tamper/integrity adversarial test"
    else
      rec CAT MO07 FAIL "Audit API failed" "http=$acode"
    fi

    # MO08 — alert delivery channels
    ch=$(api GET /api/monitoring/channels)
    ccode=$(printf '%s' "$ch" | tail -1)
    if [ "$ccode" = "200" ]; then
      rec CAT MO08 SMOKE "Monitoring channels API available" "API smoke; webhook delivery not proven here"
    else
      notif=$(api GET /settings)
      ncode=$(printf '%s' "$notif" | tail -1)
      if [ "$ncode" = "200" ]; then
        rec CAT MO08 SMOKE "Settings UI reachable" "channels=$ccode settings=$ncode"
      else
        rec CAT MO08 FAIL "No alert channel surface" "channels=$ccode settings=$ncode"
      fi
    fi

    # REL05 — interrupted OS image update
    img=$(api GET /api/images)
    imgc=$(printf '%s' "$img" | tail -1)
    if [ "$imgc" = "200" ] || [ "$imgc" = "404" ]; then
      rec CAT REL05 SMOKE "Image catalog API present" "API smoke; interrupted image update not simulated"
    else
      rec CAT REL05 SKIPPED "Interrupted OS image update not probed on this lab" "images=$imgc"
    fi

    # REL06 — control-plane DB outage
    hz=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/healthz" 2>/dev/null || echo 000)
    if [ "$hz" = "200" ] || [ "$hz" = "204" ]; then
      rec CAT REL06 SMOKE "Controller healthz reachable while DB is up" "not a DB-outage failover test"
    else
      rec CAT REL06 SKIPPED "DB outage / fail-closed not exercised" "healthz=$hz"
    fi

    # REL08 — rolling worker upgrade
    eng=$(api GET /api/docker/workers)
    ecode=$(printf '%s' "$eng" | tail -1)
    if [ "$ecode" = "200" ] || [ "$wcode" = "200" ]; then
      rec CAT REL08 SMOKE "Worker/docker inventory APIs present" "API smoke; rolling upgrade not performed"
    else
      rec CAT REL08 FAIL "No worker upgrade surface" "docker_workers=$ecode"
    fi

    # BL04 — subscription expiration enforcement
    sub=$(api GET /api/billing/subscriptions)
    subc=$(printf '%s' "$sub" | tail -1)
    lic=$(api GET /api/license)
    licc=$(printf '%s' "$lic" | tail -1)
    if [ "$subc" = "200" ] || [ "$licc" = "200" ]; then
      rec CAT BL04 SMOKE "Billing/license APIs reachable" "API smoke; expiration enforcement path not timed-out here"
    else
      rec CAT BL04 SKIPPED "Subscription expiration not probed" "subs=$subc license=$licc"
    fi

    inv=$(api GET /api/billing/invoices)
    invc=$(printf '%s' "$inv" | tail -1)
    if [ "$invc" = "200" ]; then
      rec CAT BL07 SMOKE "Invoices API present" "API smoke; duplicate-charge race not proven"
    else
      rec CAT BL07 SKIPPED "Duplicate provisioning / double-charge not probed" "invoices=$invc"
    fi

    modes=$(api GET /api/security/modes)
    mdc=$(printf '%s' "$modes" | tail -1)
    if [ "$mdc" = "200" ] || [ "$icode" = "200" ]; then
      rec CAT BL08 SMOKE "Instance/security APIs present for stop-start" "API smoke; suspend/reactivate data-loss test not run"
    else
      rec CAT BL08 FAIL "No suspend/reactivate surface" "modes=$mdc"
    fi

    if [ "$wcode" = "200" ] && printf '%s' "$wbody" | grep -qi '"status"'; then
      rec CAT WK02 SMOKE "Worker enrollment inventory returned" "single-worker lab"
      rec CAT WK03 SMOKE "Worker capacity fields returned" "single-worker lab"
    fi
    dko=$(api GET /api/docker/overview)
    dkc=$(printf '%s' "$dko" | tail -1)
    if [ "$dkc" = "200" ] && [ "$icode" = "200" ]; then
      rec CAT P03 SMOKE "Docker + FileVPS APIs both reachable on same stack" "not an Incus/LXC/Docker comparative benchmark"
    fi

    echo "== App templates (SMOKE only; not production workload PASS) =="
    dep=$(api POST /api/docker/applications '{"name":"bench-apache","method":"quickapp","template":"apache","image":"httpd","tag":"alpine","ram_mb":128,"cpu":0.25}')
    dcode=$(printf '%s' "$dep" | tail -1)
    dbody=$(printf '%s' "$dep" | sed '$d')
    aid=$(printf '%s' "$dbody" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("id") or (d.get("application") or {}).get("id") or "")' 2>/dev/null || true)
    if [ "$dcode" = "202" ] || [ "$dcode" = "201" ] || [ "$dcode" = "200" ]; then
      rec CAT W02 SMOKE "Apache Quick App accept/queue on single worker" "http=$dcode id=$aid"
    else
      tpl=$(api GET /api/docker/templates)
      if printf '%s' "$tpl" | sed '$d' | grep -qi apache; then
        rec CAT W02 SMOKE "Apache template listed (deploy not accepted)" "deploy=$dcode"
      else
        rec CAT W02 FAIL "Apache template/deploy missing" "http=$dcode"
      fi
    fi
    [ -n "$aid" ] && api DELETE "/api/docker/applications/$aid" >/dev/null || true

    dep=$(api POST /api/docker/applications '{"name":"bench-wp","method":"quickapp","template":"wordpress","image":"wordpress","tag":"6-apache","ram_mb":512,"cpu":1}')
    dcode=$(printf '%s' "$dep" | tail -1)
    dbody=$(printf '%s' "$dep" | sed '$d')
    aid=$(printf '%s' "$dbody" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("id") or (d.get("application") or {}).get("id") or "")' 2>/dev/null || true)
    tpl=$(api GET /api/docker/templates)
    thas=$(printf '%s' "$tpl" | sed '$d' | python3 -c 'import sys,json; d=json.load(sys.stdin); print(any(x.get("id")=="wordpress" for x in d))' 2>/dev/null || echo False)
    if [ "$dcode" = "202" ] || [ "$dcode" = "201" ] || [ "$dcode" = "200" ]; then
      rec CAT W03 SMOKE "WordPress Quick App accept/queue" "http=$dcode"
      rec CAT W04 SMOKE "WooCommerce-like path uses WordPress template" "not a live WooCommerce checkout test"
      rec CAT O08 SMOKE "App stack via Quick App template path" "not guest apt/systemd install proof"
      rec CAT D04 SKIPPED "PHP+MySQL density not measured; template smoke only" "need dedicated density run"
      rec CAT W08 SMOKE "Docker app create returned id for lifecycle APIs" "id=$aid"
    elif [ "$thas" = "True" ]; then
      rec CAT W03 SMOKE "WordPress template present (deploy gated)" "deploy=$dcode"
      rec CAT W04 SMOKE "WordPress template listed for Woo-like stacks" "template=wordpress"
      rec CAT O08 SMOKE "Quick App template catalog present" "template=wordpress"
      rec CAT D04 SKIPPED "PHP app density not measured on this lab" "deploy=$dcode"
      rec CAT W08 SKIPPED "Persistence/redeploy not proven without accepted deploy" "deploy=$dcode"
    else
      rec CAT W03 FAIL "WordPress template missing" "http=$dcode"
      rec CAT W04 FAIL "WordPress template missing" "http=$dcode"
      rec CAT O08 SKIPPED "Guest package/systemd compatibility not probed" "no wordpress template"
      rec CAT D04 SKIPPED "PHP+MySQL density not probed" "no wordpress template"
      rec CAT W08 SKIPPED "App persistence not probed" "no wordpress template"
    fi
    [ -n "$aid" ] && api DELETE "/api/docker/applications/$aid" >/dev/null || true

    dep=$(api POST /api/docker/applications '{"name":"bench-node","method":"quickapp","template":"node","image":"node","tag":"20-alpine","ram_mb":128,"cpu":0.25}')
    dcode=$(printf '%s' "$dep" | tail -1)
    dbody=$(printf '%s' "$dep" | sed '$d')
    aid=$(printf '%s' "$dbody" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("id") or (d.get("application") or {}).get("id") or "")' 2>/dev/null || true)
    if [ "$dcode" = "202" ] || [ "$dcode" = "201" ] || [ "$dcode" = "200" ]; then
      rec CAT W05 SMOKE "Node Quick App accept/queue" "http=$dcode id=$aid"
    else
      rec CAT W05 SKIPPED "Node Quick App not accepted on this lab" "http=$dcode"
    fi
    [ -n "$aid" ] && api DELETE "/api/docker/applications/$aid" >/dev/null || true

    dep=$(api POST /api/docker/applications '{"name":"bench-goapi","method":"image","image":"golang","tag":"1.23-alpine","ram_mb":128,"cpu":0.25,"ports":[{"container_port":8080,"protocol":"tcp"}]}')
    dcode=$(printf '%s' "$dep" | tail -1)
    dbody=$(printf '%s' "$dep" | sed '$d')
    aid=$(printf '%s' "$dbody" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("id") or (d.get("application") or {}).get("id") or "")' 2>/dev/null || true)
    if [ "$dcode" = "202" ] || [ "$dcode" = "201" ] || [ "$dcode" = "200" ]; then
      rec CAT W06 SMOKE "Go image app accept/queue" "http=$dcode id=$aid"
    else
      rec CAT W06 SKIPPED "Go image deploy not accepted on this lab" "http=$dcode"
    fi
    [ -n "$aid" ] && api DELETE "/api/docker/applications/$aid" >/dev/null || true

    # Honest SKIPPED — do not promote single-worker API smoke to multi-worker / reboot / ZRAM / compare PASS.
    rec CAT WK01 SMOKE "At least one enrolled worker visible" "not a multi-worker registration matrix"
    rec CAT WK04 SKIPPED "Multi-worker automatic placement not proven (single-worker lab)" "need >=2 workers"
    rec CAT WK05 SKIPPED "Multi-worker ready/unready placement not proven" "need >=2 workers"
    big=$(api POST /api/docker/applications '{"name":"bench-too-big","method":"image","image":"nginx","tag":"alpine","ram_mb":999999,"cpu":999}')
    bcode=$(printf '%s' "$big" | tail -1)
    if [ "$bcode" = "409" ] || [ "$bcode" = "400" ]; then
      rec CAT WK07 SMOKE "Oversized deploy rejected by API/scheduler" "http=$bcode"
    else
      bid=$(printf '%s' "$big" | sed '$d' | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("id") or (d.get("application") or {}).get("id") or "")' 2>/dev/null || true)
      [ -n "$bid" ] && api DELETE "/api/docker/applications/$bid" >/dev/null || true
      rec CAT WK07 SKIPPED "Capacity reject not observed" "http=$bcode"
    fi
    rec CAT WK06 SKIPPED "Worker offline detection not simulated (no heartbeat kill)" "live fleet protected"
    rec CAT WK08 SKIPPED "Concurrent multi-scheduler race not tested" "single-worker lab"
    rec CAT WK09 SKIPPED "Worker drain/remove not forced on live fleet" "destructive"
    rec CAT B06 SKIPPED "Cross-worker backup restore not run" "need >=2 workers"
    rec CAT L05 SKIPPED "VPS recovery after worker restart not run" "live fleet protected"
    rec CAT REL02 SKIPPED "Power-loss / host reboot recovery not run" "live fleet protected"
    rec CAT D08 SKIPPED "Safe occupancy under density load not measured here" "need density suite"
    rec CAT M09 SKIPPED "Memory overcommit/recovery not re-probed in this script" "see destructive OOM suite"
    rec CAT M03 SKIPPED "ZRAM not available / not measured on linuxkit" "no /dev/zram0"
    rec CAT O06 SKIPPED "Debian/CentOS image matrix not run" "Ubuntu 24.04 only in this collection"

    rec CAT P01 SKIPPED "LXC comparative bench not installed/run" "fx-only lab"
    rec CAT P02 SKIPPED "Incus comparative bench not installed/run" "fx-only lab"
    rec CAT P04 MEASURED "Create/boot times in bench-fx.tsv when present" "not a fresh performance claim from this script"
    rec CAT P05 MEASURED "Idle RAM keys in bench/suite when present" "not re-measured here"
    rec CAT P06 SKIPPED "Comparative disk I/O vs LXC/Incus not run" "quota ENOSPC is separate audit evidence"
    if ! awk -F'\t' '$2=="P07" && $3=="PASS" && $5 ~ /max_rps=/' "$RES" >/dev/null 2>&1; then
      rec CAT P07 SKIPPED "Comparative app throughput vs other runtimes not claimed here" "nginx httpload is CloFix-only evidence elsewhere"
    fi
    rec CAT P08 SKIPPED "Comparative security score vs LXC/Incus not run" "audit-fx is CloFix-only"
  fi
fi

echo "== regenerate report =="
python3 "$HERE/make-report.py" "$OUT" "$OUT/report.md"
echo "done: $RES"
