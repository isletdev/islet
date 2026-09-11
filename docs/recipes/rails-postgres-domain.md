# Rails with Postgres and a domain

1. **Databases, Install Postgres.** Copy the internal URL from the instance card.
2. **Apps, New app.** Paste the repository. **Detect** finds `Gemfile` and `bin/rails` and picks the Rails strategy: `bundle install`, assets precompiled during the build, Puma on port 3000.
3. Environment: `DATABASE_URL` from step 1, `RAILS_ENV=production`, `SECRET_KEY_BASE` (`bin/rails secret` locally), `RAILS_SERVE_STATIC_FILES=1` unless a CDN serves assets, and `RAILS_LOG_TO_STDOUT=1` so the Logs page shows the app.
4. Pre-deploy command: `bin/rails db:migrate`. Health check path: `/up` on Rails 7.1 and later.
5. Set the domain and **Deploy.** Rollback keeps the previous image, so a bad migration can be reverted with **Roll back** followed by `bin/rails db:rollback` from the container's terminal.
6. Background jobs: a second app from the same repository with the start command `bundle exec sidekiq` (or `bin/jobs` for Solid Queue), sharing the same environment group (`@rails-shared`).
