# af

A CLI toolkit for AI agents to automate browsers and terminals. One daemon per session manages both subsystems over Unix sockets.

- **Browser**: Chrome automation via CDP — no Playwright or Selenium needed
- **Terminal**: Headless virtual terminals (PTY + midterm VTE) with tmux-style key input
- **Designed for agents**: accessibility snapshots with element refs, JSON output, event history

## Install

```bash
go install github.com/semistrict/agent-foo/cmd/af@latest
```

## Quick Start

```bash
# Browser
af browser open https://example.com
af browser snapshot --interactive     # AI-readable DOM with @ref IDs
af browser click @e3                  # click element by ref
af browser eval "_REF.e3.textContent" # JS with ref expansion

# Terminal
af run npm test                       # run command, stream output
af run --label server npm start       # background with label
af term input --label server C-c      # send Ctrl+C
af term snapshot --label server       # read screen
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--json` | JSON output |
| `--session NAME` | Session name (default: hash of nearest `.git` root, or `default`) |

## Browser (`af browser`)

### Flags

| Flag | Description |
|------|-------------|
| `--headless` | No window |
| `--cdp URL` | Existing CDP endpoint |
| `--profile PATH` | Persistent browser profile |
| `--timeout SECS` | Command timeout (default: 5) |

### Commands

| Command | Description |
|---------|-------------|
| `open <url>` | Navigate to URL |
| `back` | Go back |
| `forward` | Go forward |
| `reload` | Reload page |
| `click <selector>` | Click element (CSS selector or `@ref`) |
| `type <selector> <text>` | Append text to element |
| `fill <selector> <text>` | Clear and type text |
| `press <key>` | Press key (`Enter`, `Tab`, `Control+a`, etc.) |
| `upload <selector> <files...>` | Upload files to input |
| `snapshot` | Accessibility tree with `@ref` IDs |
| `eval <js>` | Run JavaScript (`_REF.e1` expands to element) |
| `wait <selector>` | Wait for element to appear |
| `screenshot [path]` | Save PNG (`--full` for full page) |
| `pdf <path>` | Save page as PDF |
| `events` | Query CDP event buffer |
| `set viewport <w> <h>` | Set viewport size |
| `set media [dark\|light] [reduced-motion]` | Set media preferences |
| `session [list]` | Show/list sessions |
| `close` | Close browser |

#### Snapshot Flags

| Flag | Description |
|------|-------------|
| `-i, --interactive` | Only interactive elements |
| `-c, --compact` | Remove empty structural elements |
| `-d, --depth N` | Limit tree depth |

#### Events Flags

| Flag | Description |
|------|-------------|
| `--category CAT` | Filter: `console`, `network`, `page`, `target` |
| `--type TYPE` | Filter by event type |
| `--last N` | Last N events |
| `--since TIME` | Events after RFC3339 time |
| `clear` | Clear buffer |
| `stats` | Buffer statistics |

## Terminal

### `af run <command> [args...]`

Run a command in a virtual terminal with output streaming.

| Flag | Description |
|------|-------------|
| `--label NAME` | Instance label (default: command name) |
| `--rows N` | Terminal height (default: 24) |
| `--cols N` | Terminal width (default: 80) |
| `--timeout SECS` | Idle timeout (default: 30) |

Streams output incrementally. Ctrl+C detaches without killing the process. With `--json`, blocks until exit.

### `af term` Subcommands

| Command | Description |
|---------|-------------|
| `snapshot` | Read terminal screen (`--text` for plain, `-s` for scrollback) |
| `input <keys...>` | Send tmux-style keys (see below) |
| `sendraw <text>` | Send literal text (no key translation) |
| `resize <rows> <cols>` | Resize terminal |
| `kill [label]` | Kill instance (`--all` for all) |
| `list` | List running instances |
| `close` | Kill all and stop |

All subcommands accept `--label NAME` to target a specific instance.

`input` accepts `--timeout SECS` (default: 2) to wait for output after sending keys.

### Key Names

Tmux-style key names for `af term input`:

| Key | Name(s) |
|-----|---------|
| Arrow keys | `Up`, `Down`, `Left`, `Right` |
| Enter | `Enter` |
| Tab | `Tab` |
| Escape | `Escape` |
| Backspace | `BSpace` |
| Delete | `DC`, `Delete` |
| Insert | `IC`, `Insert` |
| Home/End | `Home`, `End` |
| Page Up/Down | `PPage`/`NPage`, `PageUp`/`PageDown` |
| Function keys | `F1`-`F12` |
| Space | `Space` |
| Ctrl | `C-x` or `^x` |
| Alt/Meta | `M-x` |
| Shift | `S-x` |

Modifiers combine: `C-M-a` for Ctrl+Alt+A, `S-Up` for Shift+Up.

Plain text is sent as-is: `af term input hello Enter` types "hello" then presses Enter.

## Session Management

```bash
af status                  # show all active sessions
af stop                    # stop all sessions
af stop --session NAME     # stop specific session
```

## Architecture

- One daemon per session, auto-launched on first use
- Communicates over Unix sockets at `~/.af/<session>.sock`
- Requests routed to browser or terminal handler by `subsystem` field
- `af stop` sends SIGTERM, waits 10s, then SIGKILL if needed
- Terminal output preserves ANSI colors
