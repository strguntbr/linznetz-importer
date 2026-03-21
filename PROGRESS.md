# Linz Netz Importer - Project Progress (March 21, 2026)

## Completed Tasks

### 1. State Persistence
- **State Struct**: Tracks `LatestDates` for each meter using the full `AT...` ID as the key.
- **JSON Storage**: Persisted in `state.json` (configurable via `STATE_FILE_PATH`).
- **Logic**: Updated only for "M" (valid) status data points during InfluxDB or Debug output.

### 2. Web Portal Automation (`runWebMode`)
- **Dynamic Meter Discovery**: Automatically iterates through all meters found in the portal by correlating radio buttons with `AT...` IDs in the same row. Hardcoded mappings removed.
- **Robust Downloads**:
  - Overrides `_blank` targets to `_self` via monkey-patching and direct JavaScript execution (`mojarra.cljs`).
  - Event-driven tracking using `chromedp.ListenTarget` to confirm download completion.
  - Fallback directory polling for maximum reliability.
- **Filtering**: Supports the new `--meter <AT...>` flag to process a specific meter.
- **Flexible Start Date**:
  - Hierarchy: `state.json` -> `START_<MeterID>` (env) -> `START_DATE` (env) -> Yesterday.
  - Correctly re-imports the last persisted day to ensure completeness of partial days.

### 3. Unified Output Handling (`handleOutput`)
- **Consistency**: Both `influx` and `debug` modes now share identical line protocol generation and state calculation logic.
- **Ingestion**: 
  - `influx`: Sends data via UDP and updates the state file.
  - `debug`: Prints data lines and simulated state updates to stdout.
- **Meter ID Handling**: Internal logic uses full `AT...` IDs; external output (Influx/Debug) uses shortened IDs via `shortenMeterID`.

### 4. Interactive Features & Safety
- **TTY Detection**: Uses `golang.org/x/term` to detect if the importer is running in an interactive terminal.
- **Safety Check**: Prevents `--select=user` from running in non-interactive environments (e.g., cron jobs, pipes).
- **Dynamic Configuration**: In interactive mode, the importer prompts for missing required environment variables (e.g., credentials, InfluxDB host) instead of failing immediately.
- **Secure Input**: Passwords are read without echoing to the terminal for privacy.

### 5. Tooling & CLI
- **New Flags**:
  - `--debugBrowserPort`: Connects to an existing browser session.
  - `--meter`: Filters processing to a specific meter ID (works for Web and Mail modes).
- **Cleanup**: Diagnostic scripts and temporary files have been removed.

## Configuration Requirements

### Environment Variables
- `LINZNETZ_USER` / `LINZNETZ_PASSWORD`: Required for standalone web login.
- `START_DATE` / `START_<MeterID>`: Optional date overrides (`YYYY-MM-DD` or `DD.MM.YYYY`).
- `STATE_FILE_PATH`: Path to state JSON.
- `INFLUX_HOST` / `INFLUX_PORT`: Required for InfluxDB output.
- `TIMEZONE`: Defaults to `Europe/Vienna`.

### CLI Flags
- `--select=missing`: Triggers the Web Portal import.
- `--output=debug`: Displays line protocol and state simulation without persisting.
- `--meter <AT...>`: Limits import to a specific meter.
- `--debugBrowserPort <port>`: Attaches to a running browser for debugging.

## Next Steps
- **Production Run**: Monitor scheduled standalone runs for any edge cases.
- **Failure Analysis**: If needed, implement automated screenshots on failure for headless mode.
