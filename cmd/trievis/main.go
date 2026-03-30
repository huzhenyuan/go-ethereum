// Copyright 2024 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

// trievis is a developer tool that visualises a contract's storage trie.
// It walks every node using NodeIterator and emits:
//  1. A structured node dump with full RLP analysis for each node.
//  2. A Graphviz DOT digraph using shape=record with a left-right layout.
//
// Usage (auto mode – resolves storage root via account trie):
//
//	trievis --datadir <dir> --address <0xABCD> [--blocknum N]
//
// Usage (manual mode – provide hashes directly):
//
//	trievis --datadir <dir> --stateroot <SR> --accounthash <AH> --storageroot <Root>
//
// --datadir must point to the directory that directly contains the "chaindata"
// subdirectory (e.g. the directory where geth's LOCK file lives).
//
// Render the DOT section with: dot -Tsvg trie.dot -o trie.svg
package main

import (
	"bytes"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/ethdb/pebble"
	"github.com/ethereum/go-ethereum/internal/flags"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/hashdb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/urfave/cli/v2"
)

var app = flags.NewApp("Ethereum contract storage trie visualiser")

var (
	dataDirFlag = &cli.StringFlag{
		Name:    "datadir",
		Aliases: []string{"d"},
		Usage:   "Data directory that directly contains the 'chaindata' folder (e.g. /data/geth)",
	}
	ancientFlag = &cli.StringFlag{
		Name:  "ancient",
		Usage: "Ancient data directory (default: <datadir>/chaindata/ancient)",
	}
	stateSchemeFlag = &cli.StringFlag{
		Name:  "state.scheme",
		Usage: "State scheme override: 'hash' or 'path' (auto-detected from DB if not set)",
	}
	addressFlag = &cli.StringFlag{
		Name:  "address",
		Usage: "Contract address (hex, e.g. 0xABCD…) – used for auto-resolve mode",
	}
	staterootFlag = &cli.StringFlag{
		Name:  "stateroot",
		Usage: "Hex-encoded state root (overrides --blocknum when set)",
	}
	accounthashFlag = &cli.StringFlag{
		Name:  "accounthash",
		Usage: "Hex-encoded keccak256(address) – required in manual mode together with --storageroot",
	}
	storagerootFlag = &cli.StringFlag{
		Name:  "storageroot",
		Usage: "Hex-encoded storage trie root – required in manual mode together with --accounthash",
	}
	blocknumFlag = &cli.Uint64Flag{
		Name:  "blocknum",
		Usage: "Block number used to derive the state root (default: latest head)",
	}
	startKeyFlag = &cli.StringFlag{
		Name:  "startkey",
		Usage: "Hex-encoded key prefix to begin iteration from (optional)",
	}
	outputFlag = &cli.StringFlag{
		Name:  "output",
		Usage: "Write output to this file in addition to stdout",
	}
	maxNodesFlag = &cli.IntFlag{
		Name:  "max-nodes",
		Usage: "Maximum number of nodes to include in the DOT diagram (0 = no limit)",
		Value: 500,
	}
)

func init() {
	app.Action = visualize
	app.Flags = []cli.Flag{
		dataDirFlag,
		ancientFlag,
		stateSchemeFlag,
		addressFlag,
		staterootFlag,
		accounthashFlag,
		storagerootFlag,
		blocknumFlag,
		startKeyFlag,
		outputFlag,
		maxNodesFlag,
	}
	app.ArgsUsage = " "
	app.Description = `Walks every node of a contract's storage trie and emits:
  1. A raw node dump with full RLP structure analysis.
  2. A Graphviz DOT digraph (shape=record, rankdir=LR) of the trie.

--datadir must point to the directory that directly contains the 'chaindata'
subdirectory (e.g. /data/geth if your chaindata lives at /data/geth/chaindata).

Mode A – auto (resolves storage root via the account trie):
  trievis --datadir /data/geth --address 0xABCD [--blocknum N]

Mode B – manual (provide hashes directly, like geth db dumptrie):
  trievis --datadir /data/geth --stateroot <SR> --accounthash <AH> --storageroot <Root>

Render with: dot -Tsvg trie.dot -o trie.svg   (or xdot trie.dot)`
}

func main() {
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ── node record ─────────────────────────────────────────────────────────────

// nodeKind is the classified type of a trie node.
type nodeKind string

const (
	kindBranch     nodeKind = "BRANCH"
	kindExtension  nodeKind = "EXTENSION"
	kindLeafPrefix nodeKind = "LEAF-PREFIX"
	kindValue      nodeKind = "VALUE"
	kindEmbedded   nodeKind = "EMBEDDED"
	kindUnknown    nodeKind = "UNKNOWN"
)

// trieNodeInfo holds all collected and analysed data for one visited node.
type trieNodeInfo struct {
	idx       int
	path      []byte      // hex nibbles; value nodes end with 0x10
	isLeaf    bool        // true iff iterator is positioned on a valueNode
	hash      common.Hash // zero for embedded/inline nodes
	blob      []byte      // raw RLP from NodeBlob(); nil if node is embedded
	parentIdx int         // index of parent node; -1 for root
	edgeLabel string      // label on the directed edge FROM parent TO this node

	kind nodeKind

	// branch fields
	branchChildren [17]string // per-slot display string, e.g. "0xaabb…", "nil"

	// extension fields
	extKeyNibbles []byte // hex nibbles of the key segment (terminator stripped)
	extNextHash   string // display string for the next-node reference

	// leaf-prefix fields
	lpKeyNibbles []byte // hex nibbles of the key segment (terminator stripped)
	lpRawValue   []byte // raw value bytes from item[1] of the RLP pair

	// value-node fields
	slotKey   common.Hash // full 32-byte storage slot key hash
	slotRaw   []byte      // raw bytes from LeafBlob() (RLP-encoded storage value)
	slotValue *big.Int    // decoded storage value (may be nil on decode failure)
}

// ── main action ──────────────────────────────────────────────────────────────

func visualize(ctx *cli.Context) error {
	datadir := ctx.String(dataDirFlag.Name)
	if datadir == "" {
		return fmt.Errorf("--datadir is required (must point to the directory containing 'chaindata/')")
	}

	hasAddress := ctx.IsSet(addressFlag.Name)
	hasManual := ctx.IsSet(accounthashFlag.Name) && ctx.IsSet(storagerootFlag.Name)
	if !hasAddress && !hasManual {
		return fmt.Errorf("provide either --address, or both --accounthash and --storageroot")
	}

	// Open the chain database directly (no node.Node intermediary).
	db, trieDB, err := openDatabases(datadir, ctx.String(stateSchemeFlag.Name), ctx.String(ancientFlag.Name))
	if err != nil {
		return err
	}
	defer trieDB.Close()
	defer db.Close()

	// Resolve state root.
	var stateRoot common.Hash
	switch {
	case ctx.IsSet(staterootFlag.Name):
		b, err := hexutil.Decode(ctx.String(staterootFlag.Name))
		if err != nil {
			return fmt.Errorf("invalid --stateroot: %w", err)
		}
		stateRoot = common.BytesToHash(b)
	case ctx.IsSet(blocknumFlag.Name):
		n := ctx.Uint64(blocknumFlag.Name)
		bHash := rawdb.ReadCanonicalHash(db, n)
		if bHash == (common.Hash{}) {
			return fmt.Errorf("canonical hash not found for block %d", n)
		}
		stateRoot = rawdb.ReadHeader(db, bHash, n).Root
	default:
		head := rawdb.ReadHeadHeaderHash(db)
		n, ok := rawdb.ReadHeaderNumber(db, head)
		if !ok {
			return fmt.Errorf("could not read head block number")
		}
		bHash := rawdb.ReadCanonicalHash(db, n)
		stateRoot = rawdb.ReadHeader(db, bHash, n).Root
	}

	// Resolve account hash and storage root.
	var (
		accountHash common.Hash
		storageRoot common.Hash
		addrStr     string
	)

	if hasAddress {
		addrStr = ctx.String(addressFlag.Name)
		address := common.HexToAddress(addrStr)
		accountHash = crypto.Keccak256Hash(address.Bytes())

		accountTrie, err := trie.New(trie.StateTrieID(stateRoot), trieDB)
		if err != nil {
			return fmt.Errorf("failed to open account trie: %w", err)
		}
		accountRLP, err := accountTrie.Get(crypto.Keccak256(address.Bytes()))
		if err != nil {
			return fmt.Errorf("failed to look up account: %w", err)
		}
		if accountRLP == nil {
			return fmt.Errorf("account not found in state trie: %s\n(state root: %s)", address, stateRoot)
		}
		var acc types.StateAccount
		if err := rlp.DecodeBytes(accountRLP, &acc); err != nil {
			return fmt.Errorf("failed to decode account RLP: %w", err)
		}
		if acc.Root == (common.Hash{}) || acc.Root == types.EmptyRootHash {
			return fmt.Errorf("account %s has no storage (root is empty)", address)
		}
		storageRoot = acc.Root
	} else {
		b1, err := hexutil.Decode(ctx.String(accounthashFlag.Name))
		if err != nil {
			return fmt.Errorf("invalid --accounthash: %w", err)
		}
		accountHash = common.BytesToHash(b1)
		b2, err := hexutil.Decode(ctx.String(storagerootFlag.Name))
		if err != nil {
			return fmt.Errorf("invalid --storageroot: %w", err)
		}
		storageRoot = common.BytesToHash(b2)
	}

	// Optional start key (keybytes).
	var startPath []byte
	if s := ctx.String(startKeyFlag.Name); s != "" {
		startPath, err = hexutil.Decode(s)
		if err != nil {
			return fmt.Errorf("invalid --startkey: %w", err)
		}
	}

	// Open the storage trie and iterate every node in pre-order.
	id := trie.StorageTrieID(stateRoot, accountHash, storageRoot)
	storageTrie, err := trie.New(id, trieDB)
	if err != nil {
		return fmt.Errorf("failed to open storage trie: %w", err)
	}
	nodeIt, err := storageTrie.NodeIterator(startPath)
	if err != nil {
		return fmt.Errorf("failed to create node iterator: %w", err)
	}

	var (
		nodes     []*trieNodeInfo
		pathStack []struct {
			path []byte
			idx  int
		}
	)

	for nodeIt.Next(true /*descend*/) {
		curPath := make([]byte, len(nodeIt.Path()))
		copy(curPath, nodeIt.Path())

		isLeaf := nodeIt.Leaf()
		hash := nodeIt.Hash()

		blob := nodeIt.NodeBlob()
		if blob != nil {
			tmp := make([]byte, len(blob))
			copy(tmp, blob)
			blob = tmp
		}

		// Walk the ancestor stack: pop entries that are not a strict prefix of
		// the current (normalised, terminator-stripped) path.
		curNibbles := stripTerm(curPath)
		for len(pathStack) > 0 {
			top := stripTerm(pathStack[len(pathStack)-1].path)
			if len(top) < len(curNibbles) && bytes.Equal(curNibbles[:len(top)], top) {
				break
			}
			pathStack = pathStack[:len(pathStack)-1]
		}

		parentIdx := -1
		edgeLabel := ""
		if len(pathStack) > 0 {
			top := pathStack[len(pathStack)-1]
			parentIdx = top.idx
			parentNibbles := stripTerm(top.path)
			edgeNibbles := curNibbles[len(parentNibbles):]
			edgeLabel = nibbleEdgeLabel(edgeNibbles)
		}

		// Push non-leaf nodes onto the stack so they can act as parents.
		if !isLeaf {
			pathStack = append(pathStack, struct {
				path []byte
				idx  int
			}{curPath, len(nodes)})
		}

		// Collect leaf data before Next() invalidates the iterator's buffers.
		var slotKey common.Hash
		var slotRaw []byte
		if isLeaf {
			rawKey := nodeIt.LeafKey()
			if len(rawKey) == 32 {
				slotKey = common.BytesToHash(rawKey)
			}
			lb := nodeIt.LeafBlob()
			slotRaw = make([]byte, len(lb))
			copy(slotRaw, lb)
		}

		n := &trieNodeInfo{
			idx:       len(nodes),
			path:      curPath,
			isLeaf:    isLeaf,
			hash:      hash,
			blob:      blob,
			parentIdx: parentIdx,
			edgeLabel: edgeLabel,
			slotKey:   slotKey,
			slotRaw:   slotRaw,
		}
		nodes = append(nodes, n)
	}
	if err := nodeIt.Error(); err != nil {
		return fmt.Errorf("iterator error: %w", err)
	}

	// Analyse each node's RLP once we have all paths.
	for _, n := range nodes {
		analyzeNode(n)
	}

	// Set up the output writer.
	var w io.Writer = os.Stdout
	if outPath := ctx.String(outputFlag.Name); outPath != "" {
		f, ferr := os.Create(outPath)
		if ferr != nil {
			return fmt.Errorf("failed to create output file: %w", ferr)
		}
		defer f.Close()
		w = io.MultiWriter(os.Stdout, f)
	}

	// ── Dump header ────────────────────────────────────────────────────────
	fmt.Fprintln(w, "=== Storage Trie Node Dump ===")
	if addrStr != "" {
		fmt.Fprintf(w, "Contract:     %s\n", addrStr)
	}
	fmt.Fprintf(w, "Account Hash: %s\n", accountHash.Hex())
	fmt.Fprintf(w, "State Root:   %s\n", stateRoot.Hex())
	fmt.Fprintf(w, "Storage Root: %s\n", storageRoot.Hex())
	fmt.Fprintf(w, "Total nodes:  %d\n\n", len(nodes))

	// ── Per-node detail ────────────────────────────────────────────────────
	for _, n := range nodes {
		printNode(w, n)
	}

	// ── DOT diagram ───────────────────────────────────────────────────────
	maxNodes := ctx.Int(maxNodesFlag.Name)
	limit := len(nodes)
	if maxNodes > 0 && limit > maxNodes {
		limit = maxNodes
	}

	fmt.Fprintln(w, "\n// ── Graphviz DOT ───────────────────────────────────────────────────")
	fmt.Fprintln(w, "// Save the digraph{} block below to trie.dot, then run:")
	fmt.Fprintln(w, "//   dot -Tsvg trie.dot -o trie.svg   (or: xdot trie.dot)")
	if limit < len(nodes) {
		fmt.Fprintf(w, "// WARNING: trie has %d nodes; only the first %d are shown.\n", len(nodes), limit)
	}
	writeDOT(w, nodes[:limit])
	return nil
}

// openDatabases opens the chaindata key-value store and trie database directly
// from the given datadir without going through the node package.
// datadir is expected to contain a "chaindata" subdirectory (e.g. /data/geth).
func openDatabases(datadir, schemeOverride, ancientOverride string) (ethdb.Database, *triedb.Database, error) {
	chaindataDir := filepath.Join(datadir, "chaindata")
	ancient := ancientOverride
	if ancient == "" {
		ancient = filepath.Join(chaindataDir, "ancient")
	}

	// Detect the key-value engine and open it read-only.
	engine := rawdb.PreexistingDatabase(chaindataDir)
	var (
		kvdb ethdb.KeyValueStore
		err  error
	)
	switch engine {
	case rawdb.DBPebble:
		kvdb, err = pebble.New(chaindataDir, 128, 500, "trievis/", true)
	case rawdb.DBLeveldb:
		kvdb, err = leveldb.New(chaindataDir, 128, 500, "trievis/", true)
	default:
		// No existing DB signals a wrong path; try pebble to surface a clear error.
		kvdb, err = pebble.New(chaindataDir, 128, 500, "trievis/", true)
		if err != nil {
			return nil, nil, fmt.Errorf("no chaindata database found at %s\n"+
				"(tip: --datadir must be the directory that directly contains 'chaindata/', e.g. /data/geth)", chaindataDir)
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open %s database at %s: %w", engine, chaindataDir, err)
	}

	db, err := rawdb.Open(kvdb, rawdb.OpenOptions{
		Ancient:  ancient,
		ReadOnly: true,
	})
	if err != nil {
		kvdb.Close()
		return nil, nil, fmt.Errorf("failed to open database with freezer: %w", err)
	}

	// Detect the state trie scheme from the database itself.
	scheme := schemeOverride
	if scheme == "" {
		scheme, err = rawdb.ParseStateScheme("", db)
		if err != nil {
			db.Close()
			return nil, nil, fmt.Errorf("failed to detect state scheme: %w", err)
		}
	}

	// Build trie database config matching the detected scheme.
	tdbConfig := &triedb.Config{}
	if scheme == rawdb.HashScheme {
		tdbConfig.HashDB = hashdb.Defaults
	} else {
		pathCfg := *pathdb.ReadOnly
		pathCfg.JournalDirectory = filepath.Join(datadir, "triedb")
		tdbConfig.PathDB = &pathCfg
	}
	tdb := triedb.NewDatabase(db, tdbConfig)
	return db, tdb, nil
}

// ── analysis ─────────────────────────────────────────────────────────────────

func analyzeNode(n *trieNodeInfo) {
	if n.isLeaf {
		n.kind = kindValue
		if len(n.slotRaw) > 0 {
			var raw []byte
			if err := rlp.DecodeBytes(n.slotRaw, &raw); err == nil {
				n.slotValue = new(big.Int).SetBytes(raw)
			}
		}
		return
	}
	if len(n.blob) == 0 {
		n.kind = kindEmbedded
		return
	}

	elems, _, err := rlp.SplitList(n.blob)
	if err != nil {
		n.kind = kindUnknown
		return
	}
	count, err := rlp.CountValues(elems)
	if err != nil {
		n.kind = kindUnknown
		return
	}
	switch count {
	case 17:
		n.kind = kindBranch
		analyzeBranch(n)
	case 2:
		analyzeShort(n)
	default:
		n.kind = kindUnknown
	}
}

func analyzeBranch(n *trieNodeInfo) {
	rest, _, err := rlp.SplitList(n.blob)
	if err != nil {
		return
	}
	for i := 0; i < 17; i++ {
		var (
			kind     rlp.Kind
			val      []byte
			splitErr error
		)
		kind, val, rest, splitErr = rlp.Split(rest)
		if splitErr != nil {
			n.branchChildren[i] = "err"
			continue
		}
		switch kind {
		case rlp.String:
			if len(val) == 32 {
				n.branchChildren[i] = fmt.Sprintf("0x%x…", val[:4])
			} else if len(val) == 0 {
				n.branchChildren[i] = "nil"
			} else {
				n.branchChildren[i] = fmt.Sprintf("emb(%x)", val)
			}
		case rlp.List:
			n.branchChildren[i] = fmt.Sprintf("inline(%dB)", len(val)+2)
		default:
			n.branchChildren[i] = "?"
		}
	}
}

func analyzeShort(n *trieNodeInfo) {
	rest, _, err := rlp.SplitList(n.blob)
	if err != nil {
		n.kind = kindUnknown
		return
	}
	// Item 0: compact-encoded key
	_, keyBytes, rest, err := rlp.Split(rest)
	if err != nil || len(keyBytes) == 0 {
		n.kind = kindUnknown
		return
	}
	// Item 1: value reference or raw value
	valKind, valBytes, _, err := rlp.Split(rest)
	if err != nil {
		n.kind = kindUnknown
		return
	}

	// Determine leaf vs extension: upper nibble of compact[0]; >= 2 → leaf.
	isLeafShort := keyBytes[0]>>4 >= 2
	keyNibbles := compactToHexNibbles(keyBytes)

	if isLeafShort {
		n.kind = kindLeafPrefix
		n.lpKeyNibbles = keyNibbles
		if valKind == rlp.String {
			n.lpRawValue = valBytes
		}
	} else {
		n.kind = kindExtension
		n.extKeyNibbles = keyNibbles
		switch {
		case valKind == rlp.String && len(valBytes) == 32:
			n.extNextHash = fmt.Sprintf("0x%x", valBytes)
		case valKind == rlp.String && len(valBytes) > 0:
			n.extNextHash = fmt.Sprintf("emb(%x)", valBytes)
		case valKind == rlp.List:
			n.extNextHash = fmt.Sprintf("inline(%dB)", len(valBytes)+2)
		default:
			n.extNextHash = "nil"
		}
	}
}

// compactToHexNibbles converts compact/HP-encoded key bytes to hex nibbles,
// mirroring the unexported trie.compactToHex + trie.keybytesToHex functions.
// The returned slice includes a trailing 0x10 terminator iff this is a leaf key.
func compactToHexNibbles(compact []byte) []byte {
	if len(compact) == 0 {
		return nil
	}
	// Expand each byte into two nibbles and append a terminator.
	l := len(compact)*2 + 1
	base := make([]byte, l)
	for i, b := range compact {
		base[i*2] = b >> 4
		base[i*2+1] = b & 0x0f
	}
	base[l-1] = 16 // terminator
	// base[0] is the upper nibble of compact[0] (the HP flag nibble).
	// If < 2 → extension: drop the terminator.
	if base[0] < 2 {
		base = base[:l-1]
	}
	// base[0]&1 == 1 → odd key length (first nibble is part of key, chop=1).
	// base[0]&1 == 0 → even key length (first nibble is padding, chop=2).
	chop := 2 - int(base[0]&1)
	return base[chop:]
}

// ── output helpers ────────────────────────────────────────────────────────────

// printNode writes a human-readable record for one trie node.
func printNode(w io.Writer, n *trieNodeInfo) {
	pathStr := pathDisplay(n.path)
	hashStr := hashDisplay(n.hash)
	fmt.Fprintf(w, "[%04d] kind=%-12s  path=%-34s  hash=%s\n",
		n.idx, n.kind, pathStr, hashStr)
	if len(n.blob) > 0 {
		fmt.Fprintf(w, "       rlp: 0x%x\n", n.blob)
	}
	switch n.kind {
	case kindBranch:
		fmt.Fprintf(w, "       branch children:\n")
		for i, child := range n.branchChildren {
			if child != "nil" {
				fmt.Fprintf(w, "         [%x] = %s\n", i, child)
			}
		}
		nilCount := 0
		for _, child := range n.branchChildren {
			if child == "nil" {
				nilCount++
			}
		}
		if nilCount > 0 {
			fmt.Fprintf(w, "         (+ %d nil slots)\n", nilCount)
		}
	case kindExtension:
		fmt.Fprintf(w, "       key-segment: %x  →  next: %s\n",
			n.extKeyNibbles, n.extNextHash)
	case kindLeafPrefix:
		fmt.Fprintf(w, "       key-segment: %x  →  raw-value: 0x%x\n",
			n.lpKeyNibbles, n.lpRawValue)
	case kindValue:
		valStr := "(decode failed)"
		if n.slotValue != nil {
			valStr = n.slotValue.String()
		}
		fmt.Fprintf(w, "       slot-key: %s\n", n.slotKey.Hex())
		fmt.Fprintf(w, "       slot-raw: 0x%x\n", n.slotRaw)
		fmt.Fprintf(w, "       slot-val: %s (decimal)\n", valStr)
	case kindEmbedded:
		fmt.Fprintf(w, "       (inline/embedded node – no separate hash)\n")
	}
	fmt.Fprintln(w)
}

// writeDOT emits a Graphviz DOT digraph using shape=record with rankdir=LR.
//
// Layout:
//   - Every non-leaf node is a record: left cell = metadata (kind/path/hash),
//     right cell = child ports labelled by nibble (for BRANCH) or key segment.
//   - BRANCH fills: #ddeeff (blue-tint).  ROOT same but titled "Root".
//   - EXTENSION / LEAF-PREFIX fills: #ffeedd (orange-tint).
//   - VALUE (leaf) nodes: simple non-record box, fill #eeddff (purple-tint).
//   - LEAF-PREFIX → VALUE edge: dashed, no arrowhead.
//   - All edges target the west (:w) port of the destination node.
func writeDOT(w io.Writer, nodes []*trieNodeInfo) {
	limit := len(nodes)

	fmt.Fprintln(w, "\ndigraph storage_trie {")
	fmt.Fprintln(w, `    graph [rankdir=LR, splines=line, ranksep=10]`)
	fmt.Fprintln(w, `    node  [shape=record, style="rounded,filled"]`)
	fmt.Fprintln(w)

	for _, n := range nodes {
		switch n.kind {
		case kindBranch:
			title := "BRANCH"
			if n.parentIdx == -1 {
				title = "Root"
			}
			var portParts []string
			for i, child := range n.branchChildren {
				if child != "nil" {
					portParts = append(portParts, fmt.Sprintf("<p%x> %x", i, i))
				}
			}
			portSection := strings.Join(portParts, " | ")
			label := fmt.Sprintf("{ %s \\n path=%s \\n hash=%s | { %s } }",
				title,
				dotRecEsc(pathDisplay(n.path)),
				dotRecEsc(hashDisplay(n.hash)),
				portSection,
			)
			fmt.Fprintf(w, "    n%d [label=\"%s\" fillcolor=\"#ddeeff\"];\n", n.idx, label)

		case kindExtension:
			keyStr := fmt.Sprintf("%x", n.extKeyNibbles)
			label := fmt.Sprintf("{ EXTENSION \\n path=%s \\n hash=%s | { <pnext> %s } }",
				dotRecEsc(pathDisplay(n.path)),
				dotRecEsc(hashDisplay(n.hash)),
				dotRecEsc(keyStr),
			)
			fmt.Fprintf(w, "    n%d [label=\"%s\" fillcolor=\"#ffeedd\"];\n", n.idx, label)

		case kindLeafPrefix:
			// keyStr := fmt.Sprintf("%x", n.lpKeyNibbles)
			label := fmt.Sprintf("{ LEAF \\n path=%s \\n hash=%s | { <pval> } }",
				dotRecEsc(pathDisplay(n.path)),
				dotRecEsc(hashDisplay(n.hash)),
				// dotRecEsc(keyStr),
			)
			fmt.Fprintf(w, "    n%d [label=\"%s\" fillcolor=\"#ffeedd\"];\n", n.idx, label)

		case kindValue:
			sk := n.slotKey.Hex()
			if len(sk) > 20 {
				sk = sk[:18] + "..."
			}
			valStr := "(decode failed)"
			if n.slotValue != nil {
				valStr = n.slotValue.String()
			}
			label := fmt.Sprintf("VALUE \\n slot=%s \\n val=%s",
				dotRecEsc(sk),
				dotRecEsc(valStr),
			)
			fmt.Fprintf(w, "    n%d [label=\"%s\" fillcolor=\"#eeddff\"];\n", n.idx, label)

		case kindEmbedded:
			label := fmt.Sprintf("EMBEDDED \\n path=%s", dotRecEsc(pathDisplay(n.path)))
			fmt.Fprintf(w, "    n%d [label=\"%s\"];\n", n.idx, label)
		}
	}

	fmt.Fprintln(w)

	for _, n := range nodes {
		if n.parentIdx < 0 || n.parentIdx >= limit {
			continue
		}
		parent := nodes[n.parentIdx]

		tailPort := ""
		switch parent.kind {
		case kindBranch:
			if len(n.edgeLabel) == 3 { // "[x]" – single hex nibble
				tailPort = fmt.Sprintf(":p%c", n.edgeLabel[1])
			}
		case kindExtension:
			tailPort = ":pnext"
		case kindLeafPrefix:
			tailPort = ":pval"
		}

		if parent.kind == kindLeafPrefix && n.kind == kindValue {
			fmt.Fprintf(w, "    n%d%s -> n%d:w [style=dashed arrowhead=none];\n", parent.idx, tailPort, n.idx)
		} else {
			fmt.Fprintf(w, "    n%d%s -> n%d:w;\n", parent.idx, tailPort, n.idx)
		}
	}

	fmt.Fprintln(w, "}")
}

// ── small utilities ───────────────────────────────────────────────────────────

// stripTerm returns the path without a trailing 0x10 leaf terminator.
func stripTerm(p []byte) []byte {
	if len(p) > 0 && p[len(p)-1] == 0x10 {
		return p[:len(p)-1]
	}
	return p
}

// nibbleEdgeLabel returns a Mermaid-friendly edge label from the nibble slice
// that separates a parent path from its child's path.
func nibbleEdgeLabel(edgeNibbles []byte) string {
	switch {
	case len(edgeNibbles) == 0:
		return ""
	case len(edgeNibbles) == 1 && edgeNibbles[0] != 0x10:
		// Single nibble → branch edge.
		return fmt.Sprintf("[%x]", edgeNibbles[0])
	case len(edgeNibbles) == 1 && edgeNibbles[0] == 0x10:
		// Leaf-prefix shortNode with an empty key segment.
		return "(leaf)"
	default:
		// Multi-nibble → short-node (extension or leaf-prefix) key segment.
		seg := edgeNibbles
		if len(seg) > 0 && seg[len(seg)-1] == 0x10 {
			seg = seg[:len(seg)-1]
		}
		return fmt.Sprintf("%x", seg)
	}
}

// pathDisplay formats a hex-nibble path for human display.
// Long paths are abbreviated; the leaf terminator is shown as "⏎".
func pathDisplay(p []byte) string {
	if len(p) == 0 {
		return "(root)"
	}
	hasLeaf := len(p) > 0 && p[len(p)-1] == 0x10
	nibbles := p
	if hasLeaf {
		nibbles = p[:len(p)-1]
	}
	const maxNibs = 16
	s := fmt.Sprintf("%x", nibbles)
	if len(nibbles) > maxNibs {
		s = fmt.Sprintf("%x", nibbles[:maxNibs]) + "…"
	}
	if hasLeaf {
		s += "⏎"
	}
	return s
}

// hashDisplay formats a node hash for compact display.
// Returns "embedded" when the hash is zero (inline nodes have no hash).
func hashDisplay(h common.Hash) string {
	if h == (common.Hash{}) {
		return "embedded"
	}
	hex := h.Hex() // "0x" + 64 hex chars
	if len(hex) > 14 {
		return hex[:12] + "…"
	}
	return hex
}

// dotRecEsc escapes characters that are syntactically special inside a
// Graphviz shape=record label cell: braces, pipes, and angle brackets.
// Backslash must be escaped first to avoid double-escaping.
func dotRecEsc(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "{", `\{`)
	s = strings.ReplaceAll(s, "}", `\}`)
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "<", `\<`)
	s = strings.ReplaceAll(s, ">", `\>`)
	return s
}
