-- Extra processes per app (worker, scheduler) and CI-gated deploys.
ALTER TABLE apps ADD COLUMN processes TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN deploy_on TEXT NOT NULL DEFAULT 'push';
