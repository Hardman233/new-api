# xAI Video and Files API

This integration exposes the official xAI SDK v6 gRPC surface for
`grok-imagine-video` while retaining the existing REST video API.

## Supported RPCs

- `xai_api.Video/GenerateVideo`
- `xai_api.Video/ExtendVideo`
- `xai_api.Video/GetDeferredVideo`
- `xai_api.Files/UploadFile`
- `xai_api.Files/ListFiles`
- `xai_api.Files/RetrieveFile`
- `xai_api.Files/DeleteFile`
- `xai_api.Files/RetrieveFileContent`
- `xai_api.Files/CreatePublicUrl`
- `xai_api.Files/RevokePublicUrl`

Video generation supports text, image, reference-image and video-edit inputs.
Inputs may use URLs, base64 data URLs, upstream xAI file IDs, or file IDs created
through this deployment's Files API. Video extension and storage options are
also supported.

## Channel

Create an xAI channel with:

- Type: `xAI`
- Model: `grok-imagine-video`
- Key: the credential issued by the selected upstream
- Base URL: empty or `https://api.x.ai` for the official gRPC API

Non-official HTTP(S) base URLs automatically use the REST video transport. The
REST gateway must expose:

- `POST /v1/videos`
- `GET /v1/videos/{task_id}`

The REST transport forwards the xAI-compatible video fields as JSON and uses
Bearer authentication. It accepts direct OpenAI-compatible video responses and
New API responses wrapped in `data`. Official `api.x.ai` traffic remains gRPC,
so adding a REST gateway does not change official SDK behavior.

`https://api.uniapi.io` is recognized as a UniAPI REST gateway. Public clients
continue to use `POST /v1/videos` and `GET /v1/videos/{task_id}` on this
deployment. The adaptor translates those calls internally to:

- `POST /xai/v1/videos/generations`
- `GET /xai/v1/videos/{request_id}`

For custom UniAPI-compatible gateways, configure the base URL with an
`/xai/v1` suffix. The adaptor maps `image` or `image_url` to `image.url`, maps
`output.upload_url`, supplies the documented required defaults when duration or
resolution is omitted, and accepts terminal responses that contain `video.url`
without a top-level status. The upstream `request_id` remains private; public
responses use this deployment's task ID.

The documented UniAPI surface supports text-to-video and one first-frame image.
Reference images, video editing, extension, and the xAI Files RPCs are rejected
for UniAPI channels instead of silently dropping unsupported fields. Those
capabilities remain available through the official xAI gRPC transport.

REST gateway support is limited to capabilities exposed by that gateway. For
example, a gateway without an extension route cannot provide official xAI video
extension even though the direct gRPC channel can.

The estimated charge uses the configured video pricing. When xAI returns
`cost_in_usd_ticks`, terminal task settlement uses that actual upstream cost and
the user's group ratio. If actual cost is absent, the submitted estimate is
kept; video usage is never reinterpreted as text-token billing.

## Environment

```dotenv
XAI_GRPC_ENABLED=true
XAI_GRPC_PORT=3001
XAI_GRPC_BIND_IP=127.0.0.1
XAI_GRPC_MAX_MESSAGE_MB=512
XAI_GRPC_REFLECTION_ENABLED=false
XAI_UPSTREAM_GRPC_MAX_MESSAGE_MB=512
XAI_PUBLIC_BASE_URL=https://ai.example.com
XAI_FILES_DIR=/data/xai-files
XAI_FILES_MAX_MB=512
XAI_FILES_OSS_ENABLED=true
XAI_FILES_KEEP_LOCAL=true
XAI_FILES_DOWNLOAD_TIMEOUT_SEC=180
```

`XAI_PUBLIC_BASE_URL` is required when callers request a public URL. Files use
the existing S3-compatible audit OSS configuration when OSS storage is enabled.
The database migration creates `xai_files` automatically.

## Nginx

The public TLS listener must accept HTTP/2. Add both gRPC service locations to
the same server block that serves the regular HTTP API:

```nginx
location /xai_api.Video/ {
    grpc_pass grpc://127.0.0.1:3001;
    grpc_set_header Authorization $http_authorization;
    grpc_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    grpc_set_header X-Forwarded-Proto https;
    grpc_set_header X-Forwarded-Host $host;
}

location /xai_api.Files/ {
    grpc_pass grpc://127.0.0.1:3001;
    grpc_set_header Authorization $http_authorization;
    grpc_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    grpc_set_header X-Forwarded-Proto https;
    grpc_set_header X-Forwarded-Host $host;
}
```

For older Nginx releases use `listen 443 ssl http2;`. For newer releases use
`listen 443 ssl;` with `http2 on;`.

After reloading Nginx, an official Python SDK client can use the platform key
and public host directly:

```python
from xai_sdk import Client

client = Client(api_key="platform-key", api_host="ai.example.com")
request = client.video.start(
    prompt="A slow camera move across a city at night",
    model="grok-imagine-video",
    duration=8,
)
print(request.request_id)
```

Video RPCs pass through token authentication, channel distribution,
concurrency controls, audit capture, task persistence and billing. Files RPCs
pass through token authentication and audit capture; stored outputs use the
same user and token ownership as the video request.
