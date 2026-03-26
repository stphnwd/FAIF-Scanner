# FAIF Scanner

Fork of [rdio-scanner](https://github.com/chuot/rdio-scanner) (v6.6.3) with audio-on-disk storage, bulk download, adjustable page size, and Radio Reference import.

## Requirements

- Go 1.18+
- Node.js 20 LTS
- GNU Make
- Python 3.10+ (for RR sidecar)

## Build

```bash
./build.sh           # build only (no restart)
./build.sh test      # build + restart test instance (port 3001)
./build.sh prod      # build + restart production (port 3000)
./build.sh both      # build + restart both
```

The Angular webapp is embedded into the Go binary at compile time via `//go:embed`. After any frontend change, both the Angular client and Go binary must be rebuilt.

## Services

| Service | Port | Description |
|---|---|---|
| `faif-scanner-go` | 3000 | Production instance |
| `faif-scanner-test` | 3001 | Test instance |
| `faif-rr-sidecar` | 8200 (localhost) | Radio Reference SOAP API proxy |

## Radio Reference Import (Optional)

Requires a RadioReference API key and Premium subscription.

1. Get API key at https://www.radioreference.com/account/api
2. Add to systemd override:
   ```bash
   sudo systemctl edit faif-scanner-go --force
   ```
   ```ini
   [Service]
   Environment="RR_APP_KEY=your-key-here"
   ```
3. Reload and restart:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart faif-scanner-go
   ```

Or enter a custom key in the admin UI under **Config > RR API Key**.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `RR_APP_KEY` | No | RadioReference API key (fallback for admin UI) |

## Modifications vs upstream rdio-scanner

- Audio stored on disk (`audio/YYYY/MM/DD/`) instead of SQLite blobs
- `audioPath` column in database for file references
- Bulk multi-file zip download (JSZip)
- Adjustable page size (10/25/50/75/100) with localStorage persistence
- Radio Reference API import via admin panel (trunked systems, conventional frequencies, agencies)
- RR API key management via environment variable or admin UI

## License

GNU General Public License v3.0 — see [LICENSE](LICENSE).
