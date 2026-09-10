# Docker image with a domain

1. **New app**, Source **Docker image**, reference such as `ghcr.io/org/app:1.4.2`, the port the container listens on, and the domain.
2. **Deploy.** Islet pulls the image, starts it on the proxy network, waits for the health check and routes the domain. **Redeploy** pulls again; if the tag moved to a new digest you get a new release and can roll back to the old one.
3. Add persistent paths under build and run settings for anything the container writes that must survive updates.
