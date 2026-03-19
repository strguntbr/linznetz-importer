# Linz Netz CSV Importer

A "one-shot" CLI tool to import energy consumption data from Linz Netz CSV reports into InfluxDB via UDP. It supports fetching reports from Gmail via IMAP or processing local files.

## Features

- **Automated Imports**: Periodically process unread emails with a specific subject.
- **Interactive Mode**: Select specific emails from your inbox and visualize data with terminal-based diagrams.
- **Local File Support**: Process CSV files directly without an internet connection.
- **InfluxDB Overwriting**: Uses the `status` field to allow re-imports to overwrite existing data.
- **Static Binary**: Statically linked for easy deployment in minimal Docker containers (e.g., Alpine).

## Environment Variables

| Variable | Description | Required | Default |
| :--- | :--- | :---: | :--- |
| `GMAIL_USER` | Gmail email address | Yes (for Mail mode) | - |
| `GMAIL_PASSWORD` | Gmail App Password | Yes (for Mail mode) | - |
| `GMAIL_IMAP_SERVER`| IMAP server address | No | `imap.gmail.com:993` |
| `INFLUX_HOST` | Target host for InfluxDB UDP | Yes (for Influx mode) | - |
| `INFLUX_PORT` | Target port for InfluxDB UDP | Yes (for Influx mode) | - |
| `TIMEZONE` | Timezone for CSV timestamps | No | `Europe/Vienna` |
| `MEASUREMENT_NAME` | InfluxDB measurement name | No | `energy_usage` |

## CLI Parameters

### `--select` (Data Source Selection)
- `unread` (Default): Processes all matching unread emails in the inbox.
- `latest`: Processes only the most recent matching email (regardless of read status). Never marks as read.
- `user`: Interactive mode. Lists all matching emails (unread marked with `(*)`) and prompts for selection. Never marks as read.
- `file:<path>`: Skips Gmail entirely and processes the local file at the specified path.

### `--output` (Result Destination)
- `influx` (Default): Sends data points to InfluxDB via UDP. Marks emails as read *only* if `--select=unread`.
- `debug`: Prints the InfluxDB Line Protocol to `stdout`. Never marks as read.
- `graph`: Displays a terminal-based stacked bar chart of the consumption data (Time on Y-axis). Never marks as read.
- `graphh`: Displays a colorful horizontal stacked bar chart (Time on X-axis, Values on Y-axis) with EEG (Green) at the bottom and Rest (Red) on top. Never marks as read.

## Usage Examples

### Standard Import (Cron-style)
```bash
# Connects to Gmail, processes unread, sends to Influx, marks as read.
./importer --select=unread --output=influx
```

### Debug a Local File
```bash
# Processes a local file and prints the Line Protocol to stdout.
./importer --select=file:AT0031_QH_20260317.csv --output=debug
```

### Interactive Visualization
```bash
# Lists all matching emails, let's you pick one, and shows a diagram.
./importer --select=user --output=graph
```

## Data Transformation Logic

The tool extracts data from the following CSV columns:
1. **Column 2**: `Datum bis` (Timestamp in local timezone).
2. **Column 3**: `total` (Total energy consumption in kWh).
3. **Column 5**: `rest` (Residual energy surplus).
4. **Column 6**: `status` (Meter reading status, e.g., "M" for measured).

**Calculated Fields**:
- `eeg = total - rest` (Energy community usage).

**InfluxDB Tags**:
- `meter_id`: Extracted from the filename (e.g., `AT0031...`).

**Missing Values**:
- If `total` is missing, it is set to `0.0`.
- If `rest` is missing, both `rest` and `eeg` fields are omitted from the data point.
