# MetaTube all-in-one stack

This Compose project deploys the custom MetaTube server, provider bridge,
FlareSolverr, and PostgreSQL as one managed stack while keeping each service
isolated in its own container.

```sh
docker compose -f compose.yaml up -d --build
```

MetaTube remains available at `http://192.168.10.166:8080`. FlareSolverr is
available at port `8191` for diagnostics, while provider requests use the
private Compose network.

The existing MetaTube configuration and PostgreSQL database use their current
Kraken bind mounts and survive stack recreation.

The provider bridge is included in this deployment directory so building the
stack does not require credentials for a second repository.
