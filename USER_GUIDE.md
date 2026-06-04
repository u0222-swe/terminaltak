# TerminalTAK — user guide

This guide covers day-to-day operation of the TerminalTAK terminal client. For an architectural overview, see [README.md](README.md).

## Contents

1. [First run](#first-run)
2. [Position editor](#position-editor)
3. [Main view](#main-view)
4. [Key bindings](#key-bindings)
5. [Channels and filtering](#channels-and-filtering)
6. [Chat](#chat)
7. [CoT log overlay](#cot-log-overlay)
8. [Files on disk](#files-on-disk)
9. [Command-line flags](#command-line-flags)
10. [Troubleshooting](#troubleshooting)
11. [TAK Server-side requirements](#tak-server-side-requirements)

---

## First run

```sh
./terminaltak
```

If `~/.config/terminaltak/cert.pem` is missing or expired, the binary opens the **enrolment** view. There are two methods, switched with `←` / `→` on the top row:

### Method A — enrol against the server (default)

Fill in:

| Field        | Value                                                                |
|--------------|----------------------------------------------------------------------|
| server host  | hostname/IP of the TAK Server                                        |
| enrol port   | usually `8446`                                                       |
| username     | LDAP / file-auth username (becomes the cert CN)                      |
| password     | LDAP / file-auth password                                            |
| insecure TLS | toggle with `Space`. Use only against self-signed labs               |

Press `Enter` to enrol. TerminalTAK fetches the server's RDN template, generates an RSA-2048 key, sends a CSR to `/Marti/api/tls/signClient/v2?clientUid=<uuid>&version=1`, and writes `cert.pem`, `key.pem`, `ca.pem` into `~/.config/terminaltak/`.

The `version=1` query asks the server to embed the `1.2.840.113549.1.9.7` extended-key-usage OID (TAK calls it the "channels OID") in your client cert. Whether that OID is **required** depends entirely on the server's CoreConfig — see [TAK Server-side requirements](#tak-server-side-requirements) for the full matrix. Some deployments need it for the channel toggle to filter server-side; others ignore it. Asking for it never hurts, so TerminalTAK does so by default.

### Method B — import a `.p12`

If you already have a PKCS#12 bundle issued by the server admin:

| Field         | Value                              |
|---------------|------------------------------------|
| `.p12` path   | absolute path to the `.p12` / `.pfx` file |
| `.p12` password | password the bundle was created with |

Press `Enter` to import. TerminalTAK extracts leaf cert, CA chain and private key into PEMs in the config dir.

> **Note:** the `.p12` flow does not collect server host / port — they default to whatever is already in `config.yaml`. If this is a brand-new install, set `server.host` and `server.stream_port` in `config.yaml` manually before relaunching.

---

## Position editor

Opened automatically on first run, and reachable any time later with **`p`** from the main view.

```
TerminalTAK — your position

  Tab/↑↓ move · Enter saves · F2 random Sweden · Esc quit
  ←/→ on the format row toggles between decimal and MGRS.

▸ (•) Decimal lat/lon    ( ) MGRS   ← →

  latitude        : 59.33
  longitude       : 18.07
  altitude (m)    :
  callsign        : TT-01
  team color      : Cyan
  role            : Team Member
  PLI interval (s): 30
  [ ] random walk in Sweden — re-randomize position each PLI tick
```

| Field            | Notes                                                                 |
|------------------|----------------------------------------------------------------------|
| coord format     | decimal or MGRS, switched with `←` / `→` on the top row              |
| latitude/longitude (decimal mode) | accepts `.` or `,` as decimal separator           |
| MGRS grid (MGRS mode) | accepts `33VXK82001500` or `33V XK 8200 1500`                  |
| altitude (m)     | optional; HAE (height above ellipsoid)                               |
| callsign         | required — what other clients display next to your icon              |
| team color       | ATAK display attribute (Cyan, Red, Blue, …). Not an access channel    |
| role             | ATAK display attribute (Team Member, HQ, RTO, Sniper, …)             |
| PLI interval (s) | 5–300                                                                 |
| random walk in Sweden | when checked, the PLI publisher picks a fresh random Swedish point on every tick. Useful for end-to-end testing without nudging the position by hand. Toggle with `Space` while focused on the row |

Shortcuts:

- **`F2`** — fill the active coord field(s) with a uniformly random point inside Sweden's bounding box (one-shot — for "always moving", use the random-walk checkbox instead)
- **`Enter`** on any text field — save and return to main view
- **`Enter`** on the random-walk row — toggle and stay in the editor
- **`Esc`** — quit the program

The PLI publisher picks up new lat/lon / callsign / interval immediately — no reconnect is needed.

---

## Main view

```
┌ TerminalTAK — takserver.example.com — TT-01 ─────────────────────────────────┐
│                                                                              │
│                      [ Braille world map with contact markers ]              │
│                                                                              │
├─────────────────────┬─────────────────────────────┬──────────────────────────┤
│ channels            │ contacts 1-3/8              │ chat                     │
│ ▸[x] tac_common (IN/OUT) │  ● ALPHA-01    59.33, 18.07 │ [12:03] ALPHA: rolling out │
│  [ ] tac_alpha  (OUT)    │  ● BRAVO-02    59.34, 18.04 │ [12:04] TT-01: copy        │
│  [x] tac_bravo  (IN/OUT) │ ▸● HQ          59.33, 18.06 │                          │
└─────────────────────┴─────────────────────────────┴──────────────────────────┘
```

Three panes cycle with `Tab` / `Shift+Tab`:

- **Channels** — observed access channels. The cursor row is highlighted; `Space` toggles, `a` enables all, `n` disables all
- **Contacts** — observed peers, sorted case-insensitively by callsign. Fixed height: when there are more contacts than fit, the list scrolls with the cursor (a `n-m/total` indicator appears in the header). `i` opens a DM to the highlighted contact. The selected contact gets an `X` marker on the map — drawn on top of any overlapping contacts — and the viewport recentres on it. Tab away from the Contacts pane and the map snaps back to auto-fit
- **Chat** — combined All-Chat + DM history for the selected conversation

Pressing `+` / `-` zooms the map in or out around the current centre (the selected contact if any, otherwise the auto-fit centre). `0` resets to auto-fit. Zoom is preserved across pane switches.

The status row at the bottom shows connection state (`connected`, `reconnecting`, `disconnected`) and last error, if any. On startup an ASCII shield logo splash is shown for 2 seconds before the TUI takes over, provided the terminal is at least 81 columns wide.

---

## Key bindings

### Main view

| Key       | Action                                                                |
|-----------|-----------------------------------------------------------------------|
| `q` / `Ctrl+C` | quit                                                             |
| `Tab` / `Shift+Tab` | cycle pane focus (Channels → Contacts → Chat)               |
| `↑` / `k` | move cursor up in focused pane                                        |
| `↓` / `j` | move cursor down in focused pane                                      |
| `Space`   | toggle highlighted channel (Channels pane only)                       |
| `a`       | enable all channels                                                   |
| `n`       | disable all channels                                                  |
| `i`       | open chat input. From Channels/Chat pane → All-Chat. From Contacts pane → DM to highlighted contact |
| `p`       | open position editor                                                  |
| `m`       | drop a marker (point dropper — aim a crosshair, then fill in details) |
| `M`       | open the markers overlay (list / delete your placed markers)          |
| `l`       | open CoT log overlay                                                  |
| `r`       | reconnect (placeholder)                                               |
| `+` / `=` | zoom map in (centred on selected contact, or auto-fit centre)         |
| `-`       | zoom map out                                                          |
| `0`       | reset zoom to auto-fit                                                |

### Chat input

| Key        | Action                              |
|------------|-------------------------------------|
| `Enter`    | send                                |
| `Esc`      | cancel without sending              |

### CoT log overlay

| Key          | Action                                |
|--------------|---------------------------------------|
| `Esc` / `q`  | close overlay                         |
| `↑` / `↓`    | scroll                                |

### Point dropper (aim)

Press `m` in the main view to drop a marker. A `+` crosshair appears on the
map and the status bar turns yellow with a live `lat, lon` readout.

| Key                 | Action                                              |
|---------------------|-----------------------------------------------------|
| `←` `↑` `↓` `→` / `hjkl` | move the crosshair (one map cell per press)     |
| `+` / `-`           | zoom the map in / out while aiming                  |
| `Enter`             | accept the position and open the marker detail form |
| `Esc`               | cancel                                              |

### Point dropper (detail form)

| Key                 | Action                                              |
|---------------------|-----------------------------------------------------|
| `Tab` / `↑` / `↓`   | move between rows                                   |
| `←` / `→` on the affiliation row | cycle Friendly / Hostile / Neutral / Unknown |
| `Enter` on a field  | drop the marker (broadcasts the CoT event)          |
| `Enter` on `[ Drop ]` | drop the marker                                   |
| `Enter` on `[ Cancel ]` / `Esc` | discard                                 |

The marker appears on your map immediately (even offline) and is broadcast to
peers on your active channels. Leaving the label blank uses
"`<affiliation> marker`".

### Markers overlay

Press `M` to list the markers you have placed.

| Key                 | Action                                              |
|---------------------|-----------------------------------------------------|
| `↑` / `↓`           | select a marker                                     |
| `d` / `x` / `Del`   | delete it (broadcasts a CoT delete to peers)        |
| `M` / `Esc` / `q`   | close the overlay                                   |

### Position editor

| Key             | Action                                                   |
|-----------------|----------------------------------------------------------|
| `Tab` / `↓` / `↑` / `Shift+Tab` | move between rows                          |
| `←` / `→` on format row | switch decimal ↔ MGRS                              |
| `F2`            | fill active coord field with a random Swedish point      |
| `Space`         | toggle random-walk-Sweden checkbox (when focused on row) |
| `Enter` on text field | save and return to main view                       |
| `Enter` on random-walk row | toggle (stay in editor)                       |
| `Enter` on `[ Exit ]` row | quit program                                  |
| `Esc` / `Ctrl+C` | quit program                                            |

### First-run setup (enrol form)

| Key             | Action                                                   |
|-----------------|----------------------------------------------------------|
| `Tab` / `↓` / `↑` / `Shift+Tab` | move between rows                          |
| `←` / `→` on method row | switch enrol-new ↔ import-p12                      |
| `Space` on insecure row | toggle skip-TLS-verify                             |
| `Enter` on text field | submit (run enrolment)                             |
| `Enter` on `[ Exit ]` row | quit program                                  |
| `Esc` / `Ctrl+C` | quit program                                            |

---

## Channels and filtering

In TAK terminology, **"groups"** is overloaded — both the LDAP-driven access channels (`testchan_common`, `testchan_alpha`, …) and the on-screen team colours (Cyan, Red, …). TerminalTAK calls the access ones **channels** in the UI to keep them apart.

The Channels pane shows every channel the server has authorised your cert for, deduplicated and tagged with direction:

```
[x] testchan_common  (IN/OUT)
[x] testchan_alpha   (OUT)
[ ] testchan_bravo   (IN)
```

- `(IN)` — server forwards events from this channel to you
- `(OUT)` — server accepts events you send into this channel
- `(IN/OUT)` — both directions

When you toggle a channel:

1. The TUI calls `PUT /Marti/api/groups/activebits?clientUid=<uid>` with the new active-bit vector. From this point on, the server applies the filter to **both directions**: outbound events (your PLI, your chat) only enter enabled channels, and inbound events get dropped before they hit your TLS connection.
2. The local contacts cache is wiped. Any tracks you had — including federation tracks (which evade the per-event server-side filter because their UIDs aren't in the server's user directory) — disappear immediately. They will reappear only if their channel is re-enabled and a fresh PLI arrives.

If you toggle a channel and **nothing changes**, the cause is almost always a mismatch between the server's filtering mode and your client cert. See [TAK Server-side requirements](#tak-server-side-requirements) for the matrix and how to diagnose.

---

## Chat

TerminalTAK supports All-Chat (broadcast to "All Chat Rooms") and direct messages.

### All-Chat

1. Press `Tab` until the Channels or Chat pane is focused.
2. Press `i` — the input opens prefilled for All Chat Rooms.
3. Type, then `Enter` to send (or `Esc` to cancel).

### Direct message

1. `Tab` until the Contacts pane is focused.
2. Move the cursor with `↑` / `↓` to the desired contact.
3. Press `i` — the input opens prefilled for a DM to that contact.
4. Type, `Enter` to send.

### Why DMs identify as ATAK-CIV

Outgoing PLI / GeoChat events advertise `<takv platform="ATAK-CIV"/>` even though we obviously aren't ATAK. This is a deliberate workaround: TAK Server keeps an internal allow-list of "chat-capable" client platforms, and identifying as `TerminalTAK` made the server forward broadcast chat to us but silently drop inbound DMs. Until we can identify what actually drives that decision, mimicking ATAK-CIV is the cheap fix.

The `<remarks source="BAO.F.ATAK.<uid>">` source string is similarly required: ATAK's reply path parses this to recover the original sender's UID when the user hits "Reply", and a non-ATAK prefix routes the reply to the wrong contact.

---

## CoT log overlay

Press **`l`** at any time in the main view to open a scrolling log of recent CoT events:

```
┌ CoT log (last 200 events) ────────────────────────────────────────────────────┐
│ 12:03:18 a-f-G-U-C   ALPHA-01   59.34, 18.07                                  │
│ 12:03:21 b-t-f       ALPHA-01 → All Chat Rooms                                │
│ 12:03:30 t-x-takp-v  (server protocol announce)                               │
│ 12:03:30 t-x-takp-q  (our protocol ack, version=0)                            │
│ ...                                                                           │
└───────────────────────────────────────────────────────────────────────────────┘
```

The buffer is bounded (last 200 events). `↑` / `↓` scrolls; `Esc` or `q` closes. Useful for verifying that the server is actually sending what you expect — particularly when filtering or routing seems off.

A persistent log of every CoT event also goes to `~/.config/terminaltak/terminaltak.log`.

---

## Files on disk

```
~/.config/terminaltak/
├── config.yaml          # YAML config (see README)
├── cert.pem             # client leaf cert (mode 0600)
├── key.pem              # client private key (mode 0600)
├── ca.pem               # server CA chain (mode 0644)
├── terminaltak.log      # rolling slog output (info+warn+error)
└── cot-trace.log        # written only when -debug-cot is passed
```

`config.yaml` is rewritten atomically (write-then-rename) so a crash mid-save will not leave a truncated file.

---

## Command-line flags

| Flag          | Effect                                                                       |
|---------------|------------------------------------------------------------------------------|
| `-reset`      | wipe `~/.config/terminaltak/` (cert, key, ca, config) and exit               |
| `-debug-cot`  | append every recv/send CoT event (raw XML) to `~/.config/terminaltak/cot-trace.log` for offline diagnosis |

`-debug-cot` is the fastest way to see what is actually crossing the wire when a contact behaves oddly. Each line records direction, UID, callsign, type, lat/lon, the wire `stale` value, and the full re-encoded XML.

```sh
./terminaltak -debug-cot
# in another shell:
tail -f ~/.config/terminaltak/cot-trace.log
grep 'callsign="abc"' ~/.config/terminaltak/cot-trace.log | head
```

---

## Troubleshooting

### "TLS handshake error: x509: certificate signed by unknown authority"

The server's CA chain is not in `ca.pem`. Re-enrol, or copy the server's CA bundle into `ca.pem` manually.

### Cert expired

`cert.pem` has a `NotAfter` in the past. Delete `cert.pem` and relaunch — TerminalTAK will detect the missing cert and walk you through re-enrolment.

### Status flips to "reconnecting" repeatedly

Check VPN / firewall to the streaming port (8089). The client backs off exponentially up to 30 s between retries. The CoT log overlay (`l`) will show no inbound events; pending outbound PLI ticks are dropped silently with a warn-level log entry.

### Channel toggles don't filter outbound traffic

There is no single answer — TAK Server has several channel-filtering modes, and which one is active is a server-side configuration decision. See [TAK Server-side requirements](#tak-server-side-requirements) for the full matrix; the short diagnostic is:

```sh
openssl x509 -in ~/.config/terminaltak/cert.pem -text -noout | grep -A2 "Extended Key Usage"
```

- **Cert has `challengePassword` (OID `1.2.840.113549.1.9.7`)** — your cert is fine. If the toggle still does nothing, the server is not using the group cache at all (it assigns groups directly from LDAP each connect), or it is using a static `<filtergroup>` on the input port. Neither is something the client can change — ask the server admin.
- **Cert is missing the OID** — re-enrol with `terminaltak` (we send `version=1`), or ask the issuing CA to include the EKU. *Or* ask the admin to set `x509useGroupCacheRequiresExtKeyUsage="false"` in `CoreConfig.xml`, which makes any cert eligible for cache-backed filtering.

### Federation contacts persist after toggling their channel off

This was the symptom that motivated wiping the contacts cache on toggle — it should be fixed. If it recurs, `r` to force a reconnect, or restart. Federation tracks evade the server's per-event channel filter because their UIDs are not in the local user directory; the server-wide active-bits filter (which we now apply on toggle) is what actually drops them.

### `testchan_common` shown twice

Old build. The current Channels pane deduplicates by name, with direction tagged in parentheses.

### Map looks stretched

If Sweden looks wider than tall, your terminal cell aspect is unusual. The map auto-corrects for typical 2:1 cell aspect plus `cos(lat)` longitude compression — non-standard terminals (very narrow cells) might still look slightly off. Resize the terminal or zoom out to confirm.

---

## TAK Server-side requirements

### Always required

| Setting (CoreConfig.xml)                    | Effect                                                                |
|---------------------------------------------|-----------------------------------------------------------------------|
| `<input protocol="tls" port="8089"/>`       | the bidirectional CoT stream port                                     |
| `<certificateSigning ... port="8446"/>`     | required for in-app enrolment (skip if you only import existing `.p12`s) |
| `<auth default="ldap" x509groups="true">`   | the server resolves access channels from LDAP groups via the cert CN  |
| `<auth x509addAnonymous="false">`           | recommended — anonymous certs default into `__ANON__`, polluting the channel list |

### Channel filtering — three modes

How the server filters traffic per channel is a server-side decision, and each mode places different requirements on the client certificate. Knowing which mode your server runs in tells you what cert you need.

| Mode | CoreConfig                                                                 | Client cert needs                                  | Channel toggle in TerminalTAK |
|------|----------------------------------------------------------------------------|----------------------------------------------------|-------------------------------|
| **A. Cache + EKU required** *(server default)* | `x509useGroupCache="true"` and `x509useGroupCacheRequiresExtKeyUsage="true"` (or unset → default `true`) | OID `1.2.840.113549.1.9.7` (`challengePassword`) in the cert's Extended Key Usage | works server-side via `groups/activebits` |
| **B. Cache, EKU not required** | `x509useGroupCache="true"` and `x509useGroupCacheRequiresExtKeyUsage="false"` | any cert the CA will sign                          | works server-side via `groups/activebits` |
| **C. No cache (direct LDAP)**  | `x509useGroupCache="false"` (or unset → default `false`)                   | any cert the CA will sign                          | client-side only: TerminalTAK still hides events from disabled channels in the UI, but the server keeps streaming everything; outbound events go to all channels you have OUT-rights on |

Mode A is what TAK Server's *own* defaults imply (cache default-`false`, but if you turn it on you also opt into requiring the EKU). Some deployments turn the cache on without the EKU requirement (mode B), making channel filtering work for any cert. Some deployments don't run the cache at all (mode C).

A fourth mechanism exists in addition to the modes above — a `<filtergroup>` element on an `<input>` port statically restricts what channels that listener accepts/forwards regardless of cert. It is not user-toggleable and TerminalTAK has no control over it.

### Diagnosing which mode you are talking to

```sh
# Does your cert carry the channels OID?
openssl x509 -in ~/.config/terminaltak/cert.pem -text -noout | grep -A2 "Extended Key Usage"
# challengePassword present  → modes A and B both work
# challengePassword absent    → only mode B or C will accept your cert for channel filtering
```

If the toggle does nothing despite a cert with the OID, the server is in mode C (or has a `<filtergroup>` overriding everything). That is a server-side change — the client cannot work around it. Ask the admin to enable `x509useGroupCache="true"`, optionally with `x509useGroupCacheRequiresExtKeyUsage="false"` if they would rather not re-issue certs.

### What TerminalTAK does by default

The enrolment flow calls `/Marti/api/tls/signClient/v2?clientUid=<uuid>&version=1`. The `version=1` query asks the server to add the channels OID. Servers in mode B or C ignore the OID; servers in mode A need it. There is no downside to having an OID a server doesn't care about, so TerminalTAK always asks.
