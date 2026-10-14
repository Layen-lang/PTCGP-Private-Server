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

- Keep each device's game and administration services bound to `127.0.0.1`,
  including automatically allocated ports for additional devices.
- The primary panel stays on loopback. The bundled configuration also enables
  the separate home Wi-Fi listener described in [Configuration](docs/CONFIGURATION.md#runtime);
  remove `runtime.lanAddress` to disable it. Do not expose any service to the Internet.
- Never reuse the generated local certificate authority for another purpose.
- Treat databases, logs, HAR files, and Android backups as private data.
- Return the selected device to **Official** mode before using an official
  account there. **Stop all** restores every managed device.

These requirements also apply to rooted physical phones. For Wi-Fi ADB, use a
trusted local network and disable wireless debugging or TCP ADB after restoring
Official mode, as described in the [phone setup guide](docs/INSTALLATION.md#physical-android-phone-usb-or-wi-fi).

The project is intended for local development and interoperability research. It
is not hardened as an Internet-facing service.
