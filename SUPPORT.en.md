# Issue Feedback

English | [简体中文](SUPPORT.md)

MnemoNAS is still under development, has not published any usable release, and does not provide stable-version support. GitHub Issues currently accept defects, usage problems, compatibility results, and feature suggestions.

Feedback must identify the tested Git commit. Maintainers triage reports according to project priorities without promising a response, fix, or release timeline.

## Feedback Channels

| Case | Recommended Channel | Notes |
| --- | --- | --- |
| Reproducible bug | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | Include reproduction steps and diagnostics |
| WebDAV client compatibility | [WebDAV compatibility report form](https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml) | Submit client validation results, mount differences, or client-specific failures |
| Feature or product suggestion | [GitHub Issues](https://github.com/seanbao/mnemonas/issues) | Describe the use case and expected benefit |
| Security vulnerability | GitHub Private Vulnerability Reporting | Do not post exploit details publicly; see [SECURITY.md](SECURITY.md) |

Usage questions can also be recorded in Issues. The title and body should make clear whether the report is a question or a confirmed bug.
For WebDAV client compatibility results, mount differences, or client-specific failures, use the WebDAV compatibility report form first.

## Before Submitting Feedback

Check:

- [README](README.en.md) and [documentation index](docs/README.en.md)
- [FAQ](docs/faq.en.md)
- [Docker deployment guide](docs/docker-deployment.en.md) or [Linux/systemd deployment guide](docs/linux-systemd-deployment.en.md)
- [WebDAV compatibility](docs/webdav-compatibility.en.md)
- Existing Issues for similar reports
- Whether a WebDAV client issue is better suited for the [WebDAV compatibility report form](https://github.com/seanbao/mnemonas/issues/new?template=webdav_compatibility.yml)

## What to Include in Defect Reports

- Tested Git commit
- Deployment method: Ubuntu/systemd, Docker, manual binary, or development environment
- Operating system, filesystem, and client information
- Reproduction steps, expected behavior, and actual behavior
- Relevant logs, screenshots, or error messages
- For systemd deployments, include `sudo mnemonas-doctor` output
- For Docker deployments, include `./scripts/mnemonas-docker-preflight.sh`, `docker compose ps`, and relevant Compose logs

Remove passwords, tokens, cookies, sensitive internal addresses, and other private information before posting logs.

## Feedback Handling Boundary

Maintainers prioritize clear, reproducible, high-impact reports. Features, interfaces, data formats, and deployment paths may still change during development.

Current review focus:

- Linux servers and common small-host/NAS scenarios
- Docker Compose and Linux/systemd deployment paths
- Browser Web UI and common WebDAV clients
- Data migration, backup, restore, and security configuration

The following reports may not be handled:

- Unofficial repackaging or heavily modified forks
- Direct public internet exposure without reverse proxy, TLS, VPN, or firewall protection
- Large-scale production capacity planning
- Reports without reproduction details, logs, or diagnostics

The project does not currently provide commercial support, hosted services, or a paid SLA.
