# End-to-end tests

Real Matrix clients, with end-to-end encryption, talk to a local [Conduit](https://conduit.rs) homeserver and a running bot.
The tests carry the build tag `e2e`, so `go test ./...` skips them.

What they cover: invitations (only bot admins), publishing, feed and API, edits and deletions, history fetched from the homeserver and paging, room admins and private chats, API keys and feed tokens (issue, scope, revoke), the CLI on the live database, encrypted rooms (commands, edits, deletions, history), and a restart of the bot.

## Run

```sh
export E2E_DIR=/tmp/matrixrss-e2e && mkdir -p $E2E_DIR/bot

# 1. the homeserver (rooms are of version 12; registration needs the token from conduit.toml)
docker run -d --name conduit-test -p 6167:6167 \
  -v $PWD/e2e/conduit.toml:/etc/conduit.toml:ro -e CONDUIT_CONFIG=/etc/conduit.toml \
  matrixconduit/matrix-conduit:latest
python3 e2e/register.py                 # rssbot, alice (bot admin), bob (room admin), carol (stranger)

# 2. the bot, with a fresh database
CGO_ENABLED=0 go build -tags goolm -o $E2E_DIR/matrixrss ./cmd/matrixrss
cp e2e/bot.env.example $E2E_DIR/bot.env
(cd $E2E_DIR/bot && set -a && . ../bot.env && set +a && exec ../matrixrss) &

# 3. the tests
CGO_ENABLED=0 go test -tags 'goolm e2e' -count=1 -v -run TestE2E ./e2e
```

Restart check (stops and starts the bot on the same database):

```sh
pkill -f $E2E_DIR/matrixrss
E2E_PHASE=down go test -tags 'goolm e2e' -count=1 -run TestDowntime ./e2e   # people talk while the bot is down
(cd $E2E_DIR/bot && set -a && . ../bot.env && set +a && exec ../matrixrss) &
E2E_PHASE=up go test -tags 'goolm e2e' -count=1 -run TestAfterRestart ./e2e
```

Delete `$E2E_DIR/bot` before a new `TestE2E` run; the scenarios expect an empty bot database. Stop the homeserver with `docker rm -f conduit-test`.
