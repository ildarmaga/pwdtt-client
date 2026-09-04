# Tunnel bench (no OS TUN)

One benchmark binary is used on the server and on the PC. The PC runner dials
the service through a protocol's local SOCKS5 endpoint, so the measurement does
not install a TUN adapter, alter routes, or require administrator privileges.

Server:

```sh
WDTT_BENCH_TOKEN='change-me' ./tunnel-bench -mode serve -listen 0.0.0.0:18080
```

PC (repeat for `wdtt`, `raw`, `csqtt`, and `wb` SOCKS endpoints):

```powershell
$env:WDTT_BENCH_TOKEN = 'change-me'
.\tunnel-bench.exe -protocol wb -proxy socks5://user:pass@127.0.0.1:10809 `
  -target SERVER_IP:18080 -mb 128 -streams 4 -json wb.json
```

The report contains unloaded median/p95 connection RTT, loaded p95 RTT,
download/upload throughput, transferred bytes, and failed stream count. Proxy
credentials and the benchmark token are never written to the report.

## Run every protocol

Copy `bench-config.example.json` to the ignored local file
`bench-config.json`, enter the four local SOCKS endpoints, then run:

```powershell
.\run-all.ps1
```

Each transport gets an individual JSON file and `reports/summary.json` contains
the comparable matrix. The runner itself never creates an OS TUN or changes
routes. The protocol process under test must expose a local SOCKS endpoint;
WDTT already has the `vk-wg-joiner` userspace netstack and WB exposes its mux
SOCKS endpoint. RAW and CSQTT use their corresponding headless SOCKS adapters.
