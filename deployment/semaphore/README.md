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

The same inventory also has a separate `Deploy MetaTube1 (JAV Master)` task.
It invokes `/usr/local/sbin/deploy-metatube1`, which targets only the original
`metatube` service at `192.168.10.166:8080` and preserves both the original
dependencies and every MetaTube2 service.

The inventory connects with a dedicated, unprivileged
`semaphore-metatube2` account. That account can only run the root-owned
`/usr/local/sbin/deploy-metatube2` wrapper through `sudo`; it does not receive
general root or Docker access. Semaphore stores its private key in the
encrypted key store. Never commit the private key, API tokens, or production
`.env` values to this repository.

## Installed state

Configured on the Unraid Semaphore server (`192.168.10.150`) on 2026-09-20:

- repository ID `1`, environment ID `1`, inventory ID `1`, template ID `1`;
- Kraken account `semaphore-metatube2` is explicitly allowed by SSH and has
  only the fixed wrapper in `/etc/sudoers.d/semaphore-metatube2`;
- the wrapper is installed root-owned at
  `/usr/local/sbin/deploy-metatube2`;
- Semaphore task `1` completed successfully in Ansible check mode, verifying
  repository checkout, inventory resolution, encrypted-key SSH access, and
  playbook wiring without restarting MetaTube2.
