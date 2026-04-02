# goleecher

A high-performance Telegram bot that downloads files from HTTP URLs or magnet links and uploads them directly to Telegram. Built with Go using the [gotd/td](https://github.com/gotd/td) MTProto library and [anacrolix/torrent](https://github.com/anacrolix/torrent) for BitTorrent support.

## Features

- **HTTP download** — download any direct-link file via [gdl](https://github.com/forest6511/gdl) (multi-connection accelerated downloader)
- **Torrent / magnet download** — download via magnet URI using a full BitTorrent client
- **Auto-upload to Telegram** — files are uploaded to the chat where the command was sent
- **Multi-file torrent upload** — torrents containing multiple files are uploaded file-by-file with cumulative progress
- **Optional ZIP archive mode** — add `zip` flag to package multi-file torrent output into one `.zip` before upload
- **Large-file splitting** — files larger than 1.95 GiB are automatically split into parts before upload
- **Parallel upload** — file parts are uploaded to Telegram in parallel (4 workers) for speed
- **Job management** — track active jobs, cancel individual jobs, or cancel all at once
- **Access control** — restrict bot usage to specific Telegram user IDs or open to everyone
- **Web dashboard** — built-in HTTP dashboard at `/` with live job status and stats
- **Graceful shutdown** — responds to SIGINT/SIGTERM, cleanly stops all operations

## Requirements

- Go 1.25+ (matches `go.mod`)
- A Telegram **API ID** and **API Hash** — get them from [my.telegram.org](https://my.telegram.org)
- A Telegram **Bot Token** — create a bot via [@BotFather](https://t.me/BotFather)

## Configuration

All configuration is done via environment variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `API_ID` | ✅ | — | Telegram API ID (integer) |
| `API_HASH` | ✅ | — | Telegram API Hash |
| `BOT_TOKEN` | ✅ | — | Telegram bot token from @BotFather |
| `ADMIN_ID` | ❌ | `0` | Telegram user ID of the bot admin (can use all commands) |
| `ALLOWED_IDS` | ❌ | *(open)* | Comma-separated list of allowed user IDs. If empty and `ADMIN_ID` is `0`, the bot accepts all users |
| `WEB_ENABLED` | ❌ | `false` | Set to `true` to enable the web dashboard HTTP server |
| `PORT` | ❌ | `8080` | Port for the web dashboard (only used when `WEB_ENABLED=true`) |
| `TEMP_DIR` | ❌ | `/tmp/goleecher` | Directory for temporary download files |

## Running

### From source

```bash
git clone https://github.com/dikurungyandere/goleecher.git
cd goleecher

export API_ID=12345678
export API_HASH=your_api_hash_here
export BOT_TOKEN=your_bot_token_here
export ADMIN_ID=your_telegram_user_id   # optional

go run .
```

### Build binary

```bash
go build -o goleecher .
API_ID=... API_HASH=... BOT_TOKEN=... ./goleecher
```

### Docker (example)

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o goleecher .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/goleecher .
ENV PORT=8080
CMD ["./goleecher"]
```

```bash
docker build -t goleecher .
docker run -e API_ID=... -e API_HASH=... -e BOT_TOKEN=... -p 8080:8080 goleecher
```

## Bot Commands

| Command | Description |
|---|---|
| `/start` | Show the welcome message and command list |
| `/leech <url\|magnet>` | Download a file from a URL or magnet link and upload it to Telegram as media |
| `/leech <url\|magnet> document` | Same as above but forces upload as a document file |
| `/leech <url\|magnet> zip` | For multi-file torrents, zip all files into one archive before upload |
| `/leech <url\|magnet> document zip` | Combine both flags: force document upload and zip multi-file torrent output |
| `/leech [document] [zip]` *(as a reply)* | Reply to a Telegram message containing a `.torrent` file to download and upload it |
| `/status` | List all currently active jobs with their progress |
| `/cancel <job_id>` | Cancel a specific job by ID |
| `/cancelall` | Cancel all active jobs (admin only) |

**Examples:**
```
/leech https://example.com/file.zip
/leech magnet:?xt=urn:btih:... document
/leech magnet:?xt=urn:btih:... zip
/leech magnet:?xt=urn:btih:... document zip
/leech document     # when replying to a .torrent file message
/cancel a1b2c3d4
```

## Web Dashboard

The bot starts an HTTP server (default port `8080`) with:

- `GET /` — Dashboard UI with live job table and stats (auto-refreshes every 2 s)
- `GET /api/jobs` — JSON array of all jobs
- `GET /api/stats` — JSON object with active job count, total jobs, total bytes transferred, and uptime

## Project Structure

```
.
├── main.go                        # Entry point: config, store, web server, bot startup
└── internal/
    ├── config/config.go           # Environment variable loading
    ├── store/store.go             # In-memory job store with mutex-safe access
    ├── jobs/manager.go            # Job lifecycle management (create, status updates, cancel)
    ├── bot/
    │   ├── bot.go                 # Telegram client setup and Run loop
    │   ├── archiver.go            # ZIP creation helper for multi-file torrent output
    │   ├── handlers.go            # Update handler, command dispatch, command implementations
    │   └── uploader.go            # Parallel file uploader with large-file splitting support
    ├── downloader/
    │   ├── http.go                # HTTP download via gdl
    │   └── torrent.go             # BitTorrent/magnet download via anacrolix/torrent
    ├── splitter/splitter.go       # Splits files >1.95 GiB into parts for Telegram upload
    └── web/
        ├── server.go              # HTTP dashboard server
        └── static/index.html      # Dashboard frontend (dark-theme, vanilla JS)
```

## Access Control

- If **neither** `ADMIN_ID` nor `ALLOWED_IDS` is set, the bot accepts messages from **all** users.
- If `ADMIN_ID` is set, that user has full access including `/cancelall`.
- If `ALLOWED_IDS` is set (e.g. `ALLOWED_IDS=111,222,333`), only those IDs (plus `ADMIN_ID`) can use the bot.

## Notes

- The session file (`session.json`) is stored in `TEMP_DIR` and persists the bot's MTProto session across restarts.
- Temporary download directories are created per-job under `TEMP_DIR/<job_id>/` and removed after the upload completes or fails.
- For multi-file torrents, default behavior uploads each file individually while preserving relative paths in filenames.
- Add `zip` to `/leech` to upload multi-file torrent results as a single archive (`<torrent_name>.zip`).
- Files larger than 1.95 GiB are split into `.part001`, `.part002`, … parts automatically before upload, since Telegram's maximum file size is 2 GiB.

## License

MIT
