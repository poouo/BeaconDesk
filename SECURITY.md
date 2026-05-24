# Security Policy

BeaconDesk is designed as a transparent, user-authorized remote assistance tool.

## Supported Versions

Security fixes are provided for the latest tagged release and the current `main` branch until the project publishes a wider support policy.

## Reporting a Vulnerability

Please report suspected vulnerabilities privately by opening a GitHub Security Advisory for `poouo/BeaconDesk`, or by contacting the maintainers through the repository owner profile if advisories are not yet enabled.

Do not publish exploit details before maintainers have had time to investigate and prepare a fix.

## Project Safety Boundaries

BeaconDesk will not accept features that enable:

- hidden or silent remote control
- permission bypass, UAC bypass, or credential theft
- antivirus or EDR evasion
- persistence without clear user consent
- covert screen, file, keyboard, microphone, or camera access

Remote control must require target-side confirmation or an explicit, visible, user-configured authorization model.

## Operational Guidance

- Use TLS for internet-facing relay deployments.
- Keep relay tokens and device identity files private.
- Review trusted devices and audit logs from the client Settings dialog.
- Treat temporary verification codes as short-lived session gates, not long-term passwords.
- Review systemd and file permissions after installation.
- Rotate shared relay tokens if they may have been exposed.
