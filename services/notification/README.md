# notification service

E-mail delivery (SMTP). Port 8004.

* `POST /notify` (internal callers), `GET /internal/deliveries` (recent delivery results).
* SMTP comes from the platform settings in the identity service (dashboard: System settings) or the env `SMTP_HOST/PORT/USERNAME/PASSWORD/FROM/TLS`
  (`starttls|tls|none`). With no SMTP configured deliveries are recorded as not sent; nothing is faked.
* Env: `INTERNAL_TOKEN`, `IDENTITY_URL`, `PUBLIC_DASHBOARD_URL`, `SMTP_*`.
