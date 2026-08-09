# notifications — go-freedesktop

[![ci](https://github.com/go-freedesktop/notifications/actions/workflows/ci.yml/badge.svg)](https://github.com/go-freedesktop/notifications/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-freedesktop/notifications.svg)](https://pkg.go.dev/github.com/go-freedesktop/notifications)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

A pure-Go (`CGO_ENABLED=0`) implementation of the freedesktop.org [Desktop
Notifications Specification](https://specifications.freedesktop.org/notification-spec/latest/):
the D-Bus service `org.freedesktop.Notifications` that desktop applications
call — through `notify-send`, libnotify, `GLib.Notification`, … — to post
notification bubbles.

It is the daemon side. Point `notify-send` at it and it decodes the `Notify`
call, renders the notification as a [go-widgets](https://github.com/go-widgets/toolkit)
`Toast`, and emits the `NotificationClosed` / `ActionInvoked` signals a client
waits on.

## What it does

- **Core** (`notifications`) — a platform-neutral model of a `Notification`,
  a defensive decoder for the hint dictionary (`urgency`, `category`,
  `resident`, `transient`, `desktop-entry`, `image-path`) and the inline image
  payload (`image-data` `(iiibiiay)` → `*image.RGBA`, 3- and 4-channel with
  rowstride), and the spec constants + truthful capability list.
- **Server** (`server_linux.go`) — exports the four methods
  (`Notify`, `CloseNotification`, `GetCapabilities`, `GetServerInformation`)
  and emits the two signals over the owned pure-Go
  [`github.com/go-freedesktop/dbus`](https://github.com/go-freedesktop/dbus),
  forwarding decoded requests to a `Handler`. `Export` claims the well-known
  name (never queueing, and permitting later replacement); `ExportReplace`
  additionally asks the bus to hand the name over from a running daemon. A
  non-Linux stub keeps the package cross-compiling.
- **Toast bridge** (`notifications/toast`) — a pure `ToToast` mapping a
  `Notification` onto a go-widgets `Toast` (summary + body → lines, urgency →
  kind, timeout / resident → life, actions → buttons that emit
  `ActionInvoked`, inline image / `image-path` / `app_icon` → icon), plus a
  small platform-neutral `Daemon` that stacks toasts, ticks their lifetimes
  and drives the close signals with the right reason (expiry / dismissal /
  close-call).
- **`cmd/notifyd`** — a production-ready reference daemon wiring it all
  together: clean startup (fails clearly if another notification daemon owns
  the name, unless `-replace` is given), graceful `SIGINT`/`SIGTERM` shutdown
  (release the name, close the connection), per-notification panic isolation,
  a bounded in-memory store, and flags for the default timeout and log
  verbosity.

## Advertised capabilities

`GetCapabilities` reports only what the bridge actually renders:

```
body   actions   body-markup   icon-static   persistence
```

`GetServerInformation` reports `("go-widgets-notifyd", "go-freedesktop", <version>, "1.2")`.

## Quickstart

```go
conn, _ := dbus.ConnectSessionBus()
daemon := toast.NewDaemon(nil, toolkit.DefaultDark(), myIconLookup)
server := notifications.NewServer(conn, daemon)
daemon.SetEmitter(server)
if err := server.Export(); err != nil { // claims org.freedesktop.Notifications
    log.Fatal(err)
}
// ... call daemon.Tick() from your render loop and draw daemon.Toasts().
```

## Running the reference daemon

`cmd/notifyd` is the reference daemon. On a Linux desktop with a session bus:

```sh
go build -o notifyd ./cmd/notifyd   # CGO_ENABLED=0, static
./notifyd -verbose
# elsewhere:
notify-send -u critical "Build failed" "3 tests red" -A reply=Reply
```

Flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-replace` | off | take `org.freedesktop.Notifications` over from a running daemon (works when the incumbent allowed replacement, as this daemon does) |
| `-timeout MS` | `5000` | default notification lifetime, used when a client requests the server default (`expire_timeout == -1`) |
| `-verbose` | off | log every notification received |
| `-version` | off | print version and exit |

Lifecycle:

- **Startup** — claims the well-known name without queueing. If another
  notification daemon already owns it, notifyd prints an informative message and
  exits `3` (re-run with `-replace` to take over).
- **Shutdown** — on `SIGINT`/`SIGTERM` it releases the name and closes the
  connection so a replacement daemon can start cleanly (context-driven).
- **Robustness** — a malformed or panicking `Notify` is isolated
  (logged and dropped, never crashing the daemon); the in-memory store is
  bounded (`toast.DefaultCap`), retiring the oldest notification when full.

Exit codes: `0` clean, `1` unexpected failure (bus connect / export), `2` bad
flags, `3` the name is already owned.

### How a notification maps to a Toast

The `./toast` bridge maps each field per the specification: summary + body →
the Toast's stacked lines (body markup stripped to plain text); urgency →
kind (critical → error pill, otherwise info); timeout / resident / critical →
lifetime (sticky when it should persist); each non-`default` action → a
right-edge button that emits `ActionInvoked` on click; inline `image-data`,
then `image-path`, then `app_icon` (resolved through the icon theme) → the
leading icon. Rendering the resulting pixel surface to the screen is the host
compositor's job — go-widgets is a pure pixel-blitting toolkit — so notifyd
renders each frame to an offscreen buffer to exercise the draw path end to end.

### systemd / D-Bus activation

`dist/` ships a D-Bus-activatable service file and a systemd **user** unit so
the daemon can start on demand the first time an app posts a notification:

```sh
install -Dm644 dist/org.freedesktop.Notifications.service \
  ~/.local/share/dbus-1/services/org.freedesktop.Notifications.service
install -Dm644 dist/notifyd.service \
  ~/.config/systemd/user/notifyd.service
install -Dm755 notifyd ~/.local/bin/notifyd   # match Exec= / ExecStart= paths
systemctl --user daemon-reload
```

Adjust the `Exec=` / `ExecStart=` paths to wherever `notifyd` is installed. With
both files in place the session bus starts `notifyd` lazily on the first
`Notify` call (`Type=dbus`, `BusName=org.freedesktop.Notifications`); for an
always-on daemon instead run `systemctl --user enable --now notifyd.service`.

## Transport

Built on the owned pure-Go
[`github.com/go-freedesktop/dbus`](https://github.com/go-freedesktop/dbus)
(`CGO_ENABLED=0` on every target). No D-Bus wire codec is reimplemented and
nothing shells out to a CLI. The server test suite exercises real
`susssasa{sv}i` marshalling over an in-memory `net.Pipe` peer-to-peer
connection, so it needs no running `dbus-daemon`.

## Scope

In scope: the four `org.freedesktop.Notifications` methods, both signals, the
hint and image-data decoding the bridge renders, and corner-anchored stacking.
Out of scope: sound (`sound-file` / `sound-name`), action icons, animated
(`icon-multi`) images, and SVG rasterisation of `image-path` values (a `.svg`
path is reported as a vector icon for the caller to resolve through an
icon-theme raster instead) — none of which the Toast surface renders, so none
are advertised.

## Relationship to wasmdesk / wasmbox

This library is the native-desktop counterpart of the in-browser notification
surface in [wasmdesk](https://github.com/wasmdesk)'s wasmbox compositor (whose
`compositor/*_notifications.rb` modules render the same notification model in a
WASM desktop). The two share the freedesktop notification vocabulary — summary,
body, urgency, actions, resident/transient — so a notification means the same
thing whether it lands on a go-widgets desktop or in the browser compositor.

## Tests & coverage

`CGO_ENABLED=0 go test ./...` — **100% statement coverage**, including every
error branch. The server suite exercises real `susssasa{sv}i` marshalling over
an in-memory `net.Pipe` peer, so it needs no running `dbus-daemon`. CI
additionally cross-builds on the six supported 64-bit targets
(amd64/arm64 natively, riscv64/loong64/ppc64le/s390x under qemu-user).

## License

BSD-3-Clause. See [LICENSE](LICENSE). Copyright the go-freedesktop/notifications
authors.
