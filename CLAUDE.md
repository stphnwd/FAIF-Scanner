Create /home/rdio/faif-scanner/CLAUDE.md — project instructions 
for this codebase.

Include:

## FAIF Scanner — Go/Angular Fork of rdio-scanner

This is a fork of chuot/rdio-scanner (v6.6.3) with custom 
modifications. The GitHub repo is https://github.com/stphnwd/FAIF-Scanner

## ⚠️ Build Process — CRITICAL

The Angular webapp is embedded into the Go binary at compile 
time via //go:embed webapp/*. After ANY frontend change, BOTH 
steps are required or the running server serves OLD files:

Step 1 — Build Angular:
cd /home/rdio/faif-scanner/client && npm run build

Step 2 — Rebuild Go binary:
cd /home/rdio/faif-scanner/server && go build -o ../rdio-scanner .

Step 3 — Restart service:
sudo systemctl restart faif-scanner-test   (test, port 3001)
sudo systemctl restart faif-scanner-go     (production, port 3000)

Shortcut: run ./build.sh from /home/rdio/faif-scanner

## Services
- faif-scanner-go — production, port 3000, base_dir /home/rdio/faif-scanner
- faif-scanner-test — test, port 3001, base_dir /home/rdio/faif-scanner-test
- faif-rr-sidecar — Radio Reference SOAP proxy, port 8200 (localhost only)
  runs from /home/rdio/faif-scanner/rr-sidecar/
  uses venv at /home/rdio/faif-scanner/rr-sidecar/venv/

## Directory layout
/home/rdio/faif-scanner/
├── client/          ← Angular frontend source
├── server/          ← Go backend source
│   └── webapp/      ← Angular build output (embedded into binary)
├── rr-sidecar/      ← Python FastAPI SOAP proxy for Radio Reference API
├── audio/           ← Audio files: audio/YYYY/MM/DD/<filename>.m4a
├── rdio-scanner     ← compiled binary (do not commit)
├── rdio-scanner.db  ← production SQLite database (do not commit)
├── build.sh         ← full rebuild script
└── CLAUDE.md        ← this file

## Custom modifications vs upstream
- server/call.go — audio stored on disk, audioPath column, 
  SaveAudioToDisk(), LoadAudioFromDisk()
- server/database.go — migration 20260325000000 adds audioPath column
- server/rr.go — Radio Reference API integration, credential storage,
  proxy endpoints to rr-sidecar
- client/src/.../search component — bulk download (JSZip), 
  adjustable page size (10/25/50/75/100)
- client/src/.../admin component — RR Configuration tab, RR Import tab
- rr-sidecar/main.py — FastAPI SOAP proxy, zeep client, 8 RR endpoints

## Branch strategy
- master — production
- feature/* — new features, test on port 3001 before merging
- fix/* — bug fixes
- style/* — UI/CSS only

## Go build path
Must be run from inside server/ directory:
cd /home/rdio/faif-scanner/server && go build -o ../rdio-scanner .
NOT from the parent directory (go.mod is inside server/)

## Radio Reference sidecar
- Credentials passed as query params on every request, never stored by sidecar
- Credentials stored XOR-obfuscated in rdioScannerConfigs table
- WSDL: http://api.radioreference.com/soap2/?wsdl&v=latest&s=doc
- All SOAP calls use keyword arguments (not positional)
- zeep Settings(strict=False) required — RR uses non-standard namespaces

## RR_APP_KEY environment variable
- The RR API key can be set via the RR_APP_KEY environment variable
  in the systemd service file (Environment="RR_APP_KEY=your-key-here")
- If set, it serves as the default fallback when no custom key is
  stored in the database
- Users can override with a custom key via the admin UI
- "Restore Default API Key" button removes the custom key and
  falls back to the environment variable
- NEVER commit API keys to the repo or store them in source files
- To set: edit /etc/systemd/system/faif-scanner-go.service (or -test),
  set Environment="RR_APP_KEY=your-key", then sudo systemctl daemon-reload
  and restart the service

## Do not commit
- rdio-scanner (binary)
- rdio-scanner.db, rdio-scanner.db-shm, rdio-scanner.db-wal
- audio/
- rr-sidecar/venv/
- extract-*.py, migrate-*.py, populate-*.py (migration scripts)
