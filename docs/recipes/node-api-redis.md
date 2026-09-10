# Node API with Redis

1. **Apps, Catalog, Redis, Install** as `cache`.
2. **Databases, cache.** Copy the internal URL (`redis://:password@cache-redis-1:6379/0`).
3. **Apps, New app**, repository URL, **Detect**. For Express, Fastify, NestJS or Hono Islet picks the Node strategy with `npm ci`, the build script if there is one, and `npm start`. Make sure the app listens on `process.env.PORT` (Islet sets it to the port in the settings, 3000 by default).
4. Add `REDIS_URL=<the URL>` to Environment variables, set a health check path (`/health` if you have one) and the domain. **Create app, Deploy.**
5. **Uptime, New check** on the public URL so you get a notification when it stops answering.
