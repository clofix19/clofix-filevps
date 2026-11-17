package lifecycle

import (
	"errors"
	"fmt"
	"strings"

	"github.com/clofix/filevps/worker/pkg/proto"
)

func (e *Engine) ConfigureClusterNode(id string, spec proto.ClusterNodeSpec) error {
	if strings.ToLower(strings.TrimSpace(spec.App)) != "redis" {
		return errors.New("only redis cluster nodes are supported")
	}
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("VPS must be running to configure a cluster node")
	}
	// Cluster members must reach each other; default bridge ports are isolated.
	if e.Network != nil {
		if err := e.Network.SetPortIsolated(id, false); err != nil {
			return fmt.Errorf("enable VPC peer access: %w", err)
		}
	}
	if err := e.guestPackageCommand(id, m.Pid, []string{"update"}); err != nil {
		return fmt.Errorf("apt update failed: %w", err)
	}
	if err := e.guestPackageCommand(id, m.Pid, []string{"install", "--no-install-recommends", "redis-server", "redis-tools"}); err != nil {
		return fmt.Errorf("redis install failed: %w", err)
	}
	dataDir := strings.TrimSpace(spec.DataDir)
	if dataDir == "" || !strings.HasPrefix(dataDir, "/data/redis/") {
		return errors.New("invalid redis data dir")
	}
	clusterEnabled := "no"
	if spec.Topology == "master_master" {
		clusterEnabled = "yes"
	}
	appendonly := "no"
	if spec.Persistent {
		appendonly = "yes"
	}
	script := fmt.Sprintf(`set -eu
DATA_DIR=%s
mkdir -p "$DATA_DIR" /var/log/redis /run/redis
chown -R redis:redis "$DATA_DIR" /var/log/redis /run/redis
cat > /etc/redis/redis.conf <<EOF
bind 0.0.0.0
protected-mode no
port 6379
daemonize yes
supervised no
pidfile /run/redis/redis-server.pid
loglevel notice
logfile /var/log/redis/redis-server.log
dir $DATA_DIR
appendonly %s
cluster-enabled %s
cluster-config-file nodes.conf
cluster-node-timeout 5000
save 900 1
save 300 10
save 60 10000
EOF
# Guest images may already start redis via init; stop without hanging on SysV scripts.
if command -v redis-cli >/dev/null 2>&1; then
  redis-cli shutdown nosave >/dev/null 2>&1 || true
fi
pkill -x redis-server >/dev/null 2>&1 || true
sleep 1
# Prefer direct start: service/systemctl restart can block forever in these guests.
redis-server /etc/redis/redis.conf
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  redis-cli ping 2>/dev/null | grep -q PONG && exit 0
  sleep 1
done
echo 'redis did not become ready' >&2
exit 1
`, shellSingleQuote(dataDir), appendonly, clusterEnabled)
	if err := e.guestShell(id, m.Pid, script); err != nil {
		return fmt.Errorf("redis configure failed: %w", err)
	}
	return nil
}

func (e *Engine) JoinCluster(id string, spec proto.ClusterNodeSpec) error {
	if strings.ToLower(strings.TrimSpace(spec.App)) != "redis" {
		return errors.New("only redis cluster join is supported")
	}
	m, err := e.get(id)
	if err != nil {
		return err
	}
	if !e.CG.Alive(id) || m.Pid <= 0 {
		return errors.New("VPS must be running to join a cluster")
	}
	switch spec.Topology {
	case "master_slave":
		if spec.Role == "primary" {
			return nil
		}
		primary := strings.TrimSpace(spec.PrimaryAddr)
		if primary == "" || !strings.Contains(primary, ":") {
			return errors.New("primary_addr required for replica join")
		}
		host := strings.Split(primary, ":")[0]
		script := fmt.Sprintf(`set -eu
redis-cli REPLICAOF '%s' 6379
redis-cli INFO replication | grep -q 'role:slave\|role:replica'
`, host)
		if err := e.guestShell(id, m.Pid, script); err != nil {
			return fmt.Errorf("replicaof failed: %w", err)
		}
		return nil
	case "master_master":
		if len(spec.MemberAddrs) < 3 {
			return errors.New("master_master join requires at least 3 member addresses")
		}
		var b strings.Builder
		b.WriteString("set -eu\n")
		b.WriteString("redis-cli --cluster create")
		for _, addr := range spec.MemberAddrs {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				continue
			}
			b.WriteByte(' ')
			b.WriteString(shellSingleQuote(addr))
		}
		b.WriteString(" --cluster-replicas 0 --cluster-yes\n")
		if err := e.guestShell(id, m.Pid, b.String()); err != nil {
			return fmt.Errorf("redis cluster create failed: %w", err)
		}
		return nil
	default:
		return errors.New("unknown topology")
	}
}

func shellSingleQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
