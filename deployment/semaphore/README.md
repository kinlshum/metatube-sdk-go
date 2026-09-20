# Semaphore deployment

The `Deploy MetaTube2 (Emby only)` Semaphore task runs
[`deploy-metatube2.yml`](deploy-metatube2.yml) against Kraken.

The deployment is intentionally limited to the dedicated Emby service:

- builds only the `metatube2` compose service from the branch configured in
  Kraken's compose file;
- creates a timestamped rollback image before changing the running service;
- recreates `metatube2` with `--no-deps`;
- verifies the Admin page and provider API;
- confirms the original `metatube`, MetaTube2 PostgreSQL, Provider Bridge2,
  and FlareSolverr2 containers were not recreated or restarted;
- restores the rollback image automatically when post-deploy verification
  fails.

Semaphore resources:

- project: `homelab`
- repository: `MetaTube SDK Go`
- branch: `codex/mdcng-fc2cmadb-providers`
- inventory: `Kraken - MetaTube2`
- host group: `metatube2_hosts`
- task template: `Deploy MetaTube2 (Emby only)`

The inventory uses a dedicated SSH key stored in Semaphore's encrypted key
store. Never commit the private key, API tokens, or production `.env` values to
this repository.

