# Security Policy

English | [简体中文](SECURITY.zh-CN.md)

## Current Support Scope

MnemoNAS is still under development and has not published any usable release. Security reports must identify the affected Git commit or development build. The project does not currently provide compatibility support for published versions or promise a response or fix timeline.

Maintainers handle security findings against the default branch on a best-effort basis according to impact, reproducible evidence, and current development priorities.

## Reporting a Vulnerability

Security vulnerability reports are handled seriously. Vulnerabilities should be reported responsibly.

### How to Report

**DO NOT** open a public GitHub issue for security vulnerabilities.

Use GitHub's **Private vulnerability reporting** feature for this repository when available.

If private reporting is unavailable, contact the project maintainer through the GitHub profile associated with the repository owner and avoid posting exploit details publicly. A dedicated security email should only be added here after the mailbox is configured and monitored.

### What to Include

Reports should include the following information:

1. **Description**: A clear description of the vulnerability
2. **Impact**: Potential impact and severity
3. **Steps to Reproduce**: Detailed steps to reproduce the issue
4. **Affected Commit**: The affected Git commit or development build
5. **Suggested Fix**: Optional suggested remediation

### Handling

- Maintainers review the report and may request additional reproduction evidence.
- Priority depends on demonstrated impact, exploitability, and the current development state.
- The project does not provide an initial-response, status-update, or fix-time SLA.
- Vulnerability details must remain private until maintainers confirm that disclosure is safe.

### Disclosure Policy

- Maintainers acknowledge reports and share status when circumstances allow.
- Maintainers decide when issue and remediation details can be disclosed based on risk and fix status.
- Vulnerability details, exploitation steps, and sensitive deployment information must not be published without confirmation.

## Development and Validation Security

The current source tree is not suitable for production deployment. Isolated development or test environments should follow these recommendations:

### Network Security

1. **Use HTTPS**: Cross-host validation should run behind a reverse proxy with TLS
2. **Firewall**: Access should be restricted to trusted networks
3. **Internal dataplane ports**: Dataplane gRPC/HTTP ports `9090/9091` should not be exposed to public or untrusted networks
4. **VPN**: Remote access should use a VPN or an equivalent controlled network boundary

### Authentication

1. **Strong Passwords**: Web UI accounts and WebDAV auth should use strong passwords
2. **Change Initial Credentials**: The initial admin password should be changed after first login
3. **Disable Unused Access**: WebDAV and sharing should be disabled when unused

### Data Protection

1. **Backup**: Maintain regular backups (see [backup guide](docs/backup-guide.en.md))
2. **Encryption**: Consider encrypting data at rest
3. **Access Control**: Minimize access permissions

### Updates

1. **Track the source**: Validation environments should record the tested Git commit and review dependency updates
2. **Monitor**: Security advisories should be monitored
3. **Test**: Updates should be tested in a staging environment first

## Dependency Checks

Run dependency vulnerability checks before releases and after dependency updates:

```bash
make install-audit-tools
make security-check
# Include frontend npm audit when the run explicitly accepts sending the dependency tree
# to the configured npm registry:
make security-check NPM_AUDIT=1
```

By default, `make security-check` covers Go with `govulncheck` and Rust with `cargo audit` for both the dataplane and `tools/proto-gen`. Frontend `npm audit --audit-level=high` is opt-in through `NPM_AUDIT=1` because it sends the dependency tree to the configured npm registry. CI runs Go, Rust, and frontend dependency checks on configured repository events.

## Known Security Considerations

### Current Limitations

1. **Limited ACL model**: Users, roles, groups, per-user root directories, and directory access rules are supported, but per-file or RFC-style ACLs are not
2. **HTTP by Default**: Built-in TLS exists, but production HTTPS is best handled by a reverse proxy
3. **Local Network Focus**: MnemoNAS is not designed for direct internet exposure without a hardened proxy/VPN layer

### Planned Security Features

- [ ] Fine-grained sharing and access policy controls
- [ ] Two-factor authentication
- [ ] External identity provider integration

## Security Audits

MnemoNAS has not yet undergone a formal security audit. Current test results do not constitute a production-security guarantee.

## Acknowledgments

The project acknowledges the following individuals for responsibly disclosing security issues:

*No security issues have been reported yet.*
