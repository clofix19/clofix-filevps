# Java application servers in Library

WildFly and Apache Tomcat are VPS software entries in the Library. Install them
into a running VPS with Protection Mode disabled. Both install Java dependencies,
register native init scripts, and start automatically through the CloFix guest
boot sequence. Java service memory accounting distinguishes their JVM command lines.

- **WildFly:** pinned 42.0.0.Final distribution from the official GitHub release,
  checked against its published SHA-256 before extraction. Java 21 is installed.
  Application HTTP binds guest port **8180** (offset 100); management is bound to
  localhost inside the VPS. Configuration, deployments and logs are kept in
  `/var/lib/wildfly/standalone`. Uninstall retains that directory.
- **Tomcat:** Ubuntu's `tomcat10` package and default Java runtime. Application HTTP
  uses guest port **8080**. The init adapter runs Tomcat without requiring systemd.
  Custom applications/configuration should live in the normal Tomcat data paths.
  Removal uses apt remove, not purge/autoremove.

Use the VPS's custom port forwarding controls to publish the required application
port. Management consoles/accounts are not automatically published or created.
At least 1 GiB RAM is recommended for either Java server. Java package removal is
blocked while a catalog-installed WildFly or Tomcat dependency remains installed.

The Library renders local upstream brand logos for every service and Quick App.
Asset source URLs and the Devicon license are stored with the SVG/raster files in
`admin/web/admin-ui/assets/service-logos/`.
