# Actor mapping source

`JAV-ACTOR-SUB.ini` is the version-controlled source of truth for MetaTube
actor substitutions.

Edit it in VS Code, commit and push the change, then run:

```sh
~/scripts/update_actor_map.bash
```

The updater distributes the file to Kraken, Unraid, and Unmini, loads it into
the MetaTube Emby plugin, restarts Emby, and verifies the installed mapping
count.
