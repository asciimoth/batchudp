# Architecture

## Overview

`batchudp` is a small UDP transport module extracted from `wireguard-go`. Its
main job is to expose a single `Bind` abstraction that:

- opens IPv4 and IPv6 UDP sockets on the same port,
- returns receive functions for each active address family,
- sends one or more datagrams to a parsed `Endpoint`,
- hides platform-specific socket setup, batching, and ancillary data details.

The public contracts live in `conn.go`.
The concrete implementations are:

- `StdNetBind` in `bind_std.go`:
  the default implementation on every non-Windows platform, and also the
  Windows fallback when RIO is unavailable.
- `WinRingBind` in `bind_windows.go`:
  the Windows-specific fast path using Registered I/O.

`NewDefaultBind` selects the implementation:

- `default.go`: non-Windows uses
  `NewStdNetBind`.
- `bind_windows.go`: Windows uses `NewWinRingBind`, which falls back to `StdNetBind` if `winrio`
  initialization fails.

## Main Data Flow

### Open

`Bind.Open` is the entry point that constructs the runtime receive/send path.

For `StdNetBind`:

1. `Open` in `bind_std.go` locks the
   bind and rejects reopening with `ErrBindAlreadyOpen`.
2. It calls `listenNet`, which builds sockets through `listenConfig()`.
3. `listenConfig()` in `controlfns.go`
   runs every function registered in the platform-specific `controlfns*.go`
   files before `bind(2)`.
4. `Open` creates one IPv4 socket and one IPv6 socket on the same port, probes
   UDP offload support via `supportsUDPOffload`, and on Linux/Android wraps each
   socket in `ipv4.PacketConn` or `ipv6.PacketConn` for batch I/O.
5. `Open` returns one receive closure per active family:
   `makeReceiveIPv4` and `makeReceiveIPv6`, both of which call `receiveIP`.

For `WinRingBind`:

1. `Open` in `bind_windows.go` creates IPv4 and IPv6 RIO sockets through `afWinRingBind.Open`.
2. Each per-family bind allocates RX/TX rings, completion queues, and a RIO
   request queue.
3. The bind preposts `packetsPerRing` receive requests for both families.
4. `Open` returns two receive functions, `receiveIPv4` and `receiveIPv6`.

### Receive

For `StdNetBind`, the receive path lives in `receiveIP` in`bind_std.go`:

- Linux and Android use `ReadBatch` through `ipv4.PacketConn` / `ipv6.PacketConn`.
- Other platforms use `UDPConn.ReadMsgUDP` and process one datagram at a time.
- Received control data is parsed by `getSrcFromControl`.
- The returned `Endpoint` is a `StdNetEndpoint`, which holds destination
  address data plus optional cached source control data.

When Linux/Android RX offload is enabled:

- `controlfns_linux.go` attempts to enable `UDP_GRO` at socket creation time.
- `supportsUDPOffload` in
  `features_linux.go` checks whether the socket actually supports `UDP_GRO` and `UDP_SEGMENT`.
- `receiveIP` reads into a reduced number of large buffers, then
  `splitCoalescedMessages` expands a coalesced GRO datagram back into the
  packet-per-buffer API expected by callers.
- `getGSOSize` from `gso_linux.go` extracts the segment size from ancillary
  data. Non-Linux builds use `gso_default.go`, where these helpers are no-ops.

For `WinRingBind`:

- `receiveIPv4` and `receiveIPv6` call `afWinRingBind.Receive`.
- `Receive` drains the completion queue, re-arms the receive request, copies the
  payload into the caller-provided buffer, and returns a `WinRingEndpoint`.
- The Windows fast path is intentionally unbatched at the `Bind` API level:
  `BatchSize()` is `1`.

### Send

For `StdNetBind`, `Send` in `bind_std.go`:

1. snapshots the selected family socket and feature flags under `mu`,
2. converts the destination `StdNetEndpoint` into a pooled `net.UDPAddr`,
3. optionally attaches sticky source control data with `setSrcControl`,
4. sends via `send`, using either `WriteBatch` on Linux/Android or
   `WriteMsgUDP` elsewhere.

On Linux/Android TX offload:

- `coalesceMessages` merges multiple same-destination datagrams into one larger
  payload when size and batch rules allow.
- `setGSOSize` in `gso_linux.go` appends `UDP_SEGMENT` control data that tells
  the kernel how to segment the payload back into packets.
- If a send fails with a kernel error that indicates broken UDP GSO support,
  `Send` disables TX offload for that socket, retries without GSO, and returns
  `ErrUDPGSODisabled` wrapping the retry result.

For `WinRingBind`, `Send` dispatches each buffer individually through
`afWinRingBind.Send`, which writes the payload and destination into the TX ring
and submits a `winrio.SendEx` request.

### Close

For `StdNetBind`, `Close` closes the IPv4 and IPv6 sockets, clears cached
packet-conn wrappers, blackhole flags, and offload state.

For `WinRingBind`, `Close` first flips `isOpen` so receive/send paths start
returning `net.ErrClosed`, wakes any completion-queue waiters, and then tears
down RIO queues, buffers, and sockets.

## Platform-Specific Logic

### Platform split by file

The repository uses Go build tags and `*_os.go` naming to isolate behavior:

- `bind_std.go`: shared `StdNetBind` implementation used everywhere.
- `bind_windows.go`: Windows RIO implementation.
- `controlfns_linux.go`: Linux and Android socket setup before bind.
- `controlfns_unix.go`: non-Windows, non-Linux socket setup.
- `controlfns_windows.go`: Windows socket buffer sizing for the `StdNetBind` fallback.
- `sticky_linux.go`: Linux-only sticky-socket source address capture and replay.
- `sticky_default.go`: no-op sticky behavior for every non-Linux build, including Android.
- `gso_linux.go`: Linux ancillary data helpers for UDP GSO/GRO.
- `gso_default.go`: no-op GSO/GRO helpers elsewhere.
- `features_linux.go`: per-socket UDP offload probing.
- `features_default.go`: offload probing stub for non-Linux builds.
- `mark_unix.go`: packet marking on Linux, Android, FreeBSD, and OpenBSD.
- `mark_default.go`: `SetMark` no-op elsewhere.
- `boundif_android.go`: Android-only socket fd exposure for integration with `wireguard-android`.

### How platform hooks are called

There are three main hook points:

1. Socket creation:
   `StdNetBind.Open -> listenNet -> listenConfig -> controlFns`.
   This is where buffer sizes, `IPV6_V6ONLY`, PKTINFO reception, and `UDP_GRO`
   are configured.
2. Receive path:
   `StdNetBind.receiveIP` calls `getSrcFromControl` and, on Linux/Android,
   `splitCoalescedMessages`.
3. Send path:
   `StdNetBind.Send` calls `setSrcControl` and, on Linux/Android with TX
   offload, `coalesceMessages` plus `setGSOSize`.

Windows RIO bypasses `listenConfig` entirely because it creates sockets and I/O
queues directly in `bind_windows.go`.

## Sticky Sockets

Sticky sockets let a received packet carry enough local addressing information
to send the reply from the same local address/interface later.

On Linux:

- `controlfns_linux.go` enables `IP_PKTINFO` for IPv4 and `IPV6_RECVPKTINFO`
  for IPv6 during socket creation.
- `getSrcFromControl` in `sticky_linux.go` copies the PKTINFO control message
  into `StdNetEndpoint.src` during receive.
- `setSrcControl` writes the cached control message back onto outgoing packets
  during send.
- `StdNetEndpoint.SrcIP`, `SrcIfidx`, and `SrcToString` decode that cached
  control data for callers.

On all other builds, including Android:

- `sticky_default.go` provides stub implementations.
- `StdNetEndpoint` still exists, but its source metadata accessors return zero
  values and outgoing sends do not attach source-selection control data.

## Android-Specific Usage

Android uses the shared `StdNetBind` implementation, but not the full Linux
sticky-socket feature set.

### What is Android-specific here

- `boundif_android.go` adds
  `PeekLookAtSocketFd4` and `PeekLookAtSocketFd6` to `StdNetBind`.
- These methods expose the live UDP socket file descriptors without
  transferring ownership. The fd remains owned by the bind and becomes invalid
  after `Close`.
- The interface is declared as `PeekLookAtSocketFd` in `conn.go` and is intended for
  `wireguard-android`.

Typical Android integration pattern:

1. create the bind with `NewDefaultBind()` or `NewStdNetBind()`,
2. call `Open`,
3. type-assert the bind to `PeekLookAtSocketFd`,
4. fetch the IPv4 and/or IPv6 fd,
5. hand those fds to Android-specific code that needs to inspect or exempt the
   sockets,
6. continue using the bind normally for receive/send.

### Android behavior differences from Linux

- Sticky socket support is intentionally disabled on Android:
  `controlfns_linux.go` skips enabling `IP_PKTINFO` and `IPV6_RECVPKTINFO` when
  `runtime.GOOS == "android"`, and `sticky_default.go` is selected instead of
  `sticky_linux.go`.

## Concurrency Model

The public contract is documented in `conn.go`.
The implementation behavior behind that contract is:

- `StdNetBind.Open` and `StdNetBind.Close` serialize lifecycle changes with
  `mu`.
- `StdNetBind.Send` only holds `mu` long enough to snapshot the current socket
  and flags, so sends can proceed concurrently with each other and with
  receives. A concurrent `Close` may cause an in-flight send to fail because the
  underlying socket was closed, but it should not corrupt bind state.
- `WinRingBind` uses `RWMutex` plus `isOpen` to let send/receive operations race
  safely with `Close`.
- `ReceiveFunc`s are intended to block in dedicated goroutines and to terminate
  with `net.ErrClosed` after `Close`.
- `Endpoint` values are implementation-specific. `StdNetEndpoint` contains
  mutable cached source state (`src`), so callers should not mutate a shared
  endpoint concurrently with `Send`.

## Tests and Support Code

- `bind_std_test.go` focuses on `StdNetBind` batching, GSO/GRO splitting, and close behavior.
- `sticky_linux_test.go` validates Linux sticky control parsing and formatting.
- `conn_test.go` exercises the higher-level bind contract.
- `bindtest/bindtest.go` provides a channel-backed `Bind` implementation used for focused tests.
- `winrio/` contains the Windows RIO
  wrapper used by `WinRingBind`.
