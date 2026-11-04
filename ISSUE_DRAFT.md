# Title

Daily-driver feedback: four issues affecting reliable use, with a branch ready — how should I contribute?

# Body

Hi — I run TrueNAS across a few boxes and I've been driving them through Claude with this MCP server for a while now, so most of what follows comes from hitting things in normal day-to-day use rather than from reading the code cold.

A handful of problems kept getting in the way of smooth access. I ended up fixing them on a branch, along with a few changes I think the server needs before it's comfortable to point at a production NAS. I can't open a PR — the compare page says pull requests are limited to collaborators — so I'm raising this instead to ask how you'd like it handled.

Branch: https://github.com/ud-devops/truenas-mcp/tree/main
(one commit on top of `main`; builds clean, `go vet` clean, tests pass including `-race`)

## What was breaking in practice

**The session dies on any message over 64KB.** stdio framing uses a `bufio.Scanner`, which caps a token at 64KB and then fails the whole stream. One large `tools/call` — an app config, a long compose file — ends the session with `token too long`, and from the client side the process just goes quiet with no error. This was the most disruptive one for me. A `json.Decoder` has no such limit and also handles pretty-printed messages.

**A port I specify is silently ignored.** `buildConnectionURLs` takes everything before the last colon and hard-codes 443, so my TrueNAS behind a reverse proxy on 8443 was simply unreachable with no indication why. The same logic truncates a bare IPv6 literal — `fd00::1` becomes `fd00:`.

**One slow call blocks everything else.** Requests are dispatched sequentially, so a long `upgrade_app` or a scrub query stalls every unrelated request on the connection until it finishes.

**`tools/list` comes back in a different order every time**, because it iterates a map. Minor, but it defeats client-side caching.

## Changes I think matter for pointing this at real storage

The thing that made me nervous using it day to day is that there's no way to say "look, but don't touch." So I added tool annotations with one auditable table as the source of truth, and a `--read-only` mode that both hides write tools from `tools/list` and refuses them at call time. Plus `--allow-tools` / `--deny-tools`, and `--list-tools` to print exactly what a given configuration exposes without needing a live NAS to check.

Alongside that: protocol version negotiation instead of a pin to `2024-11-05`, `ping` and `notifications/cancelled` (cancellation actually cancels the middleware call rather than leaving it to time out), a configurable per-call timeout, and argument validation against the declared schema so a bad argument gives the model something actionable instead of a Python traceback.

I also split `registry.go` — at 5,749 lines it was hard to contribute to — into one file per domain, and moved the JSON-RPC and transport code into `mcp/` so the protocol layer no longer imports the TrueNAS client. That's what let me test the protocol without a NAS.

Finally, some tools I kept wishing existed: snapshot create/delete/rollback (snapshots were queryable but not manageable, which leaves out the most common "snapshot before I change this, roll back if it breaks" workflow), and read-only queries for services, disks, users, groups, replication tasks, cloud sync tasks, certificates and iSCSI targets.

## Two smaller things

- `make lint` runs `go fmt`, which rewrites files and always exits 0 — so it can never fail CI on formatting drift. `gofmt -l` does.
- Worth checking `claude_desktop_config.example.json` on `main`: the `--api-key` value there looks like a real key rather than a placeholder. If it ever was live it's in the repo history and should be rotated.

## Ask

Happy to split this into separate PRs by concern — the bug fixes are independent of the read-only work and of the new tools, and I'd understand not wanting one large change. I just need a way in: either collaborator access, or if you'd rather, pull the commits from the branch directly.

Also glad to be told any of this is wrong or already planned. I haven't been able to test against every TrueNAS version, so a smoke test on your side would be worth doing before merging the new tool handlers.
