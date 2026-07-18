# MnemoNAS Documentation

English | [简体中文](README.md)

This directory contains MnemoNAS usage guides, deployment notes, and reference material.

> [!WARNING]
> MnemoNAS is still under development and has not published any usable release. These documents support source development, testing, and preparation for a future release. Submit problems and suggestions through [GitHub Issues](https://github.com/seanbao/mnemonas/issues).

## Documentation Index

### Development Validation and Future Deployment Reference

| Document | Description |
| --- | --- |
| [README](../README.en.md) | Project status and source-tree preview |
| [中文 README](../README.md) | Chinese project overview |
| [Linux/systemd deployment](linux-systemd-deployment.en.md) | Validation flow for future systemd release archives |
| [Public server quickstart](public-server-quickstart.en.md) | HTTPS and safety validation before a future public release |
| [Docker deployment](docker-deployment.en.md) | Source builds and future container-release validation |
| [Mounting guide](mounting-guide.en.md) | WebDAV client validation in a development environment |
| [Reverse proxy setup](reverse-proxy-setup.en.md) | Reverse-proxy validation for a future public entry path |
| [Public cloud firewall checklist](cloud-firewall-checklist.en.md) | Cloud-firewall review for a future public release |

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
| [Client refactor log](refactor-log.en.md) | Flutter client refactor progress, completed scope, and remaining work |
| [Development guide](development.en.md) | Local setup and gates for the server, Web UI, and Flutter client |
| [Flutter client](../client/README.en.md) | Current Android-first client scope, gaps, and validation boundaries |
| [Testing strategy](testing-strategy.en.md) | Unit, integration, E2E, and torture test strategy |
| [Development change record](release-notes.en.md) | Unreleased branch changes, validation evidence, and first-public-release preparation |
| [API reference](api-reference.en.md) | REST API endpoints and request/response formats |
| [Extension points](extension-points.en.md) | Future interface draft for S3, plugins, and runners |

### Feedback and Security

| Document | Description |
| --- | --- |
| [Feedback](../SUPPORT.en.md) | Issue reporting channels, required context, and handling boundaries |
| [反馈说明](../SUPPORT.md) | Chinese feedback document |
| [Code of Conduct](../CODE_OF_CONDUCT.md) | Conduct requirements for issue feedback |
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

**Development validation:**

1. Read the [English README](../README.en.md) or [Chinese README](../README.md).
2. Follow the [Development guide](development.en.md), or use the [Docker deployment guide](docker-deployment.en.md) to build a development image from source.
3. Validate file and WebDAV behavior with non-important test data.
4. Do not use the current source tree for production or public service.

**Troubleshooting:**

1. Check [FAQ](faq.en.md).
2. Check [WebDAV compatibility](webdav-compatibility.en.md).
3. Use [Support](../SUPPORT.en.md) to choose the right reporting path.

**Development:**

1. Read [Architecture](architecture.en.md).
2. Follow [Development guide](development.en.md).
3. Review [Testing strategy](testing-strategy.en.md) before larger changes.
