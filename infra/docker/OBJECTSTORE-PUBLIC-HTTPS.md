# Creator Objectstore source for Velox workers

The Creator uploads content-addressed bytes to the local Objectstore. Final-job
payloads contain a stable `velox-asset://sha256-<digest>` identity, a signed
HTTPS `source_uri`, SHA-256 and byte size. Velox selects/reserves a worker and
sends that reference in its existing FutureAssetPlan gRPC path. The worker
downloads from Objectstore and verifies the digest and size. The Master does
not proxy the media bytes.

The public listener is deliberately an opt-in Compose override. The normal
Objectstore listener remains bound to `127.0.0.1:9000` for Creator uploads.
The public listener only serves signed GETs; PUT, HEAD, staging and health
continue to require `OBJECTSTORE_TOKEN`.

## Required configuration

Set these values in the deployment environment file read by Compose and the
Creator process:

```dotenv
OBJECTSTORE_TOKEN=<long random private upload token>
OBJECTSTORE_SIGNING_SECRET=<at least 32 random characters shared by Creator and Objectstore>
OBJECTSTORE_CERT_DIR=/etc/letsencrypt/live/<certificate-name>
OBJECTSTORE_PUBLIC_URL=https://77.93.152.122
RENDERINGGEN_STORE_URL=http://127.0.0.1:9000
```

The TLS directory must contain `fullchain.pem` and `privkey.pem`, and the
certificate must cover the public hostname or IP in `OBJECTSTORE_PUBLIC_URL`.
Allow inbound TCP 443 to the Creator host. If the selected certificate flow
needs HTTP validation, allow TCP 80 during issuance and renewal. The public
port must not be mapped to port 9000.

Enable the listener with:

```sh
docker compose -f docker-compose.yaml -f docker-compose.objectstore-public.yaml up -d --build objectstore
```

Renew the certificate through the host's certificate manager and restart the
Objectstore after renewal so Go reloads the certificate. A renewal hook can
run `docker compose -f docker-compose.yaml -f docker-compose.objectstore-public.yaml restart objectstore`.

## Worker path

The worker accepts direct HTTPS sources only for its exact source-host
allowlist. The RenderingGen public endpoint is currently pinned to
`77.93.152.122`; redirect hops are restricted to the same allowlist and HTTPS
port 443. Change the worker allowlist together with `OBJECTSTORE_PUBLIC_URL`
if the Creator host's public address changes. The Master receives only
metadata and signed URLs, then places and dispatches the task over its existing
mTLS gRPC connection. It does not fetch or store these asset bytes.

The Creator keeps each overlay's `item_id`, `track`, `in_us`, `out_us`, and
`content_hash` in `pipelinegen.overlay-manifest.v1`; the Objectstore reference
is the timeline MP4 source, not a replacement for the per-overlay timing
entries.
