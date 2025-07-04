---
name: Bug Report
about: Report a bug to help us improve
title: '[Bug] '
labels: bug
assignees: ''
---

## Bug Description
A clear and concise description of what the bug is.

> Security vulnerabilities should not be reported in public issues. Use GitHub private vulnerability reporting as described in `SECURITY.md`.

## Steps to Reproduce
1. Go to '...'
2. Click on '...'
3. See error

## Expected Behavior
A clear and concise description of what you expected to happen.

## Actual Behavior
What actually happened.

## Environment
- **OS**: [e.g., Ubuntu 22.04, macOS 14.0]
- **MnemoNAS Version**: [e.g., 0.1.0]
- **Deployment Method**: [Ubuntu/systemd / Docker / Manual binary / Development]
- **Storage Filesystem**: [e.g., ZFS mirror, Btrfs, ext4, XFS]
- **Client**: [e.g., macOS Finder, Windows Explorer, rclone]

## Screenshots
If applicable, add screenshots to help explain your problem.

## Diagnostics
For Ubuntu/systemd deployments, paste the output of:

```bash
sudo mnemonas-doctor
```

For Docker deployment or startup issues, paste the output of:

```bash
./scripts/mnemonas-docker-preflight.sh
docker compose ps
docker compose logs --tail 100 mnemonas
```

For UI layout issues, include the browser name and viewport size, and mention whether it happens on desktop, mobile, or both.

## Logs
```
Paste relevant logs here
```

## Additional Context
Add any other context about the problem here.
