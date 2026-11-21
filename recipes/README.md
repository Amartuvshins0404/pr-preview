# Recipes

A recipe is a Compose file that runs one PR. Put it in the repo as `.preview/compose.yml`, or here
as `<repo>.yml` for a repo that carries none.

```yaml
x-preview:
  service: web
  port: 3000

services:
  web:
    build: .
    environment:
      PUBLIC_URL: ${PREVIEW_URL}
    depends_on: [db]
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: preview
```

- `x-preview.service` names the service that gets the traffic. It defaults to the only service,
  then to one named `web` or `app`.
- `x-preview.port` is the port that service listens on inside its container. It is required.
- Paths resolve from the repo root, for both kinds of recipe.
- Published `ports` and `container_name` are dropped, so PRs never collide.
- These variables are set for interpolation: `PREVIEW_URL`, `PREVIEW_HOST`, `PREVIEW_SLUG`,
  `PREVIEW_OWNER`, `PREVIEW_REPO`, `PREVIEW_PR`, `PREVIEW_SHA`.
- Put secrets for a repo in `<repo>.env` here. The service passes it as `--env-file`. Git ignores it.
- The preview is ready when the entry service answers HTTP below 502. One-shot services, such as a
  migration, may exit.
