# shellcheck shell=bash
# Shared SSH helpers for deploy/*/deploy.sh and push scripts.
# Expects: HOST USER PASS KEY PORT already set; defines ssh_cmd / scp_cmd / rsync_cmd.

ssh_prep() {
  SSH_OPTS=(-o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 -p "${PORT:-22}")
  SCP_OPTS=(-o StrictHostKeyChecking=accept-new -P "${PORT:-22}")
  RSYNC_SSH="ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 -p ${PORT:-22}"
  if [ -n "${KEY:-}" ]; then
    SSH_OPTS+=(-i "$KEY" -o PreferredAuthentications=publickey)
    SCP_OPTS+=(-i "$KEY")
    RSYNC_SSH="ssh -i $KEY -o StrictHostKeyChecking=accept-new -o PreferredAuthentications=publickey -p ${PORT:-22}"
  elif [ -n "${PASS:-}" ]; then
    command -v sshpass >/dev/null 2>&1 || {
      echo 'sshpass required for password auth (brew install sshpass / apt install sshpass)' >&2
      return 1
    }
    SSH_OPTS+=(-o PreferredAuthentications=password -o PubkeyAuthentication=no)
    SCP_OPTS+=(-o PreferredAuthentications=password -o PubkeyAuthentication=no)
    export SSHPASS="$PASS"
    RSYNC_SSH="sshpass -e ssh -o StrictHostKeyChecking=accept-new -o PreferredAuthentications=password -o PubkeyAuthentication=no -p ${PORT:-22}"
  else
    echo 'Set CLOFIX_REMOTE_PASSWORD or CLOFIX_REMOTE_SSH_KEY' >&2
    return 1
  fi
}

ssh_cmd() {
  if [ -n "${KEY:-}" ]; then
    ssh "${SSH_OPTS[@]}" "$USER@$HOST" "$@"
  else
    sshpass -e ssh "${SSH_OPTS[@]}" "$USER@$HOST" "$@"
  fi
}

scp_cmd() {
  if [ -n "${KEY:-}" ]; then
    scp "${SCP_OPTS[@]}" "$@"
  else
    sshpass -e scp "${SCP_OPTS[@]}" "$@"
  fi
}

rsync_cmd() {
  if command -v rsync >/dev/null 2>&1; then
    if [ -n "${KEY:-}" ]; then
      rsync -az --delete -e "$RSYNC_SSH" "$@"
    else
      rsync -az --delete -e "$RSYNC_SSH" "$@"
    fi
  else
    echo 'rsync not found; install rsync' >&2
    return 1
  fi
}

remote_sudo() {
  local cmd="$*"
  if [ "$USER" = "root" ]; then
    ssh_cmd "bash -lc $(printf '%q' "$cmd")"
  elif ssh_cmd "sudo -n true" 2>/dev/null; then
    ssh_cmd "sudo bash -lc $(printf '%q' "$cmd")"
  elif [ -n "${PASS:-}" ]; then
    printf '%s\n' "$PASS" | ssh_cmd "sudo -S -p '' bash -lc $(printf '%q' "$cmd")"
  else
    echo 'Need root, passwordless sudo, or CLOFIX_REMOTE_PASSWORD' >&2
    return 1
  fi
}

ensure_remote_docker() {
  if ssh_cmd "command -v docker >/dev/null && docker compose version >/dev/null 2>&1"; then
    echo 'Docker + compose already on remote'
    return 0
  fi
  echo 'Installing Docker Engine on remote…'
  remote_sudo 'export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y --no-install-recommends ca-certificates curl
    if ! command -v docker >/dev/null; then
      curl -fsSL https://get.docker.com | sh
    fi
    systemctl enable --now docker
    docker compose version >/dev/null'
}

load_remote_env() {
  HOST="${CLOFIX_REMOTE_HOST:-${CLOFIX_REMOTE_IP:-}}"
  USER="${CLOFIX_REMOTE_USER:-}"
  PASS="${CLOFIX_REMOTE_PASSWORD:-}"
  KEY="${CLOFIX_REMOTE_SSH_KEY:-}"
  PORT="${CLOFIX_REMOTE_PORT:-22}"
  [ -n "$HOST" ] || { echo 'Set CLOFIX_REMOTE_IP (or CLOFIX_REMOTE_HOST)' >&2; return 1; }
  [ -n "$USER" ] || { echo 'Set CLOFIX_REMOTE_USER' >&2; return 1; }
}

# Optional separate DB host: DB_REMOTE_IP/USER/PASSWORD/PORT (falls back to CLOFIX_REMOTE_*).
load_db_remote_env() {
  if [ -n "${DB_REMOTE_IP:-}${DB_REMOTE_HOST:-}" ]; then
    HOST="${DB_REMOTE_HOST:-${DB_REMOTE_IP}}"
    USER="${DB_REMOTE_USER:-${CLOFIX_REMOTE_USER:-root}}"
    PASS="${DB_REMOTE_PASSWORD:-${CLOFIX_REMOTE_PASSWORD:-}}"
    KEY="${DB_REMOTE_SSH_KEY:-${CLOFIX_REMOTE_SSH_KEY:-}}"
    PORT="${DB_REMOTE_PORT:-${CLOFIX_REMOTE_PORT:-22}}"
  else
    load_remote_env
  fi
  [ -n "$HOST" ] || { echo 'Set CLOFIX_REMOTE_IP or DB_REMOTE_IP' >&2; return 1; }
  [ -n "$USER" ] || { echo 'Set CLOFIX_REMOTE_USER or DB_REMOTE_USER' >&2; return 1; }
}

ensure_remote_postgres() {
  echo 'Ensuring PostgreSQL on remote…'
  remote_sudo 'export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y --no-install-recommends postgresql postgresql-contrib ca-certificates
    systemctl enable --now postgresql
    command -v psql >/dev/null'
}

# Run SQL as postgres OS user on remote. Args: SQL string
remote_psql_as_postgres() {
  local sql="$1"
  remote_sudo "sudo -u postgres psql -v ON_ERROR_STOP=1 -c $(printf '%q' "$sql")"
}

# Run a SQL file as postgres OS user on remote. Args: remote path, optional db name
remote_psql_file_as_postgres() {
  local path="$1"
  local db="${2:-postgres}"
  remote_sudo "sudo -u postgres psql -v ON_ERROR_STOP=1 -d $(printf '%q' "$db") -f $(printf '%q' "$path")"
}
