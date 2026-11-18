package lifecycle

// Fixed CloFix Library catalog. Package names are selected here only - never from the API.

func serviceCatalogPackages(serviceID string) ([]string, bool) {
	packages, ok := map[string][]string{
		"tomcat":      {"tomcat10", "default-jre-headless"},
		"wildfly":     {"openjdk-21-jre-headless", "curl", "ca-certificates", "tar"},
		"haproxy":     {"haproxy"},
		"memcached":   {"memcached"},
		"supervisor":  {"supervisor"},
		"git":         {"git"},
		"composer":    {"composer"},
		"golang":      {"golang-go"},
		"java":        {"default-jdk"},
		"ruby":        {"ruby-full"},
		"build-tools": {"build-essential", "pkg-config"},
		"sqlite":      {"sqlite3"},
		"certbot":     {"certbot"},
		"rsync":       {"rsync"},
		"nginx":       {"nginx"},
		"apache":      {"apache2"},
		"mysql":       {"mysql-server"},
		"mariadb":     {"mariadb-server"},
		"postgresql":  {"postgresql"},
		"redis":       {"redis-server"},
		"php":         {"php-fpm", "php-cli", "php-mysql", "php-xml", "php-mbstring", "php-curl", "php-zip", "php-gd"},
		"nodejs":      {"nodejs", "npm"},
		"python":      {"python3", "python3-venv", "python3-pip"},
		"wordpress":   {"wordpress", "apache2", "libapache2-mod-php", "php-mysql", "php-gd", "php-curl", "php-xml", "php-mbstring", "php-zip", "mariadb-server"},
		"joomla":      {"apache2", "libapache2-mod-php", "php-mysql", "php-gd", "php-curl", "php-xml", "php-mbstring", "php-zip", "php-intl", "mariadb-server", "curl", "tar"},
		"owasp-waf":   {"nginx", "libnginx-mod-http-modsecurity", "modsecurity-crs"},

		// Containers & cloud-native
		"docker":   {"docker.io", "docker-compose-v2"},
		"podman":   {"podman"},
		"buildah":  {"buildah"},
		"skopeo":   {"skopeo"},
		"kubectl":  {"curl", "ca-certificates"},
		"helm":     {"curl", "ca-certificates", "tar"},
		"terraform": {"curl", "ca-certificates", "unzip", "python3"},

		// IaC / config
		"ansible": {"ansible"},

		// Security & networking
		"fail2ban":  {"fail2ban"},
		"ufw":       {"ufw"},
		"wireguard": {"wireguard-tools"},
		"openvpn":   {"openvpn"},
		"nmap":      {"nmap"},
		"tcpdump":   {"tcpdump"},
		"mtr":       {"mtr-tiny"},
		"iperf3":    {"iperf3"},
		"clamav":    {"clamav", "clamav-daemon"},

		// Observability & messaging
		"netdata":       {"netdata"},
		"node-exporter": {"prometheus-node-exporter"},
		"collectd":      {"collectd"},
		"rabbitmq":      {"rabbitmq-server"},

		// Proxies / edge
		"caddy":   {"caddy"},
		"varnish": {"varnish"},
		"squid":   {"squid"},

		// Backup / cloud CLIs
		"rclone": {"rclone"},
		"restic": {"restic"},
		"awscli": {"awscli"},

		// Everyday DevOps CLI utilities
		"jq":           {"jq"},
		"httpie":       {"httpie"},
		"htop":         {"htop"},
		"tmux":         {"tmux"},
		"vim":          {"vim"},
		"neovim":       {"neovim"},
		"zip-tools":    {"zip", "unzip"},
		"tree":         {"tree"},
		"cmake":        {"cmake"},
		"ripgrep":      {"ripgrep"},
		"fd":           {"fd-find"},
		"bat":          {"bat"},
		"ncdu":         {"ncdu"},
		"sysstat":      {"sysstat"},
		"dns-tools":    {"dnsutils", "whois", "traceroute"},
		"apache-utils": {"apache2-utils"},
		"zsh":          {"zsh"},
		"fish":         {"fish"},
	}[serviceID]
	return packages, ok
}

func serviceCatalogUninstallPackages(serviceID string) ([]string, bool) {
	packages, ok := map[string][]string{
		"tomcat": {"tomcat10"}, "wildfly": {},
		"haproxy":     {"haproxy"},
		"memcached":   {"memcached"},
		"supervisor":  {"supervisor"},
		"git":         {"git"},
		"composer":    {"composer"},
		"golang":      {"golang-go"},
		"java":        {"default-jdk"},
		"ruby":        {"ruby-full"},
		"build-tools": {"build-essential", "pkg-config"},
		"sqlite":      {"sqlite3"},
		"certbot":     {"certbot"},
		"rsync":       {"rsync"},
		"nginx":       {"nginx"},
		"apache":      {"apache2"},
		"mysql":       {"mysql-server"},
		"mariadb":     {"mariadb-server"},
		"postgresql":  {"postgresql"},
		"redis":       {"redis-server"},
		"php":         {"php-fpm", "php-cli", "php-mysql", "php-xml", "php-mbstring", "php-curl", "php-zip", "php-gd"},
		"nodejs":      {"nodejs", "npm"},
		"wordpress":   {"wordpress"},

		"docker":   {"docker.io", "docker-compose-v2"},
		"podman":   {"podman"},
		"buildah":  {"buildah"},
		"skopeo":   {"skopeo"},
		"kubectl":  {},
		"helm":     {},
		"terraform": {},
		"ansible":  {"ansible"},

		"fail2ban":  {"fail2ban"},
		"ufw":       {"ufw"},
		"wireguard": {"wireguard-tools"},
		"openvpn":   {"openvpn"},
		"nmap":      {"nmap"},
		"tcpdump":   {"tcpdump"},
		"mtr":       {"mtr-tiny"},
		"iperf3":    {"iperf3"},
		"clamav":    {"clamav", "clamav-daemon"},

		"netdata":       {"netdata"},
		"node-exporter": {"prometheus-node-exporter"},
		"collectd":      {"collectd"},
		"rabbitmq":      {"rabbitmq-server"},

		"caddy":   {"caddy"},
		"varnish": {"varnish"},
		"squid":   {"squid"},

		"rclone": {"rclone"},
		"restic": {"restic"},
		"awscli": {"awscli"},

		"jq":           {"jq"},
		"httpie":       {"httpie"},
		"htop":         {"htop"},
		"tmux":         {"tmux"},
		"vim":          {"vim"},
		"neovim":       {"neovim"},
		"zip-tools":    {"zip", "unzip"},
		"tree":         {"tree"},
		"cmake":        {"cmake"},
		"ripgrep":      {"ripgrep"},
		"fd":           {"fd-find"},
		"bat":          {"bat"},
		"ncdu":         {"ncdu"},
		"sysstat":      {"sysstat"},
		"dns-tools":    {"dnsutils", "whois", "traceroute"},
		"apache-utils": {"apache2-utils"},
		"zsh":          {"zsh"},
		"fish":         {"fish"},
	}[serviceID]
	return packages, ok
}
