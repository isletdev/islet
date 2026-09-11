# Laravel with MySQL and a domain

1. **Databases, Install MySQL.** Give it a name like `shop`. Copy the internal URL from the instance card; it looks like `mysql://shop:…@islet-db-shop:3306/shop`.
2. **Apps, New app.** Paste the repository. **Detect** finds `composer.json` and `artisan` and picks the Laravel strategy: nginx and PHP-FPM in one container on port 8080, serving `public/`.
3. Environment: set `APP_KEY` (run `php artisan key:generate --show` locally), `APP_ENV=production`, `APP_URL=https://shop.example.com`, and the database parts from the URL above (`DB_CONNECTION=mysql`, `DB_HOST=islet-db-shop`, `DB_PORT=3306`, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD`). Attach the app to the database's network with **Add service** if Detect did not already.
4. Pre-deploy command: `php artisan migrate --force`. Optional build command if the front end is compiled: `npm ci && npm run build` (the PHP image ships Node).
5. Set the domain and **Deploy.** The first deploy takes a minute or two for Composer; later ones reuse the vendor layer.
6. Queues and the scheduler: add a second app from the same repository with the start command `php artisan queue:work --tries=3`, and a cron job that runs `docker exec <app container> php artisan schedule:run` every minute.
