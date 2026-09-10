# Django with Postgres

1. Install **PostgreSQL** from the catalog as `pg`, then create a database on the Databases page and copy its URL.
2. **New app, Detect.** Django is detected from `requirements.txt` or `pyproject.toml`; the start command becomes `gunicorn <project>.wsgi:application --bind 0.0.0.0:8000 --workers 2`. Adjust the project name if the guess is wrong.
3. Environment: `DATABASE_URL`, `SECRET_KEY`, `ALLOWED_HOSTS=<your domain>`, `DEBUG=0`. Pre-deploy command: `python manage.py migrate`. If you collect static files at build time, set the build command to `python manage.py collectstatic --noinput` and serve them through WhiteNoise.
4. **Deploy.** Health check path `/` or a dedicated `/healthz` view.
5. For Celery, create a second app from the same repository with the start command `celery -A <project> worker` and no domain; give it the same environment variables.
