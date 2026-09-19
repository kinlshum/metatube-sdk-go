# Admin browser regression suite

`admin-error-index.js` drives the live MetaTube Admin in headless Chrome and
asserts the acceptance cases of
[`docs/METATUBE_ADMIN_NEXT_TODO.md`](../../docs/METATUBE_ADMIN_NEXT_TODO.md)
section A: the expanded-trace error index.

It seeds real traces through the trace ingest API, then checks:

- one failed provider inside an otherwise successful run, including the
  provider, failing stage, HTTP status, message, attempt, duration, and reason;
- the trace-level summary is not duplicated on top of the timeline failure;
- the failing run node and step expand automatically;
- `Focus event` moves focus to the exact timeline event and flashes both the
  event and the step;
- collapse, reopen, and a list refresh while the drawer is open;
- multiple distinct failures and the partial-run label;
- a slow but successful call is not an error, and a clean run shows `0 errors`;
- the actor tab renders the same index;
- the page raises no JavaScript errors.

The traces it creates are deleted at the end of the run.

## Usage

```sh
mkdir -p /tmp/adminnext-e2e && cd /tmp/adminnext-e2e
npm i playwright-core     # drives the installed Chrome, no browser download
cp <repo>/deployment/e2e/admin-error-index.js .
ADMIN_BASE=http://192.168.10.166:8080 node admin-error-index.js
```

Environment overrides:

- `ADMIN_BASE` (default `http://192.168.10.166:8080`)
- `CHROME_PATH` (default `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`)

The exit code is `0` only when every check passes; a screenshot of the focused
error entry is written next to the script.
