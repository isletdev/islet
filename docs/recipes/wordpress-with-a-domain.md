# WordPress with a domain

1. **Apps, Catalog, WordPress.** Name the stack, type the domain, pick Let's Encrypt. Islet generates the database password, installs MariaDB alongside, and routes the domain.
2. Open the domain and run the WordPress installer.
3. **Backups, New plan** with the stack's volumes and the database, nightly.
4. **Domains, the domain**: turn on the www redirect and, if the site is public, a rate limit.
