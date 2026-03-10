# af

A CLI toolkit for AI agents to automate browsers, terminals, debuggers, and local servers. One daemon per session manages all subsystems over Unix sockets.

- **Browser**: Chrome automation via CDP — no Playwright or Selenium needed
- **Terminal**: Headless virtual terminals (PTY + midterm VTE) with tmux-style key input
- **Debug**: DAP-based debugging (Go via delve, etc.) and CDP JS debugging in browser sessions
- **Serve**: Local HTTP server for files/directories, managed by the daemon
- **Designed for agents**: accessibility snapshots with element refs, text wireframes, JSON output, event history

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
af browser render                     # text wireframe of the page
af browser tabs                       # list open tabs
af browser eval "_REF.e3.textContent" # JS with ref expansion

# Terminal
af run npm test                       # run command, stream output
af run --label server npm start       # background with label
af term input --label server C-c      # send Ctrl+C
af term snapshot --label server       # read screen

# Debug (DAP)
af debug launch --adapter "dlv dap" --program ./cmd/myapp
af debug breakpoint main.go 42
af debug continue

# Debug (browser JS)
af browser debug breakpoint 15 --url app.js
af browser debug continue
af browser debug eval "myVar"

# Serve
af serve .                            # serve current dir, prints URL
af serve index.html http://localhost:3000  # specific port
af serve list                         # list running servers
af serve stop myfile.html             # stop a server
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
| `open <url>` | Open URL in a new tab |
| `back` | Go back |
| `forward` | Go forward |
| `reload` | Reload page |
| `click <selector>` | Click element (CSS selector or `@ref`) |
| `type <selector> <text>` | Append text to element |
| `fill <selector> <text>` | Clear and type text |
| `press <key>` | Press key (`Enter`, `Tab`, `Control+a`, etc.) |
| `upload <selector> <files...>` | Upload files to input |
| `snapshot` | Accessibility tree with `@ref` IDs |
| `render` | Text wireframe of page layout with `@ref` labels |
| `eval <js>` | Run JavaScript (`_REF.e1` expands to element) |
| `wait <selector>` | Wait for element to appear |
| `screenshot [path]` | Save PNG (`--full` for full page) |
| `pdf <path>` | Save page as PDF |
| `events` | Query CDP event buffer |
| `tabs` | List open tabs |
| `tab <index>` | Switch to tab by index |
| `set viewport <w> <h>` | Set viewport size |
| `set media [dark\|light] [reduced-motion]` | Set media preferences |
| `session [list]` | Show/list sessions |
| `close` | Close browser |

`click`, `type`, `fill`, and `press` automatically detect and report page navigations.

Each `open` creates a new tab. Use `tabs` to list them and `tab <index>` to switch.

#### Snapshot Flags

| Flag | Description |
|------|-------------|
| `-i, --interactive` | Only interactive elements |
| `-c, --compact` | Remove empty structural elements |
| `-d, --depth N` | Limit tree depth |
| `-a, --all-tabs` | Snapshot all open tabs |

#### Render Flags

| Flag | Description |
|------|-------------|
| `-W, --width N` | Grid width in columns (default: terminal width) |
| `-H, --height N` | Grid height in rows (default: terminal height) |

#### Events Flags

| Flag | Description |
|------|-------------|
| `--category CAT` | Filter: `console`, `network`, `page`, `target` |
| `--type TYPE` | Filter by event type |
| `--last N` | Last N events |
| `--since TIME` | Events after RFC3339 time |
| `clear` | Clear buffer |
| `stats` | Buffer statistics |

### Browser JS Debugger (`af browser debug`)

Debug JavaScript in browser sessions using the CDP Debugger domain.

| Command | Description |
|---------|-------------|
| `breakpoint <line>` | Set breakpoint (`--url` regex, `--condition` expr) |
| `remove <id>` | Remove breakpoint by ID |
| `continue` | Resume execution |
| `next` | Step over |
| `stepin` | Step into |
| `stepout` | Step out |
| `pause` | Pause execution |
| `stacktrace` | Show call stack |
| `scopes [frame]` | Show scopes and variables |
| `eval <expr>` | Evaluate in paused frame (`--frame N`) |
| `exceptions [none\|uncaught\|all]` | Configure pause on exceptions |

When paused, `continue`/`next`/`stepin`/`stepout` print the top stack frames.

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

## Debug (`af debug`)

DAP-based debugging sessions. Supports any debug adapter that speaks the Debug Adapter Protocol (e.g., `dlv dap` for Go).

### Commands

| Command | Description |
|---------|-------------|
| `launch` | Launch a debug adapter and start debugging |
| `attach` | Attach to a running debug adapter |
| `breakpoint <file> <line[,line...]>` | Set breakpoints (`--condition` expr) |
| `continue` | Continue execution |
| `next` | Step over |
| `stepin` | Step into function |
| `stepout` | Step out of function |
| `pause` | Pause execution |
| `stacktrace` | Show call stack (`--thread`, `--levels`) |
| `scopes [frameId]` | Show scopes for a stack frame |
| `variables <ref>` | Show variables for a scope reference |
| `eval <expression>` | Evaluate expression (`--frame` for context) |
| `threads` | List threads |
| `events` | Show debug adapter events (`--clear`) |
| `disconnect` | Disconnect (`--no-terminate` to keep debuggee) |
| `list` | List debug sessions |

### Launch Flags

| Flag | Description |
|------|-------------|
| `--label NAME` | Debug session label |
| `--adapter CMD` | Debug adapter command (e.g., `dlv dap`) |
| `--port PORT` | Adapter TCP port |
| `--program PATH` | Program to debug |
| `--cwd DIR` | Working directory |
| `--args JSON` | Program arguments (JSON array) |
| `--stop-on-entry` | Stop on entry point |
| `--launch-args JSON` | Additional launch arguments (JSON object) |

When `continue`/`next`/`stepin`/`stepout` stops at a breakpoint, a brief stack trace is printed.

## Serve (`af serve`)

Start a local HTTP server managed by the daemon. Returns immediately with the URL.

```bash
af serve <file|dir> [url]
```

| Flag | Description |
|------|-------------|
| `--label NAME` | Server label (default: filename) |

The optional URL argument sets the port (e.g., `http://localhost:3000`). Default: random available port.

Paths are resolved client-side (`~` expands to home directory).

| Subcommand | Description |
|------------|-------------|
| `af serve list` | List running servers |
| `af serve stop <label>` | Stop a server |

## Session Management

```bash
af status                  # show all active sessions
af stop                    # stop all sessions
af stop --session NAME     # stop specific session
```

## Architecture

- One daemon per session, auto-launched on first use
- Communicates over Unix sockets at `~/.af/<session>.sock`
- Requests routed to subsystem handler by `subsystem` field (browser, term, debug, serve)
- `af stop` sends SIGTERM, waits 10s, then SIGKILL if needed
- Terminal output preserves ANSI colors
- Session name defaults to a hash of the nearest git root, so all commands from the same repo share a daemon
