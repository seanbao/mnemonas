# MnemoNAS Documentation

English | [简体中文](README.md)

This directory contains MnemoNAS usage guides, deployment notes, and reference material.

## Documentation Index

### Quick Start

| Document | Description |
| --- | --- |
| [README](../README.en.md) | Project overview and quick start |
| [中文 README](../README.md) | Chinese project overview |
| [Linux/systemd deployment](linux-systemd-deployment.en.md) | Run MnemoNAS as systemd services on a Linux server |
| [Public server quickstart](public-server-quickstart.en.md) | Recommended path for public domains, HTTPS, reverse proxy, and safety checks |
| [Docker deployment](docker-deployment.en.md) | Deploy MnemoNAS with Docker Compose |
| [Mounting guide](mounting-guide.en.md) | WebDAV mounting instructions for common platforms |
| [Reverse proxy setup](reverse-proxy-setup.en.md) | HTTPS public entry setup with Caddy, Nginx, Traefik, or Cloudflare Tunnel |
| [Public cloud firewall checklist](cloud-firewall-checklist.en.md) | Cloud security group, VPC firewall, and port exposure review |

### User Guides

| Document | Description |
| --- | --- |
| [FAQ](faq.en.md) | Common questions and troubleshooting |
| [Storage internals and operations guidance](storage-internals.en.md) | CAS design, filesystem recommendations, and tuning |
| [Backup guide](backup-guide.en.md) | 3-2-1 backup strategy and restore flow |
| [WebDAV compatibility](webdav-compatibility.en.md) | Client compatibility and protocol support |
| [Security hardening guide](security.en.md) | Authentication, HTTPS, and network security configuration |

### Development

| Document | Description |
| --- | --- |
| [Architecture](architecture.en.md) | System architecture and technology choices |
| [Design decisions](design-decisions.en.md) | Design rationale and competitive goals |
| [Roadmap](roadmap.en.md) | Priorities and capability boundaries from private file cloud to home and small-team NAS |
| [Development guide](development.en.md) | Local development setup |
| [Testing strategy](testing-strategy.en.md) | Unit, integration, E2E, and torture test strategy |
| [Hardening progress ledger](hardening-progress.en.md) | Completed hardening areas, validation evidence, and closeout items |
| [Hardening review summary](hardening-review-summary.en.md) | Current hardening worktree review groups, residual risks, and pre-release checks |
| [Release notes draft](release-notes.en.md) | Draft notes, validation evidence, and post-publish verification entry point for the next public release |
| [API reference](api-reference.en.md) | REST API endpoints and request/response formats |
| [Extension points](extension-points.en.md) | Future interface draft for S3, plugins, and runners |

### Support and Security

| Document | Description |
| --- | --- |
| [Support](../SUPPORT.en.md) | Support channels and support boundary |
| [支持说明](../SUPPORT.md) | Chinese support document |
| [Contributing guide](../CONTRIBUTING.en.md) | Contribution flow, quality gates, and safety boundaries |
| [贡献指南](../CONTRIBUTING.md) | Chinese contributing guide |
| [Code of Conduct](../CODE_OF_CONDUCT.md) | Community conduct expectations and enforcement scope |
| [行为准则](../CODE_OF_CONDUCT.zh-CN.md) | Chinese code of conduct |
| [Security policy](../SECURITY.md) | Vulnerability reporting and deployment security |
| [安全策略](../SECURITY.zh-CN.md) | Chinese security policy |

### Configuration Reference

| File | Description |
| --- | --- |
| [Configuration reference](configuration.en.md) | Complete config options |
| [mnemonas.example.toml](../mnemonas.example.toml) | Example config file |
| [CHANGELOG](../CHANGELOG.en.md) | Change history |

## Links

- [GitHub repository](https://github.com/seanbao/mnemonas)
- [Issues](https://github.com/seanbao/mnemonas/issues)
- [Support](../SUPPORT.en.md)

## Reading Path

**First-time users:**

1. Read the [English README](../README.en.md) or [Chinese README](../README.md).
2. For long-running deployment, start with [Linux/systemd deployment](linux-systemd-deployment.en.md). For temporary evaluation, start with [Docker deployment](docker-deployment.en.md).
3. If public access is needed, follow the [Public server quickstart](public-server-quickstart.en.md) for HTTPS entry setup.
4. Use the [Mounting guide](mounting-guide.en.md) to connect WebDAV clients.

**Troubleshooting:**

1. Check [FAQ](faq.en.md).
2. Check [WebDAV compatibility](webdav-compatibility.en.md).
3. Use [Support](../SUPPORT.en.md) to choose the right reporting path.

**Development:**

1. Read [Architecture](architecture.en.md).
2. Follow [Development guide](development.en.md).
3. Review [Testing strategy](testing-strategy.en.md) before larger changes.
