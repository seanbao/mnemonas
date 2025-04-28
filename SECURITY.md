# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |
| < 0.1   | :x:                |

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability, please report it responsibly.

### How to Report

**DO NOT** open a public GitHub issue for security vulnerabilities.

Use GitHub's **Private vulnerability reporting** feature for this repository when available.

If private reporting is unavailable, contact the project maintainer through the GitHub profile associated with the repository owner and avoid posting exploit details publicly. A dedicated security email should only be added here after the mailbox is configured and monitored.

### What to Include

Please include the following information:

1. **Description**: A clear description of the vulnerability
2. **Impact**: Potential impact and severity
3. **Steps to Reproduce**: Detailed steps to reproduce the issue
4. **Affected Versions**: Which versions are affected
5. **Suggested Fix**: If you have one (optional)

### Response Timeline

- **Initial Response**: Within 48 hours
- **Status Update**: Within 7 days
- **Fix Timeline**: Depends on severity
  - Critical: 24-48 hours
  - High: 7 days
  - Medium: 30 days
  - Low: Next release

### Disclosure Policy

- We will acknowledge receipt of your report
- We will provide an estimated timeline for the fix
- We will notify you when the issue is fixed
- We will credit you in the release notes (unless you prefer to remain anonymous)
- We ask that you do not publicly disclose the vulnerability until we have released a fix

## Security Best Practices

When deploying MnemoNAS:

### Network Security

1. **Use HTTPS**: Always deploy behind a reverse proxy with TLS
2. **Firewall**: Restrict access to trusted networks
3. **Internal dataplane ports**: Do not expose dataplane gRPC/HTTP ports 9090/9091 to public or untrusted networks
4. **VPN**: Consider VPN for remote access

### Authentication

1. **Strong Passwords**: Use strong passwords for Web UI accounts and WebDAV auth
2. **Change Initial Credentials**: Change the initial admin password after first login
3. **Disable Unused Access**: Disable WebDAV or sharing if you do not use them

### Data Protection

1. **Backup**: Maintain regular backups (see [backup guide](docs/backup-guide.md))
2. **Encryption**: Consider encrypting data at rest
3. **Access Control**: Minimize access permissions

### Updates

1. **Stay Updated**: Keep MnemoNAS and dependencies up to date
2. **Monitor**: Watch for security advisories
3. **Test**: Test updates in a staging environment first

## Dependency Checks

Run dependency vulnerability checks before releases and after dependency updates:

```bash
make install-audit-tools
make security-check
# Include frontend npm audit when you explicitly want to send the dependency tree
# to the configured npm registry:
make security-check NPM_AUDIT=1
```

By default, `make security-check` covers Go with `govulncheck` and Rust with `cargo audit` for both the dataplane and `tools/proto-gen`. Frontend `npm audit --audit-level=high` is opt-in through `NPM_AUDIT=1` because it sends the dependency tree to the configured npm registry. CI runs Go, Rust, and frontend dependency checks on configured repository events.

## Known Security Considerations

### Current Limitations

1. **No Fine-Grained ACL**: Users, roles, and home directories are supported, but per-file ACLs are not
2. **HTTP by Default**: Built-in TLS exists, but production HTTPS is best handled by a reverse proxy
3. **Local Network Focus**: MnemoNAS is not designed for direct internet exposure without a hardened proxy/VPN layer

### Planned Security Features

- [ ] Fine-grained sharing and access policy controls
- [ ] Two-factor authentication
- [ ] External identity provider integration

## Security Audits

MnemoNAS has not yet undergone a formal security audit. If you are interested in sponsoring a security audit, please contact us.

## Acknowledgments

We thank the following individuals for responsibly disclosing security issues:

*No security issues have been reported yet.*

---

Thank you for helping keep MnemoNAS secure!
