# n8n automation

1. **Apps, Catalog, n8n.** Name, domain, Let's Encrypt. The encryption key is generated and kept in the stack's credentials; it is in the recovery kit too.
2. Open the domain and create the owner account. Webhook URLs use the domain automatically.
3. Workflows that call other stacks on this server reach them by container name (Databases shows the internal URL).
4. **Backups:** add `volume n8n_data` to a plan; without it a re-install loses the workflows and credentials.
