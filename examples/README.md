# Sample apps

Small apps you can deploy from Islet before connecting your own code. In the panel: Apps → New app → "Try a sample" picks the repository and the folder for you; then press Deploy.

| Folder | What it shows |
|---|---|
| `static-site` | A plain HTML site served by nginx. Detection: static. |
| `node-api` | A Node HTTP API with no dependencies, honours `PORT`, health at `/health`. Detection: Node. |
| `go-service` | A Go HTTP service. Multi-stage build into a small image. Detection: Go. |
| `python-fastapi` | FastAPI served by uvicorn. Detection: Python (FastAPI). |

Every sample reads `GREETING` from the environment so you can see environment variables and redeploys work.
