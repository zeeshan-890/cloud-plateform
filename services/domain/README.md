# domain service

Add / list / delete custom domains; verify via TXT or CNAME (or `force` / `DOMAIN_DNS_STUB=true`).

On verify, writes Traefik file-provider config under `TRAEFIK_DYNAMIC_DIR` and requests a certificate from the certificate service.

| Env | Default |
|-----|---------|
| `PORT` | `8012` |
| `DOMAIN_DNS_STUB` | `true` |
| `DOMAIN_CNAME_TARGET` | `cname.jp.localhost` |
| `TRAEFIK_DYNAMIC_DIR` | `/etc/traefik/dynamic` |
| `TRAEFIK_BACKEND_URL` | `http://gateway:8000` |
| `TRAEFIK_CERT_RESOLVER` | (empty = TLS block omitted unless set) |
