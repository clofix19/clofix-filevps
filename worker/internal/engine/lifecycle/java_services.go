package lifecycle

// Fixed upstream artifact, verified against the release asset's published SHA-256.
const wildflyVersion = "42.0.0.Final"
const wildflySHA256 = "6c2e50b8f3e2c0c3ad8f5241e18276f12bfd3e6e37b750e3fe1d6ce1dd2800ce"

const wildflySetup = `set -eu
build_dir=$(mktemp -d /tmp/clofix-wildfly.XXXXXX)
trap 'rm -rf "$build_dir"' EXIT HUP INT TERM
curl --proto '=https' --tlsv1.2 -fL --retry 3 -o "$build_dir/wildfly.tar.gz" https://github.com/wildfly/wildfly/releases/download/42.0.0.Final/wildfly-42.0.0.Final.tar.gz
printf '%s  %s\n' '6c2e50b8f3e2c0c3ad8f5241e18276f12bfd3e6e37b750e3fe1d6ce1dd2800ce' "$build_dir/wildfly.tar.gz" | sha256sum -c -
mkdir -p /opt/clofix-wildfly-42.0.0.Final
tar -xzf "$build_dir/wildfly.tar.gz" --strip-components=1 -C /opt/clofix-wildfly-42.0.0.Final
ln -sfn /opt/clofix-wildfly-42.0.0.Final /opt/wildfly
id -u wildfly >/dev/null 2>&1 || useradd --system --home-dir /var/lib/wildfly --shell /usr/sbin/nologin wildfly
mkdir -p /var/lib/wildfly/standalone/log /var/lib/wildfly/standalone/data /var/lib/wildfly/standalone/tmp
cp -Rn /opt/wildfly/standalone/. /var/lib/wildfly/standalone/
chown -R wildfly:wildfly /var/lib/wildfly
`
const wildflyInit = `#!/bin/sh
### BEGIN INIT INFO
# Provides: wildfly
# Required-Start: $remote_fs $network
# Required-Stop: $remote_fs $network
# Default-Start: 2 3 4 5
# Default-Stop: 0 1 6
# Short-Description: CloFix WildFly application server
### END INIT INFO
set -eu
pidfile=/run/wildfly/clofix.pid
running() {
 [ -s "$pidfile" ] || return 1
 pid=$(cat "$pidfile")
 [ -r "/proc/$pid/cmdline" ] || return 1
 tr '\000' ' ' < "/proc/$pid/cmdline" | grep -Eq 'standalone.sh|jboss-modules.jar'
}
case "${1:-}" in
 start)
  running && exit 0
  mkdir -p /run/wildfly
  chown wildfly:wildfly /run/wildfly
  rm -f "$pidfile"
  start-stop-daemon --start --background --make-pidfile --pidfile "$pidfile" --chuid wildfly --startas /opt/wildfly/bin/standalone.sh -- -Djboss.server.base.dir=/var/lib/wildfly/standalone -Djboss.socket.binding.port-offset=100 -b 0.0.0.0 -bmanagement 127.0.0.1 >>/var/lib/wildfly/standalone/log/clofix-console.log 2>&1
  ;;
 stop)
  if running; then start-stop-daemon --stop --retry TERM/20/KILL/5 --pidfile "$pidfile"; fi
  rm -f "$pidfile"
  ;;
 restart) "$0" stop; "$0" start ;;
 status) running ;;
 *) echo 'Usage: service wildfly start|stop|restart|status' >&2; exit 2 ;;
esac
`
const tomcatInit = `#!/bin/sh
### BEGIN INIT INFO
# Provides: tomcat10
# Required-Start: $remote_fs $network
# Required-Stop: $remote_fs $network
# Default-Start: 2 3 4 5
# Default-Stop: 0 1 6
# Short-Description: CloFix Apache Tomcat 10
### END INIT INFO
set -eu
export CATALINA_HOME=/usr/share/tomcat10
export CATALINA_BASE=/var/lib/tomcat10
export CATALINA_TMPDIR=/tmp
export CATALINA_OPTS='-Xms64m -Xmx256m'
pidfile=/run/tomcat10/clofix.pid
running() {
 [ -s "$pidfile" ] || return 1
 pid=$(cat "$pidfile")
 [ -r "/proc/$pid/cmdline" ] || return 1
 tr '\000' ' ' < "/proc/$pid/cmdline" | grep -Fq org.apache.catalina.startup.Bootstrap
}
case "${1:-}" in
 start)
  running && exit 0
  mkdir -p /run/tomcat10 /var/log/tomcat10
  chown tomcat:tomcat /run/tomcat10 /var/log/tomcat10
  rm -f "$pidfile"
  start-stop-daemon --start --background --make-pidfile --pidfile "$pidfile" --chuid tomcat --startas /usr/share/tomcat10/bin/catalina.sh -- run >>/var/log/tomcat10/clofix-console.log 2>&1
  ;;
 stop)
  if running; then start-stop-daemon --stop --retry TERM/20/KILL/5 --pidfile "$pidfile"; fi
  rm -f "$pidfile"
  ;;
 restart) "$0" stop; "$0" start ;;
 status) running ;;
 *) echo 'Usage: service tomcat10 start|stop|restart|status' >&2; exit 2 ;;
esac
`

func javaServiceSetup(serviceID string) string {
	switch serviceID {
	case "wildfly":
		return wildflySetup + "\ncat > /etc/init.d/wildfly <<'CLOFIX_INIT'\n" + wildflyInit + "\nCLOFIX_INIT\nchmod 0755 /etc/init.d/wildfly\nupdate-rc.d wildfly defaults\nservice wildfly start\n"
	case "tomcat":
		return "set -eu\nid -u tomcat >/dev/null 2>&1 || useradd --system --home-dir /var/lib/tomcat10 --shell /usr/sbin/nologin tomcat\ncat > /etc/init.d/tomcat10 <<'CLOFIX_INIT'\n" + tomcatInit + "\nCLOFIX_INIT\nchmod 0755 /etc/init.d/tomcat10\nupdate-rc.d tomcat10 defaults\nservice tomcat10 start\n"
	}
	return ""
}
