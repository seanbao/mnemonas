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

Instead, please email us at: **security@mnemonas.dev** (or use GitHub's private vulnerability reporting feature)

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
3. **VPN**: Consider VPN for remote access

### Authentication

1. **Strong Passwords**: Use strong passwords for WebDAV auth
2. **Change Defaults**: Always change default credentials
3. **Rate Limiting**: Enable rate limiting for auth endpoints

### Data Protection

1. **Backup**: Maintain regular backups (see [backup guide](docs/backup-guide.md))
2. **Encryption**: Consider encrypting data at rest
3. **Access Control**: Minimize access permissions

### Updates

1. **Stay Updated**: Keep MnemoNAS and dependencies up to date
2. **Monitor**: Watch for security advisories
3. **Test**: Test updates in a staging environment first

## Known Security Considerations

### Current Limitations

1. **No Multi-User ACL**: Current version has basic auth only
2. **HTTP by Default**: HTTPS requires reverse proxy setup
3. **Local Network Focus**: Not designed for direct internet exposure

### Planned Security Features

- [ ] Multi-user with role-based access control
- [ ] Built-in TLS support
- [ ] Audit logging
- [ ] Two-factor authentication

## Security Audits

MnemoNAS has not yet undergone a formal security audit. If you are interested in sponsoring a security audit, please contact us.

## Acknowledgments

We thank the following individuals for responsibly disclosing security issues:

*No security issues have been reported yet.*

---

Thank you for helping keep MnemoNAS secure!
