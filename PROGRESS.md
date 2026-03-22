# Linz Netz Importer - Project Progress (March 21, 2026)

## Completed Tasks
### 1. State Persistence
- **State Struct**: Tracks `LatestFinal` and `LatestIntermediate` for each meter using the full `AT...` ID as the key in a `Meters` map.
- **JSON Storage**: Persisted in `state.json` (configurable via `STATE_FILE_PATH`).
- **Logic**: 
    - `LatestFinal`: Updated only for "M" (valid/final) status data points.
    - `LatestIntermediate`: Updated for all data points (newest data received).

### 2. Web Portal Automation (`runWebMode`)
- **Skip Logic**: 
    - Automatically skips download if data is already up to date based on the time of day.
    - Before 12:00 CET: Skips if `LatestIntermediate` is from yesterday or newer (meaning we have data for the day before yesterday).
    - After 12:00 CET: Skips if `LatestIntermediate` is from today or newer (meaning we have data for yesterday).
    - Bypassed if `--force` is used or if the state file is missing.
- **Dynamic Meter Discovery**: ...

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

### 5. Modular Architecture Refactoring
- **Package Structure**: Refactored the monolith `main.go` into specialized internal packages:
    - `internal/config`: Configuration, TTY detection, and user prompting.
    - `internal/exporter`: Decoupled output sinks (InfluxDB, Debug, Diagrams) via the `Exporter` interface.
    - `internal/models`: Shared data structures.
    - `internal/parser`: Source-agnostic CSV parsing.
    - `internal/source`: Data acquisition logic for Web, Mail, and File sources.
    - `internal/state`: Persistent state management.
    - `internal/util`: Helper functions and regex utilities.
- **Maintainability**: Improved code readability and separation of concerns.
- **Testability**: Updated test suite to verify logic across the new modular structure.
    - Added unit tests for `ShouldSkipDownload` logic in `internal/source`.
    - Added unit tests for state updates in `internal/exporter`.
    - Added unit tests for state persistence in `internal/state`.

### 6. Docker & Containerization
- **Base Image**: Switched to `zenika/alpine-chrome:latest`, which includes Chromium and CA certificates pre-installed.
- **Security**: The container now runs as the non-privileged `chrome` user.
- **Execution Model**: Replaced complex `crond` setup with a simple shell loop (`while true`) for periodic imports.
- **Persistence**: Implemented Docker volumes (`./data:/data`) to persist `state.json` across container restarts.
- **Stability**: Added `disable-dev-shm-usage` flag to `chromedp` to prevent crashes in container environments.

### 7. Tooling & CLI
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
