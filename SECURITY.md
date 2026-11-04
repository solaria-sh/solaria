# Security policy

## Supported versions

Solaria is pre-release. Only the latest commit on `main` gets security fixes until v1.0.

## Reporting a vulnerability

**Please don't open a public issue.** Report vulnerabilities privately through [GitHub's private vulnerability reporting](https://github.com/renantarouco/solaria/security/advisories/new).

Include what you found, how to reproduce it, and the impact you expect. You'll get an acknowledgement within a week. We'll agree on a disclosure date with you once a fix is ready.

## Scope

Solaria handles credentials for agent hosts and model providers. Reports about any of these are especially welcome:

- secrets leaking into state files, digests, logs, plan output or rendered artifacts;
- a provider granting an agent more access than its card declares;
- unsafe handling of the files and containers that Solaria builds.
