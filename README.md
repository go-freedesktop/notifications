# notifications

[![CI](https://github.com/go-freedesktop/notifications/actions/workflows/ci.yml/badge.svg)](https://github.com/go-freedesktop/notifications/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-freedesktop/notifications.svg)](https://pkg.go.dev/github.com/go-freedesktop/notifications)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-freedesktop/notifications)](https://goreportcard.com/report/github.com/go-freedesktop/notifications)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

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
  and emits the two signals over
  [`github.com/godbus/dbus/v5`](https://github.com/godbus/dbus), forwarding
  decoded requests to a `Handler`. A non-Linux stub keeps the package
  cross-compiling.
- **Toast bridge** (`notifications/toast`) — a pure `ToToast` mapping a
  `Notification` onto a go-widgets `Toast` (summary + body → lines, urgency →
  kind, timeout / resident → life, actions → buttons that emit
  `ActionInvoked`, inline image / `image-path` / `app_icon` → icon), plus a
  small platform-neutral `Daemon` that stacks toasts, ticks their lifetimes
  and drives the close signals with the right reason (expiry / dismissal /
  close-call).
- **`cmd/notifyd`** — a reference go-widgets daemon wiring it all together.

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

Or just run the reference daemon:

```
go run ./cmd/notifyd
# elsewhere:
notify-send -u critical "Build failed" "3 tests red" -A reply=Reply
```

## Transport

Built on `github.com/godbus/dbus/v5` (pinned to `v5.2.2`, CGO-free on
linux/darwin). No D-Bus wire codec is reimplemented and nothing shells out to a
CLI. The server test suite exercises real `susssasa{sv}i` marshalling over an
in-memory `net.Pipe` peer-to-peer connection, so it needs no running
`dbus-daemon`.

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

## License

BSD-3-Clause. See [LICENSE](LICENSE). Copyright the go-freedesktop/notifications
authors.
