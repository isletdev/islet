# Next.js with Postgres and a domain

1. **Apps, Catalog, PostgreSQL, Install.** Name it `pg`. Islet generates the password and creates the stack.
2. **Databases, pg.** Create a database for the app (for example `shop`). Copy the connection URL it shows; it is only displayed once.
3. **Apps, Your apps, New app.** Repository URL, branch, **Detect** ("Next.js app, port 3000"). In Environment variables paste `DATABASE_URL=<the URL>` plus any `NEXT_PUBLIC_*` values (those are baked in at build time; everything else is injected at run time). Set the domain.
4. Under **Show build and run settings**, set the pre-deploy command to your migration, for example `npx prisma migrate deploy`. It runs once in the new image before traffic switches. If the app uses the ISR cache, add `/app/.next/cache` under Persistent paths.
5. **Create app, Deploy.** The database is reachable from the app container by the URL shown on the Databases page because both stacks join the proxy network.
6. **Backups, New plan.** Sources: `database pg` and Islet state, destination of your choice, preset "Everything nightly". The dump runs right before every snapshot.
