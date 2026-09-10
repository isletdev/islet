# Go service

1. **New app, Detect.** A `go.mod` gives the Go strategy: a multi-stage build with the official image into a small Alpine runtime. The binary must listen on `PORT` (8080 by default).
2. Set a health check path and the domain. **Deploy.** Builds take under a minute; later builds reuse the module cache layer.
3. Keep secrets in Environment variables; they are injected at run time and never written into the image.
