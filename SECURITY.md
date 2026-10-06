# Security Policy

Bairro em Ação is a **trusted, single-user local application**. By default the API binds to `127.0.0.1`. In notebook server mode (`scripts/servidor-celular.sh`) it is reachable from the local network over HTTPS but has **no login**: anyone on the same Wi-Fi can open it. See "Privacy and security" in the README.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private vulnerability reporting: **Security → Report a vulnerability** on this repository. Include steps to reproduce, the affected commit, and the impact you observed.

You can expect an initial response within a few days. Fixes are released on the `main` branch.

## Secrets and personal data

- Database credentials live only in `.env`, which is git-ignored.
- `backend/data/` is git-ignored. It holds the photos, which show streets and may show people, and the local certificate authority. Its private key, `backend/data/tls/ca-key.pem`, can issue certificates that phones with the CA installed will trust. Never share it. If it leaks, delete `backend/data/tls/`, restart, and remove the old certificate from every phone.
- Never commit real photos, coordinates or reports from someone's walk without their consent.
- If you accidentally commit a secret, revoke or rotate it immediately; rewriting history alone is not enough.
