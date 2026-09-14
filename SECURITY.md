# Security policy

## Supported version

Security fixes are applied to the latest release only.

## Reporting a vulnerability

Do not publish credentials, traffic captures, certificates, device identifiers,
or proof-of-concept data in a public issue. Use GitHub's private vulnerability
reporting feature for this repository. Include the affected version, impact,
and the smallest reproduction that does not contain personal or game-account
data.

## Safe operation

- Keep every service bound to `127.0.0.1`.
- Do not expose ports 443, 8080, or 8081 to a LAN or the Internet.
- Never reuse the generated local certificate authority for another purpose.
- Treat databases, logs, HAR files, and Android backups as private data.
- Return the client to **Official** mode before using an official account.

The project is intended for local development and interoperability research. It
is not hardened as an Internet-facing service.
