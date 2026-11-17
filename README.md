# go-orbitdb

[![Go](https://github.com/orbitdb/go-orbitdb/actions/workflows/go.yml/badge.svg)](https://github.com/orbitdb/go-orbitdb/actions/workflows/go.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/orbitdb/go-orbitdb.svg)](https://pkg.go.dev/github.com/orbitdb/go-orbitdb)

A Go implementation of [OrbitDB](https://github.com/orbitdb/orbitdb), the
serverless, peer-to-peer database built on IPFS and libp2p.

It is a port of `@orbitdb/core` 4 and **interoperates with it**: identities,
log entries, manifests, access controllers and the sync protocol are
byte-for-byte compatible, so Go and JavaScript peers open and replicate the
same databases. This is checked on every push against a live
`@orbitdb/core` 4.0.0 node (see [Compatibility](#compatibility)).

```go
node, _ := ipfs.New(ctx, ipfs.Options{Repo: "./blocks"})
odb, _ := orbitdb.New(ctx, orbitdb.Options{IPFS: node, Directory: "./orbitdb"})
defer odb.Stop()

kv, _ := odb.OpenKeyValue(ctx, "settings")
kv.Put(ctx, "theme", "dark")
fmt.Println(kv.Address()) // /orbitdb/zdpu... — share it with peers
```

## Install

```sh
go get github.com/orbitdb/go-orbitdb
```

Go 1.26.6 or later.

## What's included

| @orbitdb/core | Go package | Notes |
|---|---|---|
| `createOrbitDB`, `open` | [`orbitdb`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb) | `Open`, typed `OpenEvents` / `OpenKeyValue` / `OpenKeyValueIndexed` / `OpenDocuments`, `OpenAs[T]` |
| Events, KeyValue, KeyValueIndexed, Documents | [`databases`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/databases) | custom types via `UseDatabaseType` |
| Log, Entry, Heads, Clock, conflict resolution | [`oplog`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/oplog) | append, join, traverse, range iterator, entry/payload encryption hooks |
| Sync | [`syncutils`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/syncutils) | gossipsub updates + `/orbitdb/heads/<address>` exchange |
| IPFS and OrbitDB access controllers | [`accesscontrollers`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/accesscontrollers) | grant / revoke, custom types via `UseAccessController` |
| Identities, publickey provider | [`identities`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/identities) | custom providers via `providers.UseIdentityProvider` |
| KeyStore | [`keystore`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/keystore) | secp256k1 via go-libp2p; reads JS keystores |
| Memory, LRU, Level, IPFS block, Composed storage | [`storage`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/storage) | |
| `@orbitdb/simple-encryption` | [`encryption`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/encryption) | AES-GCM + PBKDF2, same ciphertext format |
| Helia | [`ipfs`](https://pkg.go.dev/github.com/orbitdb/go-orbitdb/ipfs) | go-libp2p host, gossipsub and a boxo bitswap block service |

## Usage

### Databases

```go
events, _ := odb.OpenEvents(ctx, "log")
hash, _ := events.Add(ctx, map[string]any{"msg": "hello"})
all, _ := events.All(ctx) // oldest first

type Post struct {
	ID    string `json:"_id"` // documents are keyed by "_id" unless configured otherwise
	Title string `json:"title"`
}
docs, _ := odb.OpenDocuments(ctx, "posts")
docs.Put(ctx, Post{ID: "p1", Title: "first"}) // structs are stored through their JSON form
matches, _ := docs.Query(ctx, func(d map[string]any) bool { return d["title"] == "first" })

kvi, _ := odb.OpenKeyValueIndexed(ctx, "settings-indexed") // reads from a local index
```

Values are anything dag-cbor can hold: `nil`, booleans, numbers, strings,
`[]byte`, CIDs, slices and string-keyed maps, or structs (encoded via
`encoding/json`). Reads return `map[string]any`, `[]any`, `int64`,
`float64`, `string`, `[]byte`, `bool` or `nil`. As in JavaScript, a float with
an integral value is stored as an integer.

### Replication

Open the same address on another peer:

```go
db, _ := odb.OpenKeyValue(ctx, "/orbitdb/zdpu...")
sub := db.Events().Subscribe()
for ev := range sub.Events() {
	if ev.Type == databases.EventUpdate {
		fmt.Println("update:", ev.Entry.Payload)
	}
}
```

The manifest, access controller, heads, missing ancestors and writer
identities are fetched from connected peers over bitswap. Peers find each
other however you connect their libp2p hosts (`node.Connect`, or your own
discovery); there is no DHT or delegated routing by default.

To talk to a JavaScript peer, give its Helia node TCP (or another transport
both sides share), Noise, Yamux, gossipsub and bitswap.

### Access control

By default only the creator may write. Pass an access controller when
creating a database:

```go
// Anyone may write.
orbitdb.WithAccessController(accesscontrollers.IPFS(accesscontrollers.IPFSOptions{Write: []string{"*"}}))

// Writers listed in a database of their own, changeable later.
db, _ := odb.OpenEvents(ctx, "team", orbitdb.WithAccessController(accesscontrollers.OrbitDB(accesscontrollers.OrbitDBOptions{})))
db.AccessController().(*accesscontrollers.OrbitDBAccessController).Grant(ctx, "write", otherIdentityID)
```

Every entry, local or from a peer, is checked against the access controller
and its signature before it joins the log. The controllers also require the
key that signed an entry to be the key of the identity the entry names.

### Encryption

```go
password := []byte("correct horse battery staple")
data, _ := encryption.NewSimple(password)
replication, _ := encryption.NewSimple(password)
db, _ := odb.OpenKeyValue(ctx, "secret", orbitdb.WithEncryption(oplog.Encryption{Data: data, Replication: replication}))
```

`encryption.Simple` reads and writes the format of `@orbitdb/simple-encryption`
and uses a fresh random nonce for every message. Any `oplog.Encrypter` works.

### Persistence

`ipfs.Options.Repo` keeps blocks (log entries, identities, manifests) in
LevelDB; `orbitdb.Options.Directory` holds the keystore and each database's
heads and index. With both set, databases survive restarts. Without `Repo`,
blocks live in memory.

## Demo

```sh
go run ./cmd/orbitdb-demo --dir /tmp/a
# in a second terminal, with the multiaddr and address the first printed:
go run ./cmd/orbitdb-demo --dir /tmp/b --peer /ip4/127.0.0.1/tcp/.../p2p/12D3... --db /orbitdb/zdpu...
> put greeting hello
```

## Compatibility

Two kinds of tests pin the Go implementation to `@orbitdb/core` 4.0.0:

- **Golden vectors.** [`tools/jsvectors`](tools/jsvectors) runs scenarios on
  `@orbitdb/core` with the keys of its own test fixture keystore and records
  every identity, entry block, hash, traversal order, iterator range,
  manifest, access controller and encrypted block. Signatures are
  deterministic (RFC 6979), so the Go tests replay the same scenarios and
  require identical bytes: multi-writer joins, references, same-clock ties,
  every payload type and both encryption hooks included. CI regenerates the
  vectors to prove they are what `@orbitdb/core` produces.
- **Live interop.** [`interop`](interop) starts a JavaScript OrbitDB peer on
  Helia and replicates with it both ways: JS-created and Go-created databases,
  concurrent writers converging to identical logs, documents, write
  permissions, OrbitDB access controller grants and encrypted databases.

```sh
go test -race ./...                                             # unit + vector tests
(cd interop/js-peer && npm ci) && go test -tags interop ./interop/  # needs Node.js 22
(cd tools/jsvectors && npm ci && npm run check)                 # vectors are reproducible
```

## Differences from the JavaScript API

- Every call takes a `context.Context`; iteration uses `iter.Seq2`.
- Missing keys and documents return errors wrapping `databases.ErrNotFound`
  instead of `undefined`.
- Events are delivered on channels (`db.Events().Subscribe()`); emitting
  never blocks and never drops.
- Iterator `Amount` of zero means "all" (JavaScript uses -1).
- Documents keys are strings; a numeric key written by a JavaScript peer is
  looked up by its decimal form.

## License

MIT
