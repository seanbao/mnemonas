# Support

English | [简体中文](SUPPORT.md)

This document describes MnemoNAS support channels, issue triage, and maintenance boundaries.

## Support Channels

| Case | Recommended Channel | Notes |
| --- | --- | --- |
| Reproducible bug | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | Include reproduction steps and diagnostics |
| Feature or product suggestion | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | Describe the use case and expected benefit |
| Security vulnerability | GitHub Private Vulnerability Reporting | Do not post exploit details publicly; see [SECURITY.md](SECURITY.md) |

Usage questions can also be recorded in Issues. Please make it clear whether the report is a question or a confirmed bug.

## Before Opening an Issue

Please check:

- [README](README.en.md) and [documentation index](docs/README.en.md)
- [FAQ](docs/faq.en.md)
- [Docker deployment guide](docs/docker-deployment.en.md) or [Linux/systemd deployment guide](docs/linux-systemd-deployment.en.md)
- [WebDAV compatibility](docs/webdav-compatibility.en.md)
- Existing Issues for similar reports

## What to Include in Bug Reports

- MnemoNAS version or Git commit
- Deployment method: Ubuntu/systemd, Docker, manual binary, or development environment
- Operating system, filesystem, and client information
- Reproduction steps, expected behavior, and actual behavior
- Relevant logs, screenshots, or error messages
- For systemd deployments, include `sudo mnemonas-doctor` output
- For Docker deployments, include `./scripts/mnemonas-docker-preflight.sh`, `docker compose ps`, and relevant Compose logs

Remove passwords, tokens, cookies, sensitive internal addresses, and other private information before posting logs.

## Support Boundary

Maintainers try to address clear, reproducible, high-impact reports, but no fixed response time or commercial SLA is promised.

Current focus:

- Linux servers and common small-host/NAS scenarios
- Docker Compose and Linux/systemd deployment paths
- Browser Web UI and common WebDAV clients
- Data migration, backup, restore, and security configuration

Limited support may be available for:

- Unofficial repackaging or heavily modified forks
- Direct public internet exposure without reverse proxy, TLS, VPN, or firewall protection
- Large-scale production capacity planning
- Reports without reproduction details, logs, or diagnostics

Commercial support, hosted services, and enterprise SLA are outside the current project scope.
